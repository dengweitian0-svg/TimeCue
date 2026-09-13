package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// fakeStore 用于验证应用服务与存储实现解耦。
type fakeStore struct {
	created             domain.Schedule
	createCalls         int
	getResult           domain.Schedule
	getErr              error
	updateResult        domain.Schedule
	updateErr           error
	updateCalls         int
	updated             domain.ScheduleUpdateCandidate
	override            domain.OccurrenceOverride
	overrides           []domain.OccurrenceOverride
	overrideListErr     error
	overrideErr         error
	occurrence          domain.OccurrenceUpdateResult
	occurrenceErr       error
	occurrenceCalls     int
	occurrenceCandidate domain.OccurrenceOverrideCandidate
	deleteTitle         string
	deleteErr           error
	deleteCalls         int
	deleteID            domain.ScheduleID
	deleteRev           int64
	listResult          ListSchedulesResponse
	listErr             error
	lastListReq         ListSchedulesRequest
}

// CreateSchedule 记录候选并返回确定的持久化日程。
func (s *fakeStore) CreateSchedule(_ context.Context, candidate domain.ScheduleCandidate) (domain.Schedule, error) {
	s.createCalls++
	s.created = domain.Schedule{
		ID:                 1,
		Title:              candidate.Title,
		Notes:              candidate.Notes,
		Cron:               candidate.Cron,
		UntilDateLocal:     candidate.UntilDateLocal,
		MonthEndPolicy:     candidate.MonthEndPolicy,
		Open:               candidate.Open,
		FirstStartLocal:    candidate.FirstStartLocal,
		OriginalStartLocal: candidate.OriginalStartLocal,
		DurationMinutes:    candidate.DurationMinutes,
		Revision:           1,
		CreatedAtUTC:       candidate.CreatedAtUTC,
		UpdatedAtUTC:       candidate.UpdatedAtUTC,
	}
	return s.created, nil
}

// GetSchedule 返回测试预置的持久化日程。
func (s *fakeStore) GetSchedule(_ context.Context, _ domain.ScheduleID) (domain.Schedule, error) {
	return s.getResult, s.getErr
}

// UpdateSchedule 记录更新候选并返回测试预置的日程。
func (s *fakeStore) UpdateSchedule(_ context.Context, _ domain.ScheduleID, candidate domain.ScheduleUpdateCandidate) (domain.Schedule, error) {
	s.updateCalls++
	s.updated = candidate
	return s.updateResult, s.updateErr
}

// GetOccurrenceOverride 返回测试预置的例外或未找到错误。
func (s *fakeStore) GetOccurrenceOverride(_ context.Context, _ domain.ScheduleID, _ string) (domain.OccurrenceOverride, error) {
	return s.override, s.overrideErr
}

// ListOccurrenceOverrides 返回测试预置的例外列表。
func (s *fakeStore) ListOccurrenceOverrides(_ context.Context, _ domain.ScheduleID) ([]domain.OccurrenceOverride, error) {
	return s.overrides, s.overrideListErr
}

// UpdateOccurrence 记录完整例外候选并返回测试预置结果。
func (s *fakeStore) UpdateOccurrence(_ context.Context, _ domain.ScheduleID, candidate domain.OccurrenceOverrideCandidate) (domain.OccurrenceUpdateResult, error) {
	s.occurrenceCalls++
	s.occurrenceCandidate = candidate
	return s.occurrence, s.occurrenceErr
}

// DeleteSchedule 记录删除请求并返回测试预置的标题和错误。
func (s *fakeStore) DeleteSchedule(_ context.Context, id domain.ScheduleID, expectedRevision int64) (string, error) {
	s.deleteCalls++
	s.deleteID = id
	s.deleteRev = expectedRevision
	return s.deleteTitle, s.deleteErr
}

// ListSchedules 返回测试预置的分页结果并记录请求。
func (s *fakeStore) ListSchedules(_ context.Context, request ListSchedulesRequest) (ListSchedulesResponse, error) {
	s.lastListReq = request
	return s.listResult, s.listErr
}

// fakeClock 用于提供确定的时间快照。
type fakeClock struct {
	snapshot domain.TimeSnapshot
	err      error
}

