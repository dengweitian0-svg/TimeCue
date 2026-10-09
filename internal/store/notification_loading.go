package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/model"
)

// PrepareNotifications 在同一写事务内计算完整候选，预加载或冻结到期事实。
func (s *NotificationStore) PrepareNotifications(ctx context.Context, window model.NotificationWindow, prepare model.PrepareNotificationWindow) (*model.NotificationBatch, error) {
	if prepare == nil || window.Start.IsZero() || window.AsOf.IsZero() || !window.End.After(window.Start) || window.End.Sub(window.Start) > 2*time.Minute || window.ScheduleID < 0 {
		return nil, fmt.Errorf("invalid notification preparation window")
	}
	if window.FreezeDue && (window.Start.Second() != 0 || window.Start.Nanosecond() != 0 || window.End.Sub(window.Start) != time.Minute || window.Start.After(window.AsOf)) {
		return nil, fmt.Errorf("invalid due minute")
	}
	db, err := s.db.handle()
	if err != nil {
		return nil, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// 先取得写锁，规则变更与候选计算不能交错。
	if _, err = tx.ExecContext(ctx, `UPDATE schedules SET revision = revision WHERE id = -1`); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT schedule_id, revision, effective_from, effective_until,
		name, type, description, period_open, period_cron, single_notify_time, cancelled, deleted
		FROM schedule_versions WHERE (? = 0 OR schedule_id = ?) AND
		((effective_from < ? AND (effective_until IS NULL OR effective_until >= ?)) OR
		(effective_from >= ? AND effective_from <= ? AND (cancelled = 1 OR deleted = 1 OR period_open = 0)))
		ORDER BY schedule_id, revision`, window.ScheduleID, window.ScheduleID, window.End.UnixNano(), window.Start.UnixNano(), window.Start.UnixNano(), window.AsOf.UnixNano())
	if err != nil {
		return nil, err
	}
	versions, err := readScheduleVersions(rows)
	rows.Close()
	if err != nil {
		return nil, err
	}
	candidates, err := prepare(versions, window.Start, window.End)
	if err != nil {
		return nil, err
	}
	stops := make(map[int]time.Time)
	for _, v := range versions {
		if (v.Schedule.Deleted || v.Schedule.Cancelled || (v.Schedule.Period != nil && !v.Schedule.Period.Open)) && !v.From.After(window.AsOf) {
			if v.From.After(stops[v.Schedule.ID]) {
				stops[v.Schedule.ID] = v.From
			}
		}
	}
	for _, c := range candidates {
		if c.NotifyTime.Before(window.Start) || !c.NotifyTime.Before(window.End) || (window.ScheduleID != 0 && c.ScheduleID != window.ScheduleID) {
			return nil, fmt.Errorf("candidate outside notification window")
		}
		stopped := !stops[c.ScheduleID].Before(c.NotifyTime)
		freeze := window.FreezeDue && !c.NotifyTime.After(window.AsOf)
		var name, description any
		if freeze {
			name, description = c.Name, c.Description
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO notifications
			(schedule_id, notify_time, delivery_stopped, snapshot_taken, name_snapshot, description_snapshot)
			VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(schedule_id, notify_time) DO NOTHING`, c.ScheduleID, c.NotifyTime.UnixNano(), stopped, freeze, name, description); err != nil {
			return nil, err
		}
		if stopped {
			if _, err = tx.ExecContext(ctx, `UPDATE notifications SET delivery_stopped = 1 WHERE schedule_id = ? AND notify_time = ?`, c.ScheduleID, c.NotifyTime.UnixNano()); err != nil {
				return nil, err
			}
		}
		if freeze {
			if _, err = tx.ExecContext(ctx, `UPDATE notifications SET snapshot_taken = 1, name_snapshot = ?, description_snapshot = ?
				WHERE schedule_id = ? AND notify_time = ? AND snapshot_taken = 0`, name, description, c.ScheduleID, c.NotifyTime.UnixNano()); err != nil {
				return nil, err
			}
		}
	}
	// 加载不消费展示预算，不把 failed 重扫成 loaded。
	eligible := `notify_time >= ? AND notify_time < ? AND (? = 0 OR schedule_id = ?)
		AND notify_time % 60000000000 = 0 AND deleted = 0 AND delivery_stopped = 0 AND acknowledged_at IS NULL
		AND EXISTS (SELECT 1 FROM schedules s WHERE s.id = notifications.schedule_id AND s.deleted = 0 AND s.cancelled = 0
		AND (s.type = 'single' OR s.period_open = 1) AND (s.type != 'single' OR s.single_notify_time % 60000000000 = 0))`
	args := []any{window.Start.UnixNano(), window.End.UnixNano(), window.ScheduleID, window.ScheduleID}
	if _, err = tx.ExecContext(ctx, `UPDATE notifications SET status = 'loaded' WHERE status = 'pending' AND `+eligible, args...); err != nil {
		return nil, err
	}
	rows, err = tx.QueryContext(ctx, notificationSelect+` WHERE notify_time >= ? AND notify_time < ? AND (? = 0 OR schedule_id = ?) ORDER BY notify_time, id`, args...)
	if err != nil {
		return nil, err
	}
	notifications, err := scanNotifications(rows)
	rows.Close()
	if err != nil {
		return nil, err
	}
	if window.FreezeDue {
		eligible += ` AND snapshot_taken = 1`
	}
	rows, err = tx.QueryContext(ctx, `SELECT id FROM notifications WHERE status = 'loaded' AND `+eligible+` ORDER BY notify_time, id`, args...)
	if err != nil {
		return nil, err
	}
	var ids []int
	for rows.Next() {
		var id int
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &model.NotificationBatch{Notifications: notifications, EligibleIDs: ids}, nil
}

func readScheduleVersions(rows *sql.Rows) ([]model.ScheduleVersion, error) {
	var result []model.ScheduleVersion
	for rows.Next() {
		var v model.ScheduleVersion
		var from int64
		var until, open, single sql.NullInt64
		var description, cron sql.NullString
		var cancelled, deleted bool
		s := &v.Schedule
		if err := rows.Scan(&s.ID, &s.Revision, &from, &until, &s.Name, &s.Type, &description, &open, &cron, &single, &cancelled, &deleted); err != nil {
			return nil, err
		}
		v.From = time.Unix(0, from).In(model.BeijingLocation())
		if until.Valid {
			at := time.Unix(0, until.Int64).In(model.BeijingLocation())
			v.Until = &at
		}
		s.Cancelled, s.Deleted = cancelled, deleted
		if description.Valid {
			s.Description = &description.String
		}
		if s.Type == model.PeriodSchedule {
			s.Period = &model.Period{Open: open.Int64 != 0}
			if err := json.Unmarshal([]byte(cron.String), &s.Period.Cron); err != nil {
				return nil, err
			}
		} else {
			s.Single = &model.Single{NotifyTime: time.Unix(0, single.Int64).In(model.BeijingLocation())}
		}
		result = append(result, v)
	}
	return result, rows.Err()
}
