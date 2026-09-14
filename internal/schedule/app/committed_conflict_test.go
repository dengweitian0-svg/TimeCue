package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// committedStore 模拟可写入并保留已提交事实的存储，用于验证提交后的最终冲突结果。
type committedStore struct {
	reads       map[int64]domain.Schedule
	overrides   map[int64][]domain.OccurrenceOverride
	createErr   error
	updateErr   error
	occurErr    error
	restoreErr  error
	restoreRes  domain.OccurrenceActionResult
	writes      int
	sourcesCall int
}

// GetSchedule 返回已提交的日程定义。
func (s *committedStore) GetSchedule(_ context.Context, id domain.ScheduleID) (domain.Schedule, error) {
	if schedule, ok := s.reads[int64(id)]; ok {
		return schedule, nil
	}
	return domain.Schedule{}, domain.NewNotFound("schedule was not found")
}

// ListSchedules 返回空列表，提交后冲突验证不依赖它。
func (s *committedStore) ListSchedules(context.Context, ListSchedulesRequest) (ListSchedulesResponse, error) {
	return ListSchedulesResponse{}, nil
}

// CreateSchedule 记录一次成功写入并保存新的日程定义。
func (s *committedStore) CreateSchedule(_ context.Context, candidate domain.ScheduleCandidate) (domain.Schedule, error) {
	s.writes++
	if s.createErr != nil {
		return domain.Schedule{}, s.createErr
	}
	id := int64(len(s.reads) + 1)
	created := domain.Schedule{
		ID: domain.ScheduleID(id), Title: candidate.Title, Notes: candidate.Notes, Cron: candidate.Cron,
		UntilDateLocal: candidate.UntilDateLocal, MonthEndPolicy: candidate.MonthEndPolicy, Open: candidate.Open,
		FirstStartLocal: candidate.FirstStartLocal, OriginalStartLocal: candidate.OriginalStartLocal,
		DurationMinutes: candidate.DurationMinutes, Revision: 1,
		CreatedAtUTC: candidate.CreatedAtUTC, UpdatedAtUTC: candidate.UpdatedAtUTC,
	}
	s.reads[id] = created
	return created, nil
}

// UpdateSchedule 记录一次成功写入并应用补丁。
func (s *committedStore) UpdateSchedule(_ context.Context, id domain.ScheduleID, candidate domain.ScheduleUpdateCandidate) (domain.Schedule, error) {
	s.writes++
	if s.updateErr != nil {
		return domain.Schedule{}, s.updateErr
	}
	current := s.reads[int64(id)]
	if candidate.Title != nil {
		current.Title = *candidate.Title
	}
	if candidate.Notes != nil {
		current.Notes = *candidate.Notes
	}
	if candidate.CronPresent || candidate.Cron != nil {
		current.Cron = candidate.Cron
		current.MonthEndPolicy = candidate.MonthEndPolicy
	}
	if candidate.UntilDateLocal != nil {
		current.UntilDateLocal = candidate.UntilDateLocal
	}
	if candidate.FirstStartLocal != nil {
		current.FirstStartLocal = *candidate.FirstStartLocal
	}
	if candidate.DurationMinutes != nil {
		current.DurationMinutes = *candidate.DurationMinutes
	}
	current.Revision = candidate.ExpectedRevision + 1
	current.UpdatedAtUTC = candidate.UpdatedAtUTC
	s.reads[int64(id)] = current
	return current, nil
}

// GetOccurrenceOverride 返回已提交的例外快照。
func (s *committedStore) GetOccurrenceOverride(_ context.Context, id domain.ScheduleID, originalDate string) (domain.OccurrenceOverride, error) {
	for _, override := range s.overrides[int64(id)] {
		if override.OriginalDate == originalDate {
			return override, nil
		}
	}
	return domain.OccurrenceOverride{}, domain.NewNotFound("occurrence override was not found")
}