// Snapshot 返回预置的时间快照。
func (c *fakeClock) Snapshot(_ context.Context) (domain.TimeSnapshot, error) {
	return c.snapshot, c.err
}

// TestServiceCreateScheduleValidatesBeforeStore 验证应用层在存储副作用前完成全部输入校验。
func TestServiceCreateScheduleValidatesBeforeStore(t *testing.T) {
	location := time.FixedZone("UTC+8", 8*60*60)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, location)
	store := &fakeStore{}
	service := NewService(store, &fakeClock{snapshot: domain.TimeSnapshot{Now: now, Location: location}})
	start := now.Add(time.Hour).Unix()
	end := now.Add(2 * time.Hour).Unix()

	got, err := service.CreateSchedule(context.Background(), CreateScheduleRequest{Title: "  meeting  ", StartUnix: &start, EndUnix: &end})
	if err != nil {
		t.Fatalf("CreateSchedule() error = %v", err)
	}
	if got.Title != "meeting" || got.ID != 1 || got.DurationMinutes != 60 {
		t.Fatalf("result = %#v", got)
	}
	if store.createCalls != 1 || store.created.Title != "meeting" || store.created.Cron != nil || !store.created.Open {
		t.Fatalf("store state = %#v, calls = %d", store.created, store.createCalls)
	}

	badStart := start + 1
	_, err = service.CreateSchedule(context.Background(), CreateScheduleRequest{Title: "bad", StartUnix: &badStart, EndUnix: &end})
	if err == nil {
		t.Fatal("invalid CreateSchedule() error = nil")
	}
	if store.createCalls != 1 {
		t.Fatalf("invalid request reached store: calls = %d", store.createCalls)
	}
}

// TestServiceCreateScheduleRejectsMissingFields 验证缺少端点时不会读取时钟或调用存储。
func TestServiceCreateScheduleRejectsMissingFields(t *testing.T) {
	store := &fakeStore{}
	service := NewService(store, &fakeClock{})
	_, err := service.CreateSchedule(context.Background(), CreateScheduleRequest{Title: "missing"})
	if err == nil {
		t.Fatal("CreateSchedule() error = nil")
	}
	var businessErr *domain.Error
	if !errors.As(err, &businessErr) || businessErr.Code != domain.Validation {
		t.Fatalf("error = %#v, want validation error", err)
	}
	if store.createCalls != 0 {
		t.Fatalf("store calls = %d, want 0", store.createCalls)
	}
}

// TestServiceUpdateScheduleDerivesMissingEnd 验证更新开始时间时按已保存时长补齐结束时间。
func TestServiceUpdateScheduleDerivesMissingEnd(t *testing.T) {
	location := time.FixedZone("UTC+8", 8*60*60)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, location)
	existingStart := now.Add(time.Hour)
	existing := domain.Schedule{
		ID:              7,
		Title:           "old",
		FirstStartLocal: existingStart,
		DurationMinutes: 60,
		Revision:        3,
	}
	updated := existing
	updated.Title = "new"
	updated.FirstStartLocal = now.Add(2 * time.Hour)
	updated.Revision++
	newStart := updated.FirstStartLocal.Unix()
	newTitle := " new "
	store := &fakeStore{getResult: existing, updateResult: updated}
	service := NewService(store, &fakeClock{snapshot: domain.TimeSnapshot{Now: now, Location: location}})

	got, err := service.UpdateSchedule(context.Background(), existing.ID, UpdateScheduleRequest{
		Title:            &newTitle,
		StartUnix:        &newStart,
		ExpectedRevision: existing.Revision,
	})
	if err != nil {
		t.Fatalf("UpdateSchedule() error = %v", err)
	}
	if got.Title != "new" || store.updateCalls != 1 {
		t.Fatalf("result = %#v, update calls = %d", got, store.updateCalls)
	}
	if store.updated.FirstStartLocal == nil || !store.updated.FirstStartLocal.Equal(updated.FirstStartLocal) {
		t.Fatalf("updated start = %v, want %v", store.updated.FirstStartLocal, updated.FirstStartLocal)
	}
	if store.updated.DurationMinutes == nil || *store.updated.DurationMinutes != existing.DurationMinutes {
		t.Fatalf("updated duration = %v, want %d", store.updated.DurationMinutes, existing.DurationMinutes)
	}
	if store.updated.ExpectedRevision != existing.Revision {
		t.Fatalf("expected revision = %d, want %d", store.updated.ExpectedRevision, existing.Revision)
	}
}

