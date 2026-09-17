package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/config"
	"github.com/dengweitian0-svg/TimeCue/internal/platform"
	"github.com/dengweitian0-svg/TimeCue/internal/schedule/app"
	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
	"github.com/dengweitian0-svg/TimeCue/internal/storage/sqlite"
)

// desktopTestClock 为运行时集成测试提供固定且可取消的时间快照。
type desktopTestClock struct {
	snapshot domain.TimeSnapshot
}

// Snapshot 返回固定时间事实并响应调用方取消。
func (c desktopTestClock) Snapshot(ctx context.Context) (domain.TimeSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return domain.TimeSnapshot{}, err
	}
	return c.snapshot, nil
}

// openDesktopTestStore 打开临时 SQLite Store 并注册测试清理。
func openDesktopTestStore(t *testing.T) *sqlite.Store {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store, err := sqlite.OpenStore(context.Background(), t.TempDir()+"/timecue.db", t.TempDir(), logger)
	if err != nil {
		t.Fatalf("OpenStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// newDesktopTestApplication 创建绑定了临时 Store 和测试平台 fake 的运行时。
func newDesktopTestApplication(t *testing.T, host *platform.FakeDesktopHost, notifications *platform.FakeNotificationCenter) (*DesktopApplication, context.CancelFunc) {
	t.Helper()
	location := time.FixedZone("CST", 8*60*60)
	clock := desktopTestClock{snapshot: domain.TimeSnapshot{Now: time.Date(2026, 1, 1, 8, 0, 0, 0, location), Location: location}}
	if host == nil {
		host = &platform.FakeDesktopHost{}
	}
	if notifications == nil {
		notifications = &platform.FakeNotificationCenter{}
	}
	return newRunningDesktopTestApplication(t, DesktopDependencies{Host: host, Notifications: notifications, Clock: clock})
}

// newRunningDesktopTestApplication 启动一个已完成 DOM ready 的测试运行时并返回外部取消函数。
func newRunningDesktopTestApplication(t *testing.T, dependencies DesktopDependencies) (*DesktopApplication, context.CancelFunc) {
	t.Helper()
	application := NewDesktopApplication(dependencies)
	root := t.TempDir()
	cfg := config.Config{
		App:           config.AppConfig{Name: "TimeCue", Version: "test"},
		Log:           config.LogConfig{Level: "info", Path: filepath.Join(root, "logs", "timecue.log")},
		Backup:        config.BackupConfig{Dir: filepath.Join(root, "backups")},
		Window:        config.WindowConfig{CloseBehavior: config.CloseBehaviorTray},
		ResolvedPaths: config.ResolvedPaths{ConfigPath: filepath.Join(root, "config.yaml"), DatabasePath: filepath.Join(root, "data", "timecue.db"), LogPath: filepath.Join(root, "logs", "timecue.log"), BackupDir: filepath.Join(root, "backups"), PreferencesPath: t.TempDir() + "/preferences.yaml"},
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- application.Run(ctx, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), openDesktopTestStore(t))
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-result:
		case <-time.After(3 * time.Second):
			t.Error("desktop application did not stop during cleanup")
		}
	})
	waitForDesktopCondition(t, func() bool { return application.Service() != nil })
	if err := application.OnStartup(context.Background()); err != nil {
		t.Fatalf("OnStartup() error = %v", err)
	}
	if err := application.OnDomReady(context.Background()); err != nil {
		t.Fatalf("OnDomReady() error = %v", err)
	}
	waitForDesktopCondition(t, func() bool { return application.GetDesktopStatus().State == DesktopStateRunning })
	return application, cancel
}

// waitForDesktopCondition 等待运行时异步组装或状态转换完成。
func waitForDesktopCondition(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		if condition() {
			return
		}
		select {
		case <-deadline:
			t.Fatal("desktop condition was not reached")
		case <-time.After(time.Millisecond):
		}
	}
}

