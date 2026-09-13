package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/app"
	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

const occurrenceOverrideColumns = `
id, schedule_id, occurrence_local, open, actual_start_local,
duration_minutes, title, notes`

var _ app.OccurrenceReader = (*Store)(nil)
var _ app.OccurrenceUpdater = (*Store)(nil)
var _ app.OccurrenceCanceller = (*Store)(nil)
var _ app.OccurrenceRestorer = (*Store)(nil)

// GetOccurrenceOverride 读取指定日程和原始日期的例外快照。
func (s *Store) GetOccurrenceOverride(ctx context.Context, id domain.ScheduleID, originalDate string) (domain.OccurrenceOverride, error) {
	if s == nil || s.db == nil {
		return domain.OccurrenceOverride{}, errors.New("sqlite store is not open")
	}
	if id <= 0 {
		return domain.OccurrenceOverride{}, domain.NewValidation("schedule ID must be positive", "id")
	}
	if strings.TrimSpace(originalDate) == "" {
		return domain.OccurrenceOverride{}, domain.NewValidation("original date is required", "original_date")
	}
	row := s.db.QueryRowContext(ctx, "SELECT "+occurrenceOverrideColumns+" FROM occurrence_overrides WHERE schedule_id = ? AND occurrence_local = ?", int64(id), originalDate)
	override, err := scanOccurrenceOverrideRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.OccurrenceOverride{}, domain.NewNotFound("occurrence override was not found")
		}
		return domain.OccurrenceOverride{}, fmt.Errorf("select occurrence override %d/%s: %w", id, originalDate, err)
	}
	return override, nil
}

// ListOccurrenceOverrides 读取日程的全部例外，由领域层按实际时间参与合并和区间过滤。
func (s *Store) ListOccurrenceOverrides(ctx context.Context, id domain.ScheduleID) (overrides []domain.OccurrenceOverride, err error) {
	if s == nil || s.db == nil {
		return nil, errors.New("sqlite store is not open")
	}
	if id <= 0 {
		return nil, domain.NewValidation("schedule ID must be positive", "id")
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+occurrenceOverrideColumns+" FROM occurrence_overrides WHERE schedule_id = ? ORDER BY id ASC", int64(id))
	if err != nil {
		return nil, fmt.Errorf("query occurrence overrides for schedule %d: %w", id, err)
	}
	return scanOccurrenceOverrideRows(rows)
}