// TestServiceUpdateScheduleDerivesMissingStart 验证更新结束时间时按已保存时长补齐开始时间。
func TestServiceUpdateScheduleDerivesMissingStart(t *testing.T) {
	location := time.FixedZone("UTC+8", 8*60*60)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, location)
	existing := domain.Schedule{
		ID:              10,
		FirstStartLocal: now.Add(time.Hour),
		DurationMinutes: 60,
		Revision:        2,
	}
	newEnd := now.Add(4 * time.Hour).Unix()
	store := &fakeStore{getResult: existing, updateResult: existing}
	service := NewService(store, &fakeClock{snapshot: domain.TimeSnapshot{Now: now, Location: location}})
	if _, err := service.UpdateSchedule(context.Background(), existing.ID, UpdateScheduleRequest{
		EndUnix:          &newEnd,
		ExpectedRevision: existing.Revision,
	}); err != nil {
		t.Fatalf("UpdateSchedule() error = %v", err)
	}
	wantStart := now.Add(3 * time.Hour)
	if store.updated.FirstStartLocal == nil || !store.updated.FirstStartLocal.Equal(wantStart) {
		t.Fatalf("updated start = %v, want %v", store.updated.FirstStartLocal, wantStart)
	}
	if store.updated.DurationMinutes == nil || *store.updated.DurationMinutes != existing.DurationMinutes {
		t.Fatalf("updated duration = %v, want %d", store.updated.DurationMinutes, existing.DurationMinutes)
	}
}

// TestServiceUpdateScheduleAllowsHistoricalTitleSave 验证历史日程未改端点时仍可重新保存标题。
func TestServiceUpdateScheduleAllowsHistoricalTitleSave(t *testing.T) {
	location := time.FixedZone("UTC+8", 8*60*60)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, location)
	existing := domain.Schedule{
		ID:              8,
		Title:           "old",
		FirstStartLocal: time.Date(2020, 1, 1, 10, 0, 0, 0, location),
		DurationMinutes: 60,
		Revision:        1,
	}
	newTitle := "history"
	store := &fakeStore{getResult: existing, updateResult: existing}
	service := NewService(store, &fakeClock{snapshot: domain.TimeSnapshot{Now: now, Location: location}})
	if _, err := service.UpdateSchedule(context.Background(), existing.ID, UpdateScheduleRequest{
		Title:            &newTitle,
		ExpectedRevision: existing.Revision,
	}); err != nil {
		t.Fatalf("historical UpdateSchedule() error = %v", err)
	}
	if store.updated.FirstStartLocal != nil || store.updated.DurationMinutes != nil {
		t.Fatal("historical title update unexpectedly changed time fields")
	}
}

// TestServiceUpdateScheduleRejectsUnsupportedFields 验证单次分支不会吞掉周期、取消和恢复字段。
func TestServiceUpdateScheduleRejectsUnsupportedFields(t *testing.T) {
	location := time.FixedZone("UTC+8", 8*60*60)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, location)
	existing := domain.Schedule{ID: 9, FirstStartLocal: now.Add(time.Hour), DurationMinutes: 60, Revision: 1}
	cron := "0 9 * * *"
	cancel := false
	restore := false
	for name, request := range map[string]UpdateScheduleRequest{
		"cron":    {Cron: &cron, ExpectedRevision: 1},
		"cancel":  {Cancel: &cancel, ExpectedRevision: 1},
		"restore": {Restore: &restore, ExpectedRevision: 1},
	} {
		t.Run(name, func(t *testing.T) {
			store := &fakeStore{getResult: existing, updateResult: existing}
			service := NewService(store, &fakeClock{snapshot: domain.TimeSnapshot{Now: now, Location: location}})
			_, err := service.UpdateSchedule(context.Background(), existing.ID, request)
			var businessErr *domain.Error
			if !errors.As(err, &businessErr) || businessErr.Code != domain.Validation {
				t.Fatalf("error = %#v, want validation", err)
			}
			if store.updateCalls != 0 {
				t.Fatalf("update calls = %d, want 0", store.updateCalls)
			}
		})
	}
}

