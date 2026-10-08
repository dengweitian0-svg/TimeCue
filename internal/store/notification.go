package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/model"
)

// NotificationStore 负责提醒记录及其投递、确认和删除状态。
type NotificationStore struct {
	db *Sqlite
}

// NewNotificationStore 创建使用指定数据库的提醒存储。
func NewNotificationStore(db *Sqlite) (*NotificationStore, error) {
	if db == nil {
		return nil, fmt.Errorf("[store] cannot create notification store, database is nil")
	}
	return &NotificationStore{db: db}, nil
}

// CreateNotification 检查日程状态并幂等地创建一条提醒。
func (s *NotificationStore) CreateNotification(ctx context.Context, scheduleID int, notifyAt time.Time) (*model.Notification, error) {
	if scheduleID <= 0 {
		return nil, fmt.Errorf("[store] schedule ID must be positive, schedule_id=%d", scheduleID)
	}
	if notifyAt.IsZero() {
		return nil, fmt.Errorf("[store] notification time must not be zero, notify_at=%q", notifyAt.Format(time.RFC3339Nano))
	}
	db, err := s.db.handle()
	if err != nil {
		return nil, fmt.Errorf("[store] cannot create notification, schedule_id=%d, notify_at=%q: %w", scheduleID, notifyAt.UTC().Format(time.RFC3339Nano), err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("[store] failed to begin notification creation, schedule_id=%d, notify_at=%q: %w", scheduleID, notifyAt.UTC().Format(time.RFC3339Nano), err)
	}
	defer tx.Rollback()
	// 在同一事务内检查日程状态并写入提醒，避免状态变化与写入交错。
	var scheduleType model.ScheduleType
	var periodOpen, singleTime sql.NullInt64
	var cancelled, deleted int
	err = tx.QueryRowContext(ctx, `SELECT type, period_open, single_notify_time, cancelled, deleted
		FROM schedules WHERE id = ?`, scheduleID).Scan(&scheduleType, &periodOpen, &singleTime, &cancelled, &deleted)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("[store] schedule not found, schedule_id=%d: %w", scheduleID, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("[store] failed to read schedule state, schedule_id=%d: %w", scheduleID, err)
	}
	if deleted != 0 {
		return nil, fmt.Errorf("[store] cannot create notification for deleted schedule, schedule_id=%d: %w", scheduleID, ErrScheduleDeleted)
	}
	if cancelled != 0 {
		return nil, fmt.Errorf("[store] cannot create notification for cancelled schedule, schedule_id=%d: %w", scheduleID, ErrScheduleCancelled)
	}
	if scheduleType == model.PeriodSchedule && periodOpen.Int64 == 0 {
		return nil, fmt.Errorf("[store] cannot create notification for paused schedule, schedule_id=%d: %w", scheduleID, ErrSchedulePaused)
	}
	if scheduleType == model.SingleSchedule && singleTime.Int64 != notifyAt.UTC().UnixNano() {
		return nil, fmt.Errorf("[store] notification time does not match single schedule, schedule_id=%d, notify_at=%q, scheduled_at=%q", scheduleID, notifyAt.UTC().Format(time.RFC3339Nano), time.Unix(0, singleTime.Int64).UTC().Format(time.RFC3339Nano))
	}
	// 唯一约束保证同一日程和触发时间只保存一条提醒。
	_, err = tx.ExecContext(ctx, `INSERT INTO notifications (schedule_id, notify_time)
		VALUES (?, ?) ON CONFLICT(schedule_id, notify_time) DO NOTHING`, scheduleID, notifyAt.UTC().UnixNano())
	if err != nil {
		return nil, fmt.Errorf("[store] failed to create notification, schedule_id=%d, notify_at=%q: %w", scheduleID, notifyAt.UTC().Format(time.RFC3339Nano), err)
	}
	notification, err := scanNotification(tx.QueryRowContext(ctx, notificationSelect+
		` WHERE schedule_id = ? AND notify_time = ?`, scheduleID, notifyAt.UTC().UnixNano()))
	if err != nil {
		return nil, fmt.Errorf("[store] failed to read created notification, schedule_id=%d, notify_at=%q: %w", scheduleID, notifyAt.UTC().Format(time.RFC3339Nano), err)
	}
	if notification.Deleted {
		return nil, fmt.Errorf("[store] existing notification is deleted, notification_id=%d, schedule_id=%d: %w", notification.ID, scheduleID, ErrNotificationDeleted)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("[store] failed to commit notification creation, schedule_id=%d, notify_at=%q: %w", scheduleID, notifyAt.UTC().Format(time.RFC3339Nano), err)
	}
	return notification, nil
}

// GetNotificationByStartTime 返回半开区间内可投递的待处理提醒。
func (s *NotificationStore) GetNotificationByStartTime(ctx context.Context, startTime, endTime time.Time) ([]*model.Notification, error) {
	if !endTime.After(startTime) {
		return nil, fmt.Errorf("[store] end time must be after start time, start_time=%q, end_time=%q", startTime.UTC().Format(time.RFC3339Nano), endTime.UTC().Format(time.RFC3339Nano))
	}
	db, err := s.db.handle()
	if err != nil {
		return nil, fmt.Errorf("[store] cannot list notifications, start_time=%q, end_time=%q: %w", startTime.UTC().Format(time.RFC3339Nano), endTime.UTC().Format(time.RFC3339Nano), err)
	}
	// 日程已取消、删除或暂停时，即使提醒仍在库中也不可投递。
	rows, err := db.QueryContext(ctx, `SELECT n.id, n.schedule_id, n.status, n.notify_time, n.acknowledged_at, n.deleted
		FROM notifications n JOIN schedules s ON s.id = n.schedule_id
		WHERE n.notify_time >= ? AND n.notify_time < ? AND n.status = 'pending'
		AND n.acknowledged_at IS NULL AND n.deleted = 0 AND s.deleted = 0 AND s.cancelled = 0
		AND (s.type = 'single' OR s.period_open = 1)
		ORDER BY n.notify_time, n.id`, startTime.UTC().UnixNano(), endTime.UTC().UnixNano())
	if err != nil {
		return nil, fmt.Errorf("[store] failed to list pending notifications, start_time=%q, end_time=%q: %w", startTime.UTC().Format(time.RFC3339Nano), endTime.UTC().Format(time.RFC3339Nano), err)
	}
	defer rows.Close()
	notifications, err := scanNotifications(rows)
	if err != nil {
		return nil, fmt.Errorf("[store] failed to scan pending notifications, start_time=%q, end_time=%q: %w", startTime.UTC().Format(time.RFC3339Nano), endTime.UTC().Format(time.RFC3339Nano), err)
	}
	return notifications, nil
}

// GetNotificationByID 包含已软删除提醒，用于历史和状态检查。
func (s *NotificationStore) GetNotificationByID(ctx context.Context, notificationID int) (*model.Notification, error) {
	db, err := s.db.handle()
	if err != nil {
		return nil, fmt.Errorf("[store] cannot get notification, notification_id=%d: %w", notificationID, err)
	}
	notification, err := scanNotification(db.QueryRowContext(ctx, notificationSelect+` WHERE id = ?`, notificationID))
	if err != nil {
		return nil, fmt.Errorf("[store] failed to get notification, notification_id=%d: %w", notificationID, err)
	}
	return notification, nil
}

// GetNotificationHistoryByScheduleID 返回指定日程的所有提醒，包含已软删除记录。
func (s *NotificationStore) GetNotificationHistoryByScheduleID(ctx context.Context, scheduleID int) ([]*model.Notification, error) {
	db, err := s.db.handle()
	if err != nil {
		return nil, fmt.Errorf("[store] cannot list notification history, schedule_id=%d: %w", scheduleID, err)
	}
	rows, err := db.QueryContext(ctx, notificationSelect+` WHERE schedule_id = ? ORDER BY notify_time DESC, id DESC`, scheduleID)
	if err != nil {
		return nil, fmt.Errorf("[store] failed to list notification history, schedule_id=%d: %w", scheduleID, err)
	}
	defer rows.Close()
	notifications, err := scanNotifications(rows)
	if err != nil {
		return nil, fmt.Errorf("[store] failed to scan notification history, schedule_id=%d: %w", scheduleID, err)
	}
	return notifications, nil
}

// SetDeliveryResult 只接受投递成功或失败；已成功投递的提醒不能回退。
func (s *NotificationStore) SetDeliveryResult(ctx context.Context, notificationID int, status model.NotifyStatus) error {
	if status != model.NotifySent && status != model.NotifyFailed {
		return fmt.Errorf("[store] delivery status must be sent or failed, notification_id=%d, status=%q", notificationID, status)
	}
	db, err := s.db.handle()
	if err != nil {
		return fmt.Errorf("[store] cannot set delivery result, notification_id=%d, status=%q: %w", notificationID, status, err)
	}
	result, err := db.ExecContext(ctx, `UPDATE notifications SET status = ?
		WHERE id = ? AND deleted = 0 AND status IN ('pending', 'failed')`, status, notificationID)
	if err != nil {
		return fmt.Errorf("[store] failed to set delivery result, notification_id=%d, status=%q: %w", notificationID, status, err)
	}
	if err := requireAffected(result); err != nil {
		if !errors.Is(err, ErrNotFound) {
			return fmt.Errorf("[store] failed to inspect delivery update, notification_id=%d, status=%q: %w", notificationID, status, err)
		}
		return fmt.Errorf("[store] cannot set delivery result, notification_id=%d, status=%q: %w", notificationID, status, s.notificationTransitionError(ctx, notificationID))
	}
	return nil
}

// ConfirmNotification 记录用户首次确认时间，不改变投递结果。
func (s *NotificationStore) ConfirmNotification(ctx context.Context, notificationID int, confirmedAt time.Time) error {
	_, err := s.AcknowledgeNotifications(ctx, []int{notificationID}, confirmedAt)
	return err
}

// DeleteNotificationByID 仅标记提醒已删除，保留历史数据。
func (s *NotificationStore) DeleteNotificationByID(ctx context.Context, notificationID int) error {
	db, err := s.db.handle()
	if err != nil {
		return fmt.Errorf("[store] cannot connect to sqlite, notification_id=%d: %w", notificationID, err)
	}
	result, err := db.ExecContext(ctx, `UPDATE notifications SET deleted = 1 WHERE id = ? AND deleted = 0`, notificationID)
	if err != nil {
		return fmt.Errorf("[store] failed to delete notification, notification_id=%d: %w", notificationID, err)
	}
	if err := requireAffected(result); err != nil {
		return fmt.Errorf("[store] failed to delete notification, notification_id=%d: %w", notificationID, err)
	}
	return nil
}

func (s *NotificationStore) notificationTransitionError(ctx context.Context, notificationID int) error {
	db, err := s.db.handle()
	if err != nil {
		return err
	}
	notification, err := scanNotification(db.QueryRowContext(ctx, notificationSelect+` WHERE id = ?`, notificationID))
	if err != nil {
		return err
	}
	if notification.Deleted {
		return ErrNotificationDeleted
	}
	return ErrInvalidTransition
}

const notificationSelect = `SELECT id, schedule_id, status, notify_time, acknowledged_at, deleted FROM notifications`

func scanNotification(row scanner) (*model.Notification, error) {
	var notification model.Notification
	var notifyTime int64
	var acknowledgedAt sql.NullInt64
	var deleted int
	err := row.Scan(&notification.ID, &notification.ScheduleID, &notification.Status,
		&notifyTime, &acknowledgedAt, &deleted)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("[store] failed to scan notification: %w", err)
	}
	notification.NotifyTime = time.Unix(0, notifyTime).In(model.BeijingLocation())
	if acknowledgedAt.Valid {
		at := time.Unix(0, acknowledgedAt.Int64).In(model.BeijingLocation())
		notification.AcknowledgedAt = &at
	}
	notification.Deleted = deleted != 0
	return &notification, nil
}

