package app

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// UpdateOccurrence 校验某个原始日期的补丁，读取当前有效发生后写入完整例外快照。
func (s *Service) UpdateOccurrence(ctx context.Context, id domain.ScheduleID, request UpdateOccurrenceRequest) (OccurrenceDTO, error) {
	if s.store == nil {
		return OccurrenceDTO{}, errors.New("schedule store is not configured")
	}
	updater, ok := s.store.(OccurrenceUpdater)
	if !ok {
		return OccurrenceDTO{}, errors.New("schedule store does not support occurrence updates")
	}
	if s.clock == nil {
		return OccurrenceDTO{}, errors.New("schedule clock is not configured")
	}
	snapshot, err := s.clock.Snapshot(ctx)
	if err != nil {
		return OccurrenceDTO{}, fmt.Errorf("snapshot current time: %w", err)
	}
	resolved, err := resolveOccurrenceUpdate(ctx, s.store, snapshot, id, request)
	if err != nil {
		return OccurrenceDTO{}, err
	}

	var result domain.OccurrenceUpdateResult
	err = s.withMutation(func() error {
		var writeErr error
		result, writeErr = updater.UpdateOccurrence(ctx, id, resolved.Candidate)
		return writeErr
	})
	if err != nil {
		return OccurrenceDTO{}, fmt.Errorf("update occurrence %d/%s: %w", id, request.OriginalDate, err)
	}
	updated := occurrenceDTO(result)
	// 最终冲突基于提交后的快照计算，预览之后的事实变化以已提交状态为准。
	operation := changeOperation{
		operation: ChangeOperationUpdateOccurrence, scheduleID: id, originalDate: request.OriginalDate,
	}
	conflict, err := s.committedConflict(ctx, snapshot, operation)
	if err != nil {
		return OccurrenceDTO{}, err
	}
	updated.Conflict = conflict
	return updated, nil
}

// occurrenceUpdateResolution 保存一次单次编辑校验后的完整候选、当前发生和变更后的发生。
type occurrenceUpdateResolution struct {
	Schedule  domain.Schedule
	Candidate domain.OccurrenceOverrideCandidate
	Current   domain.Occurrence
	Post      domain.Occurrence
}

// resolveOccurrenceUpdate 校验单次编辑补丁并补齐缺失端点，不产生任何写入副作用。
// 正式提交和只读预览共用同一入口；override 参数为空时从存储读取当前例外。
func resolveOccurrenceUpdate(ctx context.Context, store ScheduleStore, snapshot domain.TimeSnapshot, id domain.ScheduleID, request UpdateOccurrenceRequest) (occurrenceUpdateResolution, error) {
	if id <= 0 {
		return occurrenceUpdateResolution{}, domain.NewValidation("schedule ID must be positive", "id")
	}
	if request.ExpectedRevision <= 0 {
		return occurrenceUpdateResolution{}, domain.NewValidation("expected revision must be positive", "expected_revision")
	}
	if strings.TrimSpace(request.OriginalDate) == "" {
		return occurrenceUpdateResolution{}, domain.NewValidation("original date is required", "original_date")
	}
	startPresent := request.StartUnixPresent || request.StartUnix != nil
	endPresent := request.EndUnixPresent || request.EndUnix != nil
	titlePresent := request.TitlePresent || request.Title != nil
	notesPresent := request.NotesPresent || request.Notes != nil
	if !startPresent && !endPresent && !titlePresent && !notesPresent {
		return occurrenceUpdateResolution{}, domain.NewValidation("at least one occurrence field must be provided", "start_unix", "end_unix", "title", "notes")
	}
	if startPresent && request.StartUnix == nil {
		return occurrenceUpdateResolution{}, domain.NewValidation("start time must be an integer", "start_unix")
	}
	if endPresent && request.EndUnix == nil {
		return occurrenceUpdateResolution{}, domain.NewValidation("end time must be an integer", "end_unix")
	}
	if titlePresent && request.Title == nil {
		return occurrenceUpdateResolution{}, domain.NewValidation("title must be a string", "title")
	}
	if notesPresent && request.Notes == nil {
		return occurrenceUpdateResolution{}, domain.NewValidation("notes must be a string", "notes")
	}
	reader, ok := store.(OccurrenceReader)
	if !ok {
		return occurrenceUpdateResolution{}, errors.New("schedule store does not support occurrence reads")
	}

	// 预读父日程只用于确认身份和版本；存储事务会再次以版本条件守住并发边界。
	schedule, err := store.GetSchedule(ctx, id)
	if err != nil {
		return occurrenceUpdateResolution{}, fmt.Errorf("read schedule %d for occurrence update: %w", id, err)
	}
	if schedule.Revision != request.ExpectedRevision {
		return occurrenceUpdateResolution{}, domain.NewStaleRevision("schedule revision is stale", "expected_revision")
	}

	override, overrideErr := reader.GetOccurrenceOverride(ctx, id, request.OriginalDate)
	if overrideErr != nil && !isNotFound(overrideErr) {
		return occurrenceUpdateResolution{}, fmt.Errorf("read occurrence override %d/%s: %w", id, request.OriginalDate, overrideErr)
	}
	hasOverride := overrideErr == nil

	var current domain.Occurrence
	if hasOverride {
		if !override.Open || !override.SnapshotPresent || override.DurationMinutes <= 0 {
			return occurrenceUpdateResolution{}, domain.NewValidation("occurrence override has no editable snapshot", "original_date")
		}
		current = domain.Occurrence{
			ScheduleID:   override.ScheduleID,
			OriginalDate: override.OriginalDate,
			StartLocal:   override.StartLocal,
			EndLocal:     override.StartLocal.Add(duration(override.DurationMinutes)),
			Title:        override.Title,
			Notes:        override.Notes,
		}
	} else {
		if !schedule.Open {
			// 关闭的父组不再产生基础发生，只有既有例外快照才可被编辑。
			return occurrenceUpdateResolution{}, domain.NewValidation("closed schedule has no occurrence to edit", "original_date")
		}
		var found bool
		current, found, err = domain.OccurrenceOnDate(schedule, request.OriginalDate, snapshot.Location)
		if err != nil {
			return occurrenceUpdateResolution{}, err
		}
		if !found {
			return occurrenceUpdateResolution{}, domain.NewValidation("original date does not belong to the current schedule", "original_date")
		}
	}

	startUnix, endUnix, err := deriveOccurrenceEndpoints(current.StartLocal.Unix(), current.EndLocal.Unix(), request.StartUnix, request.EndUnix)
	if err != nil {
		return occurrenceUpdateResolution{}, err
	}
	title := current.Title
	if titlePresent {
		title = strings.TrimSpace(*request.Title)
		if title == "" {
			return occurrenceUpdateResolution{}, domain.NewValidation("title must not be empty", "title")
		}
	}
	notes := current.Notes
	if notesPresent {
		notes = *request.Notes
	}
	candidate, err := domain.ValidateOccurrenceOverride(snapshot, request.OriginalDate, startUnix, endUnix, title, notes)
	if err != nil {
		return occurrenceUpdateResolution{}, err
	}
	candidate.ExpectedRevision = request.ExpectedRevision
	post := current
	post.StartLocal = candidate.StartLocal
	post.EndLocal = candidate.StartLocal.Add(duration(candidate.DurationMinutes))
	post.Title = candidate.Title
	post.Notes = candidate.Notes
	return occurrenceUpdateResolution{Schedule: schedule, Candidate: candidate, Current: current, Post: post}, nil
}

