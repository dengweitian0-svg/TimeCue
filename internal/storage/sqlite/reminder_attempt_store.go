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
var _ app.ReminderProcessingStore = (*Store)(nil)
var _ app.RecoveryStore = (*Store)(nil)

const reminderAttemptPageLimit = 200

// GetSchedulerRecoveryCheckpoint 读取单例恢复 checkpoint；尚未写入时返回 Exists=false。
func (s *Store) GetSchedulerRecoveryCheckpoint(ctx context.Context) (checkpoint app.RecoveryCheckpoint, err error) {
	if s == nil || s.db == nil {
		return app.RecoveryCheckpoint{}, errors.New("sqlite store is not open")
	}
	var updatedText string
	err = s.db.QueryRowContext(ctx, `
		SELECT checkpoint_unix, updated_at_utc
		FROM scheduler_recovery_checkpoint
		WHERE id = 1`).Scan(&checkpoint.CheckpointUnix, &updatedText)
	if errors.Is(err, sql.ErrNoRows) {
		return app.RecoveryCheckpoint{}, nil
	}
	if err != nil {
		return app.RecoveryCheckpoint{}, fmt.Errorf("read scheduler recovery checkpoint: %w", err)
	}
	updatedAt, parseErr := parseStoredTime(updatedText, "scheduler_recovery_checkpoint.updated_at_utc")
	if parseErr != nil {
		return app.RecoveryCheckpoint{}, parseErr
	}
	checkpoint.Exists = true
	checkpoint.UpdatedAtUTC = updatedAt.UTC()
	return checkpoint, nil
}