// TestServiceUpdateScheduleRejectsOutOfRangeWithoutWrite 验证端点越界时不会调用更新存储。
func TestServiceUpdateScheduleRejectsOutOfRangeWithoutWrite(t *testing.T) {
	location := time.FixedZone("UTC+8", 8*60*60)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, location)
	existing := domain.Schedule{ID: 11, FirstStartLocal: now.Add(time.Hour), DurationMinutes: 60, Revision: 1}
	tooLate := time.Date(2028, 1, 1, 10, 0, 0, 0, location).Unix()
	store := &fakeStore{getResult: existing, updateResult: existing}
	service := NewService(store, &fakeClock{snapshot: domain.TimeSnapshot{Now: now, Location: location}})
	_, err := service.UpdateSchedule(context.Background(), existing.ID, UpdateScheduleRequest{
		StartUnix:        &tooLate,
		ExpectedRevision: existing.Revision,
	})
	var businessErr *domain.Error
	if !errors.As(err, &businessErr) || businessErr.Code != domain.TimeRangeExceeded {
		t.Fatalf("error = %#v, want time range error", err)
	}
	if store.updateCalls != 0 {
		t.Fatalf("update calls = %d, want 0", store.updateCalls)
	}
}

// TestServiceDeleteScheduleReturnsTitle 验证应用层透传成功标题和版本参数。
func TestServiceDeleteScheduleReturnsTitle(t *testing.T) {
	store := &fakeStore{deleteTitle: "to delete"}
	service := NewService(store, nil)
	title, err := service.DeleteSchedule(context.Background(), 12, DeleteScheduleRequest{ExpectedRevision: 4})
	if err != nil || title != "to delete" {
		t.Fatalf("DeleteSchedule() = %q, %v", title, err)
	}
	if store.deleteCalls != 1 || store.deleteID != 12 || store.deleteRev != 4 {
		t.Fatalf("delete request = calls %d, id %d, revision %d", store.deleteCalls, store.deleteID, store.deleteRev)
	}
}

// TestServiceDeleteSchedulePreservesTitleOnFailure 验证删除失败时仍返回存储层已读取的标题。
func TestServiceDeleteSchedulePreservesTitleOnFailure(t *testing.T) {
	store := &fakeStore{deleteTitle: "failed delete", deleteErr: domain.NewStaleRevision("schedule revision is stale", "expected_revision")}
	service := NewService(store, nil)
	title, err := service.DeleteSchedule(context.Background(), 13, DeleteScheduleRequest{ExpectedRevision: 4})
	var businessErr *domain.Error
	if title != "failed delete" || !errors.As(err, &businessErr) || businessErr.Code != domain.StaleRevision {
		t.Fatalf("DeleteSchedule() = %q, %#v", title, err)
	}
}

// TestServiceListSchedulesAppliesDefaultLimit 验证列表请求的零值 limit 使用稳定默认值。
func TestServiceListSchedulesAppliesDefaultLimit(t *testing.T) {
	store := &fakeStore{listResult: ListSchedulesResponse{Items: []ScheduleDTO{{ID: 1}}}}
	service := NewService(store, nil)
	_, err := service.ListSchedules(context.Background(), ListSchedulesRequest{})
	if err != nil {
		t.Fatalf("ListSchedules() error = %v", err)
	}
	if store.lastListReq.Limit != defaultScheduleListLimit {
		t.Fatalf("limit = %d, want %d", store.lastListReq.Limit, defaultScheduleListLimit)
	}
}

// TestServiceGetScheduleTimeBoundsUsesClock 验证边界查询使用时钟消费者提供的快照。
func TestServiceGetScheduleTimeBoundsUsesClock(t *testing.T) {
	location := time.FixedZone("UTC+8", 8*60*60)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, location)
	service := NewService(nil, &fakeClock{snapshot: domain.TimeSnapshot{Now: now, Location: location}})
	bounds, err := service.GetScheduleTimeBounds(context.Background())
	if err != nil {
		t.Fatalf("GetScheduleTimeBounds() error = %v", err)
	}
	if bounds.SingleMinUnix != time.Date(2026, 1, 1, 0, 0, 0, 0, location).Unix() {
		t.Fatalf("SingleMinUnix = %d", bounds.SingleMinUnix)
	}
}
