package app

import (
	"testing"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// TestRuntimeContractsExposeStableLifecycleValues 验证生命周期契约的序列化值稳定且状态 DTO 可控。
func TestRuntimeContractsExposeStableLifecycleValues(t *testing.T) {
	if RuntimeStateRunning != "running" || RuntimeStateSuspended != "suspended" || RuntimeStateStopping != "stopping" {
		t.Fatalf("unexpected runtime states: %q %q %q", RuntimeStateRunning, RuntimeStateSuspended, RuntimeStateStopping)
	}
	if RuntimeEventSuspend != "suspend" || RuntimeEventResume != "resume" || RuntimeEventClockChanged != "clock_changed" || RuntimeEventStop != "stop" {
		t.Fatalf("unexpected runtime events")
	}
	status := SchedulerStatus{Available: true, Epoch: 7, State: RuntimeStateRunning}
	if status.State != RuntimeStateRunning || status.Epoch != 7 {
		t.Fatalf("status = %+v", status)
	}
	event := RuntimeEvent{Type: RuntimeEventResume, Snapshot: domain.TimeSnapshot{Location: nil}}
	if event.Type != RuntimeEventResume {
		t.Fatalf("event = %+v", event)
	}
}