// ListReminderAttemptsForRecovery 分页读取所有提醒尝试，供恢复过滤稳定身份和终态。
func (s *Store) ListReminderAttemptsForRecovery(ctx context.Context, request app.RecoveryAttemptListRequest) (app.RecoveryAttemptListResponse, error) {
	if s == nil || s.db == nil {
		return app.RecoveryAttemptListResponse{}, errors.New("sqlite store is not open")
	}
	if request.AfterID < 0 {
		return app.RecoveryAttemptListResponse{}, domain.NewValidation("recovery attempt cursor is invalid", "cursor")
	}
	limit := request.Limit
	if limit == 0 {
		limit = reminderAttemptPageLimit
	}
	if limit <= 0 || limit > reminderAttemptPageLimit {
		return app.RecoveryAttemptListResponse{}, domain.NewValidation("recovery attempt limit is invalid", "limit")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, schedule_id, occurrence_local, attempted_at_utc, status, processed_at_utc
		FROM reminder_attempts
		WHERE id > ?
		ORDER BY id ASC
		LIMIT ?`, request.AfterID, limit+1)
	if err != nil {
		return app.RecoveryAttemptListResponse{}, fmt.Errorf("query recovery reminder attempts: %w", err)
	}
	items, err := scanReminderAttemptRows(rows)
	if err != nil {
		return app.RecoveryAttemptListResponse{}, fmt.Errorf("scan recovery reminder attempts: %w", err)
	}
	response := app.RecoveryAttemptListResponse{Items: items}
	if len(items) > limit {
		response.Items = items[:limit]
		response.NextCursor = response.Items[len(response.Items)-1].ID
	}
	return response, nil
}

// ClaimRecoveryBatch 在短事务中插入新身份或重占用遗留 claimed 身份。
// submitted、failed、skipped 和已确认记录只保留原状态，不会返回发送资格。
func (s *Store) ClaimRecoveryBatch(ctx context.Context, identities []domain.OccurrenceIdentity, attemptedAtUTC time.Time) (claimed []app.RecoveryClaimResult, err error) {
	if s == nil || s.db == nil {
		return nil, errors.New("sqlite store is not open")
	}
	seen := make(map[domain.OccurrenceIdentity]struct{}, len(identities))
	for _, identity := range identities {
		if err := validateReminderIdentity(identity); err != nil {
			return nil, err
		}
		if _, exists := seen[identity]; exists {
			return nil, domain.NewValidation("recovery claim identities must be unique", "identity")
		}
		seen[identity] = struct{}{}
	}
	if len(identities) == 0 {
		return nil, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin recovery claim transaction: %w", err)
	}
	defer func() {
		if err == nil {
			return
		}
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			err = errors.Join(err, fmt.Errorf("rollback recovery claim transaction: %w", rollbackErr))
		}
	}()

	for _, identity := range identities {
		result, execErr := tx.ExecContext(ctx, `
			INSERT INTO reminder_attempts(schedule_id, occurrence_local, attempted_at_utc, status)
			VALUES (?, ?, ?, 'claimed')
			ON CONFLICT(schedule_id, occurrence_local) DO NOTHING`,
			int64(identity.ScheduleID), identity.OriginalDate, formatUTCTime(attemptedAtUTC))
		if execErr != nil {
			err = fmt.Errorf("claim recovery reminder %d/%s: %w", identity.ScheduleID, identity.OriginalDate, execErr)
			return nil, err
		}
		rowsAffected, affectedErr := result.RowsAffected()
		if affectedErr != nil {
			err = fmt.Errorf("read recovery claim result %d/%s: %w", identity.ScheduleID, identity.OriginalDate, affectedErr)
			return nil, err
		}
		if rowsAffected == 1 {
			claimed = append(claimed, app.RecoveryClaimResult{Identity: identity, Mode: app.RecoveryCandidateNew})
			continue
		}
		var status string
		if queryErr := tx.QueryRowContext(ctx, `
			SELECT status FROM reminder_attempts
			WHERE schedule_id = ? AND occurrence_local = ?`, int64(identity.ScheduleID), identity.OriginalDate).Scan(&status); queryErr != nil {
			err = fmt.Errorf("read existing recovery reminder %d/%s: %w", identity.ScheduleID, identity.OriginalDate, queryErr)
			return nil, err
		}
		if status != string(app.ReminderAttemptClaimed) {
			continue
		}
		if _, updateErr := tx.ExecContext(ctx, `
			UPDATE reminder_attempts SET attempted_at_utc = ?
			WHERE schedule_id = ? AND occurrence_local = ? AND status = 'claimed'`,
			formatUTCTime(attemptedAtUTC), int64(identity.ScheduleID), identity.OriginalDate); updateErr != nil {
			err = fmt.Errorf("reclaim recovery reminder %d/%s: %w", identity.ScheduleID, identity.OriginalDate, updateErr)
			return nil, err
		}
		claimed = append(claimed, app.RecoveryClaimResult{Identity: identity, Mode: app.RecoveryCandidateReclaim})
	}
	if err = tx.Commit(); err != nil {
		err = fmt.Errorf("commit recovery claim transaction: %w", err)
		return nil, err
	}
	return claimed, nil
}

// CommitRecoveryBatch 在单一短事务中写入通知终态并推进本批 checkpoint。
// 结果写入或 checkpoint 写入任一失败都会回滚整批，遗留 claimed 留待下次启动重试。
func (s *Store) CommitRecoveryBatch(ctx context.Context, commit app.RecoveryBatchCommit) (err error) {
	if s == nil || s.db == nil {
		return errors.New("sqlite store is not open")
	}
	if commit.UpdatedAtUTC.IsZero() {
		return domain.NewValidation("recovery checkpoint update time is required", "updated_at_utc")
	}
	seen := make(map[domain.OccurrenceIdentity]struct{}, len(commit.Results))
	for _, result := range commit.Results {
		if err := validateReminderIdentity(result.Identity); err != nil {
			return err
		}
		if result.Status != app.ReminderAttemptSubmitted && result.Status != app.ReminderAttemptFailed && result.Status != app.ReminderAttemptSkipped {
			return domain.NewValidation("recovery result status is invalid", "status")
		}
		if _, exists := seen[result.Identity]; exists {
			return domain.NewValidation("recovery result identities must be unique", "identity")
		}
		seen[result.Identity] = struct{}{}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin recovery result transaction: %w", err)
	}
	defer func() {
		if err == nil {
			return
		}
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			err = errors.Join(err, fmt.Errorf("rollback recovery result transaction: %w", rollbackErr))
		}
	}()
	for _, result := range commit.Results {
		updated, updateErr := tx.ExecContext(ctx, `
			UPDATE reminder_attempts SET status = ?
			WHERE schedule_id = ? AND occurrence_local = ? AND status = 'claimed'`,
			string(result.Status), int64(result.Identity.ScheduleID), result.Identity.OriginalDate)
		if updateErr != nil {
			err = fmt.Errorf("write recovery result %d/%s: %w", result.Identity.ScheduleID, result.Identity.OriginalDate, updateErr)
			return err
		}
		rowsAffected, affectedErr := updated.RowsAffected()
		if affectedErr != nil {
			err = fmt.Errorf("read recovery result %d/%s: %w", result.Identity.ScheduleID, result.Identity.OriginalDate, affectedErr)
			return err
		}
		if rowsAffected != 1 {
			err = fmt.Errorf("recovery reminder %d/%s is not claimed", result.Identity.ScheduleID, result.Identity.OriginalDate)
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `
		INSERT INTO scheduler_recovery_checkpoint(id, checkpoint_unix, updated_at_utc)
		VALUES (1, ?, ?)
		ON CONFLICT(id) DO UPDATE SET checkpoint_unix = MAX(scheduler_recovery_checkpoint.checkpoint_unix, excluded.checkpoint_unix), updated_at_utc = excluded.updated_at_utc`,
		commit.CheckpointUnix, formatUTCTime(commit.UpdatedAtUTC)); err != nil {
		err = fmt.Errorf("write scheduler recovery checkpoint: %w", err)
		return err
	}
	if err = tx.Commit(); err != nil {
		err = fmt.Errorf("commit recovery result transaction: %w", err)
		return err
	}
	return nil
}

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

// ListPendingReminderAttempts 按记录 ID 分页读取 submitted 且尚未确认的提醒尝试。
// 数据库层只负责窄分页和严格过滤，应用层再依据当前发生事实重建并排序。
func (s *Store) ListPendingReminderAttempts(ctx context.Context, request app.ListPendingReminderAttemptsRequest) (app.ListPendingReminderAttemptsResponse, error) {
	if s == nil || s.db == nil {
		return app.ListPendingReminderAttemptsResponse{}, errors.New("sqlite store is not open")
	}
	if request.AfterID < 0 {
		return app.ListPendingReminderAttemptsResponse{}, domain.NewValidation("reminder attempt cursor is invalid", "cursor")
	}
	limit := request.Limit
	if limit == 0 {
		limit = reminderAttemptPageLimit
	}
	if limit <= 0 || limit > reminderAttemptPageLimit {
		return app.ListPendingReminderAttemptsResponse{}, domain.NewValidation("reminder attempt limit is invalid", "limit")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, schedule_id, occurrence_local, attempted_at_utc, status, processed_at_utc
		FROM reminder_attempts
		WHERE id > ? AND status = 'submitted' AND processed_at_utc IS NULL
		ORDER BY id ASC
		LIMIT ?`, request.AfterID, limit+1)
	if err != nil {
		return app.ListPendingReminderAttemptsResponse{}, fmt.Errorf("query pending reminder attempts: %w", err)
	}
	items, err := scanReminderAttemptRows(rows)
	if err != nil {
		return app.ListPendingReminderAttemptsResponse{}, fmt.Errorf("scan pending reminder attempts: %w", err)
	}
	response := app.ListPendingReminderAttemptsResponse{}
	if len(items) > limit {
		items = items[:limit]
		response.NextCursor = items[len(items)-1].ID
	}
	response.Items = items
	return response, nil
}

