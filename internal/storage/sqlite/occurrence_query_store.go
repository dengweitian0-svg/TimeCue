package sqlite

import (
	"context"
	"database/sql"

	"errors"
	"fmt"
	"strings"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/app"
	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

const occurrenceSourceLimit = 200

var _ app.OccurrenceSourceReader = (*Store)(nil)

// GetOccurrenceSource 在单一只读事务中读取一个日程及其例外，供发送前最终资格校验使用。
func (s *Store) GetOccurrenceSource(ctx context.Context, id domain.ScheduleID) (app.OccurrenceSourceDTO, error) {
	if s == nil || s.db == nil {
		return app.OccurrenceSourceDTO{}, errors.New("sqlite store is not open")
	}
	if id <= 0 {
		return app.OccurrenceSourceDTO{}, domain.NewValidation("schedule ID must be positive", "id")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return app.OccurrenceSourceDTO{}, fmt.Errorf("begin occurrence source read transaction: %w", err)
	}
	rollback := func(cause error) (app.OccurrenceSourceDTO, error) {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			return app.OccurrenceSourceDTO{}, errors.Join(cause, fmt.Errorf("rollback occurrence source read transaction: %w", rollbackErr))
		}
		return app.OccurrenceSourceDTO{}, cause
	}
	schedule, err := scanScheduleRow(tx.QueryRowContext(ctx, "SELECT "+scheduleColumns+" FROM schedules WHERE id = ?", int64(id)))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return rollback(domain.NewNotFound(fmt.Sprintf("schedule %d was not found", id)))
		}
		return rollback(fmt.Errorf("read occurrence source schedule %d: %w", id, err))
	}
	rows, err := tx.QueryContext(ctx, "SELECT "+occurrenceOverrideColumns+" FROM occurrence_overrides WHERE schedule_id = ? ORDER BY id ASC", int64(id))
	if err != nil {
		return rollback(fmt.Errorf("read occurrence source overrides %d: %w", id, err))
	}
	overrides, err := scanOccurrenceOverrideRows(rows)
	if err != nil {
		return rollback(fmt.Errorf("scan occurrence source overrides %d: %w", id, err))
	}
	if err := tx.Commit(); err != nil {
		return app.OccurrenceSourceDTO{}, fmt.Errorf("commit occurrence source read transaction: %w", err)
	}
	return app.OccurrenceSourceDTO{Schedule: schedule, Overrides: overrides}, nil
}

// ListOccurrenceSources 按日程 ID 分批读取定义和例外快照，不生成实例行。
func (s *Store) ListOccurrenceSources(ctx context.Context, request app.ListOccurrenceSourcesRequest) (app.ListOccurrenceSourcesResponse, error) {
	if s == nil || s.db == nil {
		return app.ListOccurrenceSourcesResponse{}, errors.New("sqlite store is not open")
	}
	if request.AfterScheduleID < 0 {
		return app.ListOccurrenceSourcesResponse{}, domain.NewValidation("schedule source cursor is invalid", "cursor")
	}
	limit := request.Limit
	if limit == 0 {
		limit = occurrenceSourceLimit
	}
	if limit <= 0 || limit > occurrenceSourceLimit {
		return app.ListOccurrenceSourcesResponse{}, domain.NewValidation("source limit is invalid", "limit")
	}

	rows, err := s.db.QueryContext(ctx, "SELECT "+scheduleColumns+" FROM schedules WHERE id > ? ORDER BY id ASC LIMIT ?", int64(request.AfterScheduleID), limit+1)
	if err != nil {
		return app.ListOccurrenceSourcesResponse{}, fmt.Errorf("query occurrence source schedules: %w", err)
	}
	schedules, err := scanScheduleRows(rows)
	if err != nil {
		return app.ListOccurrenceSourcesResponse{}, fmt.Errorf("scan occurrence source schedules: %w", err)
	}
	hasNext := len(schedules) > limit
	if hasNext {
		schedules = schedules[:limit]
	}
	if len(schedules) == 0 {
		return app.ListOccurrenceSourcesResponse{}, nil
	}

	ids := make([]any, 0, len(schedules))
	placeholders := make([]string, 0, len(schedules))
	for _, schedule := range schedules {
		ids = append(ids, int64(schedule.ID))
		placeholders = append(placeholders, "?")
	}
	overrideRows, err := s.db.QueryContext(ctx,
		"SELECT "+occurrenceOverrideColumns+" FROM occurrence_overrides WHERE schedule_id IN ("+strings.Join(placeholders, ",")+") ORDER BY schedule_id ASC, id ASC",
		ids...)
	if err != nil {
		return app.ListOccurrenceSourcesResponse{}, fmt.Errorf("query occurrence source overrides: %w", err)
	}
	overrides, err := scanOccurrenceOverrideRows(overrideRows)
	if err != nil {
		return app.ListOccurrenceSourcesResponse{}, fmt.Errorf("scan occurrence source overrides: %w", err)
	}
	bySchedule := make(map[domain.ScheduleID][]domain.OccurrenceOverride, len(schedules))
	for _, override := range overrides {
		bySchedule[override.ScheduleID] = append(bySchedule[override.ScheduleID], override)
	}
	response := app.ListOccurrenceSourcesResponse{Items: make([]app.OccurrenceSourceDTO, 0, len(schedules))}
	for _, schedule := range schedules {
		response.Items = append(response.Items, app.OccurrenceSourceDTO{
			Schedule: schedule, Overrides: bySchedule[schedule.ID],
		})
	}
	if hasNext {
		response.NextCursor = schedules[len(schedules)-1].ID
	}
	return response, nil
}