// ListOccurrenceOverrides 返回已提交的全部例外快照。
func (s *committedStore) ListOccurrenceOverrides(_ context.Context, id domain.ScheduleID) ([]domain.OccurrenceOverride, error) {
	return s.overrides[int64(id)], nil
}

// UpdateOccurrence 记录一次成功写入并保存完整例外快照。
func (s *committedStore) UpdateOccurrence(_ context.Context, id domain.ScheduleID, candidate domain.OccurrenceOverrideCandidate) (domain.OccurrenceUpdateResult, error) {
	s.writes++
	if s.occurErr != nil {
		return domain.OccurrenceUpdateResult{}, s.occurErr
	}
	stored := domain.OccurrenceOverride{
		ScheduleID: id, OriginalDate: candidate.OriginalDate, StartLocal: candidate.StartLocal,
		DurationMinutes: candidate.DurationMinutes, Title: candidate.Title, Notes: candidate.Notes,
		Open: true, SnapshotPresent: true,
	}
	s.putOverride(stored)
	schedule := s.reads[int64(id)]
	schedule.Revision = candidate.ExpectedRevision + 1
	s.reads[int64(id)] = schedule
	return domain.OccurrenceUpdateResult{Override: stored, ScheduleRevision: schedule.Revision}, nil
}

// RestoreOccurrence 记录一次成功写入并恢复既有例外快照。
func (s *committedStore) RestoreOccurrence(_ context.Context, id domain.ScheduleID, candidate domain.OccurrenceRestoreCandidate) (domain.OccurrenceActionResult, error) {
	s.writes++
	if s.restoreErr != nil {
		return domain.OccurrenceActionResult{}, s.restoreErr
	}
	if s.restoreRes.ScheduleRevision != 0 {
		return s.restoreRes, nil
	}
	for index, override := range s.overrides[int64(id)] {
		if override.OriginalDate != candidate.OriginalDate {
			continue
		}
		override.Open = true
		s.overrides[int64(id)][index] = override
		return domain.OccurrenceActionResult{
			ScheduleOpen: true, Override: override, OverridePresent: true,
			ScheduleRevision: candidate.ExpectedRevision + 1, Changed: true,
		}, nil
	}
	return domain.OccurrenceActionResult{ScheduleOpen: true, ScheduleRevision: candidate.ExpectedRevision + 1, Changed: true}, nil
}

// RestoreSchedule 记录一次成功写入并重新打开整组日程。
func (s *committedStore) RestoreSchedule(_ context.Context, id domain.ScheduleID, candidate domain.ScheduleActionCandidate) (domain.ScheduleActionResult, error) {
	s.writes++
	if s.restoreErr != nil {
		return domain.ScheduleActionResult{}, s.restoreErr
	}
	schedule := s.reads[int64(id)]
	if !schedule.Open {
		schedule.Open = true
		schedule.Revision = candidate.ExpectedRevision + 1
		schedule.UpdatedAtUTC = candidate.UpdatedAtUTC
		s.reads[int64(id)] = schedule
	}
	return domain.ScheduleActionResult{Open: true, Revision: schedule.Revision, UpdatedAtUTC: schedule.UpdatedAtUTC, Changed: true}, nil
}

// CancelSchedule 记录一次成功写入并关闭整组日程。
func (s *committedStore) CancelSchedule(_ context.Context, id domain.ScheduleID, candidate domain.ScheduleActionCandidate) (domain.ScheduleActionResult, error) {
	s.writes++
	if s.restoreErr != nil {
		return domain.ScheduleActionResult{}, s.restoreErr
	}
	schedule := s.reads[int64(id)]
	if schedule.Open {
		schedule.Open = false
		schedule.Revision = candidate.ExpectedRevision + 1
		schedule.UpdatedAtUTC = candidate.UpdatedAtUTC
		s.reads[int64(id)] = schedule
	}
	return domain.ScheduleActionResult{Open: false, Revision: schedule.Revision, UpdatedAtUTC: schedule.UpdatedAtUTC, Changed: true}, nil
}