// GetReminderAttempt 按稳定身份读取任意发送状态及处理时间。
func (s *Store) GetReminderAttempt(ctx context.Context, identity domain.OccurrenceIdentity) (app.ReminderAttemptRecord, error) {
	if s == nil || s.db == nil {
		return app.ReminderAttemptRecord{}, errors.New("sqlite store is not open")
	}
	if err := validateReminderIdentity(identity); err != nil {
		return app.ReminderAttemptRecord{}, err
	}
	record, err := scanReminderAttemptRow(s.db.QueryRowContext(ctx, `
		SELECT id, schedule_id, occurrence_local, attempted_at_utc, status, processed_at_utc
		FROM reminder_attempts
		WHERE schedule_id = ? AND occurrence_local = ?`, int64(identity.ScheduleID), identity.OriginalDate))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return app.ReminderAttemptRecord{}, domain.NewNotFound(fmt.Sprintf("reminder attempt %d/%s was not found", identity.ScheduleID, identity.OriginalDate))
		}
		return app.ReminderAttemptRecord{}, fmt.Errorf("read reminder attempt %d/%s: %w", identity.ScheduleID, identity.OriginalDate, err)
	}
	return record, nil
}

// ConfirmReminderAttempt 在单一事务中幂等写入首次确认时间，并在提交后返回结果。
// 已确认记录只读返回原时间；claimed、failed 和 skipped 不得被伪装成可确认。
func (s *Store) ConfirmReminderAttempt(ctx context.Context, identity domain.OccurrenceIdentity, processedAtUTC time.Time) (result app.ReminderConfirmationResult, err error) {
	if s == nil || s.db == nil {
		return app.ReminderConfirmationResult{}, errors.New("sqlite store is not open")
	}
	if err := validateReminderIdentity(identity); err != nil {
		return app.ReminderConfirmationResult{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return app.ReminderConfirmationResult{}, fmt.Errorf("begin confirm reminder transaction: %w", err)
	}
	defer func() {
		if err == nil {
			return
		}
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			err = errors.Join(err, fmt.Errorf("rollback confirm reminder transaction: %w", rollbackErr))
		}
	}()

	record, err := scanReminderAttemptRow(tx.QueryRowContext(ctx, `
		SELECT id, schedule_id, occurrence_local, attempted_at_utc, status, processed_at_utc
		FROM reminder_attempts
		WHERE schedule_id = ? AND occurrence_local = ?`, int64(identity.ScheduleID), identity.OriginalDate))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			err = domain.NewNotFound(fmt.Sprintf("reminder attempt %d/%s was not found", identity.ScheduleID, identity.OriginalDate))
			return app.ReminderConfirmationResult{}, err
		}
		err = fmt.Errorf("read reminder attempt %d/%s for confirmation: %w", identity.ScheduleID, identity.OriginalDate, err)
		return app.ReminderConfirmationResult{}, err
	}
	if record.ProcessedAtUTC != nil {
		result = app.ReminderConfirmationResult{ProcessedAtUTC: record.ProcessedAtUTC.UTC(), Changed: false}
	} else {
		if record.Status != app.ReminderAttemptSubmitted {
			err = domain.NewReminderNotProcessable("reminder attempt is not submitted", "status")
			return app.ReminderConfirmationResult{}, err
		}
		updateResult, updateErr := tx.ExecContext(ctx, `
			UPDATE reminder_attempts
			SET processed_at_utc = ?
			WHERE id = ? AND status = 'submitted' AND processed_at_utc IS NULL`,
			formatUTCTime(processedAtUTC), record.ID)
		if updateErr != nil {
			err = fmt.Errorf("confirm reminder attempt %d/%s: %w", identity.ScheduleID, identity.OriginalDate, updateErr)
			return app.ReminderConfirmationResult{}, err
		}
		rowsAffected, rowsErr := updateResult.RowsAffected()
		if rowsErr != nil {
			err = fmt.Errorf("read confirmation result %d/%s: %w", identity.ScheduleID, identity.OriginalDate, rowsErr)
			return app.ReminderConfirmationResult{}, err
		}
		if rowsAffected != 1 {
			err = domain.NewReminderNotProcessable("reminder attempt changed before confirmation", "status")
			return app.ReminderConfirmationResult{}, err
		}
		result = app.ReminderConfirmationResult{ProcessedAtUTC: processedAtUTC.UTC(), Changed: true}
	}
	if err = tx.Commit(); err != nil {
		err = fmt.Errorf("commit confirm reminder transaction: %w", err)
		return app.ReminderConfirmationResult{}, err
	}
	if s.logger != nil {
		s.logger.Info("reminder processed", "schedule_id", identity.ScheduleID, "original_date", identity.OriginalDate, "changed", result.Changed, "processed_at_utc", result.ProcessedAtUTC)
	}
	return result, nil
}

