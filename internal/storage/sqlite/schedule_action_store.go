package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/app"
	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

var _ app.ScheduleCanceller = (*Store)(nil)
var _ app.ScheduleRestorer = (*Store)(nil)

// CancelSchedule 在事务内关闭重复日程并原子递增父版本。
func (s *Store) CancelSchedule(ctx context.Context, id domain.ScheduleID, candidate domain.ScheduleActionCandidate) (domain.ScheduleActionResult, error) {
	return s.changeScheduleOpen(ctx, id, candidate, false, "cancellation")
}

// RestoreSchedule 在事务内重新打开重复日程并原子递增父版本。
func (s *Store) RestoreSchedule(ctx context.Context, id domain.ScheduleID, candidate domain.ScheduleActionCandidate) (domain.ScheduleActionResult, error) {
	return s.changeScheduleOpen(ctx, id, candidate, true, "restoration")
}

// changeScheduleOpen 在单一事务中读取、校验、变更、回读并提交整组状态。
func (s *Store) changeScheduleOpen(ctx context.Context, id domain.ScheduleID, candidate domain.ScheduleActionCandidate, targetOpen bool, operation string) (result domain.ScheduleActionResult, err error) {
	if s == nil || s.db == nil {
		return domain.ScheduleActionResult{}, errors.New("sqlite store is not open")
	}
	if id <= 0 {
		return domain.ScheduleActionResult{}, domain.NewValidation("schedule ID must be positive", "id")
	}
	if candidate.ExpectedRevision <= 0 {
		return domain.ScheduleActionResult{}, domain.NewValidation("expected revision must be positive", "expected_revision")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.ScheduleActionResult{}, fmt.Errorf("begin schedule %s transaction: %w", operation, err)
	}
	defer func() {
		if err == nil {
			return
		}
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			err = errors.Join(err, fmt.Errorf("rollback schedule %s transaction: %w", operation, rollbackErr))
		}
	}()

	schedule, err := scheduleInTransaction(ctx, tx, id)
	if err != nil {
		return domain.ScheduleActionResult{}, err
	}
	if !schedule.IsRecurring() {
		return domain.ScheduleActionResult{}, domain.NewValidation("schedule action requires a recurring schedule", "id")
	}
	if schedule.Revision != candidate.ExpectedRevision {
		return domain.ScheduleActionResult{}, domain.NewStaleRevision("schedule revision is stale", "expected_revision")
	}
	if schedule.Open == targetOpen {
		if err = tx.Commit(); err != nil {
			return domain.ScheduleActionResult{}, fmt.Errorf("commit idempotent schedule %s transaction: %w", operation, err)
		}
		return scheduleActionResult(schedule, false), nil
	}

	updateResult, err := tx.ExecContext(ctx, `
		UPDATE schedules
		SET open = ?, updated_at_utc = ?, revision = revision + 1
		WHERE id = ? AND revision = ?`, boolToInt(targetOpen), formatUTCTime(candidate.UpdatedAtUTC), int64(id), candidate.ExpectedRevision)
	if err != nil {
		return domain.ScheduleActionResult{}, fmt.Errorf("update schedule %d for %s: %w", id, operation, err)
	}
	rowsAffected, err := updateResult.RowsAffected()
	if err != nil {
		return domain.ScheduleActionResult{}, fmt.Errorf("read schedule %d %s update count: %w", id, operation, err)
	}
	if rowsAffected != 1 {
		return domain.ScheduleActionResult{}, domain.NewStaleRevision("schedule revision is stale", "expected_revision")
	}

	schedule, err = scheduleInTransaction(ctx, tx, id)
	if err != nil {
		return domain.ScheduleActionResult{}, fmt.Errorf("read changed schedule %d after %s: %w", id, operation, err)
	}
	if err = tx.Commit(); err != nil {
		return domain.ScheduleActionResult{}, fmt.Errorf("commit schedule %s transaction: %w", operation, err)
	}
	if s.logger != nil {
		s.logger.Info("schedule state changed", "schedule_id", id, "operation", operation, "open", schedule.Open, "revision", schedule.Revision)
	}
	return scheduleActionResult(schedule, true), nil
}

// scheduleInTransaction 复用统一日程行扫描并转换事务内的缺失错误。
func scheduleInTransaction(ctx context.Context, tx *sql.Tx, id domain.ScheduleID) (domain.Schedule, error) {
	schedule, err := scanScheduleRow(tx.QueryRowContext(ctx, "SELECT "+scheduleColumns+" FROM schedules WHERE id = ?", int64(id)))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Schedule{}, domain.NewNotFound(fmt.Sprintf("schedule %d was not found", id))
		}
		return domain.Schedule{}, fmt.Errorf("select schedule %d: %w", id, err)
	}
	return schedule, nil
}

// scheduleActionResult 将已提交日程映射为整组动作结果。
func scheduleActionResult(schedule domain.Schedule, changed bool) domain.ScheduleActionResult {
	return domain.ScheduleActionResult{Open: schedule.Open, Revision: schedule.Revision, UpdatedAtUTC: schedule.UpdatedAtUTC.UTC(), Changed: changed}
}
