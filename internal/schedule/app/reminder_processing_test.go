package app

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// processingTestStore 为提醒处理用例提供可控的查询、重建和确认端口。
type processingTestStore struct {
	occurrenceQueryFake
	attempts       []ReminderAttemptRecord
	confirmCalls   int
	confirmResult  ReminderConfirmationResult
	confirmErr     error
	confirmedAtUTC time.Time
}

// ListPendingReminderAttempts 返回测试用例中的待处理记录页。
func (s *processingTestStore) ListPendingReminderAttempts(_ context.Context, request ListPendingReminderAttemptsRequest) (ListPendingReminderAttemptsResponse, error) {
	items := make([]ReminderAttemptRecord, 0)
	for _, attempt := range s.attempts {
		if attempt.ID <= request.AfterID || attempt.Status != ReminderAttemptSubmitted || attempt.ProcessedAtUTC != nil {
			continue
		}
		items = append(items, attempt)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	if request.Limit > 0 && len(items) > request.Limit {
		return ListPendingReminderAttemptsResponse{Items: items[:request.Limit], NextCursor: items[request.Limit-1].ID}, nil
	}
	return ListPendingReminderAttemptsResponse{Items: items}, nil
}

// GetReminderAttempt 返回测试用例中的稳定身份记录。
func (s *processingTestStore) GetReminderAttempt(_ context.Context, identity domain.OccurrenceIdentity) (ReminderAttemptRecord, error) {
	for _, attempt := range s.attempts {
		if attempt.Identity == identity {
			return attempt, nil
		}
	}
	return ReminderAttemptRecord{}, domain.NewNotFound("reminder attempt is missing")
}

// GetOccurrenceSource 返回测试用例中的当前日程及例外事实。
func (s *processingTestStore) GetOccurrenceSource(_ context.Context, id domain.ScheduleID) (OccurrenceSourceDTO, error) {
	for _, source := range s.sources {
		if source.Schedule.ID == id {
			return source, nil
		}
	}
	return OccurrenceSourceDTO{}, domain.NewNotFound("occurrence source is missing")
}

// ConfirmReminderAttempt 记录确认时间并返回预置的事务结果。
func (s *processingTestStore) ConfirmReminderAttempt(_ context.Context, _ domain.OccurrenceIdentity, processedAtUTC time.Time) (ReminderConfirmationResult, error) {
	s.confirmCalls++
	s.confirmedAtUTC = processedAtUTC
	if s.confirmErr != nil {
		return ReminderConfirmationResult{}, s.confirmErr
	}
	if s.confirmResult.ProcessedAtUTC.IsZero() {
		s.confirmResult.ProcessedAtUTC = processedAtUTC
	}
	return s.confirmResult, nil
}

// TestListPendingRemindersFiltersAndSortsWithUnavailableFallback 验证严格过滤、实时排序和缺失内容保留。
func TestListPendingRemindersFiltersAndSortsWithUnavailableFallback(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	store := &processingTestStore{
		occurrenceQueryFake: occurrenceQueryFake{generation: 7, sources: []OccurrenceSourceDTO{
			{Schedule: makeQuerySchedule(1, 9)},
			{Schedule: makeQuerySchedule(2, 9)},
		}},
		attempts: []ReminderAttemptRecord{
			{ID: 1, Identity: domain.OccurrenceIdentity{ScheduleID: 1, OriginalDate: "2026-01-01"}, AttemptedAtUTC: now.Add(-time.Hour), Status: ReminderAttemptSubmitted},
			{ID: 2, Identity: domain.OccurrenceIdentity{ScheduleID: 2, OriginalDate: "2026-01-01"}, AttemptedAtUTC: now.Add(-time.Hour), Status: ReminderAttemptSubmitted},
			{ID: 3, Identity: domain.OccurrenceIdentity{ScheduleID: 1, OriginalDate: "2026-01-02"}, AttemptedAtUTC: now.Add(-2 * time.Hour), Status: ReminderAttemptSubmitted},
			{ID: 4, Identity: domain.OccurrenceIdentity{ScheduleID: 99, OriginalDate: "2026-01-01"}, AttemptedAtUTC: now.Add(3 * 24 * time.Hour), Status: ReminderAttemptSubmitted},
			{ID: 5, Identity: domain.OccurrenceIdentity{ScheduleID: 1, OriginalDate: "2026-01-03"}, AttemptedAtUTC: now, Status: ReminderAttemptClaimed},
			{ID: 6, Identity: domain.OccurrenceIdentity{ScheduleID: 1, OriginalDate: "2026-01-04"}, AttemptedAtUTC: now, Status: ReminderAttemptSubmitted, ProcessedAtUTC: timePtr(now)},
		},
	}
	service := NewService(store, &fakeClock{snapshot: domain.TimeSnapshot{Now: now, Location: time.UTC}})
	first, err := service.ListPendingReminders(context.Background(), ListPendingRemindersRequest{Limit: 2})
	if err != nil {
		t.Fatalf("first page error = %v", err)
	}
	if len(first.Items) != 2 || first.NextCursor == "" {
		t.Fatalf("first page = %+v", first)
	}
	if first.Items[0].ScheduleID != 99 || first.Items[0].ContentStatus != ReminderContentUnavailable {
		t.Fatalf("unavailable fallback item = %+v", first.Items[0])
	}
	if first.Items[1].ScheduleID != 1 || first.Items[1].OriginalDate != "2026-01-02" || first.Items[1].ContentStatus != ReminderContentAvailable {
		t.Fatalf("available item ordering = %+v", first.Items[1])
	}
	second, err := service.ListPendingReminders(context.Background(), ListPendingRemindersRequest{Limit: 2, Cursor: first.NextCursor})
	if err != nil {
		t.Fatalf("second page error = %v", err)
	}
	if len(second.Items) != 2 || second.NextCursor != "" {
		t.Fatalf("second page = %+v", second)
	}
	if second.Items[0].ScheduleID != 1 || second.Items[1].ScheduleID != 2 || second.Items[0].OriginalDate != "2026-01-01" || second.Items[1].OriginalDate != "2026-01-01" {
		t.Fatalf("same-time ordering = %+v", second.Items)
	}
	for _, item := range append(first.Items, second.Items...) {
		if item.Status != ReminderStatusPending {
			t.Fatalf("listed item status = %q", item.Status)
		}
	}
}

// TestListPendingRemindersRejectsChangedGeneration 验证日程事实在分页期间变化时必须刷新游标。
func TestListPendingRemindersRejectsChangedGeneration(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	store := &processingTestStore{
		occurrenceQueryFake: occurrenceQueryFake{generation: 1, changeDuringQuery: true, sources: []OccurrenceSourceDTO{{Schedule: makeQuerySchedule(1, 9)}}},
		attempts:            []ReminderAttemptRecord{{ID: 1, Identity: domain.OccurrenceIdentity{ScheduleID: 1, OriginalDate: "2026-01-01"}, AttemptedAtUTC: now, Status: ReminderAttemptSubmitted}},
	}
	service := NewService(store, &fakeClock{snapshot: domain.TimeSnapshot{Now: now, Location: time.UTC}})
	_, err := service.ListPendingReminders(context.Background(), ListPendingRemindersRequest{Limit: 1})
	var businessErr *domain.Error
	if !errors.As(err, &businessErr) || businessErr.Code != domain.StaleCursor {
		t.Fatalf("stale generation error = %#v", err)
	}
}

// TestGetReminderDetailRebuildsCurrentContentAndHidesNonPendingContent 验证详情实时重建和状态隔离。
func TestGetReminderDetailRebuildsCurrentContentAndHidesNonPendingContent(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	schedule := makeQuerySchedule(1, 9)
	store := &processingTestStore{
		occurrenceQueryFake: occurrenceQueryFake{generation: 1, sources: []OccurrenceSourceDTO{{Schedule: schedule}}},
		attempts: []ReminderAttemptRecord{
			{ID: 1, Identity: domain.OccurrenceIdentity{ScheduleID: 1, OriginalDate: "2026-01-01"}, AttemptedAtUTC: now, Status: ReminderAttemptSubmitted},
			{ID: 2, Identity: domain.OccurrenceIdentity{ScheduleID: 1, OriginalDate: "2026-01-02"}, AttemptedAtUTC: now, Status: ReminderAttemptSubmitted, ProcessedAtUTC: timePtr(now.Add(time.Minute))},
			{ID: 3, Identity: domain.OccurrenceIdentity{ScheduleID: 1, OriginalDate: "2026-01-03"}, AttemptedAtUTC: now, Status: ReminderAttemptFailed},
		},
	}
	clock := &fakeClock{snapshot: domain.TimeSnapshot{Now: now, Location: time.UTC}}
	service := NewService(store, clock)
	detail, err := service.GetReminderDetail(context.Background(), domain.OccurrenceIdentity{ScheduleID: 1, OriginalDate: "2026-01-01"})
	if err != nil || detail.ContentStatus != ReminderContentAvailable || detail.Title == nil || *detail.Title != "schedule" {
		t.Fatalf("pending detail = %+v, %v", detail, err)
	}
	store.sources[0].Schedule.Title = "edited"
	store.sources[0].Schedule.Notes = "current notes"
	detail, err = service.GetReminderDetail(context.Background(), domain.OccurrenceIdentity{ScheduleID: 1, OriginalDate: "2026-01-01"})
	if err != nil || detail.Title == nil || *detail.Title != "edited" || detail.Notes == nil || *detail.Notes != "current notes" {
		t.Fatalf("edited detail = %+v, %v", detail, err)
	}
	confirmed, err := service.GetReminderDetail(context.Background(), domain.OccurrenceIdentity{ScheduleID: 1, OriginalDate: "2026-01-02"})
	if err != nil || confirmed.Status != ReminderStatusConfirmed || confirmed.Title != nil || confirmed.ContentStatus != ReminderContentUnavailable {
		t.Fatalf("confirmed detail = %+v, %v", confirmed, err)
	}
	notProcessable, err := service.GetReminderDetail(context.Background(), domain.OccurrenceIdentity{ScheduleID: 1, OriginalDate: "2026-01-03"})
	if err != nil || notProcessable.Status != ReminderStatusNotProcessable || notProcessable.Title != nil {
		t.Fatalf("not processable detail = %+v, %v", notProcessable, err)
	}
	missing, err := service.GetReminderDetail(context.Background(), domain.OccurrenceIdentity{ScheduleID: 9, OriginalDate: "2026-01-01"})
	if err != nil || missing.Status != ReminderStatusMissing || missing.ContentStatus != ReminderContentUnavailable {
		t.Fatalf("missing detail = %+v, %v", missing, err)
	}
}

// TestConfirmReminderUsesUTCClockAndMutationBoundary 验证确认使用 UTC 快照并经过正式变更协调器。
func TestConfirmReminderUsesUTCClockAndMutationBoundary(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	store := &processingTestStore{confirmResult: ReminderConfirmationResult{Changed: true}}
	clock := &fakeClock{snapshot: domain.TimeSnapshot{Now: now, Location: now.Location()}}
	coordinator := NewMutationCoordinator()
	service := NewServiceWithCoordinator(store, clock, coordinator)
	result, err := service.ConfirmReminder(context.Background(), domain.OccurrenceIdentity{ScheduleID: 1, OriginalDate: "2026-01-01"})
	if err != nil {
		t.Fatalf("ConfirmReminder() error = %v", err)
	}
	if result.ProcessedAtUTC.Location() != time.UTC || !result.ProcessedAtUTC.Equal(now.UTC()) || !result.Changed {
		t.Fatalf("confirmation result = %+v", result)
	}
	if store.confirmCalls != 1 || !store.confirmedAtUTC.Equal(now.UTC()) || coordinator.Epoch() != 1 {
		t.Fatalf("confirmation boundary calls=%d time=%v epoch=%d", store.confirmCalls, store.confirmedAtUTC, coordinator.Epoch())
	}
}

// timePtr 返回测试所需的独立时间指针。
func timePtr(value time.Time) *time.Time {
	value = value.UTC()
	return &value
}
