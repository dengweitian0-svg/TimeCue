package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/model"
)

// RecoveryStart 从实际版本及事实确定最早边界，不采用最后确认或固定天数。
func (s *NotificationStore) RecoveryStart(ctx context.Context, end time.Time) (time.Time, error) {
	db, err := s.db.handle()
	if err != nil {
		return time.Time{}, err
	}
	var earliest sql.NullInt64
	err = db.QueryRowContext(ctx, `SELECT min(at) FROM (SELECT effective_from AS at FROM schedule_versions WHERE effective_from < ?
		UNION ALL SELECT notify_time AS at FROM notifications WHERE deleted = 0 AND notify_time < ?)`, end.UnixNano(), end.UnixNano()).Scan(&earliest)
	if err != nil || !earliest.Valid {
		return time.Time{}, err
	}
	return time.Unix(0, earliest.Int64).In(model.BeijingLocation()), nil
}

// CountRecoveryMembers 只读当前资格，不把 failed 重扫成 loaded。
func (s *NotificationStore) CountRecoveryMembers(ctx context.Context, ids []int, now time.Time) (int, error) {
	db, err := s.db.handle()
	if err != nil {
		return 0, err
	}
	data, err := json.Marshal(ids)
	if err != nil {
		return 0, err
	}
	var count int
	err = db.QueryRowContext(ctx, `SELECT count(*) FROM notifications n JOIN schedules s ON s.id = n.schedule_id
		WHERE n.id IN (SELECT value FROM json_each(?)) AND n.deleted = 0 AND n.delivery_stopped = 0
		AND n.acknowledged_at IS NULL AND n.snapshot_taken = 1 AND n.name_snapshot IS NOT NULL
		AND n.notify_time <= ? AND n.notify_time % 60000000000 = 0 AND n.status IN ('loaded', 'failed')
		AND s.deleted = 0 AND s.cancelled = 0 AND (s.type = 'single' OR s.period_open = 1)
		AND (s.type != 'single' OR s.single_notify_time % 60000000000 = 0)`, string(data), now.UnixNano()).Scan(&count)
	return count, err
}

// ReadNotificationMembers 在一个读取事务内分页，单个 JSON 参数避免 SQLite 变量上限。
func (s *NotificationStore) ReadNotificationMembers(ctx context.Context, ids []int, after model.NotificationPosition, limit int) ([]*model.Schedule, []*model.Notification, int, error) {
	if limit < 1 || limit > 51 {
		return nil, nil, 0, fmt.Errorf("invalid member page size")
	}
	db, err := s.db.handle()
	if err != nil {
		return nil, nil, 0, err
	}
	data, err := json.Marshal(ids)
	if err != nil {
		return nil, nil, 0, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, 0, err
	}
	defer tx.Rollback()
	filter := ` WHERE deleted = 0 AND id IN (SELECT value FROM json_each(?))`
	var total int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM notifications`+filter, string(data)).Scan(&total); err != nil {
		return nil, nil, 0, err
	}
	args := []any{string(data)}
	if after.ID > 0 {
		filter += ` AND (notify_time > ? OR (notify_time = ? AND id > ?))`
		args = append(args, after.Time.UnixNano(), after.Time.UnixNano(), after.ID)
	}
	rows, err := tx.QueryContext(ctx, notificationSelect+filter+` ORDER BY notify_time, id LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, nil, 0, err
	}
	values, err := scanNotifications(rows)
	rows.Close()
	if err != nil {
		return nil, nil, 0, err
	}
	rules := make([]*model.Schedule, 0)
	seen := make(map[int]bool)
	for _, n := range values {
		if seen[n.ScheduleID] {
			continue
		}
		seen[n.ScheduleID] = true
		rule, err := scanSchedule(tx.QueryRowContext(ctx, scheduleSelect+` WHERE id = ?`, n.ScheduleID))
		if err != nil {
			return nil, nil, 0, err
		}
		rules = append(rules, rule)
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, 0, err
	}
	return rules, values, total, nil
}