// UpdateOccurrence 在同一事务中写入例外完整快照并递增父日程版本。
func (s *Store) UpdateOccurrence(ctx context.Context, id domain.ScheduleID, candidate domain.OccurrenceOverrideCandidate) (result domain.OccurrenceUpdateResult, err error) {
	if s == nil || s.db == nil {
		return domain.OccurrenceUpdateResult{}, errors.New("sqlite store is not open")
	}
	if id <= 0 {
		return domain.OccurrenceUpdateResult{}, domain.NewValidation("schedule ID must be positive", "id")
	}
	if candidate.ExpectedRevision <= 0 {
		return domain.OccurrenceUpdateResult{}, domain.NewValidation("expected revision must be positive", "expected_revision")
	}
	if strings.TrimSpace(candidate.OriginalDate) == "" {
		return domain.OccurrenceUpdateResult{}, domain.NewValidation("original date is required", "original_date")
	}
	if candidate.StartLocal.IsZero() || candidate.StartLocal.Second() != 0 || candidate.StartLocal.Nanosecond() != 0 {
		return domain.OccurrenceUpdateResult{}, domain.NewValidation("occurrence start time is invalid", "start_unix")
	}
	if err := validateOccurrenceLocalTime(candidate.StartLocal); err != nil {
		return domain.OccurrenceUpdateResult{}, domain.NewValidation(err.Error(), "start_unix")
	}
	if candidate.DurationMinutes <= 0 {
		return domain.OccurrenceUpdateResult{}, domain.NewValidation("occurrence duration must be positive", "start_unix", "end_unix")
	}
	if strings.TrimSpace(candidate.Title) == "" {
		return domain.OccurrenceUpdateResult{}, domain.NewValidation("title must not be empty", "title")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.OccurrenceUpdateResult{}, fmt.Errorf("begin update occurrence transaction: %w", err)
	}
	defer func() {
		if err == nil {
			return
		}
		// 例外写入和父版本更新必须整体回滚，不能留下不可对应的快照。
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			err = errors.Join(err, fmt.Errorf("rollback update occurrence transaction: %w", rollbackErr))
		}
	}()

	var revision int64
	err = tx.QueryRowContext(ctx, "SELECT revision FROM schedules WHERE id = ?", int64(id)).Scan(&revision)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			err = domain.NewNotFound(fmt.Sprintf("schedule %d was not found", id))
			return domain.OccurrenceUpdateResult{}, err
		}
		err = fmt.Errorf("select schedule %d revision for occurrence update: %w", id, err)
		return domain.OccurrenceUpdateResult{}, err
	}
	if revision != candidate.ExpectedRevision {
		err = domain.NewStaleRevision("schedule revision is stale", "expected_revision")
		return domain.OccurrenceUpdateResult{}, err
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO occurrence_overrides(
			schedule_id, occurrence_local, open, actual_start_local,
			duration_minutes, title, notes
		) VALUES (?, ?, 1, ?, ?, ?, ?)
		ON CONFLICT(schedule_id, occurrence_local) DO UPDATE SET
			open = excluded.open,
			actual_start_local = excluded.actual_start_local,
			duration_minutes = excluded.duration_minutes,
			title = excluded.title,
			notes = excluded.notes`,
		int64(id),
		candidate.OriginalDate,
		domain.FormatLocalTime(candidate.StartLocal),
		candidate.DurationMinutes,
		candidate.Title,
		candidate.Notes,
	)
	if err != nil {
		err = fmt.Errorf("upsert occurrence override %d/%s: %w", id, candidate.OriginalDate, err)
		return domain.OccurrenceUpdateResult{}, err
	}

	updateResult, err := tx.ExecContext(ctx, "UPDATE schedules SET updated_at_utc = ?, revision = revision + 1 WHERE id = ? AND revision = ?", formatUTCTime(candidate.UpdatedAtUTC), int64(id), candidate.ExpectedRevision)
	if err != nil {
		err = fmt.Errorf("update schedule %d revision for occurrence: %w", id, err)
		return domain.OccurrenceUpdateResult{}, err
	}
	rowsAffected, err := updateResult.RowsAffected()
	if err != nil {
		err = fmt.Errorf("read schedule %d revision update count: %w", id, err)
		return domain.OccurrenceUpdateResult{}, err
	}
	if rowsAffected != 1 {
		err = domain.NewStaleRevision("schedule revision is stale", "expected_revision")
		return domain.OccurrenceUpdateResult{}, err
	}

	override, err := scanOccurrenceOverrideRow(tx.QueryRowContext(ctx, "SELECT "+occurrenceOverrideColumns+" FROM occurrence_overrides WHERE schedule_id = ? AND occurrence_local = ?", int64(id), candidate.OriginalDate))
	if err != nil {
		err = fmt.Errorf("select updated occurrence override %d/%s: %w", id, candidate.OriginalDate, err)
		return domain.OccurrenceUpdateResult{}, err
	}
	var updatedRevision int64
	if err = tx.QueryRowContext(ctx, "SELECT revision FROM schedules WHERE id = ?", int64(id)).Scan(&updatedRevision); err != nil {
		err = fmt.Errorf("select updated schedule %d revision: %w", id, err)
		return domain.OccurrenceUpdateResult{}, err
	}
	if err = tx.Commit(); err != nil {
		err = fmt.Errorf("commit update occurrence transaction: %w", err)
		return domain.OccurrenceUpdateResult{}, err
	}
	s.bumpGeneration()
	if s.logger != nil {
		s.logger.Info("occurrence updated", "schedule_id", id, "original_date", candidate.OriginalDate, "revision", updatedRevision)
	}
	return domain.OccurrenceUpdateResult{Override: override, ScheduleRevision: updatedRevision}, nil
}

// CancelOccurrence 在同一事务中标记例外为取消并递增父日程版本。
func (s *Store) CancelOccurrence(ctx context.Context, id domain.ScheduleID, candidate domain.OccurrenceCancelCandidate) (result domain.OccurrenceActionResult, err error) {
	if s == nil || s.db == nil {
		return domain.OccurrenceActionResult{}, errors.New("sqlite store is not open")
	}
	if id <= 0 {
		return domain.OccurrenceActionResult{}, domain.NewValidation("schedule ID must be positive", "id")
	}
	if candidate.ExpectedRevision <= 0 {
		return domain.OccurrenceActionResult{}, domain.NewValidation("expected revision must be positive", "expected_revision")
	}
	if strings.TrimSpace(candidate.OriginalDate) == "" {
		return domain.OccurrenceActionResult{}, domain.NewValidation("original date is required", "original_date")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.OccurrenceActionResult{}, fmt.Errorf("begin cancel occurrence transaction: %w", err)
	}
	defer func() {
		if err == nil {
			return
		}
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			err = errors.Join(err, fmt.Errorf("rollback cancel occurrence transaction: %w", rollbackErr))
		}
	}()

	revision, scheduleOpen, err := readScheduleState(tx, id)
	if err != nil {
		return domain.OccurrenceActionResult{}, err
	}
	if revision != candidate.ExpectedRevision {
		return domain.OccurrenceActionResult{}, domain.NewStaleRevision("schedule revision is stale", "expected_revision")
	}

	override, overrideErr := scanOccurrenceOverrideRow(tx.QueryRowContext(ctx, "SELECT "+occurrenceOverrideColumns+" FROM occurrence_overrides WHERE schedule_id = ? AND occurrence_local = ?", int64(id), candidate.OriginalDate))
	if overrideErr == nil && !override.Open {
		if err = tx.Commit(); err != nil {
			return domain.OccurrenceActionResult{}, fmt.Errorf("commit idempotent cancel occurrence transaction: %w", err)
		}
		return domain.OccurrenceActionResult{ScheduleOpen: scheduleOpen, Override: override, OverridePresent: true, ScheduleRevision: revision}, nil
	}
	if overrideErr != nil && !errors.Is(overrideErr, sql.ErrNoRows) {
		return domain.OccurrenceActionResult{}, fmt.Errorf("select occurrence override %d/%s for cancellation: %w", id, candidate.OriginalDate, overrideErr)
	}
	if errors.Is(overrideErr, sql.ErrNoRows) {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO occurrence_overrides(schedule_id, occurrence_local, open)
			VALUES (?, ?, 0)`, int64(id), candidate.OriginalDate)
		if err != nil {
			return domain.OccurrenceActionResult{}, fmt.Errorf("insert cancelled occurrence %d/%s: %w", id, candidate.OriginalDate, err)
		}
	} else {
		_, err = tx.ExecContext(ctx, `
			UPDATE occurrence_overrides SET open = 0
			WHERE schedule_id = ? AND occurrence_local = ?`, int64(id), candidate.OriginalDate)
		if err != nil {
			return domain.OccurrenceActionResult{}, fmt.Errorf("cancel occurrence override %d/%s: %w", id, candidate.OriginalDate, err)
		}
	}

	updateResult, err := tx.ExecContext(ctx, "UPDATE schedules SET updated_at_utc = ?, revision = revision + 1 WHERE id = ? AND revision = ?", formatUTCTime(candidate.UpdatedAtUTC), int64(id), candidate.ExpectedRevision)
	if err != nil {
		return domain.OccurrenceActionResult{}, fmt.Errorf("update schedule %d revision for cancellation: %w", id, err)
	}
	rowsAffected, err := updateResult.RowsAffected()
	if err != nil {
		return domain.OccurrenceActionResult{}, fmt.Errorf("read schedule %d cancellation revision update count: %w", id, err)
	}
	if rowsAffected != 1 {
		return domain.OccurrenceActionResult{}, domain.NewStaleRevision("schedule revision is stale", "expected_revision")
	}

	override, err = scanOccurrenceOverrideRow(tx.QueryRowContext(ctx, "SELECT "+occurrenceOverrideColumns+" FROM occurrence_overrides WHERE schedule_id = ? AND occurrence_local = ?", int64(id), candidate.OriginalDate))
	if err != nil {
		return domain.OccurrenceActionResult{}, fmt.Errorf("select cancelled occurrence override %d/%s: %w", id, candidate.OriginalDate, err)
	}
	updatedRevision := candidate.ExpectedRevision + 1
	if err = tx.QueryRowContext(ctx, "SELECT revision FROM schedules WHERE id = ?", int64(id)).Scan(&updatedRevision); err != nil {
		return domain.OccurrenceActionResult{}, fmt.Errorf("select cancelled schedule %d revision: %w", id, err)
	}
	if err = tx.Commit(); err != nil {
		return domain.OccurrenceActionResult{}, fmt.Errorf("commit cancel occurrence transaction: %w", err)
	}
	s.bumpGeneration()
	if s.logger != nil {
		s.logger.Info("occurrence cancelled", "schedule_id", id, "original_date", candidate.OriginalDate, "revision", updatedRevision)
	}
	return domain.OccurrenceActionResult{ScheduleOpen: scheduleOpen, Override: override, OverridePresent: true, ScheduleRevision: updatedRevision, Changed: true}, nil
}

