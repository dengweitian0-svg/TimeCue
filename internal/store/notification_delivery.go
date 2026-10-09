package store

import (
	"context"
	"fmt"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/model"
)

// ReadDeliveryMembers 在同一事务核验成员；重试许可才允许 failed 转 loaded。
func (s *NotificationStore) ReadDeliveryMembers(ctx context.Context, ids []int, now time.Time, reload bool) ([]*model.Notification, error) {
	if now.IsZero() {
		return nil, fmt.Errorf("delivery time is zero")
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
	if _, err = tx.ExecContext(ctx, `UPDATE schedules SET revision = revision WHERE id = -1`); err != nil {
		return nil, err
	}
	values := make([]*model.Notification, 0, len(ids))
	seen := make(map[int]bool, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return nil, fmt.Errorf("invalid delivery member")
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		n, err := scanNotification(tx.QueryRowContext(ctx, notificationSelect+` WHERE id = ?`, id))
		if err != nil {
			return nil, err
		}
		if n.Deleted || n.DeliveryStopped || n.AcknowledgedAt != nil || !n.SnapshotTaken || n.NameSnapshot == nil || n.NotifyTime.After(now) || n.NotifyTime.Second() != 0 || n.NotifyTime.Nanosecond() != 0 {
			continue
		}
		if n.Status != model.NotifyLoaded && !(reload && n.Status == model.NotifyFailed) {
			continue
		}
		var eligible bool
		if err = tx.QueryRowContext(ctx, `SELECT deleted = 0 AND cancelled = 0 AND (type = 'single' OR period_open = 1) FROM schedules WHERE id = ?`, n.ScheduleID).Scan(&eligible); err != nil {
			return nil, err
		}
		if !eligible {
			continue
		}
		if reload && n.Status == model.NotifyFailed {
			if _, err = tx.ExecContext(ctx, `UPDATE notifications SET status = 'loaded' WHERE id = ? AND status = 'failed'`, id); err != nil {
				return nil, err
			}
			n.Status = model.NotifyLoaded
		}
		values = append(values, n)
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return values, nil
}

// SaveDeliveryResult 原子回填冻结集合，保留停止、确认及历史文本，sent 不降级。
func (s *NotificationStore) SaveDeliveryResult(ctx context.Context, ids []int, status model.NotifyStatus) error {
	if len(ids) == 0 || (status != model.NotifySent && status != model.NotifyFailed) {
		return fmt.Errorf("invalid delivery result")
	}
	db, err := s.db.handle()
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE schedules SET revision = revision WHERE id = -1`); err != nil {
		return err
	}
	for _, id := range ids {
		n, err := scanNotification(tx.QueryRowContext(ctx, notificationSelect+` WHERE id = ?`, id))
		if err != nil {
			return err
		}
		if n.Deleted || !n.SnapshotTaken || (n.Status != model.NotifyLoaded && n.Status != model.NotifyFailed && n.Status != model.NotifySent) {
			return ErrInvalidTransition
		}
		if n.Status == model.NotifySent {
			continue
		}
		if _, err = tx.ExecContext(ctx, `UPDATE notifications SET status = ? WHERE id = ? AND status IN ('loaded','failed')`, status, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
