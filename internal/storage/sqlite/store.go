package sqlite

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/app"
	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

const scheduleColumns = `
	id, title, cron, open, first_start_local, original_start_local,
	duration_minutes, revision, created_at_utc, updated_at_utc`

const scheduleListLimit = 50

var _ app.ScheduleStore = (*Store)(nil)

// CreateSchedule 在单一事务中写入并读取日程。
func (s *Store) CreateSchedule(ctx context.Context, candidate domain.ScheduleCandidate) (schedule domain.Schedule, err error) {
	if s == nil || s.db == nil {
		return domain.Schedule{}, errors.New("sqlite store is not open")
	}

	// 应用层校验已经完成，此处开始把单次日程事实写入 SQLite 事务。
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
	// 这里只写入单次日程定义，不预先展开未来发生实例或保存结束时间。
	result, err := tx.ExecContext(ctx, `
		INSERT INTO schedules(
			title, cron, open, first_start_local, original_start_local,
			duration_minutes, revision, created_at_utc, updated_at_utc
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		candidate.Title,
		cron,
		boolToInt(candidate.Open),
		domain.FormatLocalTime(candidate.FirstStartLocal),
		domain.FormatLocalTime(candidate.OriginalStartLocal),
		candidate.DurationMinutes,
		1,
		formatUTCTime(candidate.CreatedAtUTC),
		formatUTCTime(candidate.UpdatedAtUTC),
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
	if s.logger != nil {
		s.logger.Info("schedule created", "schedule_id", schedule.ID)
	}
	return schedule, nil
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
		cron              sql.NullString
		open              int
		firstStartText    string
		originalStartText string
		createdText       string
		updatedText       string
	)
	if err := scan(
		&id,
		&schedule.Title,
		&cron,
		&open,
		&firstStartText,
		&originalStartText,
		&schedule.DurationMinutes,
		&schedule.Revision,
		&createdText,
		&updatedText,
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
	schedule.Cron = nullStringPointer(cron)
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
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("close schedule rows: %w", closeErr)
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
		Cron:            schedule.Cron,
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
