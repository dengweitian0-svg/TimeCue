package sqlite

import (
	"context"

	"errors"
	"fmt"
	"strings"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/app"
	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

const occurrenceSourceLimit = 200

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