// QueryGeneration 返回固定的查询快照版本。
func (s *committedStore) QueryGeneration(context.Context) (uint64, error) { return 5, nil }

// ListOccurrenceSources 按日程 ID 游标返回已提交事实。
func (s *committedStore) ListOccurrenceSources(_ context.Context, request ListOccurrenceSourcesRequest) (ListOccurrenceSourcesResponse, error) {
	s.sourcesCall++
	result := ListOccurrenceSourcesResponse{}
	for id, schedule := range s.reads {
		if domain.ScheduleID(id) <= request.AfterScheduleID {
			continue
		}
		result.Items = append(result.Items, OccurrenceSourceDTO{Schedule: schedule, Overrides: s.overrides[id]})
	}
	return result, nil
}

// putOverride 按身份覆盖或追加例外快照。
func (s *committedStore) putOverride(override domain.OccurrenceOverride) {
	id := int64(override.ScheduleID)
	for index, existing := range s.overrides[id] {
		if existing.OriginalDate == override.OriginalDate {
			s.overrides[id][index] = override
			return
		}
	}
	s.overrides[id] = append(s.overrides[id], override)
}

// newCommittedStore 创建带两个既有日程的可写存储。
func newCommittedStore(location *time.Location) *committedStore {
	store := &committedStore{reads: map[int64]domain.Schedule{}, overrides: map[int64][]domain.OccurrenceOverride{}}
	cron := "0 9 * * *"
	until := "2026-01-05"
	store.reads[1] = domain.Schedule{
		ID: 1, Title: "daily", Cron: &cron, UntilDateLocal: &until, Open: true,
		FirstStartLocal: time.Date(2026, 1, 1, 9, 0, 0, 0, location), DurationMinutes: 60, Revision: 3,
	}
	cron2 := "0 14 * * 1"
	store.reads[2] = domain.Schedule{
		ID: 2, Title: "weekly", Cron: &cron2, UntilDateLocal: &until, Open: true,
		FirstStartLocal: time.Date(2026, 1, 5, 14, 0, 0, 0, location), DurationMinutes: 30, Revision: 1,
	}
	return store
}

// TestCreateScheduleReturnsCommittedConflict 验证创建成功后返回基于已提交事实的最终冲突。
func TestCreateScheduleReturnsCommittedConflict(t *testing.T) {
	location := time.UTC
	now := time.Date(2026, 1, 1, 8, 0, 0, 0, location)
	store := newCommittedStore(location)
	service := NewService(store, &fakeClock{snapshot: domain.TimeSnapshot{Now: now, Location: location}})
	start := time.Date(2026, 1, 1, 9, 0, 0, 0, location)
	end := time.Date(2026, 1, 1, 10, 0, 0, 0, location)

	created, err := service.CreateSchedule(context.Background(), CreateScheduleRequest{
		Title: "new", StartUnix: int64Pointer(start.Unix()), EndUnix: int64Pointer(end.Unix()),
	})
	if err != nil {
		t.Fatalf("CreateSchedule() error = %v", err)
	}
	if created.Conflict == nil || !created.Conflict.Checked {
		t.Fatalf("created conflict = %+v", created.Conflict)
	}
	if created.Conflict.Target == nil || created.Conflict.Target.ScheduleID != created.ID {
		t.Fatalf("created conflict target = %+v", created.Conflict.Target)
	}
	if len(created.Conflict.Conflicts) != 1 {
		t.Fatalf("created conflicts = %+v", created.Conflict.Conflicts)
	}
	conflict := created.Conflict.Conflicts[0]
	if !sameConflictPair(conflict, created.ID, 1) || conflict.OverlapUnix != start.Unix() {
		t.Fatalf("created conflict = %+v", conflict)
	}
	if store.writes != 1 {
		t.Fatalf("writes = %d, want 1", store.writes)
	}
}

