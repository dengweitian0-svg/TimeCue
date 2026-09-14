package sqlite

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/app"
	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

const scheduleColumns = `
	id, title, notes, cron, open, first_start_local, original_start_local,
	duration_minutes, revision, created_at_utc, updated_at_utc, until_date_local,
	month_end_policy`

const scheduleListLimit = 50

var _ app.ScheduleStore = (*Store)(nil)
var _ app.ScheduleUpdater = (*Store)(nil)
var _ app.ScheduleDeleter = (*Store)(nil)

// CreateSchedule 在单一事务中写入并读取日程。
func (s *Store) CreateSchedule(ctx context.Context, candidate domain.ScheduleCandidate) (schedule domain.Schedule, err error) {
	if s == nil || s.db == nil {
		return domain.Schedule{}, errors.New("sqlite store is not open")
	}

	// 应用层校验已经完成，此处开始把日程定义写入 SQLite 事务。
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Schedule{}, fmt.Errorf("begin create schedule transaction: %w", err)
	}
	defer func() {
		if err == nil {
			return
		}
		// INSERT、SELECT 或 COMMIT 任一步失败都不能留下部分事实。
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			err = errors.Join(err, fmt.Errorf("rollback create schedule transaction: %w", rollbackErr))
		}
	}()

	var cron any
	if candidate.Cron != nil {
		cron = *candidate.Cron
	}
	var monthEndPolicy any
	if candidate.MonthEndPolicy != nil {
		monthEndPolicy = *candidate.MonthEndPolicy
	}
	// 这里只写入日程定义，不预先展开未来发生实例或保存结束时间。
	result, err := tx.ExecContext(ctx, `
		INSERT INTO schedules(
			title, notes, cron, open, first_start_local, original_start_local,
			duration_minutes, revision, created_at_utc, updated_at_utc, until_date_local,
			month_end_policy
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		candidate.Title,
		candidate.Notes,
		cron,
		boolToInt(candidate.Open),
		domain.FormatLocalTime(candidate.FirstStartLocal),
		domain.FormatLocalTime(candidate.OriginalStartLocal),
		candidate.DurationMinutes,
		1,
		formatUTCTime(candidate.CreatedAtUTC),
		formatUTCTime(candidate.UpdatedAtUTC),
		candidate.UntilDateLocal,
		monthEndPolicy,
	)
	if err != nil {
		err = fmt.Errorf("insert schedule: %w", err)
		return domain.Schedule{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		err = fmt.Errorf("read inserted schedule ID: %w", err)
		return domain.Schedule{}, err
	}

	// 返回值必须来自数据库实际状态，而不是只回显写入前的候选。
	row := tx.QueryRowContext(ctx, "SELECT "+scheduleColumns+" FROM schedules WHERE id = ?", id)
	schedule, err = scanScheduleRow(row)
	if err != nil {
		err = fmt.Errorf("select inserted schedule %d: %w", id, err)
		return domain.Schedule{}, err
	}

	// 提交成功是向应用层返回创建成功的必要条件。
	if err = tx.Commit(); err != nil {
		err = fmt.Errorf("commit create schedule transaction: %w", err)
		return domain.Schedule{}, err
	}
	s.bumpGeneration()
	if s.logger != nil {
		s.logger.Info("schedule created", "schedule_id", schedule.ID)
	}
	return schedule, nil
}

// UpdateSchedule 在事务内重新检查日程并执行版本条件更新。
func (s *Store) UpdateSchedule(ctx context.Context, id domain.ScheduleID, candidate domain.ScheduleUpdateCandidate) (schedule domain.Schedule, err error) {
	if s == nil || s.db == nil {
		return domain.Schedule{}, errors.New("sqlite store is not open")
	}
	if id <= 0 {
		return domain.Schedule{}, domain.NewValidation("schedule ID must be positive", "id")
	}
	if candidate.ExpectedRevision <= 0 {
		return domain.Schedule{}, domain.NewValidation("expected revision must be positive", "expected_revision")
	}
	cronPresent := candidate.CronPresent || candidate.Cron != nil
	untilPresent := candidate.UntilDateLocalPresent || candidate.UntilDateLocal != nil
	monthEndPolicyPresent := candidate.MonthEndPolicyPresent || candidate.MonthEndPolicy != nil
	openPresent := candidate.OpenPresent || candidate.Open != nil
	if candidate.Title == nil && candidate.Notes == nil && candidate.FirstStartLocal == nil && candidate.OriginalStartLocal == nil && candidate.DurationMinutes == nil && !cronPresent && !untilPresent && !monthEndPolicyPresent && !openPresent {
		return domain.Schedule{}, domain.NewValidation("schedule update is empty", "title", "start_unix", "end_unix")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Schedule{}, fmt.Errorf("begin update schedule transaction: %w", err)
	}
	defer func() {
		if err == nil {
			return
		}
		// 读取、条件更新、回读或提交任一步失败都必须撤销本次事务。
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			err = errors.Join(err, fmt.Errorf("rollback update schedule transaction: %w", rollbackErr))
		}
	}()

	existing, err := scanScheduleRow(tx.QueryRowContext(ctx, "SELECT "+scheduleColumns+" FROM schedules WHERE id = ?", int64(id)))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			err = &domain.Error{Code: domain.NotFound, Message: fmt.Sprintf("schedule %d was not found", id), Cause: err}
			return domain.Schedule{}, err
		}
		err = fmt.Errorf("select schedule %d for update: %w", id, err)
		return domain.Schedule{}, err
	}
	// 事务内重新判断目标类型，防止预读后并发变化导致类型或截止字段被错误覆盖。
	targetRecurring := existing.IsRecurring()
	if cronPresent {
		targetRecurring = candidate.Cron != nil
	}
	if !existing.IsRecurring() && (!targetRecurring && untilPresent || targetRecurring && (!untilPresent || candidate.UntilDateLocal == nil)) {
		err = domain.NewValidation("single to recurring conversion requires cron and until date", "cron", "until_date_local")
		return domain.Schedule{}, err
	}
	if !existing.IsRecurring() && cronPresent && candidate.Cron == nil {
		err = domain.NewValidation("single schedule cron cannot be cleared", "cron")
		return domain.Schedule{}, err
	}
	if existing.IsRecurring() && !targetRecurring && candidate.UntilDateLocal != nil {
		err = domain.NewValidation("single conversion cannot carry a cutoff", "until_date_local")
		return domain.Schedule{}, err
	}
	if existing.IsRecurring() && targetRecurring && untilPresent && candidate.UntilDateLocal == nil {
		err = domain.NewValidation("recurring cutoff cannot be cleared", "until_date_local")
		return domain.Schedule{}, err
	}
	if existing.IsRecurring() && !targetRecurring {
		// 类型落到单次后这些重复字段不再适用，即使调用方省略显式清空标记也不能残留。
		candidate.UntilDateLocal = nil
		candidate.UntilDateLocalPresent = true
		candidate.MonthEndPolicy = nil
		candidate.MonthEndPolicyPresent = true
		if candidate.Open == nil {
			open := true
			candidate.Open = &open
			candidate.OpenPresent = true
		}
	}
	if !existing.IsRecurring() && !cronPresent && untilPresent {
		err = domain.NewValidation("single schedule cannot carry recurrence fields", "cron", "until_date_local")
		return domain.Schedule{}, err
	}
	if candidate.Open != nil && !*candidate.Open {
		err = domain.NewValidation("single schedule must remain open", "open")
		return domain.Schedule{}, err
	}
	if existing.Revision != candidate.ExpectedRevision {
		err = domain.NewStaleRevision("schedule revision is stale", "expected_revision")
		return domain.Schedule{}, err
	}

	setClauses := make([]string, 0, 10)
	args := make([]any, 0, 12)
	if candidate.Title != nil {
		setClauses = append(setClauses, "title = ?")
		args = append(args, *candidate.Title)
	}
	if candidate.Notes != nil {
		setClauses = append(setClauses, "notes = ?")
		args = append(args, *candidate.Notes)
	}
	if cronPresent {
		setClauses = append(setClauses, "cron = ?")
		if candidate.Cron == nil {
			args = append(args, nil)
		} else {
			args = append(args, *candidate.Cron)
		}
	}
	if untilPresent {
		setClauses = append(setClauses, "until_date_local = ?")
		if candidate.UntilDateLocal == nil {
			args = append(args, nil)
		} else {
			args = append(args, *candidate.UntilDateLocal)
		}
	}
	if monthEndPolicyPresent {
		setClauses = append(setClauses, "month_end_policy = ?")
		if candidate.MonthEndPolicy == nil {
			args = append(args, nil)
		} else {
			args = append(args, *candidate.MonthEndPolicy)
		}
	}
	if candidate.FirstStartLocal != nil {
		setClauses = append(setClauses, "first_start_local = ?")
		args = append(args, domain.FormatLocalTime(*candidate.FirstStartLocal))
	}
	if candidate.DurationMinutes != nil {
		if *candidate.DurationMinutes <= 0 {
			err = domain.NewValidation("duration must be positive", "duration_minutes")
			return domain.Schedule{}, err
		}
		setClauses = append(setClauses, "duration_minutes = ?")
		args = append(args, *candidate.DurationMinutes)
	}
	if candidate.OriginalStartLocal != nil {
		setClauses = append(setClauses, "original_start_local = ?")
		args = append(args, domain.FormatLocalTime(*candidate.OriginalStartLocal))
	}
	if openPresent {
		setClauses = append(setClauses, "open = ?")
		if candidate.Open == nil {
			err = domain.NewValidation("open must be a boolean", "open")
			return domain.Schedule{}, err
		}
		args = append(args, boolToInt(*candidate.Open))
	}
	if !targetRecurring {
		// 数据库触发器也保护该不变量；这里提前返回机器可读错误，避免泄露驱动错误。
		open := existing.Open
		if candidate.Open != nil {
			open = *candidate.Open
		}
		if !open {
			err = domain.NewValidation("single schedule must remain open", "open")
			return domain.Schedule{}, err
		}
	}
	setClauses = append(setClauses, "updated_at_utc = ?", "revision = revision + 1")
	args = append(args, formatUTCTime(candidate.UpdatedAtUTC), int64(id), candidate.ExpectedRevision)
	result, err := tx.ExecContext(ctx, "UPDATE schedules SET "+strings.Join(setClauses, ", ")+" WHERE id = ? AND revision = ?", args...)
	if err != nil {
		err = fmt.Errorf("update schedule %d: %w", id, err)
		return domain.Schedule{}, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		err = fmt.Errorf("read updated schedule %d count: %w", id, err)
		return domain.Schedule{}, err
	}
	if rowsAffected != 1 {
		err = domain.NewStaleRevision("schedule revision is stale", "expected_revision")
		return domain.Schedule{}, err
	}

	schedule, err = scanScheduleRow(tx.QueryRowContext(ctx, "SELECT "+scheduleColumns+" FROM schedules WHERE id = ?", int64(id)))
	if err != nil {
		err = fmt.Errorf("select updated schedule %d: %w", id, err)
		return domain.Schedule{}, err
	}
	if err = tx.Commit(); err != nil {
		err = fmt.Errorf("commit update schedule transaction: %w", err)
		return domain.Schedule{}, err
	}
	s.bumpGeneration()
	if s.logger != nil {
		beforeType := scheduleType(existing)
		afterType := scheduleType(schedule)
		s.logger.Info("schedule updated", "schedule_id", schedule.ID, "revision", schedule.Revision, "month_end_policy", schedule.MonthEndPolicy, "schedule_type_before", beforeType, "schedule_type_after", afterType, "type_conversion", beforeType != afterType)
	}
	return schedule, nil
}

// scheduleType 为更新日志提供稳定、非敏感的日程类型上下文。
func scheduleType(schedule domain.Schedule) string {
	if schedule.IsRecurring() {
		return "recurring"
	}
	return "single"
}

// DeleteSchedule 在事务内检查版本并物理删除日程，返回已读取的标题。
func (s *Store) DeleteSchedule(ctx context.Context, id domain.ScheduleID, expectedRevision int64) (title string, err error) {
	if s == nil || s.db == nil {
		return "", errors.New("sqlite store is not open")
	}
	if id <= 0 {
		return "", domain.NewValidation("schedule ID must be positive", "id")
	}
	if expectedRevision <= 0 {
		return "", domain.NewValidation("expected revision must be positive", "expected_revision")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin delete schedule transaction: %w", err)
	}
	defer func() {
		if err == nil {
			return
		}
		// 读取、删除或提交任一步失败都不能留下部分级联清理结果。
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			err = errors.Join(err, fmt.Errorf("rollback delete schedule transaction: %w", rollbackErr))
		}
	}()

	var revision int64
	err = tx.QueryRowContext(ctx, "SELECT title, revision FROM schedules WHERE id = ?", int64(id)).Scan(&title, &revision)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			title = ""
			err = &domain.Error{Code: domain.NotFound, Message: fmt.Sprintf("schedule %d was not found", id), Cause: err}
			return title, err
		}
		err = fmt.Errorf("select schedule %d for deletion: %w", id, err)
		return title, err
	}
	if revision != expectedRevision {
		err = domain.NewStaleRevision("schedule revision is stale", "expected_revision")
		return title, err
	}

	result, err := tx.ExecContext(ctx, "DELETE FROM schedules WHERE id = ? AND revision = ?", int64(id), expectedRevision)
	if err != nil {
		err = fmt.Errorf("delete schedule %d: %w", id, err)
		return title, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		err = fmt.Errorf("read deleted schedule %d count: %w", id, err)
		return title, err
	}
	if rowsAffected != 1 {
		err = domain.NewStaleRevision("schedule revision is stale", "expected_revision")
		return title, err
	}
	if err = tx.Commit(); err != nil {
		err = fmt.Errorf("commit delete schedule transaction: %w", err)
		return title, err
	}
	s.bumpGeneration()
	if s.logger != nil {
		s.logger.Info("schedule deleted", "schedule_id", id, "revision", expectedRevision)
	}
	return title, nil
}

// GetSchedule 按 ID 读取日程。
func (s *Store) GetSchedule(ctx context.Context, id domain.ScheduleID) (domain.Schedule, error) {
	if s == nil || s.db == nil {
		return domain.Schedule{}, errors.New("sqlite store is not open")
	}
	row := s.db.QueryRowContext(ctx, "SELECT "+scheduleColumns+" FROM schedules WHERE id = ?", int64(id))
	schedule, err := scanScheduleRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Schedule{}, &domain.Error{
				Code:    domain.NotFound,
				Message: fmt.Sprintf("schedule %d was not found", id),
				Cause:   err,
			}
		}
		return domain.Schedule{}, fmt.Errorf("select schedule %d: %w", id, err)
	}
	return schedule, nil
}

// ListSchedules 按 ID 游标分页读取日程。
func (s *Store) ListSchedules(ctx context.Context, request app.ListSchedulesRequest) (app.ListSchedulesResponse, error) {
	if s == nil || s.db == nil {
		return app.ListSchedulesResponse{}, errors.New("sqlite store is not open")
	}
	limit := request.Limit
	if limit == 0 {
		limit = scheduleListLimit
	}
	if limit < 0 || limit == int(^uint(0)>>1) {
		return app.ListSchedulesResponse{}, domain.NewValidation("limit is invalid", "limit")
	}
	cursorID, err := decodeCursor(request.Cursor)
	if err != nil {
		// 非法游标是输入错误，不应伪装成数据库故障或退回第一页。
		return app.ListSchedulesResponse{}, err
	}

	query := "SELECT " + scheduleColumns + " FROM schedules"
	args := make([]any, 0, 2)
	if cursorID != 0 {
		query += " WHERE id > ?"
		args = append(args, int64(cursorID))
	}
	query += " ORDER BY id ASC LIMIT ?"
	args = append(args, limit+1)

	// 使用 LIMIT + 1 区分“刚好填满”与“确实存在下一页”。
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return app.ListSchedulesResponse{}, fmt.Errorf("query schedules: %w", err)
	}
	schedules, err := scanScheduleRows(rows)
	if err != nil {
		return app.ListSchedulesResponse{}, fmt.Errorf("scan schedules: %w", err)
	}

	hasNextPage := len(schedules) > limit
	if hasNextPage {
		schedules = schedules[:limit]
	}
	response := app.ListSchedulesResponse{Items: make([]app.ScheduleDTO, 0, len(schedules))}
	for _, schedule := range schedules {
		response.Items = append(response.Items, scheduleDTO(schedule))
	}
	if hasNextPage && len(response.Items) > 0 {
		response.NextCursor = encodeCursor(domain.ScheduleID(response.Items[len(response.Items)-1].ID))
	}
	return response, nil
}

// scanScheduleRow 将数据库行转换为领域日程。
func scanScheduleRow(row *sql.Row) (domain.Schedule, error) {
	return scanScheduleFields(row.Scan)
}

// scanScheduleFields 将单条 SQL 行转换为领域日程，复用 Row 和 Rows 的同一字段映射。
func scanScheduleFields(scan func(...any) error) (domain.Schedule, error) {
	var (
		schedule          domain.Schedule
		id                int64
		notes             string
		cron              sql.NullString
		untilDate         sql.NullString
		open              int
		firstStartText    string
		originalStartText string
		createdText       string
		updatedText       string
		monthEndPolicy    sql.NullString
	)
	if err := scan(
		&id,
		&schedule.Title,
		&notes,
		&cron,
		&open,
		&firstStartText,
		&originalStartText,
		&schedule.DurationMinutes,
		&schedule.Revision,
		&createdText,
		&updatedText,
		&untilDate,
		&monthEndPolicy,
	); err != nil {
		return domain.Schedule{}, err
	}
	firstStart, err := parseStoredTime(firstStartText, "first_start_local")
	if err != nil {
		return domain.Schedule{}, err
	}
	originalStart, err := parseStoredTime(originalStartText, "original_start_local")
	if err != nil {
		return domain.Schedule{}, err
	}
	createdAt, err := parseStoredTime(createdText, "created_at_utc")
	if err != nil {
		return domain.Schedule{}, err
	}
	updatedAt, err := parseStoredTime(updatedText, "updated_at_utc")
	if err != nil {
		return domain.Schedule{}, err
	}
	if open != 0 && open != 1 {
		return domain.Schedule{}, fmt.Errorf("invalid open value %d", open)
	}

	schedule.ID = domain.ScheduleID(id)
	schedule.Notes = notes
	schedule.Cron = nullStringPointer(cron)
	schedule.UntilDateLocal = nullStringPointer(untilDate)
	schedule.MonthEndPolicy = nullStringPointer(monthEndPolicy)
	schedule.Open = open == 1
	schedule.FirstStartLocal = firstStart
	schedule.OriginalStartLocal = originalStart
	schedule.CreatedAtUTC = createdAt.UTC()
	schedule.UpdatedAtUTC = updatedAt.UTC()
	return schedule, nil
}

// scanScheduleRows 将查询结果转换为领域日程列表并完整关闭游标。
func scanScheduleRows(rows *sql.Rows) (schedules []domain.Schedule, err error) {
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			closeErr = fmt.Errorf("close schedule rows: %w", closeErr)
			if err == nil {
				err = closeErr
			} else {
				err = errors.Join(err, closeErr)
			}
		}
	}()
	for rows.Next() {
		// 每一行都经过同一转换规则，避免单条读取和分页读取产生不同状态。
		var values domain.Schedule
		values, err = scanScheduleFields(rows.Scan)
		if err != nil {
			return nil, err
		}
		schedules = append(schedules, values)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate schedule rows: %w", err)
	}
	return schedules, nil
}

// encodeCursor 将日程 ID 编码为分页游标。
func encodeCursor(id domain.ScheduleID) string {
	if id <= 0 {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(int64(id), 10)))
}

// decodeCursor 将分页游标解码为日程 ID。
func decodeCursor(cursor string) (domain.ScheduleID, error) {
	if cursor == "" {
		return 0, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, &domain.Error{Code: domain.Validation, Message: "cursor is invalid", Fields: []string{"cursor"}, Cause: err}
	}
	id, err := strconv.ParseInt(string(decoded), 10, 64)
	if err != nil || id <= 0 {
		return 0, &domain.Error{Code: domain.Validation, Message: "cursor is invalid", Fields: []string{"cursor"}, Cause: err}
	}
	return domain.ScheduleID(id), nil
}

// scheduleDTO 将领域日程转换为应用层的受控分页视图。
func scheduleDTO(schedule domain.Schedule) app.ScheduleDTO {
	return app.ScheduleDTO{
		ID:              int64(schedule.ID),
		Title:           schedule.Title,
		Notes:           schedule.Notes,
		Cron:            schedule.Cron,
		UntilDateLocal:  schedule.UntilDateLocal,
		MonthEndPolicy:  schedule.MonthEndPolicy,
		Open:            schedule.Open,
		FirstStartLocal: domain.FormatLocalTime(schedule.FirstStartLocal),
		DurationMinutes: schedule.DurationMinutes,
		Revision:        schedule.Revision,
		CreatedAtUTC:    schedule.CreatedAtUTC,
		UpdatedAtUTC:    schedule.UpdatedAtUTC,
	}
}

// boolToInt 将 Go 布尔状态转换为 SQLite 的 CHECK 约束取值。
func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// formatUTCTime 将审计时间标准化为 UTC 文本，避免数据库中混入本地偏移。
func formatUTCTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

// parseStoredTime 解析 SQLite 中保存的 RFC3339 时间文本。
func parseStoredTime(value, field string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse %s %q: %w", field, value, err)
	}
	return parsed, nil
}

// nullStringPointer 将 SQL NULL 恢复为真正的 nil 指针。
func nullStringPointer(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	result := value.String
	return &result
}