// validateReminderIdentity 校验提醒存储端口使用的稳定身份。
func validateReminderIdentity(identity domain.OccurrenceIdentity) error {
	if identity.ScheduleID <= 0 || identity.OriginalDate == "" {
		return domain.NewValidation("reminder identity is invalid", "schedule_id", "original_date")
	}
	return nil
}

// scanReminderAttemptRow 将单行提醒尝试转换为应用层记录。
func scanReminderAttemptRow(row *sql.Row) (app.ReminderAttemptRecord, error) {
	return scanReminderAttemptFields(row.Scan)
}

// scanReminderAttemptRows 扫描并完整关闭提醒尝试行集，同时保留关闭错误。
func scanReminderAttemptRows(rows *sql.Rows) (records []app.ReminderAttemptRecord, err error) {
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			closeErr = fmt.Errorf("close reminder attempt rows: %w", closeErr)
			if err == nil {
				err = closeErr
			} else {
				err = errors.Join(err, closeErr)
			}
		}
	}()
	for rows.Next() {
		record, scanErr := scanReminderAttemptFields(rows.Scan)
		if scanErr != nil {
			return nil, scanErr
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate reminder attempt rows: %w", err)
	}
	return records, nil
}

// scanReminderAttemptFields 解析提醒尝试字段和可空确认时间。
func scanReminderAttemptFields(scan func(...any) error) (app.ReminderAttemptRecord, error) {
	var (
		record        app.ReminderAttemptRecord
		scheduleID    int64
		originalDate  string
		attemptedText string
		status        string
		processedText sql.NullString
	)
	if err := scan(&record.ID, &scheduleID, &originalDate, &attemptedText, &status, &processedText); err != nil {
		return app.ReminderAttemptRecord{}, err
	}
	attemptedAt, err := parseStoredTime(attemptedText, "attempted_at_utc")
	if err != nil {
		return app.ReminderAttemptRecord{}, err
	}
	record.Identity = domain.OccurrenceIdentity{ScheduleID: domain.ScheduleID(scheduleID), OriginalDate: originalDate}
	record.AttemptedAtUTC = attemptedAt.UTC()
	record.Status = app.ReminderAttemptStatus(status)
	if processedText.Valid {
		processedAt, parseErr := parseStoredTime(processedText.String, "processed_at_utc")
		if parseErr != nil {
			return app.ReminderAttemptRecord{}, parseErr
		}
		processedAt = processedAt.UTC()
		record.ProcessedAtUTC = &processedAt
	}
	return record, nil
}
