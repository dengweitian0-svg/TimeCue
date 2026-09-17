package platform

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/app"
	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

var (
	_ DesktopHost            = (*FakeDesktopHost)(nil)
	_ WindowController       = (*FakeWindowController)(nil)
	_ FrontendEvents         = (*FakeFrontendEvents)(nil)
	_ ActivationSource       = (*FakeActivationSource)(nil)
	_ EnvironmentEventSource = (*FakeEnvironmentEventSource)(nil)
	_ TrayPort               = (*FakeTrayPort)(nil)
	_ NotificationCenter     = (*FakeNotificationCenter)(nil)
)

// TestActivationUsesDomainIdentity 验证激活目标复用领域稳定身份且不携带重复时间身份。
func TestActivationUsesDomainIdentity(t *testing.T) {
	identity := domain.OccurrenceIdentity{ScheduleID: 42, OriginalDate: "2026-09-16"}
	activation := Activation{Kind: ActivationKindToast, Target: &identity}

	if activation.Kind != ActivationKindToast {
		t.Fatalf("activation kind = %q, want %q", activation.Kind, ActivationKindToast)
	}
	if activation.Target == nil || *activation.Target != identity {
		t.Fatalf("activation target = %+v, want %+v", activation.Target, identity)
	}
}

// TestFakeEventSourcesCloseTheirOwnedStreams 验证事件源关闭后停止输出并关闭自有事件流。
func TestFakeEventSourcesCloseTheirOwnedStreams(t *testing.T) {
	activationSource := NewFakeActivationSource(1)
	if err := activationSource.Emit(Activation{Kind: ActivationKindSecondInstance}); err != nil {
		t.Fatalf("activation Emit() error = %v", err)
	}
	if err := activationSource.Close(); err != nil {
		t.Fatalf("activation Close() error = %v", err)
	}
	for range activationSource.Events() {
	}
	if err := activationSource.Emit(Activation{}); err == nil {
		t.Fatal("activation Emit() after Close() error = nil")
	}
	if err := activationSource.Close(); err != nil {
		t.Fatalf("second activation Close() error = %v", err)
	}

	environmentSource := NewFakeEnvironmentEventSource(1)
	if err := environmentSource.Emit(app.RuntimeEvent{Type: app.RuntimeEventClockChanged}); err != nil {
		t.Fatalf("environment Emit() error = %v", err)
	}
	if err := environmentSource.Close(); err != nil {
		t.Fatalf("environment Close() error = %v", err)
	}
	for range environmentSource.Events() {
	}
}

// TestFakeEventSourceCloseWakesBlockedEmit 验证关闭与无缓冲发送并发时不会向已关闭通道写入。
func TestFakeEventSourceCloseWakesBlockedEmit(t *testing.T) {
	source := NewFakeActivationSource(0)
	result := make(chan error, 1)
	go func() { result <- source.Emit(Activation{Kind: ActivationKindToast}) }()

	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("Emit() before Close() error = %v", err)
		}
		t.Fatal("unbuffered Emit() unexpectedly completed without a receiver")
	case <-time.After(10 * time.Millisecond):
	}
	if err := source.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("blocked Emit() error = nil after Close()")
		}
	case <-time.After(time.Second):
		t.Fatal("blocked Emit() did not return after Close()")
	}
}

// TestFakeTrayCallbacksRemainRuntimeOwned 验证托盘只触发运行时拥有的回调，关闭不主动触发退出。
func TestFakeTrayCallbacksRemainRuntimeOwned(t *testing.T) {
	tray := &FakeTrayPort{}
	opened := 0
	exited := 0
	if err := tray.Start(context.Background(), TrayCallbacks{
		OnOpen: func() { opened++ },
		OnExit: func() { exited++ },
	}); err != nil {
		t.Fatalf("tray Start() error = %v", err)
	}
	tray.InvokeOpen()
	tray.InvokeExit()
	if opened != 1 || exited != 1 {
		t.Fatalf("callbacks before Close() = opened:%d exited:%d, want one each", opened, exited)
	}
	if err := tray.Close(); err != nil {
		t.Fatalf("tray Close() error = %v", err)
	}
	if opened != 1 || exited != 1 {
		t.Fatalf("callbacks changed during Close(): opened=%d exited=%d", opened, exited)
	}
	tray.InvokeOpen()
	tray.InvokeExit()
	if opened != 1 || exited != 1 {
		t.Fatalf("callbacks after Close() = opened:%d exited:%d, want unchanged", opened, exited)
	}
	if err := tray.Close(); err != nil {
		t.Fatalf("second tray Close() error = %v", err)
	}
}

// TestFakeNotificationCenterSeparatesCleanupAndClose 验证通知清理是独立步骤且关闭后拒绝新提交。
func TestFakeNotificationCenterSeparatesCleanupAndClose(t *testing.T) {
	center := &FakeNotificationCenter{}
	if err := center.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if err := center.Submit(context.Background(), app.ReminderNotification{Identity: domain.OccurrenceIdentity{ScheduleID: 1, OriginalDate: "2026-09-16"}}); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if err := center.Cleanup(context.Background()); err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}
	if center.CleanupCount != 1 {
		t.Fatalf("cleanup count = %d, want 1", center.CleanupCount)
	}
	if err := center.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := center.Submit(context.Background(), app.ReminderNotification{}); err == nil {
		t.Fatal("Submit() after Close() error = nil")
	}
	if err := center.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

// TestFakeHooksPropagateErrors 验证 fake 不吞掉适配器边界错误，便于上层测试错误传播。
func TestFakeHooksPropagateErrors(t *testing.T) {
	wantErr := errors.New("boundary failure")
	host := &FakeDesktopHost{RunFunc: func(context.Context) error { return wantErr }}
	if err := host.Run(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("Run() error = %v, want %v", err, wantErr)
	}
	center := &FakeNotificationCenter{CleanupFunc: func(context.Context) error { return wantErr }}
	if err := center.Cleanup(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("Cleanup() error = %v, want %v", err, wantErr)
	}
}