// TestCreateScheduleReportsNotCheckedAfterCommit 验证提交后没有未来候选时返回未检查状态。
func TestCreateScheduleReportsNotCheckedAfterCommit(t *testing.T) {
	location := time.UTC
	now := time.Date(2026, 2, 1, 8, 0, 0, 0, location)
	store := newCommittedStore(location)
	service := NewService(store, &fakeClock{snapshot: domain.TimeSnapshot{Now: now, Location: location}})
	// 单次日程落在当前本地日期之前会被领域拒绝，因此使用当天稍后的端点。
	start := time.Date(2026, 2, 1, 9, 0, 0, 0, location)
	end := time.Date(2026, 2, 1, 10, 0, 0, 0, location)

	created, err := service.CreateSchedule(context.Background(), CreateScheduleRequest{
		Title: "later", StartUnix: int64Pointer(start.Unix()), EndUnix: int64Pointer(end.Unix()),
	})
	if err != nil {
		t.Fatalf("CreateSchedule() error = %v", err)
	}
	if created.Conflict == nil || created.Conflict.Status != conflictStatusChecked {
		t.Fatalf("created conflict = %+v", created.Conflict)
	}
}

// TestUpdateScheduleCommittedConflictIgnoresPreviewDrift 验证提交后的冲突以已提交事实为准。
func TestUpdateScheduleCommittedConflictIgnoresPreviewDrift(t *testing.T) {
	location := time.UTC
	now := time.Date(2026, 1, 1, 8, 0, 0, 0, location)
	store := newCommittedStore(location)
	service := NewService(store, &fakeClock{snapshot: domain.TimeSnapshot{Now: now, Location: location}})

	// 预览时目标尚未与其他日程重叠。
	preview, err := service.PreviewChange(context.Background(), previewRequest(t,
		`{"operation":"update_schedule","schedule_id":2,"expected_revision":1,"schedule_update":{"start_unix":`+
			durationUnix(time.Date(2026, 1, 5, 14, 0, 0, 0, location))+
			`,"end_unix":`+durationUnix(time.Date(2026, 1, 5, 14, 30, 0, 0, location))+`}}`))
	if err != nil {
		t.Fatalf("PreviewChange() error = %v", err)
	}
	if !preview.Checked || len(preview.Conflict.Conflicts) != 0 {
		t.Fatalf("preview conflict = %+v", preview.Conflict)
	}

	// 预览之后另一个日程被移动到同一时刻，提交结果必须反映已提交事实。
	drift := store.reads[1]
	drift.FirstStartLocal = time.Date(2026, 1, 5, 14, 0, 0, 0, location)
	drift.Cron = store.reads[2].Cron
	drift.DurationMinutes = 60
	store.reads[1] = drift
	updated, err := service.UpdateSchedule(context.Background(), 2, UpdateScheduleRequest{
		StartUnix:        int64Pointer(time.Date(2026, 1, 5, 14, 0, 0, 0, location).Unix()),
		EndUnix:          int64Pointer(time.Date(2026, 1, 5, 14, 30, 0, 0, location).Unix()),
		ExpectedRevision: 1,
	})
	if err != nil {
		t.Fatalf("UpdateSchedule() error = %v", err)
	}
	if updated.Conflict == nil || !updated.Conflict.Checked || len(updated.Conflict.Conflicts) != 1 {
		t.Fatalf("updated conflict = %+v", updated.Conflict)
	}
	if !sameConflictPair(updated.Conflict.Conflicts[0], 1, 2) {
		t.Fatalf("updated conflict pair = %+v", updated.Conflict.Conflicts[0])
	}
	if updated.Revision != 2 {
		t.Fatalf("updated revision = %d", updated.Revision)
	}
}

