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
	created     domain.Schedule
	createCalls int
	getResult   domain.Schedule
	getErr      error
	listResult  ListSchedulesResponse
	listErr     error
	lastListReq ListSchedulesRequest
}

// CreateSchedule 记录候选并返回确定的持久化日程。
func (s *fakeStore) CreateSchedule(_ context.Context, candidate domain.ScheduleCandidate) (domain.Schedule, error) {
	s.createCalls++
	s.created = domain.Schedule{
		ID:                 1,
		Title:              candidate.Title,
		Cron:               candidate.Cron,
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
