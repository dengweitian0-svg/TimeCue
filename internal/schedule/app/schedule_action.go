package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// CancelSchedule 取消重复日程整组发生，并使用一次审计时钟快照。
func (s *Service) CancelSchedule(ctx context.Context, id domain.ScheduleID, request ScheduleActionRequest) (ScheduleActionDTO, error) {
	if err := validateScheduleActionRequest(id, request); err != nil {
		return ScheduleActionDTO{}, err
	}
	if s.store == nil {
		return ScheduleActionDTO{}, errors.New("schedule store is not configured")
	}
	canceller, ok := s.store.(ScheduleCanceller)
	if !ok {
		return ScheduleActionDTO{}, errors.New("schedule store does not support schedule cancellation")
	}
	if s.clock == nil {
		return ScheduleActionDTO{}, errors.New("schedule clock is not configured")
	}

	schedule, err := s.store.GetSchedule(ctx, id)
	if err != nil {
		return ScheduleActionDTO{}, fmt.Errorf("read schedule %d for cancellation: %w", id, err)
	}
	if !schedule.IsRecurring() {
		return ScheduleActionDTO{}, domain.NewValidation("schedule cancellation requires a recurring schedule", "id")
	}
	if schedule.Revision != request.ExpectedRevision {
		// 预读发现旧版本时不调用写端口；存储事务仍会再次检查版本。
		return ScheduleActionDTO{}, domain.NewStaleRevision("schedule revision is stale", "expected_revision")
	}
	snapshot, err := s.clock.Snapshot(ctx)
	if err != nil {
		return ScheduleActionDTO{}, fmt.Errorf("snapshot current time: %w", err)
	}
	result, err := canceller.CancelSchedule(ctx, id, domain.ScheduleActionCandidate{
		ExpectedRevision: request.ExpectedRevision,
		UpdatedAtUTC:     snapshot.Now.UTC(),
	})
	if err != nil {
		return ScheduleActionDTO{}, fmt.Errorf("cancel schedule %d: %w", id, err)
	}
	return scheduleActionDTO(id, result), nil
}

// RestoreSchedule 恢复重复日程整组发生，并保留存储端的幂等结果。
func (s *Service) RestoreSchedule(ctx context.Context, id domain.ScheduleID, request ScheduleActionRequest) (ScheduleActionDTO, error) {
	if err := validateScheduleActionRequest(id, request); err != nil {
		return ScheduleActionDTO{}, err
	}
	if s.store == nil {
		return ScheduleActionDTO{}, errors.New("schedule store is not configured")
	}
	restorer, ok := s.store.(ScheduleRestorer)
	if !ok {
		return ScheduleActionDTO{}, errors.New("schedule store does not support schedule restoration")
	}
	if s.clock == nil {
		return ScheduleActionDTO{}, errors.New("schedule clock is not configured")
	}

	schedule, err := s.store.GetSchedule(ctx, id)
	if err != nil {
		return ScheduleActionDTO{}, fmt.Errorf("read schedule %d for restoration: %w", id, err)
	}
	if !schedule.IsRecurring() {
		return ScheduleActionDTO{}, domain.NewValidation("schedule restoration requires a recurring schedule", "id")
	}
	if schedule.Revision != request.ExpectedRevision {
		// 恢复同样必须在预读版本匹配时才产生写副作用。
		return ScheduleActionDTO{}, domain.NewStaleRevision("schedule revision is stale", "expected_revision")
	}
	snapshot, err := s.clock.Snapshot(ctx)
	if err != nil {
		return ScheduleActionDTO{}, fmt.Errorf("snapshot current time: %w", err)
	}
	result, err := restorer.RestoreSchedule(ctx, id, domain.ScheduleActionCandidate{
		ExpectedRevision: request.ExpectedRevision,
		UpdatedAtUTC:     snapshot.Now.UTC(),
	})
	if err != nil {
		return ScheduleActionDTO{}, fmt.Errorf("restore schedule %d: %w", id, err)
	}
	return scheduleActionDTO(id, result), nil
}

// validateScheduleActionRequest 校验整组操作共用的标识和版本字段。
func validateScheduleActionRequest(id domain.ScheduleID, request ScheduleActionRequest) error {
	if id <= 0 {
		return domain.NewValidation("schedule ID must be positive", "id")
	}
	if request.ExpectedRevision <= 0 {
		return domain.NewValidation("expected revision must be positive", "expected_revision")
	}
	return nil
}

// scheduleActionDTO 将整组状态转换结果投影为最小公共响应。
func scheduleActionDTO(id domain.ScheduleID, result domain.ScheduleActionResult) ScheduleActionDTO {
	return ScheduleActionDTO{
		ScheduleID:   int64(id),
		Open:         result.Open,
		Revision:     result.Revision,
		UpdatedAtUTC: result.UpdatedAtUTC.UTC(),
	}
}