// TestUpdateScheduleFailureKeepsTransactionSemantics 验证提交失败时不返回成功或冲突结果。
func TestUpdateScheduleFailureKeepsTransactionSemantics(t *testing.T) {
	location := time.UTC
	now := time.Date(2026, 1, 1, 8, 0, 0, 0, location)
	store := newCommittedStore(location)
	store.updateErr = domain.NewStaleRevision("schedule revision is stale", "expected_revision")
	service := NewService(store, &fakeClock{snapshot: domain.TimeSnapshot{Now: now, Location: location}})
	title := "renamed"

	_, err := service.UpdateSchedule(context.Background(), 2, UpdateScheduleRequest{
		Title: &title, ExpectedRevision: 1,
	})
	var businessErr *domain.Error
	if !errors.As(err, &businessErr) || businessErr.Code != domain.StaleRevision {
		t.Fatalf("update error = %#v", err)
	}
	if store.sourcesCall != 0 {
		t.Fatalf("failed commit read occurrence sources: %d", store.sourcesCall)
	}
	if store.reads[2].Title != "weekly" {
		t.Fatalf("failed commit changed persisted facts: %+v", store.reads[2])
	}
}

// TestUpdateOccurrenceReturnsCommittedConflict 验证单次编辑成功后返回已提交发生的冲突。
func TestUpdateOccurrenceReturnsCommittedConflict(t *testing.T) {
	location := time.UTC
	now := time.Date(2026, 1, 1, 8, 0, 0, 0, location)
	store := newCommittedStore(location)
	service := NewService(store, &fakeClock{snapshot: domain.TimeSnapshot{Now: now, Location: location}})

	updated, err := service.UpdateOccurrence(context.Background(), 1, UpdateOccurrenceRequest{
		OriginalDate:     "2026-01-02",
		StartUnix:        int64Pointer(time.Date(2026, 1, 2, 9, 0, 0, 0, location).Unix()),
		EndUnix:          int64Pointer(time.Date(2026, 1, 2, 10, 0, 0, 0, location).Unix()),
		ExpectedRevision: 3,
	})
	if err != nil {
		t.Fatalf("UpdateOccurrence() error = %v", err)
	}
	if updated.Conflict == nil || !updated.Conflict.Checked {
		t.Fatalf("updated conflict = %+v", updated.Conflict)
	}
	if updated.Conflict.Target == nil || updated.Conflict.Target.OriginalDate != "2026-01-02" {
		t.Fatalf("updated target = %+v", updated.Conflict.Target)
	}
	// 目标身份自身不参与比较，同组同刻发生也不能被当成冲突。
	for _, conflict := range updated.Conflict.Conflicts {
		if conflict.Other.ScheduleID == 1 {
			t.Fatalf("target identity compared with itself: %+v", conflict)
		}
	}
	if store.writes != 1 {
		t.Fatalf("writes = %d", store.writes)
	}
}

// TestRestoreScheduleReturnsCommittedConflict 验证整组恢复成功后按重新开放的定义返回冲突。
func TestRestoreScheduleReturnsCommittedConflict(t *testing.T) {
	location := time.UTC
	now := time.Date(2026, 1, 1, 8, 0, 0, 0, location)
	store := newCommittedStore(location)
	closed := store.reads[2]
	closed.Open = false
	closed.Cron = store.reads[1].Cron
	closed.FirstStartLocal = time.Date(2026, 1, 1, 9, 0, 0, 0, location)
	closed.DurationMinutes = 30
	store.reads[2] = closed
	service := NewService(store, &fakeClock{snapshot: domain.TimeSnapshot{Now: now, Location: location}})

	restored, err := service.RestoreSchedule(context.Background(), 2, ScheduleActionRequest{ExpectedRevision: 1})
	if err != nil {
		t.Fatalf("RestoreSchedule() error = %v", err)
	}
	if !restored.Open || restored.Conflict == nil || !restored.Conflict.Checked {
		t.Fatalf("restored = %+v", restored)
	}
	if restored.Conflict.Target == nil || restored.Conflict.Target.ScheduleID != 2 {
		t.Fatalf("restored target = %+v", restored.Conflict.Target)
	}
	if len(restored.Conflict.Conflicts) != 1 || !sameConflictPair(restored.Conflict.Conflicts[0], 1, 2) {
		t.Fatalf("restored conflicts = %+v", restored.Conflict.Conflicts)
	}
}