// TestDesktopApplicationDefersRuntimeResourcesUntilDomReady 验证 OnStartup 不触碰桌面资源，初始化从 OnDomReady 才开始。
func TestDesktopApplicationDefersRuntimeResourcesUntilDomReady(t *testing.T) {
	host := &platform.FakeDesktopHost{RunFunc: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }}
	notifications := &platform.FakeNotificationCenter{}
	location := time.FixedZone("CST", 8*60*60)
	clock := desktopTestClock{snapshot: domain.TimeSnapshot{Now: time.Date(2026, 1, 1, 8, 0, 0, 0, location), Location: location}}
	application := NewDesktopApplication(DesktopDependencies{Host: host, Notifications: notifications, Clock: clock})
	cfg := config.Config{App: config.AppConfig{Name: "TimeCue", Version: "test"}, Log: config.LogConfig{Level: "info", Path: "test.log"}, Backup: config.BackupConfig{Dir: t.TempDir()}, Window: config.WindowConfig{CloseBehavior: config.CloseBehaviorTray}}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- application.Run(ctx, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), openDesktopTestStore(t))
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-result:
		case <-time.After(3 * time.Second):
			t.Error("deferred desktop application did not stop")
		}
	})
	waitForDesktopCondition(t, func() bool { return application.Service() != nil })
	if notifications.Initialized {
		t.Fatal("notification center initialized before DOM ready")
	}
	if err := application.OnStartup(context.Background()); err != nil {
		t.Fatalf("OnStartup() error = %v", err)
	}
	if notifications.Initialized {
		t.Fatal("notification center initialized during OnStartup")
	}
	if err := application.OnDomReady(context.Background()); err != nil {
		t.Fatalf("OnDomReady() error = %v", err)
	}
	if !notifications.Initialized {
		t.Fatal("notification center was not initialized during OnDomReady")
	}
}

// TestDesktopApplicationAssemblesOneServiceCoordinatorScheduler 验证完整 Store 只组装一组核心运行组件。
func TestDesktopApplicationAssemblesOneServiceCoordinatorScheduler(t *testing.T) {
	host := &platform.FakeDesktopHost{RunFunc: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }}
	application, cancel := newDesktopTestApplication(t, host, nil)
	defer cancel()
	if application.Service() == nil || application.Coordinator() == nil || application.Scheduler() == nil || application.Bindings() == nil {
		t.Fatal("desktop application did not assemble all core components")
	}
	if application.Service().Coordinator() != application.Coordinator() {
		t.Fatal("service and scheduler do not share one coordinator")
	}
	if status := application.GetDesktopStatus(); status.State != DesktopStateRunning {
		t.Fatalf("desktop state = %q, want running", status.State)
	}
	if host.QuitCalls() != 0 {
		t.Fatalf("host Quit count before stop = %d, want 0", host.QuitCalls())
	}
}

// TestStopFromHostCleansUpWithoutRecursivelyQuitting 验证宿主关闭回调复用正式清理状态机且不再次请求 Quit。
func TestStopFromHostCleansUpWithoutRecursivelyQuitting(t *testing.T) {
	host := &platform.FakeDesktopHost{RunFunc: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }}
	application, cancel := newDesktopTestApplication(t, host, nil)
	defer cancel()
	if err := application.StopFromHost(context.Background()); err != nil {
		t.Fatalf("StopFromHost() error = %v", err)
	}
	if host.QuitCalls() != 0 {
		t.Fatalf("host Quit count = %d, want 0", host.QuitCalls())
	}
}

// TestTrayExitReturnsBeforeTrayClose 验证托盘回调先返回，避免关闭托盘时等待自身回调形成死锁。
func TestTrayExitReturnsBeforeTrayClose(t *testing.T) {
	host := &platform.FakeDesktopHost{RunFunc: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }}
	releaseClose := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseClose) }) }
	t.Cleanup(release)
	tray := &platform.FakeTrayPort{CloseFunc: func() error { <-releaseClose; return nil }}
	location := time.FixedZone("CST", 8*60*60)
	application, cancel := newRunningDesktopTestApplication(t, DesktopDependencies{
		Host:          host,
		Tray:          tray,
		Notifications: &platform.FakeNotificationCenter{},
		Clock: desktopTestClock{snapshot: domain.TimeSnapshot{
			Now:      time.Date(2026, 1, 1, 8, 0, 0, 0, location),
			Location: location,
		}},
	})
	defer cancel()

	callbackReturned := make(chan struct{})
	go func() {
		tray.InvokeExit()
		close(callbackReturned)
	}()
	select {
	case <-callbackReturned:
	case <-time.After(time.Second):
		t.Fatal("tray exit callback did not return before tray close")
	}
	release()
	waitForDesktopCondition(t, func() bool {
		return application.GetDesktopStatus().State == DesktopStateStopped && host.QuitCalls() == 1 && tray.Closed
	})
}

