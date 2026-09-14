package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/app"
	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

var _ app.ReminderAttemptStore = (*Store)(nil)

// ClaimReminderAttempt 在事务中原子占用一次发生的提醒机会。
// 唯一身份已被占用时返回 false；无论既有状态为何都不允许再次发送。
func (s *Store) ClaimReminderAttempt(ctx context.Context, identity domain.OccurrenceIdentity, attemptedAtUTC time.Time) (claimed bool, err error) {
	if s == nil || s.db == nil {
		return false, errors.New("sqlite store is not open")
	}
	if identity.ScheduleID <= 0 || identity.OriginalDate == "" {
		return false, domain.NewValidation("reminder identity is invalid", "schedule_id", "original_date")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin claim reminder attempt transaction: %w", err)
	}
	defer func() {
		if err == nil {
			return
		}
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			err = errors.Join(err, fmt.Errorf("rollback claim reminder attempt transaction: %w", rollbackErr))
		}
	}()

	result, err := tx.ExecContext(ctx, `
		INSERT INTO reminder_attempts(schedule_id, occurrence_local, attempted_at_utc, status)
		VALUES (?, ?, ?, 'claimed')
		ON CONFLICT(schedule_id, occurrence_local) DO NOTHING`,
		int64(identity.ScheduleID), identity.OriginalDate, formatUTCTime(attemptedAtUTC))
	if err != nil {
		err = fmt.Errorf("claim reminder attempt %d/%s: %w", identity.ScheduleID, identity.OriginalDate, err)
		return false, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		err = fmt.Errorf("read claim reminder attempt result %d/%s: %w", identity.ScheduleID, identity.OriginalDate, err)
		return false, err
	}
	if err = tx.Commit(); err != nil {
		err = fmt.Errorf("commit claim reminder attempt transaction: %w", err)
		return false, err
	}
	return rowsAffected == 1, nil
}

// SetReminderAttemptStatus 只把已占用的提醒尝试推进到终态。
// 仅更新状态列，保留未来处理状态扩展所需的其他字段边界。
func (s *Store) SetReminderAttemptStatus(ctx context.Context, identity domain.OccurrenceIdentity, status app.ReminderAttemptStatus) (err error) {
	if s == nil || s.db == nil {
		return errors.New("sqlite store is not open")
	}
	if identity.ScheduleID <= 0 || identity.OriginalDate == "" {
		return domain.NewValidation("reminder identity is invalid", "schedule_id", "original_date")
	}
	if status != app.ReminderAttemptSubmitted && status != app.ReminderAttemptFailed && status != app.ReminderAttemptSkipped {
		return domain.NewValidation("reminder attempt result is invalid", "status")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin update reminder attempt transaction: %w", err)
	}
	defer func() {
		if err == nil {
			return
		}
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			err = errors.Join(err, fmt.Errorf("rollback update reminder attempt transaction: %w", rollbackErr))
		}
	}()

	result, err := tx.ExecContext(ctx, `
		UPDATE reminder_attempts SET status = ?
		WHERE schedule_id = ? AND occurrence_local = ? AND status = 'claimed'`,
		string(status), int64(identity.ScheduleID), identity.OriginalDate)
	if err != nil {
		err = fmt.Errorf("update reminder attempt %d/%s: %w", identity.ScheduleID, identity.OriginalDate, err)
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		err = fmt.Errorf("read reminder attempt update result %d/%s: %w", identity.ScheduleID, identity.OriginalDate, err)
		return err
	}
	if rowsAffected != 1 {
		err = fmt.Errorf("reminder attempt %d/%s is not claimed", identity.ScheduleID, identity.OriginalDate)
		return err
	}
	if err = tx.Commit(); err != nil {
		err = fmt.Errorf("commit update reminder attempt transaction: %w", err)
		return err
	}
	return nil
}