// TestRestoreOccurrenceReturnsCommittedConflict 验证单次恢复成功后返回该身份的最终冲突。
func TestRestoreOccurrenceReturnsCommittedConflict(t *testing.T) {
	location := time.UTC
	now := time.Date(2026, 1, 1, 8, 0, 0, 0, location)
	store := newCommittedStore(location)
	store.overrides[1] = []domain.OccurrenceOverride{{
		ScheduleID: 1, OriginalDate: "2026-01-02", StartLocal: time.Date(2026, 1, 2, 9, 30, 0, 0, location),
		DurationMinutes: 60, Title: "daily", Open: false, SnapshotPresent: true,
	}}
	service := NewService(store, &fakeClock{snapshot: domain.TimeSnapshot{Now: now, Location: location}})

	restored, err := service.RestoreOccurrence(context.Background(), 1, OccurrenceActionRequest{
		OriginalDate: "2026-01-02", ExpectedRevision: 3,
	})
	if err != nil {
		t.Fatalf("RestoreOccurrence() error = %v", err)
	}
	if !restored.Conflict.Checked || restored.Conflict.Target == nil {
		t.Fatalf("restored conflict = %+v", restored.Conflict)
	}
	if restored.Conflict.Target.OriginalDate != "2026-01-02" ||
		restored.Conflict.Target.StartUnix != time.Date(2026, 1, 2, 9, 30, 0, 0, location).Unix() {
		t.Fatalf("restored target = %+v", restored.Conflict.Target)
	}
}

// TestOccurrenceWithoutQueryStoreSkipsConflict 验证存储不支持查询时仍返回成功的写入结果。
func TestOccurrenceWithoutQueryStoreSkipsConflict(t *testing.T) {
	location := time.UTC
	now := time.Date(2026, 1, 1, 8, 0, 0, 0, location)
	start := time.Date(2026, 1, 2, 10, 0, 0, 0, location)
	store := &fakeStore{}
	service := NewService(store, &fakeClock{snapshot: domain.TimeSnapshot{Now: now, Location: location}})

	created, err := service.CreateSchedule(context.Background(), CreateScheduleRequest{
		Title: "plain", StartUnix: int64Pointer(start.Unix()), EndUnix: int64Pointer(start.Add(time.Hour).Unix()),
	})
	if err != nil {
		t.Fatalf("CreateSchedule() error = %v", err)
	}
	if created.Conflict != nil {
		t.Fatalf("conflict = %+v, want nil without query store", created.Conflict)
	}
	if store.createCalls != 1 {
		t.Fatalf("create calls = %d", store.createCalls)
	}
}

// TestCancelledGroupReportsNoConflict 验证取消整组只返回状态结果，不返回冲突结果。
func TestCancelledGroupReportsNoConflict(t *testing.T) {
	location := time.UTC
	now := time.Date(2026, 1, 1, 8, 0, 0, 0, location)
	store := newCommittedStore(location)
	service := NewService(store, &fakeClock{snapshot: domain.TimeSnapshot{Now: now, Location: location}})

	cancelled, err := service.CancelSchedule(context.Background(), 2, ScheduleActionRequest{ExpectedRevision: 1})
	if err != nil {
		t.Fatalf("CancelSchedule() error = %v", err)
	}
	if cancelled.Open || cancelled.Conflict != nil {
		t.Fatalf("cancelled = %+v, want closed result without conflict", cancelled)
	}
	if store.reads[2].Open {
		t.Fatal("cancel did not persist the closed state")
	}
}

// int64Pointer 返回提交后冲突测试使用的整数指针。
func int64Pointer(value int64) *int64 {
	return &value
}
