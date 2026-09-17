package wailshost

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// TestRunIsSynchronousAndSingleUse 验证主循环同步执行且不能重复运行。
func TestRunIsSynchronousAndSingleUse(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	host := New(HostOptions{Runner: func(RunCallbacks) error {
		close(started)
		<-release
		return nil
	}})
	done := make(chan error, 1)
	go func() { done <- host.Run(context.Background()) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("runner did not start")
	}
	select {
	case <-done:
		t.Fatal("Run returned before synchronous runner completed")
	default:
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if err := host.Run(context.Background()); !errors.Is(err, ErrRunAlreadyCalled) {
		t.Fatalf("second Run() error = %v, want %v", err, ErrRunAlreadyCalled)
	}
}

// TestExternalCancellationUsesNonCancelledCleanupContext 验证外部取消进入唯一 Stop 且清理 context 不再取消。
func TestExternalCancellationUsesNonCancelledCleanupContext(t *testing.T) {
	stopCalled := make(chan context.Context, 1)
	quitCalled := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	var host *Host
	host = New(HostOptions{
		Runner: func(callbacks RunCallbacks) error {
			callbacks.OnStartup(context.Background())
			callbacks.OnDomReady(context.Background())
			<-quitCalled
			return nil
		},
		Runtime: RuntimeCallbacks{Quit: func(context.Context) error {
			close(quitCalled)
			return nil
		}},
		Lifecycle: LifecycleCallbacks{Stop: func(cleanup context.Context) error {
			stopCalled <- cleanup
			return nil
		}},
	})
	runDone := make(chan error, 1)
	go func() { runDone <- host.Run(ctx) }()
	cancel()
	var cleanup context.Context
	select {
	case cleanup = <-stopCalled:
	case <-time.After(time.Second):
		t.Fatal("formal Stop was not requested")
	}
	if cleanup.Err() != nil {
		t.Fatalf("cleanup context err = %v, want nil", cleanup.Err())
	}
	if err := <-runDone; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

// TestStartupFailureStopsHostAndReturnsError 验证启动失败不仅记录日志，还会停止宿主并返回 Bootstrap。
func TestStartupFailureStopsHostAndReturnsError(t *testing.T) {
	wantErr := errors.New("startup failed")
	quitCalled := make(chan struct{})
	host := New(HostOptions{
		Runner: func(callbacks RunCallbacks) error {
			callbacks.OnStartup(context.Background())
			callbacks.OnDomReady(context.Background())
			<-quitCalled
			return nil
		},
		Runtime: RuntimeCallbacks{Quit: func(context.Context) error {
			close(quitCalled)
			return nil
		}},
		Lifecycle: LifecycleCallbacks{
			OnStartup: func(context.Context) error { return wantErr },
			Stop:      func(context.Context) error { return nil },
		},
	})
	if err := host.Run(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("Run() error = %v, want %v", err, wantErr)
	}
}

// TestLifecycleGatesRuntimeAndUsesSavedContext 验证 DOM ready 前禁止 Runtime 调用且动作使用保存 context。
func TestLifecycleGatesRuntimeAndUsesSavedContext(t *testing.T) {
	startup := context.WithValue(context.Background(), "phase", "startup")
	domReady := context.WithValue(context.Background(), "phase", "dom-ready")
	var mu sync.Mutex
	var shown, emitted context.Context
	var host *Host
	host = New(HostOptions{
		Runtime: RuntimeCallbacks{
			WindowShow: func(ctx context.Context) error { mu.Lock(); shown = ctx; mu.Unlock(); return nil },
			EventsEmit: func(ctx context.Context, _ string, _ any) error { mu.Lock(); emitted = ctx; mu.Unlock(); return nil },
		},
		Lifecycle: LifecycleCallbacks{OnStartup: func(ctx context.Context) error {
			if err := hostWindowShow(host, ctx); !errors.Is(err, ErrRuntimeNotReady) {
				t.Errorf("startup Window.Show() error = %v, want %v", err, ErrRuntimeNotReady)
			}
			return nil
		}},
	})
	host.OnStartup(startup)
	if err := hostWindowShow(host, startup); !errors.Is(err, ErrRuntimeNotReady) {
		t.Fatalf("pre-ready Window.Show() error = %v, want %v", err, ErrRuntimeNotReady)
	}
	host.OnDomReady(domReady)
	if err := host.Window().Show(context.Background()); err != nil {
		t.Fatalf("Window.Show() error = %v", err)
	}
	if err := host.Events().Emit(context.Background(), "activation", map[string]any{"sequence": 1}); err != nil {
		t.Fatalf("Events.Emit() error = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if shown != domReady || emitted != domReady {
		t.Fatalf("Runtime contexts = (%v, %v), want saved DOM-ready context", shown, emitted)
	}
}

// hostWindowShow 提供测试中的最小窗口调用，避免在生命周期回调中重复取端口。
func hostWindowShow(host *Host, ctx context.Context) error {
	return host.Window().Show(ctx)
}

// TestBeforeCloseReadsDynamicPreferenceAndAllowsQuitAfterStop 验证 tray/exit 分支和退出门闩。
func TestBeforeCloseReadsDynamicPreferenceAndAllowsQuitAfterStop(t *testing.T) {
	var behavior CloseBehavior = CloseBehaviorTray
	var hidden context.Context
	stopCalled := make(chan context.Context, 1)
	quitCalled := make(chan context.Context, 1)
	host := New(HostOptions{
		Runtime: RuntimeCallbacks{
			WindowHide: func(ctx context.Context) error { hidden = ctx; return nil },
			Quit:       func(ctx context.Context) error { quitCalled <- ctx; return nil },
		},
		Lifecycle: LifecycleCallbacks{
			CloseBehavior: func(context.Context) (CloseBehavior, error) { return behavior, nil },
			Stop:          func(ctx context.Context) error { stopCalled <- ctx; return nil },
		},
	})
	host.OnStartup(context.Background())
	host.OnDomReady(context.Background())
	if prevent := host.OnBeforeClose(context.Background()); !prevent || hidden == nil {
		t.Fatal("tray close did not hide and prevent")
	}
	behavior = CloseBehaviorExit
	if prevent := host.OnBeforeClose(context.Background()); !prevent {
		t.Fatal("exit close was not prevented")
	}
	select {
	case <-stopCalled:
	case <-time.After(time.Second):
		t.Fatal("exit close did not request Stop")
	}
	select {
	case <-quitCalled:
	case <-time.After(time.Second):
		t.Fatal("successful Stop did not request Quit")
	}
	if prevent := host.OnBeforeClose(context.Background()); prevent {
		t.Fatal("allowQuit did not allow the next close")
	}
}

// TestShutdownAndEnvironmentCallbacksAreIdempotent 验证 shutdown 兜底和 Windows 电源回调的转发。
func TestShutdownAndEnvironmentCallbacksAreIdempotent(t *testing.T) {
	var mu sync.Mutex
	stopCount, shutdownCount, suspendCount, resumeCount := 0, 0, 0, 0
	host := New(HostOptions{Lifecycle: LifecycleCallbacks{
		Stop:       func(context.Context) error { mu.Lock(); stopCount++; mu.Unlock(); return nil },
		OnShutdown: func(context.Context) { mu.Lock(); shutdownCount++; mu.Unlock() },
		OnSuspend:  func() error { mu.Lock(); suspendCount++; mu.Unlock(); return nil },
		OnResume:   func() error { mu.Lock(); resumeCount++; mu.Unlock(); return nil },
	}})
	host.OnSuspend()
	host.OnResume()
	host.OnShutdown(context.Background())
	host.OnShutdown(context.Background())
	time.Sleep(10 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if stopCount != 1 || shutdownCount != 1 || suspendCount != 1 || resumeCount != 1 {
		t.Fatalf("counts = stop:%d shutdown:%d suspend:%d resume:%d", stopCount, shutdownCount, suspendCount, resumeCount)
	}
}

// TestSlogLoggerMapsWailsLevels 验证 Wails 日志等级映射到 slog 的既有等级。
func TestSlogLoggerMapsWailsLevels(t *testing.T) {
	handler := &recordHandler{}
	logger := NewSlogLogger(slog.New(handler))
	logger.Print("print")
	logger.Trace("trace")
	logger.Debug("debug")
	logger.Info("info")
	logger.Warning("warning")
	logger.Error("error")
	logger.Fatal("fatal")
	want := []slog.Level{slog.LevelInfo, slog.LevelDebug, slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError, slog.LevelError}
	if len(handler.levels) != len(want) {
		t.Fatalf("levels = %v, want %v", handler.levels, want)
	}
	for i := range want {
		if handler.levels[i] != want[i] {
			t.Fatalf("level[%d] = %v, want %v", i, handler.levels[i], want[i])
		}
	}
}

// recordHandler 捕获 slog 记录等级供日志适配测试使用。
type recordHandler struct {
	levels []slog.Level
}

// Enabled 接受全部等级以验证完整映射。
func (h *recordHandler) Enabled(context.Context, slog.Level) bool { return true }

// Handle 记录单条日志的等级。
func (h *recordHandler) Handle(_ context.Context, record slog.Record) error {
	h.levels = append(h.levels, record.Level)
	return nil
}

// WithAttrs 保持测试 handler 的简单状态。
func (h *recordHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

// WithGroup 保持测试 handler 的简单状态。
func (h *recordHandler) WithGroup(string) slog.Handler { return h }
