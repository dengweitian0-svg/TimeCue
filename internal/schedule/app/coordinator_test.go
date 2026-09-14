package app

import (
	"errors"
	"testing"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// TestMutationCoordinatorTransitionsIdempotently 验证生命周期转换只在状态事实变化时递增 epoch。
func TestMutationCoordinatorTransitionsIdempotently(t *testing.T) {
	coordinator := NewMutationCoordinator()
	if got := coordinator.State(); got != RuntimeStateRunning {
		t.Fatalf("initial state = %q, want %q", got, RuntimeStateRunning)
	}
	if err := coordinator.ApplyRuntimeEvent(RuntimeEvent{Type: RuntimeEventSuspend}); err != nil {
		t.Fatalf("suspend error = %v", err)
	}
	if got := coordinator.State(); got != RuntimeStateSuspended {
		t.Fatalf("suspended state = %q", got)
	}
	if got := coordinator.Epoch(); got != 1 {
		t.Fatalf("suspend epoch = %d, want 1", got)
	}
	if err := coordinator.ApplyRuntimeEvent(RuntimeEvent{Type: RuntimeEventSuspend}); err != nil {
		t.Fatalf("duplicate suspend error = %v", err)
	}
	if got := coordinator.Epoch(); got != 1 {
		t.Fatalf("duplicate suspend epoch = %d, want 1", got)
	}
	if err := coordinator.ApplyRuntimeEvent(RuntimeEvent{Type: RuntimeEventResume}); err != nil {
		t.Fatalf("resume error = %v", err)
	}
	if got := coordinator.Epoch(); got != 2 {
		t.Fatalf("resume epoch = %d, want 2", got)
	}
	if err := coordinator.ApplyRuntimeEvent(RuntimeEvent{Type: RuntimeEventStop}); err != nil {
		t.Fatalf("stop error = %v", err)
	}
	if got := coordinator.State(); got != RuntimeStateStopping {
		t.Fatalf("stopping state = %q", got)
	}
	if got := coordinator.Epoch(); got != 3 {
		t.Fatalf("stop epoch = %d, want 3", got)
	}
	if err := coordinator.ApplyRuntimeEvent(RuntimeEvent{Type: RuntimeEventStop}); err != nil {
		t.Fatalf("duplicate stop error = %v", err)
	}
	if got := coordinator.Epoch(); got != 3 {
		t.Fatalf("duplicate stop epoch = %d, want 3", got)
	}
}

// TestMutationCoordinatorRejectsMutationAfterStop 验证停止事件与写入准入共享顺序并拒绝新写入。
func TestMutationCoordinatorRejectsMutationAfterStop(t *testing.T) {
	coordinator := NewMutationCoordinator()
	if err := coordinator.ApplyRuntimeEvent(RuntimeEvent{Type: RuntimeEventStop}); err != nil {
		t.Fatalf("stop error = %v", err)
	}
	if err := coordinator.BeginMutation(); err == nil {
		t.Fatal("BeginMutation() error = nil")
	} else {
		var runtimeErr *domain.Error
		if !errors.As(err, &runtimeErr) || runtimeErr.Code != domain.RuntimeStopping {
			t.Fatalf("BeginMutation() error = %v", err)
		}
	}
}
