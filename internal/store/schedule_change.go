package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/model"
)

// ApplyScheduleChange 串行检查版本、保存历史、切换未来提醒并持久化操作回执。
func (s *ScheduleStore) ApplyScheduleChange(ctx context.Context, change model.ScheduleChange, prepare model.PrepareScheduleChange) (*model.ScheduleWrite, error) {
	db, err := s.db.handle()
	if err != nil {
		return nil, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// 先取得写锁，再读取规则，独立连接的并发编辑也由 SQLite 裁决。
	if _, err = tx.ExecContext(ctx, `UPDATE schedules SET revision = revision WHERE id = ?`, change.Schedule.ID); err != nil {
		return nil, err
	}
	if change.Key != "" {
		if saved, err := readScheduleReceipt(ctx, tx, change.Key, change.Digest); err != nil || saved != nil {
			return saved, err
		}
	}
	now := s.now().In(model.BeijingLocation())
	schedule := change.Schedule
	if schedule.Single != nil {
		single := *schedule.Single
		single.NotifyTime = single.NotifyTime.In(model.BeijingLocation())
		schedule.Single = &single
	}
	var current *model.Schedule
	if schedule.ID != 0 {
		current, err = scanSchedule(tx.QueryRowContext(ctx, scheduleSelect+` WHERE id = ? AND deleted = 0`, schedule.ID))
		if err != nil {
			return nil, err
		}
		if current.Revision != change.ExpectedRevision {
			return nil, model.ErrScheduleConflict
		}
	}
	if err = schedule.Validate(); err != nil {
		return nil, err
	}
	next, err := prepare(current, now)
	if err != nil {
		return nil, err
	}
	if next != nil {
		if err = model.ValidateUnixNanoTime(*next); err != nil {
			return nil, err
		}
	}
	if current == nil {
		schedule.Revision = 1
		schedule.CreatedAt = now
	} else {
		schedule.Revision = current.Revision + 1
		schedule.CreatedAt = current.CreatedAt
		// 到期内容在变更前冻结；未投递和投递失败同样保存原文。
		if err = freezeDue(ctx, tx, schedule.ID, now); err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE notifications SET deleted = 1
            WHERE schedule_id = ? AND notify_time > ? AND status IN ('pending', 'loaded') AND acknowledged_at IS NULL AND snapshot_taken = 0`, schedule.ID, now.UnixNano()); err != nil {
			return nil, err
		}
		if schedule.Period != nil && !schedule.Period.Open {
			if _, err = tx.ExecContext(ctx, `UPDATE notifications SET delivery_stopped = 1 WHERE schedule_id = ?`, schedule.ID); err != nil {
				return nil, err
			}
		}
		if _, err = tx.ExecContext(ctx, `UPDATE schedule_versions SET effective_until = ? WHERE schedule_id = ? AND effective_until IS NULL`, now.UnixNano(), schedule.ID); err != nil {
			return nil, err
		}
	}
	schedule.UpdatedAt = now
	var open, cron, single any
	if schedule.Period != nil {
		open = schedule.Period.Open
		encoded, _ := json.Marshal(schedule.Period.Cron)
		cron = string(encoded)
	} else {
		single = schedule.Single.NotifyTime.UnixNano()
	}
	if current == nil {
		result, e := tx.ExecContext(ctx, `INSERT INTO schedules (name, type, description, period_open, period_cron, single_notify_time, created_at, updated_at, revision)
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, schedule.Name, schedule.Type, schedule.Description, open, cron, single, now.UnixNano(), now.UnixNano(), schedule.Revision)
		if e != nil {
			return nil, e
		}
		id, e := result.LastInsertId()
		if e != nil {
			return nil, e
		}
		schedule.ID = int(id)
	} else {
		if _, err = tx.ExecContext(ctx, `UPDATE schedules SET name = ?, description = ?, period_open = ?, period_cron = ?, single_notify_time = ?, updated_at = ?, revision = ? WHERE id = ?`,
			schedule.Name, schedule.Description, open, cron, single, now.UnixNano(), schedule.Revision, schedule.ID); err != nil {
			return nil, err
		}
	}
	if err = saveScheduleVersion(ctx, tx, schedule.ID, now); err != nil {
		return nil, err
	}
	result := &model.ScheduleWrite{Schedule: &schedule, EffectiveAt: now, Before: current}
	if next != nil {
		// 仅复用已撤销、仍在未来且未触发的候选；已执行或确认的事实不能复活。
		if _, err = tx.ExecContext(ctx, `INSERT INTO notifications (schedule_id, notify_time) VALUES (?, ?)
            ON CONFLICT(schedule_id, notify_time) DO UPDATE SET deleted = 0, delivery_stopped = 0
            WHERE notifications.deleted = 1 AND notifications.status IN ('pending', 'loaded') AND notifications.acknowledged_at IS NULL AND notifications.snapshot_taken = 0 AND notifications.notify_time > ?`, schedule.ID, next.UnixNano(), now.UnixNano()); err != nil {
			return nil, err
		}
		result.Notification, err = scanNotification(tx.QueryRowContext(ctx, notificationSelect+` WHERE schedule_id = ? AND notify_time = ?`, schedule.ID, next.UnixNano()))
		if err != nil {
			return nil, err
		}
	}
	if change.Key != "" {
		encoded, e := json.Marshal(result)
		if e != nil {
			return nil, e
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO schedule_write_receipts (operation_key, request_digest, result_json) VALUES (?, ?, ?)`, change.Key, change.Digest, string(encoded)); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

func freezeDue(ctx context.Context, tx *sql.Tx, id int, now time.Time) error {
	_, err := tx.ExecContext(ctx, `UPDATE notifications SET snapshot_taken = 1,
        name_snapshot = (SELECT name FROM schedules WHERE id = schedule_id),
        description_snapshot = (SELECT description FROM schedules WHERE id = schedule_id)
        WHERE schedule_id = ? AND notify_time <= ? AND snapshot_taken = 0`, id, now.UnixNano())
	return err
}

func saveScheduleVersion(ctx context.Context, tx *sql.Tx, id int, now time.Time) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO schedule_versions
        SELECT id, revision, ?, NULL, name, type, description, period_open, period_cron, single_notify_time, cancelled, deleted FROM schedules WHERE id = ?`, now.UnixNano(), id)
	return err
}

// stopSchedule 保留删除之前的规则依据和到期文本，并永久停止旧投递。
func (s *ScheduleStore) stopSchedule(ctx context.Context, id int, deleted bool) (*model.ScheduleWrite, error) {
	return s.stopScheduleWithReceipt(ctx, id, deleted, nil)
}

// ApplyScheduleDelete 复用正式停止事务，在写锁内优先核对回执再检查版本。
func (s *ScheduleStore) ApplyScheduleDelete(ctx context.Context, request model.ScheduleDelete) (*model.ScheduleWrite, error) {
	return s.stopScheduleWithReceipt(ctx, request.ID, true, &request)
}

// ReadScheduleReceipt 使用独立 SELECT 核对原回执，未命中不写入任何记录。
func (s *ScheduleStore) ReadScheduleReceipt(ctx context.Context, key, digest string) (*model.ScheduleWrite, error) {
	db, err := s.db.handle()
	if err != nil {
		return nil, err
	}
	return readScheduleReceipt(ctx, db, key, digest)
}

func readScheduleReceipt(ctx context.Context, reader schemaReader, key, digest string) (*model.ScheduleWrite, error) {
	var savedDigest, raw string
	err := reader.QueryRowContext(ctx, `SELECT request_digest, result_json FROM schedule_write_receipts WHERE operation_key = ?`, key).Scan(&savedDigest, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if savedDigest != digest {
		return nil, model.ErrIdempotencyConflict
	}
	var result model.ScheduleWrite
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return nil, err
	}
	result.Replayed = true
	return &result, nil
}

func (s *ScheduleStore) stopScheduleWithReceipt(ctx context.Context, id int, deleted bool, request *model.ScheduleDelete) (*model.ScheduleWrite, error) {
	db, err := s.db.handle()
	if err != nil {
		return nil, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE schedules SET revision = revision WHERE id = ?`, id); err != nil {
		return nil, err
	}
	if request != nil {
		if saved, err := readScheduleReceipt(ctx, tx, request.Key, request.Digest); err != nil || saved != nil {
			return saved, err
		}
	}
	current, err := scanSchedule(tx.QueryRowContext(ctx, scheduleSelect+` WHERE id = ?`, id))
	if err != nil {
		return nil, err
	}
	if current.Deleted {
		return nil, ErrNotFound
	}
	if request != nil && current.Revision != request.ExpectedRevision {
		return nil, model.ErrScheduleConflict
	}
	now := s.now().In(model.BeijingLocation())
	if err = freezeDue(ctx, tx, id, now); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE notifications SET delivery_stopped = 1,
        deleted = CASE WHEN notify_time > ? AND status IN ('pending', 'loaded') AND acknowledged_at IS NULL AND snapshot_taken = 0 THEN 1 ELSE deleted END WHERE schedule_id = ?`, now.UnixNano(), id); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE schedule_versions SET effective_until = ? WHERE schedule_id = ? AND effective_until IS NULL`, now.UnixNano(), id); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE schedules SET deleted = ?, cancelled = ?, revision = revision + 1, updated_at = ? WHERE id = ?`, deleted, current.Cancelled || !deleted, now.UnixNano(), id); err != nil {
		return nil, err
	}
	if err = saveScheduleVersion(ctx, tx, id, now); err != nil {
		return nil, err
	}
	after := *current
	after.Deleted = deleted
	after.Cancelled = current.Cancelled || !deleted
	after.Revision++
	after.UpdatedAt = now
	result := &model.ScheduleWrite{
		Schedule:    &after,
		Before:      current,
		EffectiveAt: now,
	}
	if request != nil {
		encoded, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schedule_write_receipts(operation_key, request_digest, result_json) VALUES(?,?,?)`, request.Key, request.Digest, string(encoded)); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}