// RestoreOccurrence 在同一事务中恢复完整快照或移除空取消例外。
func (s *Store) RestoreOccurrence(ctx context.Context, id domain.ScheduleID, candidate domain.OccurrenceRestoreCandidate) (result domain.OccurrenceActionResult, err error) {
	if s == nil || s.db == nil {
		return domain.OccurrenceActionResult{}, errors.New("sqlite store is not open")
	}
	if id <= 0 {
		return domain.OccurrenceActionResult{}, domain.NewValidation("schedule ID must be positive", "id")
	}
	if candidate.ExpectedRevision <= 0 {
		return domain.OccurrenceActionResult{}, domain.NewValidation("expected revision must be positive", "expected_revision")
	}
	if strings.TrimSpace(candidate.OriginalDate) == "" {
		return domain.OccurrenceActionResult{}, domain.NewValidation("original date is required", "original_date")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.OccurrenceActionResult{}, fmt.Errorf("begin restore occurrence transaction: %w", err)
	}
	defer func() {
		if err == nil {
			return
		}
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			err = errors.Join(err, fmt.Errorf("rollback restore occurrence transaction: %w", rollbackErr))
		}
	}()

	revision, scheduleOpen, err := readScheduleState(tx, id)
	if err != nil {
		return domain.OccurrenceActionResult{}, err
	}
	if revision != candidate.ExpectedRevision {
		return domain.OccurrenceActionResult{}, domain.NewStaleRevision("schedule revision is stale", "expected_revision")
	}

	override, overrideErr := scanOccurrenceOverrideRow(tx.QueryRowContext(ctx, "SELECT "+occurrenceOverrideColumns+" FROM occurrence_overrides WHERE schedule_id = ? AND occurrence_local = ?", int64(id), candidate.OriginalDate))
	if errors.Is(overrideErr, sql.ErrNoRows) {
		if err = tx.Commit(); err != nil {
			return domain.OccurrenceActionResult{}, fmt.Errorf("commit idempotent restore occurrence transaction: %w", err)
		}
		return domain.OccurrenceActionResult{ScheduleOpen: scheduleOpen, ScheduleRevision: revision}, nil
	}
	if overrideErr != nil {
		return domain.OccurrenceActionResult{}, fmt.Errorf("select occurrence override %d/%s for restoration: %w", id, candidate.OriginalDate, overrideErr)
	}
	if override.Open {
		if err = tx.Commit(); err != nil {
			return domain.OccurrenceActionResult{}, fmt.Errorf("commit idempotent restored occurrence transaction: %w", err)
		}
		return domain.OccurrenceActionResult{ScheduleOpen: scheduleOpen, Override: override, OverridePresent: true, ScheduleRevision: revision}, nil
	}
	if !override.SnapshotPresent && !candidate.RemoveCancellation {
		return domain.OccurrenceActionResult{}, domain.NewValidation("cancelled occurrence has no restorable snapshot", "original_date")
	}

	if candidate.RemoveCancellation {
		if override.SnapshotPresent {
			return domain.OccurrenceActionResult{}, domain.NewValidation("complete occurrence snapshot must be restored", "original_date")
		}
		_, err = tx.ExecContext(ctx, "DELETE FROM occurrence_overrides WHERE schedule_id = ? AND occurrence_local = ? AND open = 0", int64(id), candidate.OriginalDate)
	} else {
		_, err = tx.ExecContext(ctx, "UPDATE occurrence_overrides SET open = 1 WHERE schedule_id = ? AND occurrence_local = ? AND open = 0", int64(id), candidate.OriginalDate)
	}
	if err != nil {
		return domain.OccurrenceActionResult{}, fmt.Errorf("restore occurrence override %d/%s: %w", id, candidate.OriginalDate, err)
	}

	updateResult, err := tx.ExecContext(ctx, "UPDATE schedules SET updated_at_utc = ?, revision = revision + 1 WHERE id = ? AND revision = ?", formatUTCTime(candidate.UpdatedAtUTC), int64(id), candidate.ExpectedRevision)
	if err != nil {
		return domain.OccurrenceActionResult{}, fmt.Errorf("update schedule %d revision for restoration: %w", id, err)
	}
	rowsAffected, err := updateResult.RowsAffected()
	if err != nil {
		return domain.OccurrenceActionResult{}, fmt.Errorf("read schedule %d restoration revision update count: %w", id, err)
	}
	if rowsAffected != 1 {
		return domain.OccurrenceActionResult{}, domain.NewStaleRevision("schedule revision is stale", "expected_revision")
	}

	updatedRevision := candidate.ExpectedRevision + 1
	if err = tx.QueryRowContext(ctx, "SELECT revision FROM schedules WHERE id = ?", int64(id)).Scan(&updatedRevision); err != nil {
		return domain.OccurrenceActionResult{}, fmt.Errorf("select restored schedule %d revision: %w", id, err)
	}
	if candidate.RemoveCancellation {
		if err = tx.Commit(); err != nil {
			return domain.OccurrenceActionResult{}, fmt.Errorf("commit restore occurrence transaction: %w", err)
		}
		s.bumpGeneration()
		if s.logger != nil {
			s.logger.Info("occurrence restored", "schedule_id", id, "original_date", candidate.OriginalDate, "revision", updatedRevision)
		}
		return domain.OccurrenceActionResult{ScheduleOpen: scheduleOpen, ScheduleRevision: updatedRevision, Changed: true}, nil
	}
	override, err = scanOccurrenceOverrideRow(tx.QueryRowContext(ctx, "SELECT "+occurrenceOverrideColumns+" FROM occurrence_overrides WHERE schedule_id = ? AND occurrence_local = ?", int64(id), candidate.OriginalDate))
	if err != nil {
		return domain.OccurrenceActionResult{}, fmt.Errorf("select restored occurrence override %d/%s: %w", id, candidate.OriginalDate, err)
	}
	if err = tx.Commit(); err != nil {
		return domain.OccurrenceActionResult{}, fmt.Errorf("commit restore occurrence transaction: %w", err)
	}
	s.bumpGeneration()
	if s.logger != nil {
		s.logger.Info("occurrence restored", "schedule_id", id, "original_date", candidate.OriginalDate, "revision", updatedRevision)
	}
	return domain.OccurrenceActionResult{ScheduleOpen: scheduleOpen, Override: override, OverridePresent: true, ScheduleRevision: updatedRevision, Changed: true}, nil
}

