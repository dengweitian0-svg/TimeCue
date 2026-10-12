package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/model"
)

const recoveryVersionSelect = `SELECT schedule_id, revision, effective_from, effective_until,
	name, type, description, period_open, period_cron, single_notify_time, cancelled, deleted FROM schedule_versions`

// RecoverNotifications 在写锁内准备和提交有界历史批次，失败不公布继续位置。
func (s *NotificationStore) RecoverNotifications(ctx context.Context, w model.RecoveryWindow, prepare model.PrepareRecoveryVersion) (*model.RecoveryBatch, error) {
	if prepare == nil || w.Start.IsZero() || !w.End.After(w.Start) || w.AsOf.IsZero() {
		return nil, fmt.Errorf("invalid recovery window")
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
	batch := &model.RecoveryBatch{Next: w.Position}
	if !w.Position.Facts {
		rows, err := tx.QueryContext(ctx, recoveryVersionSelect+` WHERE effective_from < ?
			AND (effective_until IS NULL OR effective_until >= ?)
			AND (schedule_id > ? OR (schedule_id = ? AND revision >= ?))
			ORDER BY schedule_id, revision LIMIT 64`, w.End.UnixNano(), w.Start.UnixNano(), w.Position.ScheduleID, w.Position.ScheduleID, w.Position.Revision)
		if err != nil {
			return nil, err
		}
		versions, err := readScheduleVersions(rows)
		rows.Close()
		if err != nil {
			return nil, err
		}
		for _, v := range versions {
			after := time.Time{}
			if v.Schedule.ID == w.Position.ScheduleID && v.Schedule.Revision == w.Position.Revision {
				after = w.Position.After
			}
			end := w.End
			// 回拨后不补造此刻尚未到期的事实，继续位置保留原版本。
			if end.After(w.AsOf.Add(time.Nanosecond)) {
				end = w.AsOf.Add(time.Nanosecond)
			}
			if !end.After(w.Start) {
				return nil, fmt.Errorf("recovery clock is before window")
			}
			candidates, next, done, err := prepare(ctx, v, w.Start, end, after, 500-len(batch.Notifications))
			if err != nil {
				return nil, err
			}
			if len(candidates) > 500-len(batch.Notifications) {
				return nil, fmt.Errorf("recovery candidate batch exceeds 500")
			}
			for _, c := range candidates {
				if c.ScheduleID != v.Schedule.ID || c.NotifyTime.Before(w.Start) || !c.NotifyTime.Before(end) {
					return nil, fmt.Errorf("recovery candidate outside version window")
				}
				if _, err = tx.ExecContext(ctx, `INSERT INTO notifications (schedule_id, notify_time, snapshot_taken, name_snapshot, description_snapshot)
					VALUES (?, ?, 1, ?, ?) ON CONFLICT(schedule_id, notify_time) DO NOTHING`, c.ScheduleID, c.NotifyTime.UnixNano(), c.Name, c.Description); err != nil {
					return nil, err
				}
				if _, err = tx.ExecContext(ctx, `UPDATE notifications SET snapshot_taken = 1, name_snapshot = ?, description_snapshot = ?
					WHERE schedule_id = ? AND notify_time = ? AND snapshot_taken = 0 AND deleted = 0`, c.Name, c.Description, c.ScheduleID, c.NotifyTime.UnixNano()); err != nil {
					return nil, err
				}
				n, err := scanNotification(tx.QueryRowContext(ctx, notificationSelect+` WHERE schedule_id = ? AND notify_time = ?`, c.ScheduleID, c.NotifyTime.UnixNano()))
				if err != nil {
					return nil, err
				}
				n, err = reconcileRecoveryNotification(ctx, tx, n, w.AsOf)
				if err != nil {
					return nil, err
				}
				batch.Notifications = append(batch.Notifications, n)
			}
			batch.Next = model.RecoveryPosition{ScheduleID: v.Schedule.ID, Revision: v.Schedule.Revision, After: next}
			if done {
				// 若因回拨截断了窗口，不能跳过原终点之前的剩余触发。
				if end.Before(w.End) {
					break
				}
				batch.Next.Revision++
				batch.Next.After = time.Time{}
			}
			if !done || len(batch.Notifications) == 500 {
				break
			}
		}
		if len(versions) == 0 {
			batch.Next = model.RecoveryPosition{Facts: true}
		}
	} else {
		p := w.Position.Fact
		rows, err := tx.QueryContext(ctx, notificationSelect+` WHERE deleted = 0 AND notify_time % 60000000000 = 0
			AND notify_time >= ? AND notify_time < ? AND notify_time <= ?
			AND (notify_time > ? OR (notify_time = ? AND id > ?)) ORDER BY notify_time, id LIMIT 500`,
			w.Start.UnixNano(), w.End.UnixNano(), w.AsOf.UnixNano(), p.Time.UnixNano(), p.Time.UnixNano(), p.ID)
		if err != nil {
			return nil, err
		}
		values, err := scanNotifications(rows)
		rows.Close()
		if err != nil {
			return nil, err
		}
		for _, n := range values {
			value, err := reconcileRecoveryNotification(ctx, tx, n, w.AsOf)
			if err != nil {
				return nil, err
			}
			batch.Notifications = append(batch.Notifications, value)
			batch.Next.Fact = model.NotificationPosition{Time: n.NotifyTime, ID: n.ID}
		}
		batch.Done = len(values) < 500 && !w.AsOf.Before(w.End.Add(-time.Nanosecond))
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return batch, nil
}

// reconcileRecoveryNotification 保留永久停止和历史结果，不许可 failed 重加载。
func reconcileRecoveryNotification(ctx context.Context, tx *sql.Tx, n *model.Notification, now time.Time) (*model.Notification, error) {
	if n.Deleted {
		return n, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE notifications SET delivery_stopped = 1 WHERE id = ? AND EXISTS
		(SELECT 1 FROM schedule_versions v WHERE v.schedule_id = notifications.schedule_id
		AND v.effective_from >= notifications.notify_time AND v.effective_from <= ?
		AND (v.deleted = 1 OR v.cancelled = 1 OR v.period_open = 0))`, n.ID, now.UnixNano()); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE notifications SET status = 'loaded' WHERE id = ? AND status = 'pending'
		AND delivery_stopped = 0 AND acknowledged_at IS NULL AND snapshot_taken = 1 AND name_snapshot IS NOT NULL
		AND EXISTS (SELECT 1 FROM schedules s WHERE s.id = notifications.schedule_id AND s.deleted = 0
		AND s.cancelled = 0 AND (s.type = 'single' OR s.period_open = 1)
		AND (s.type != 'single' OR s.single_notify_time % 60000000000 = 0))`, n.ID); err != nil {
		return nil, err
	}
	return scanNotification(tx.QueryRowContext(ctx, notificationSelect+` WHERE id = ?`, n.ID))
}
