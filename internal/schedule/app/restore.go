package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// RestoreOccurrence 恢复某个原始日期的发生，并按当前规则处理空取消例外。
func (s *Service) RestoreOccurrence(ctx context.Context, id domain.ScheduleID, request OccurrenceActionRequest) (OccurrenceActionDTO, error) {
	if err := validateOccurrenceActionRequest(id, request); err != nil {
		return OccurrenceActionDTO{}, err
	}
	if s.store == nil {
		return OccurrenceActionDTO{}, errors.New("schedule store is not configured")
	}
	reader, ok := s.store.(OccurrenceReader)
	if !ok {
		return OccurrenceActionDTO{}, errors.New("schedule store does not support occurrence reads")
	}
	restorer, ok := s.store.(OccurrenceRestorer)
	if !ok {
		return OccurrenceActionDTO{}, errors.New("schedule store does not support occurrence restoration")
	}
	if s.clock == nil {
		return OccurrenceActionDTO{}, errors.New("schedule clock is not configured")
	}

	schedule, err := s.store.GetSchedule(ctx, id)
	if err != nil {
		return OccurrenceActionDTO{}, fmt.Errorf("read schedule %d for occurrence restoration: %w", id, err)
	}
	if schedule.Revision != request.ExpectedRevision {
		return OccurrenceActionDTO{}, domain.NewStaleRevision("schedule revision is stale", "expected_revision")
	}
	override, overrideErr := reader.GetOccurrenceOverride(ctx, id, request.OriginalDate)
	if overrideErr != nil && !isNotFound(overrideErr) {
		return OccurrenceActionDTO{}, fmt.Errorf("read occurrence override %d/%s: %w", id, request.OriginalDate, overrideErr)
	}
	snapshot, snapshotErr := s.clock.Snapshot(ctx)
	if snapshotErr != nil {
		return OccurrenceActionDTO{}, fmt.Errorf("snapshot current time: %w", snapshotErr)
	}

	removeCancellation := false
	if overrideErr != nil {
		// 没有取消记录时，恢复是幂等操作，但必须确认当前规则仍有该身份，
		// 避免把一个不存在的目标伪装成成功。
		if _, found, occurrenceErr := domain.OccurrenceOnDate(schedule, request.OriginalDate, snapshot.Location); occurrenceErr != nil {
			return OccurrenceActionDTO{}, occurrenceErr
		} else if !found {
			return OccurrenceActionDTO{}, domain.NewValidation("original date does not belong to the current schedule", "original_date")
		}
	} else if !override.Open && !override.SnapshotPresent {
		// 空快照取消只代表“屏蔽当前规则的一次发生”；规则尚未恢复时，
		// 保留该事实并拒绝恢复，不得删除用户原本的取消意图。
		if _, found, occurrenceErr := domain.OccurrenceOnDate(schedule, request.OriginalDate, snapshot.Location); occurrenceErr != nil {
			return OccurrenceActionDTO{}, occurrenceErr
		} else if !found {
			return OccurrenceActionDTO{}, domain.NewValidation("original date does not belong to the current schedule", "original_date")
		}
		removeCancellation = true
	}

	candidate := domain.OccurrenceRestoreCandidate{
		OriginalDate:       request.OriginalDate,
		ExpectedRevision:   request.ExpectedRevision,
		UpdatedAtUTC:       snapshot.Now.UTC(),
		RemoveCancellation: removeCancellation,
	}
	result, restoreErr := restorer.RestoreOccurrence(ctx, id, candidate)
	if restoreErr != nil {
		return OccurrenceActionDTO{}, fmt.Errorf("restore occurrence %d/%s: %w", id, request.OriginalDate, restoreErr)
	}
	return occurrenceActionDTO(id, request.OriginalDate, result), nil
}