// TestRunAssemblyFailureClosesOwnedPlatformResources 验证已移交给运行时的平台资源在组装失败时不泄漏。
func TestRunAssemblyFailureClosesOwnedPlatformResources(t *testing.T) {
	activation := platform.NewFakeActivationSource(1)
	environment := platform.NewFakeEnvironmentEventSource(1)
	tray := &platform.FakeTrayPort{}
	notifications := &platform.FakeNotificationCenter{}
	application := NewDesktopApplication(DesktopDependencies{
		Activation:    activation,
		Environment:   environment,
		Tray:          tray,
		Notifications: notifications,
	})
	err := application.Run(context.Background(), config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), openDesktopTestStore(t))
	if err == nil {
		t.Fatal("Run() error = nil, want missing clock error")
	}
	if !activation.Closed || !environment.Closed || !tray.Closed || !notifications.Closed {
		t.Fatalf("resource closure = activation:%t environment:%t tray:%t notifications:%t, want all closed", activation.Closed, environment.Closed, tray.Closed, notifications.Closed)
	}
	if err := application.OnDomReady(context.Background()); err != nil {
		t.Fatalf("late OnDomReady() error = %v", err)
	}
	if activation.Started || environment.Started || tray.Started || notifications.Initialized {
		t.Fatal("late OnDomReady restarted resources after failed assembly")
	}
}

// TestRunCancellationBeforeDomReadyClosesOwnedResources 验证宿主在 DOM ready 前退出时仍执行完整资源清理。
func TestRunCancellationBeforeDomReadyClosesOwnedResources(t *testing.T) {
	activation := platform.NewFakeActivationSource(1)
	environment := platform.NewFakeEnvironmentEventSource(1)
	tray := &platform.FakeTrayPort{}
	notifications := &platform.FakeNotificationCenter{}
	location := time.FixedZone("CST", 8*60*60)
	clock := desktopTestClock{snapshot: domain.TimeSnapshot{Now: time.Date(2026, 1, 1, 8, 0, 0, 0, location), Location: location}}
	host := &platform.FakeDesktopHost{RunFunc: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }}
	application := NewDesktopApplication(DesktopDependencies{
		Host:          host,
		Activation:    activation,
		Environment:   environment,
		Tray:          tray,
		Notifications: notifications,
		Clock:         clock,
	})
	cfg := config.Config{Window: config.WindowConfig{CloseBehavior: config.CloseBehaviorTray}, ResolvedPaths: config.ResolvedPaths{PreferencesPath: t.TempDir() + "/preferences.yaml"}}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- application.Run(ctx, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), openDesktopTestStore(t))
	}()
	waitForDesktopCondition(t, func() bool { return application.Service() != nil })
	cancel()
	if err := <-result; err != nil {
		t.Fatalf("Run() error = %v, want nil cancellation", err)
	}
	if !activation.Closed || !environment.Closed || !tray.Closed || !notifications.Closed {
		t.Fatalf("resource closure = activation:%t environment:%t tray:%t notifications:%t, want all closed", activation.Closed, environment.Closed, tray.Closed, notifications.Closed)
	}
	if err := application.OnDomReady(context.Background()); err != nil {
		t.Fatalf("late OnDomReady() error = %v", err)
	}
	if activation.Started || environment.Started || tray.Started || notifications.Initialized {
		t.Fatal("late OnDomReady restarted resources after cancellation")
	}
}