// readScheduleState 在事务内读取父日程版本和 Open 状态并统一转换缺失错误。
func readScheduleState(tx *sql.Tx, id domain.ScheduleID) (int64, bool, error) {
	var revision int64
	var open int
	if err := tx.QueryRow("SELECT revision, open FROM schedules WHERE id = ?", int64(id)).Scan(&revision, &open); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, false, domain.NewNotFound(fmt.Sprintf("schedule %d was not found", id))
		}
		return 0, false, fmt.Errorf("select schedule %d state: %w", id, err)
	}
	if open != 0 && open != 1 {
		return 0, false, fmt.Errorf("invalid schedule open value %d", open)
	}
	return revision, open == 1, nil
}

// scanOccurrenceOverrideRow 将单条例外数据库行转换为领域快照。
func scanOccurrenceOverrideRow(row *sql.Row) (domain.OccurrenceOverride, error) {
	return scanOccurrenceOverrideFields(row.Scan)
}

// scanOccurrenceOverrideFields 复用单条读取和列表读取的例外字段映射。
func scanOccurrenceOverrideFields(scan func(...any) error) (domain.OccurrenceOverride, error) {
	var (
		override    domain.OccurrenceOverride
		id          int64
		scheduleID  int64
		open        int
		actualStart sql.NullString
		duration    sql.NullInt64
		title       sql.NullString
		notes       sql.NullString
	)
	if err := scan(&id, &scheduleID, &override.OriginalDate, &open, &actualStart, &duration, &title, &notes); err != nil {
		return domain.OccurrenceOverride{}, err
	}
	if open != 0 && open != 1 {
		return domain.OccurrenceOverride{}, fmt.Errorf("invalid occurrence override open value %d", open)
	}
	allPresent := actualStart.Valid && duration.Valid && title.Valid && notes.Valid
	allAbsent := !actualStart.Valid && !duration.Valid && !title.Valid && !notes.Valid
	if !allPresent && !allAbsent {
		return domain.OccurrenceOverride{}, errors.New("occurrence override snapshot is incomplete")
	}
	if open == 1 && !allPresent {
		return domain.OccurrenceOverride{}, errors.New("open occurrence override has no complete snapshot")
	}
	override.ScheduleID = domain.ScheduleID(scheduleID)
	override.Open = open == 1
	override.SnapshotPresent = allPresent
	if !allPresent {
		return override, nil
	}
	parsedStart, err := parseStoredTime(actualStart.String, "actual_start_local")
	if err != nil {
		return domain.OccurrenceOverride{}, err
	}
	if duration.Int64 <= 0 || int64(int(duration.Int64)) != duration.Int64 {
		return domain.OccurrenceOverride{}, fmt.Errorf("invalid occurrence override duration %d", duration.Int64)
	}
	override.StartLocal = parsedStart
	override.DurationMinutes = int(duration.Int64)
	override.Title = title.String
	override.Notes = notes.String
	return override, nil
}

// scanOccurrenceOverrideRows 将列表查询转换为快照并合并 rows 关闭错误。
func scanOccurrenceOverrideRows(rows *sql.Rows) (overrides []domain.OccurrenceOverride, err error) {
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			closeErr = fmt.Errorf("close occurrence override rows: %w", closeErr)
			if err == nil {
				err = closeErr
			} else {
				err = errors.Join(err, closeErr)
			}
		}
	}()
	for rows.Next() {
		override, scanErr := scanOccurrenceOverrideFields(rows.Scan)
		if scanErr != nil {
			return nil, scanErr
		}
		overrides = append(overrides, override)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate occurrence override rows: %w", err)
	}
	return overrides, nil
}

// validateOccurrenceLocalTime 保留给存储边界的时间字段校验，避免写入无法回读的值。
func validateOccurrenceLocalTime(value time.Time) error {
	if value.Year() < 1 || value.Year() > 9999 {
		return errors.New("occurrence time is out of range")
	}
	return nil
}