func scanNotifications(rows *sql.Rows) ([]*model.Notification, error) {
	result := make([]*model.Notification, 0)
	for rows.Next() {
		notification, err := scanNotification(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, notification)
	}
	return result, rows.Err()
}

// AcknowledgeNotifications 先校验全部成员，再原子保存各条首次确认事实。
func (s *NotificationStore) AcknowledgeNotifications(ctx context.Context, ids []int, at time.Time) (result []*model.Notification, err error) {
	defer func() {
		if err != nil {
			err = fmt.Errorf("[store] cannot acknowledge notifications, count=%d, confirmed_at=%q: %w", len(ids), at.Format(time.RFC3339Nano), err)
		}
	}()
	if at.IsZero() || len(ids) == 0 || len(ids) > 500 {
		return nil, model.ErrInvalidAcknowledgement
	}
	db, err := s.db.handle()
	if err != nil {
		return nil, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("[store] cannot begin acknowledgement: %w", err)
	}
	defer tx.Rollback()
	result = make([]*model.Notification, 0, len(ids))
	seen := map[int]bool{}
	for _, id := range ids {
		if id <= 0 {
			return nil, model.ErrInvalidAcknowledgement
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		n, err := scanNotification(tx.QueryRowContext(ctx, notificationSelect+` WHERE id = ?`, id))
		if err != nil {
			return nil, fmt.Errorf("[store] cannot read acknowledgement member, notification_id=%d: %w", id, err)
		}
		if n.Deleted || n.NotifyTime.After(at) {
			return nil, model.ErrInvalidAcknowledgement
		}
		result = append(result, n)
	}
	for _, n := range result {
		if n.AcknowledgedAt != nil {
			continue
		}
		if _, err = tx.ExecContext(ctx, `UPDATE notifications SET acknowledged_at = ? WHERE id = ? AND acknowledged_at IS NULL`, at.UnixNano(), n.ID); err != nil {
			return nil, err
		}
		value := at.In(model.BeijingLocation())
		n.AcknowledgedAt = &value
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}