// TestWailsBindingsUseJSONPresenceAndSQLiteStore 验证绑定通过现有 JSON 解码器调用创建/读取/列表/删除用例。
func TestWailsBindingsUseJSONPresenceAndSQLiteStore(t *testing.T) {
	host := &platform.FakeDesktopHost{RunFunc: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }}
	application, cancel := newDesktopTestApplication(t, host, nil)
	defer cancel()
	binding := application.Bindings()
	start := time.Date(2026, 1, 1, 9, 0, 0, 0, time.FixedZone("CST", 8*60*60)).Unix()
	created, err := binding.CreateSchedule(`{"title":"Focus","start_unix":` + formatInt(start) + `,"end_unix":` + formatInt(start+30*60) + `}`)
	if err != nil {
		t.Fatalf("CreateSchedule() error = %v", err)
	}
	if created.ID <= 0 || created.Revision != 1 {
		t.Fatalf("created schedule = %+v", created)
	}
	fetched, err := binding.GetSchedule(created.ID)
	if err != nil {
		t.Fatalf("GetSchedule() error = %v", err)
	}
	if fetched.Title != "Focus" {
		t.Fatalf("fetched title = %q, want Focus", fetched.Title)
	}
	listed, err := binding.ListSchedules(structuredListRequest())
	if err != nil {
		t.Fatalf("ListSchedules() error = %v", err)
	}
	if len(listed.Items) != 1 {
		t.Fatalf("listed items = %d, want 1", len(listed.Items))
	}
	if _, err := binding.GetScheduleTimeBounds(); err != nil {
		t.Fatalf("GetScheduleTimeBounds() error = %v", err)
	}
	if got := binding.GetRuntimeStatus(); got.State == "" {
		t.Fatalf("GetRuntimeStatus() = %+v, want a lifecycle state", got)
	}
	if got := binding.GetDesktopStatus(); got.State != DesktopStateRunning {
		t.Fatalf("GetDesktopStatus() = %+v, want running", got)
	}
	closeBehavior, err := binding.GetCloseBehavior()
	if err != nil || closeBehavior.Behavior != string(config.CloseBehaviorTray) {
		t.Fatalf("GetCloseBehavior() = %+v, error = %v", closeBehavior, err)
	}
	if err := binding.SaveCloseBehavior(CloseBehaviorDTO{Behavior: string(config.CloseBehaviorExit)}); err != nil {
		t.Fatalf("SaveCloseBehavior() error = %v", err)
	}
	closeBehavior, err = binding.GetCloseBehavior()
	if err != nil || closeBehavior.Behavior != string(config.CloseBehaviorExit) {
		t.Fatalf("GetCloseBehavior() after save = %+v, error = %v", closeBehavior, err)
	}
	deleted, err := binding.DeleteSchedule(created.ID, `{"expected_revision":1}`)
	if err != nil {
		t.Fatalf("DeleteSchedule() error = %v", err)
	}
	if deleted.Title != "Focus" {
		t.Fatalf("deleted title = %q, want Focus", deleted.Title)
	}
}

// TestStorageDirectoryBindingsExposeRestartOnlyState 验证保存后 effective 仍是旧目录、saved 是新目录。
func TestStorageDirectoryBindingsExposeRestartOnlyState(t *testing.T) {
	host := &platform.FakeDesktopHost{RunFunc: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }}
	access := &platform.FakeDirectoryAccess{ValidateFunc: func(_ context.Context, path string) (string, error) { return filepath.Clean(path), nil }}
	application, cancel := newRunningDesktopTestApplication(t, DesktopDependencies{Host: host, Notifications: &platform.FakeNotificationCenter{}, Clock: desktopTestClock{snapshot: domain.TimeSnapshot{Now: time.Now(), Location: time.Local}}, DirectoryAccess: access})
	defer cancel()
	binding := application.Bindings()
	before, err := binding.GetStorageDirectories()
	if err != nil {
		t.Fatalf("GetStorageDirectories() error = %v", err)
	}
	newDirectory := filepath.Join(t.TempDir(), "new logs")
	after, err := binding.SaveStorageDirectory(SaveStorageDirectoryRequest{Kind: "logs", Path: newDirectory})
	if err != nil {
		t.Fatalf("SaveStorageDirectory() error = %v", err)
	}
	if after.EffectivePath != before.Logs.EffectivePath {
		t.Fatalf("effective log path = %q, want old %q", after.EffectivePath, before.Logs.EffectivePath)
	}
	if after.SavedPath != filepath.Clean(newDirectory) || !after.RestartRequired {
		t.Fatalf("saved directory = %+v, want new path and restart_required", after)
	}
	current, err := binding.GetStorageDirectories()
	if err != nil {
		t.Fatalf("GetStorageDirectories() after save error = %v", err)
	}
	if current.Logs.EffectivePath != before.Logs.EffectivePath || current.Logs.SavedPath != after.SavedPath {
		t.Fatalf("directory snapshot after save = %+v, want effective old and saved new", current.Logs)
	}
	if access.Calls() != 1 {
		t.Fatalf("directory validation calls = %d, want one", access.Calls())
	}
}

