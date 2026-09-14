package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// TestServiceRejectsCreateAfterStopping 验证停止事件之后创建不会进入存储且保留运行时错误分类。
func TestServiceRejectsCreateAfterStopping(t *testing.T) {
	location := time.FixedZone("CST", 8*60*60)
	now := time.Date(2026, 1, 1, 8, 0, 0, 0, location)
	start, end := now.Add(time.Hour).Unix(), now.Add(2*time.Hour).Unix()
	store := &fakeStore{}
	coordinator := NewMutationCoordinator()
	if err := coordinator.ApplyRuntimeEvent(RuntimeEvent{Type: RuntimeEventStop}); err != nil {
		t.Fatalf("stop error = %v", err)
	}
	service := NewServiceWithCoordinator(store, &fakeClock{snapshot: domain.TimeSnapshot{Now: now, Location: location}}, coordinator)
	_, err := service.CreateSchedule(context.Background(), CreateScheduleRequest{Title: "blocked", StartUnix: &start, EndUnix: &end})
	var runtimeErr *domain.Error
	if !errors.As(err, &runtimeErr) || runtimeErr.Code != domain.RuntimeStopping {
		t.Fatalf("CreateSchedule() error = %v, want runtime stopping", err)
	}
	if store.createCalls != 0 {
		t.Fatalf("create calls = %d, want 0", store.createCalls)
	}
}