// deriveOccurrenceEndpoints 按当前发生时长补齐只提供一个端点的补丁。
func deriveOccurrenceEndpoints(currentStart, currentEnd int64, requestedStart, requestedEnd *int64) (int64, int64, error) {
	if currentEnd <= currentStart || (currentStart < 0 && currentEnd > math.MaxInt64+currentStart) {
		return 0, 0, domain.NewValidation("stored occurrence duration is invalid", "start_unix", "end_unix")
	}
	durationSeconds := currentEnd - currentStart
	if durationSeconds%60 != 0 {
		return 0, 0, domain.NewValidation("stored occurrence duration is invalid", "start_unix", "end_unix")
	}
	startUnix, endUnix := currentStart, currentEnd
	if requestedStart != nil {
		startUnix = *requestedStart
	}
	if requestedEnd != nil {
		endUnix = *requestedEnd
	} else if requestedStart != nil {
		var overflow bool
		endUnix, overflow = addInt64(startUnix, durationSeconds)
		if overflow {
			return 0, 0, domain.NewValidation("occurrence end time is out of range", "start_unix", "end_unix")
		}
	}
	if requestedStart == nil && requestedEnd != nil {
		var overflow bool
		startUnix, overflow = addInt64(endUnix, -durationSeconds)
		if overflow {
			return 0, 0, domain.NewValidation("occurrence start time is out of range", "start_unix", "end_unix")
		}
	}
	return startUnix, endUnix, nil
}

// duration 将完整快照的分钟数转换为实际结束时间增量。
func duration(minutes int) time.Duration {
	return time.Duration(minutes) * time.Minute
}

// isNotFound 判断窄存储端口是否报告了可继续回退到基础发生的未找到错误。
func isNotFound(err error) bool {
	var businessErr *domain.Error
	return errors.As(err, &businessErr) && businessErr.Code == domain.NotFound
}

// occurrenceDTO 将存储层返回的完整快照转换为对外实际发生视图。
func occurrenceDTO(result domain.OccurrenceUpdateResult) OccurrenceDTO {
	end := result.Override.StartLocal.Add(duration(result.Override.DurationMinutes))
	return OccurrenceDTO{
		ScheduleID:       int64(result.Override.ScheduleID),
		OriginalDate:     result.Override.OriginalDate,
		StartUnix:        result.Override.StartLocal.Unix(),
		EndUnix:          end.Unix(),
		Title:            result.Override.Title,
		Notes:            result.Override.Notes,
		ScheduleRevision: result.ScheduleRevision,
	}
}