// TestStorageDirectorySaveRejectsStoppedRuntime 验证停止、清理失败等非 running 状态不允许保存配置。
func TestStorageDirectorySaveRejectsStoppedRuntime(t *testing.T) {
	host := &platform.FakeDesktopHost{RunFunc: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }}
	application, cancel := newRunningDesktopTestApplication(t, DesktopDependencies{Host: host, Notifications: &platform.FakeNotificationCenter{}, Clock: desktopTestClock{snapshot: domain.TimeSnapshot{Now: time.Now(), Location: time.Local}}, DirectoryAccess: &platform.FakeDirectoryAccess{}})
	cancel()
	if err := application.StopFromHost(context.Background()); err != nil {
		t.Fatalf("StopFromHost() error = %v", err)
	}
	_, err := application.Bindings().SaveStorageDirectory(SaveStorageDirectoryRequest{Kind: "logs", Path: t.TempDir()})
	var runtimeErr *domain.Error
	if !errors.As(err, &runtimeErr) || runtimeErr.Code != domain.RuntimeStopping {
		t.Fatalf("SaveStorageDirectory() error = %v, want RUNTIME_STOPPING", err)
	}
}

// TestStorageDirectoryChooserCancellationAndFailure 验证取消不改变配置，选择器错误经正式边界返回。
func TestStorageDirectoryChooserCancellationAndFailure(t *testing.T) {
	host := &platform.FakeDesktopHost{RunFunc: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }}
	chooser := &platform.FakeDirectoryChooser{}
	application, cancel := newRunningDesktopTestApplication(t, DesktopDependencies{Host: host, Notifications: &platform.FakeNotificationCenter{}, Clock: desktopTestClock{snapshot: domain.TimeSnapshot{Now: time.Now(), Location: time.Local}}, DirectoryChooser: chooser})
	defer cancel()
	before, err := application.Bindings().GetStorageDirectories()
	if err != nil {
		t.Fatalf("GetStorageDirectories() error = %v", err)
	}
	selected, err := application.Bindings().ChooseStorageDirectory("logs")
	if err != nil || selected != "" {
		t.Fatalf("ChooseStorageDirectory() = (%q, %v), want cancellation", selected, err)
	}
	after, err := application.Bindings().GetStorageDirectories()
	if err != nil || after.Logs.SavedPath != before.Logs.SavedPath {
		t.Fatalf("directory changed after cancellation: before=%+v after=%+v error=%v", before.Logs, after.Logs, err)
	}
	wantErr := errors.New("directory dialog failed")
	chooser.ChooseFunc = func(context.Context, string, string) (string, error) { return "", wantErr }
	if _, err := application.Bindings().ChooseStorageDirectory("backups"); !errors.Is(err, wantErr) {
		t.Fatalf("ChooseStorageDirectory() error = %v, want %v", err, wantErr)
	}
}

// TestFormatErrorHidesInternalCause 验证错误 formatter 只输出稳定业务字段而不泄露底层原因。
func TestFormatErrorHidesInternalCause(t *testing.T) {
	formatted := (ErrorFormatter{}).Format(domain.NewValidation("invalid title", "title"))
	if formatted.Code != string(domain.Validation) || formatted.Message != "invalid title" {
		t.Fatalf("formatted error = %+v", formatted)
	}
	if len(formatted.Fields) != 1 || formatted.Fields[0] != "title" {
		t.Fatalf("formatted fields = %+v", formatted.Fields)
	}
}

// structuredListRequest 返回强类型列表请求，避免测试绑定依赖底层 IPC 载荷。
func structuredListRequest() app.ListSchedulesRequest { return app.ListSchedulesRequest{} }

// formatInt 将测试时间转换为 JSON 整数字面量。
func formatInt(value int64) string { return fmt.Sprintf("%d", value) }

