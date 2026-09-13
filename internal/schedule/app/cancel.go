package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// CancelOccurrence 取消某个原始日期的发生，并保留已有例外快照。
func (s *Service) CancelOccurrence(ctx context.Context, id domain.ScheduleID, request OccurrenceActionRequest) (OccurrenceActionDTO, error) {
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
	canceller, ok := s.store.(OccurrenceCanceller)
	if !ok {
		return OccurrenceActionDTO{}, errors.New("schedule store does not support occurrence cancellation")
	}
	if s.clock == nil {
		return OccurrenceActionDTO{}, errors.New("schedule clock is not configured")
	}

	schedule, err := s.store.GetSchedule(ctx, id)
	if err != nil {
		return OccurrenceActionDTO{}, fmt.Errorf("read schedule %d for occurrence cancellation: %w", id, err)
	}
	if schedule.Revision != request.ExpectedRevision {
		return OccurrenceActionDTO{}, domain.NewStaleRevision("schedule revision is stale", "expected_revision")
	}
	override, overrideErr := reader.GetOccurrenceOverride(ctx, id, request.OriginalDate)
	if overrideErr != nil && !isNotFound(overrideErr) {
		return OccurrenceActionDTO{}, fmt.Errorf("read occurrence override %d/%s: %w", id, request.OriginalDate, overrideErr)
	}
	if overrideErr != nil {
		// 空快照只允许标记当前规则真实产生的身份，避免制造孤立取消事实。
		snapshot, snapshotErr := s.clock.Snapshot(ctx)
		if snapshotErr != nil {
			return OccurrenceActionDTO{}, fmt.Errorf("snapshot current time: %w", snapshotErr)
		}
		_, found, occurrenceErr := domain.OccurrenceOnDate(schedule, request.OriginalDate, snapshot.Location)
		if occurrenceErr != nil {
			return OccurrenceActionDTO{}, occurrenceErr
		}
		if !found {
			return OccurrenceActionDTO{}, domain.NewValidation("original date does not belong to the current schedule", "original_date")
		}
		candidate := domain.OccurrenceCancelCandidate{
			OriginalDate:     request.OriginalDate,
			ExpectedRevision: request.ExpectedRevision,
			UpdatedAtUTC:     snapshot.Now.UTC(),
		}
		result, cancelErr := canceller.CancelOccurrence(ctx, id, candidate)
		if cancelErr != nil {
			return OccurrenceActionDTO{}, fmt.Errorf("cancel occurrence %d/%s: %w", id, request.OriginalDate, cancelErr)
		}
		return occurrenceActionDTO(id, request.OriginalDate, result), nil
	}

	// 已有例外（无论是开放快照还是已取消状态）本身就是稳定身份的事实，
	// 取消操作不再要求当前规则仍能计算出其原始位置。
	snapshot, snapshotErr := s.clock.Snapshot(ctx)
	if snapshotErr != nil {
		return OccurrenceActionDTO{}, fmt.Errorf("snapshot current time: %w", snapshotErr)
	}
	candidate := domain.OccurrenceCancelCandidate{
		OriginalDate:     request.OriginalDate,
		ExpectedRevision: request.ExpectedRevision,
		UpdatedAtUTC:     snapshot.Now.UTC(),
	}
	result, cancelErr := canceller.CancelOccurrence(ctx, id, candidate)
	if cancelErr != nil {
		return OccurrenceActionDTO{}, fmt.Errorf("cancel occurrence %d/%s: %w", id, request.OriginalDate, cancelErr)
	}
	if result.OverridePresent && result.Override.OriginalDate == "" {
		result.Override = override
	}
	return occurrenceActionDTO(id, request.OriginalDate, result), nil
}

// validateOccurrenceActionRequest 校验取消/恢复共用的标识和版本字段。
func validateOccurrenceActionRequest(id domain.ScheduleID, request OccurrenceActionRequest) error {
	if id <= 0 {
		return domain.NewValidation("schedule ID must be positive", "id")
	}
	if request.ExpectedRevision <= 0 {
		return domain.NewValidation("expected revision must be positive", "expected_revision")
	}
	if strings.TrimSpace(request.OriginalDate) == "" {
		return domain.NewValidation("original date is required", "original_date")
	}
	return nil
}

// occurrenceActionDTO 将持久化操作结果转换为不暴露内部时间对象的 DTO。
func occurrenceActionDTO(id domain.ScheduleID, originalDate string, result domain.OccurrenceActionResult) OccurrenceActionDTO {
	dto := OccurrenceActionDTO{
		ScheduleID:       int64(id),
		OriginalDate:     originalDate,
		ScheduleOpen:     result.ScheduleOpen,
		ScheduleRevision: result.ScheduleRevision,
	}
	if !result.OverridePresent {
		return dto
	}
	dto.Cancelled = !result.Override.Open
	dto.SnapshotPresent = result.Override.SnapshotPresent
	if !result.Override.SnapshotPresent {
		return dto
	}
	startUnix := result.Override.StartLocal.Unix()
	endUnix := result.Override.StartLocal.Add(duration(result.Override.DurationMinutes)).Unix()
	title, notes := result.Override.Title, result.Override.Notes
	dto.StartUnix = &startUnix
	dto.EndUnix = &endUnix
	dto.Title = &title
	dto.Notes = &notes
	return dto
}