// TestActivationHandshakeKeepsLatestPendingEvent 验证前端未就绪时只保留最新激活并按序列补发。
func TestActivationHandshakeKeepsLatestPendingEvent(t *testing.T) {
	host := &platform.FakeDesktopHost{RunFunc: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }}
	frontend := &platform.FakeFrontendEvents{}
	activation := platform.NewFakeActivationSource(2)
	window := &platform.FakeWindowController{}
	location := time.FixedZone("CST", 8*60*60)
	clock := desktopTestClock{snapshot: domain.TimeSnapshot{Now: time.Date(2026, 1, 1, 8, 0, 0, 0, location), Location: location}}
	application, cancel := newRunningDesktopTestApplication(t, DesktopDependencies{Host: host, Window: window, Frontend: frontend, Activation: activation, Notifications: &platform.FakeNotificationCenter{}, Clock: clock})
	defer cancel()
	identity := domain.OccurrenceIdentity{ScheduleID: 9, OriginalDate: "2026-01-01"}
	if err := activation.Emit(platform.Activation{Kind: platform.ActivationKindToast, Target: &identity}); err != nil {
		t.Fatalf("activation Emit() error = %v", err)
	}
	waitForDesktopCondition(t, func() bool { return application.GetDesktopStatus().ActivationSequence == 1 })
	if frontend.EventCount() != 0 {
		t.Fatalf("frontend events before ready = %d, want 0", frontend.EventCount())
	}
	response, err := application.Bindings().FrontendReady(FrontendReadyRequest{LastSequence: 0})
	if err != nil {
		t.Fatalf("FrontendReady() error = %v", err)
	}
	if response.Activation == nil || response.Activation.Sequence != 1 || response.Activation.Target.ScheduleID != 9 {
		t.Fatalf("FrontendReady() response = %+v", response)
	}
	if frontend.EventCount() != 1 {
		t.Fatalf("frontend events after ready = %d, want 1", frontend.EventCount())
	}
	if window.ShowCalls() != 1 || window.ActivateCalls() != 1 {
		t.Fatalf("window activation counts = show:%d activate:%d, want one each", window.ShowCalls(), window.ActivateCalls())
	}
	if err := activation.Emit(platform.Activation{Kind: platform.ActivationKindSecondInstance}); err != nil {
		t.Fatalf("second-instance Emit() error = %v", err)
	}
	waitForDesktopCondition(t, func() bool {
		return application.GetDesktopStatus().ActivationSequence == 2 && window.ShowCalls() == 2 && window.ActivateCalls() == 2
	})
	if frontend.EventCount() != 1 {
		t.Fatalf("frontend events after second-instance activation = %d, want unchanged", frontend.EventCount())
	}
	if window.ShowCalls() != 2 || window.ActivateCalls() != 2 {
		t.Fatalf("window activation counts after second instance = show:%d activate:%d, want two each", window.ShowCalls(), window.ActivateCalls())
	}
}

// TestStopCleanupFailureIsRetryable 验证通知清理失败会停在 CleanupFailed，成功重试后请求宿主退出。
func TestStopCleanupFailureIsRetryable(t *testing.T) {
	host := &platform.FakeDesktopHost{RunFunc: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }}
	var mu sync.Mutex
	cleanupCalls := 0
	wantErr := errors.New("notification cleanup failed")
	notifications := &platform.FakeNotificationCenter{CleanupFunc: func(context.Context) error {
		mu.Lock()
		defer mu.Unlock()
		cleanupCalls++
		if cleanupCalls == 1 {
			return wantErr
		}
		return nil
	}}
	application, cancel := newDesktopTestApplication(t, host, notifications)
	defer cancel()
	if err := application.Stop(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("Stop() error = %v, want cleanup error", err)
	}
	if status := application.GetDesktopStatus(); status.State != DesktopStateCleanupFailed {
		t.Fatalf("state after failed cleanup = %q, want cleanup_failed", status.State)
	}
	if _, err := application.Bindings().RetryCleanup(); err != nil {
		t.Fatalf("RetryCleanup() error = %v", err)
	}
	if status := application.GetDesktopStatus(); status.State != DesktopStateStopped {
		t.Fatalf("state after retry = %q, want stopped", status.State)
	}
	if host.QuitCalls() != 1 {
		t.Fatalf("host Quit count = %d, want 1", host.QuitCalls())
	}
}
