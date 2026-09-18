package runtime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
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
	return newRunningDesktopTestApplicationWithStore(t, dependencies, openDesktopTestStore(t))
}

// newRunningDesktopTestApplicationWithStore 启动测试运行时并保留 SQLite Store，便于注入提醒发送结果。
func newRunningDesktopTestApplicationWithStore(t *testing.T, dependencies DesktopDependencies, store *sqlite.Store) (*DesktopApplication, context.CancelFunc) {
	t.Helper()
	application := NewDesktopApplication(dependencies)
	root := t.TempDir()
	cfg := config.Config{
		App:           config.AppConfig{Name: "TimeCue", Version: "test"},
		Log:           config.LogConfig{Level: "info", Path: filepath.Join(root, "logs", "timecue.log")},
		Backup:        config.BackupConfig{Dir: filepath.Join(root, "backups")},
		Window:        config.WindowConfig{CloseBehavior: config.CloseBehaviorTray},
		Theme:         config.ThemePeach,
		ResolvedPaths: config.ResolvedPaths{ConfigPath: filepath.Join(root, "config.yaml"), DatabasePath: filepath.Join(root, "data", "timecue.db"), LogPath: filepath.Join(root, "logs", "timecue.log"), BackupDir: filepath.Join(root, "backups")},
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- application.Run(ctx, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), store)
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

// TestStopDuringDomReadyStartupCancelsStartupGate 验证停止不会等待尚未启动的 scheduler channel。
func TestStopDuringDomReadyStartupCancelsStartupGate(t *testing.T) {
	host := &platform.FakeDesktopHost{RunFunc: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }}
	startupEntered := make(chan struct{})
	notifications := &platform.FakeNotificationCenter{InitializeFunc: func(ctx context.Context) error {
		close(startupEntered)
		<-ctx.Done()
		return ctx.Err()
	}}
	application := NewDesktopApplication(DesktopDependencies{Host: host, Notifications: notifications, Clock: desktopTestClock{snapshot: domain.TimeSnapshot{Now: time.Now(), Location: time.Local}}})
	root := t.TempDir()
	cfg := config.Config{
		App:           config.AppConfig{Name: "TimeCue", Version: "test"},
		Log:           config.LogConfig{Level: "info", Path: filepath.Join(root, "logs", "timecue.log")},
		Backup:        config.BackupConfig{Dir: filepath.Join(root, "backups")},
		Window:        config.WindowConfig{CloseBehavior: config.CloseBehaviorTray},
		ResolvedPaths: config.ResolvedPaths{ConfigPath: filepath.Join(root, "config.yaml"), DatabasePath: filepath.Join(root, "data", "timecue.db"), LogPath: filepath.Join(root, "logs", "timecue.log"), BackupDir: filepath.Join(root, "backups")},
	}
	runCtx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() {
		runDone <- application.Run(runCtx, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), openDesktopTestStore(t))
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-runDone:
		case <-time.After(3 * time.Second):
			t.Error("desktop application did not stop during cleanup")
		}
	})
	waitForDesktopCondition(t, func() bool { return application.Service() != nil })
	if err := application.OnStartup(context.Background()); err != nil {
		t.Fatalf("OnStartup() error = %v", err)
	}
	domReadyDone := make(chan error, 1)
	go func() { domReadyDone <- application.OnDomReady(context.Background()) }()
	select {
	case <-startupEntered:
	case <-time.After(time.Second):
		t.Fatal("DOM-ready startup did not enter notification initialization")
	}
	stopDone := make(chan error, 1)
	go func() { stopDone <- application.Stop(context.Background()) }()
	select {
	case err := <-stopDone:
		if err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() waited for an unstarted scheduler")
	}
	select {
	case err := <-domReadyDone:
		if err != nil {
			t.Fatalf("OnDomReady() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("OnDomReady() did not finish after startup cancellation")
	}
	if status := application.GetDesktopStatus(); status.State != DesktopStateStopped {
		t.Fatalf("desktop state = %q, want stopped", status.State)
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

// TestTrayExitClosesResourcesWhenNotificationCleanupFails 验证通知清理失败不会阻断托盘正式退出。
func TestTrayExitClosesResourcesWhenNotificationCleanupFails(t *testing.T) {
	host := &platform.FakeDesktopHost{RunFunc: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }}
	activation := platform.NewFakeActivationSource(1)
	environment := platform.NewFakeEnvironmentEventSource(1)
	tray := &platform.FakeTrayPort{}
	notifications := &platform.FakeNotificationCenter{CleanupFunc: func(context.Context) error { return errors.New("notification history unavailable") }}
	location := time.FixedZone("CST", 8*60*60)
	application, cancel := newRunningDesktopTestApplication(t, DesktopDependencies{
		Host:          host,
		Activation:    activation,
		Environment:   environment,
		Tray:          tray,
		Notifications: notifications,
		Clock: desktopTestClock{snapshot: domain.TimeSnapshot{
			Now:      time.Date(2026, 1, 1, 8, 0, 0, 0, location),
			Location: location,
		}},
	})
	defer cancel()
	var logs bytes.Buffer
	application.mu.Lock()
	application.logger = slog.New(slog.NewTextHandler(&logs, nil))
	application.mu.Unlock()

	tray.InvokeExit()
	waitForDesktopCondition(t, func() bool {
		return application.GetDesktopStatus().State == DesktopStateStopped && host.QuitCalls() == 1
	})
	if !activation.Closed || !environment.Closed || !tray.Closed || !notifications.Closed {
		t.Fatalf("resource closure = activation:%t environment:%t tray:%t notifications:%t, want all closed", activation.Closed, environment.Closed, tray.Closed, notifications.Closed)
	}
	if notifications.CleanupCount != 1 {
		t.Fatalf("notification cleanup count = %d, want 1", notifications.CleanupCount)
	}
	if count := strings.Count(logs.String(), "notification history cleanup failed before exit"); count != 1 {
		t.Fatalf("notification cleanup warning count = %d, want 1; logs = %s", count, logs.String())
	}
	if !strings.Contains(logs.String(), "level=WARN") {
		t.Fatalf("notification cleanup failure was not logged at WARN: %s", logs.String())
	}
	if strings.Contains(logs.String(), "desktop runtime error") {
		t.Fatalf("notification cleanup failure was logged as a runtime error: %s", logs.String())
	}
}

// TestStopCancelsEnvironmentForwarderBeforeCoordinatorStop 验证 Stop 先取消环境转发，避免等待协调锁形成阻塞环。
func TestStopCancelsEnvironmentForwarderBeforeCoordinatorStop(t *testing.T) {
	eventContext, cancelEvent := context.WithCancel(context.Background())
	coordinator := app.NewMutationCoordinator()
	notifications := &platform.FakeNotificationCenter{}
	application := &DesktopApplication{
		deps:            DesktopDependencies{Notifications: notifications},
		coordinator:     coordinator,
		schedulerCancel: cancelEvent,
		state:           DesktopStateRunning,
	}
	application.environmentForwarder.Add(1)
	locked := make(chan struct{})
	go func() {
		coordinator.Lock()
		close(locked)
		<-eventContext.Done()
		coordinator.Unlock()
		application.environmentForwarder.Done()
	}()
	<-locked

	stopDone := make(chan error, 1)
	go func() { stopDone <- application.StopFromHost(context.Background()) }()
	select {
	case err := <-stopDone:
		if err != nil {
			t.Fatalf("StopFromHost() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("StopFromHost() blocked while environment forwarder held coordinator lock")
	}
	if status := application.GetDesktopStatus(); status.State != DesktopStateStopped {
		t.Fatalf("desktop state = %q, want stopped", status.State)
	}
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
	cfg := config.Config{Window: config.WindowConfig{CloseBehavior: config.CloseBehaviorTray}, ResolvedPaths: config.ResolvedPaths{ConfigPath: filepath.Join(t.TempDir(), "config.yaml")}}
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

// TestConfigBindingsExposeLogLevelAndRestoreDefault 验证日志级别保存与显式恢复默认的运行时边界。
func TestConfigBindingsExposeLogLevelAndRestoreDefault(t *testing.T) {
	host := &platform.FakeDesktopHost{RunFunc: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }}
	application, cancel := newDesktopTestApplication(t, host, nil)
	defer cancel()
	binding := application.Bindings()

	initial, err := binding.GetLogLevel()
	if err != nil {
		t.Fatalf("GetLogLevel() error = %v", err)
	}
	if initial.EffectiveLevel != "info" || initial.SavedLevel != "info" || initial.RestartRequired {
		t.Fatalf("initial log level = %+v, want info without restart", initial)
	}
	saved, err := binding.SaveLogLevel(SaveLogLevelRequest{Level: "debug"})
	if err != nil {
		t.Fatalf("SaveLogLevel() error = %v", err)
	}
	if saved.EffectiveLevel != "info" || saved.SavedLevel != "debug" || !saved.RestartRequired {
		t.Fatalf("saved log level = %+v, want effective info/saved debug", saved)
	}
	if err := binding.RestoreConfigDefault(RestoreConfigDefaultRequest{Property: string(config.ConfigPropertyLogLevel)}); err != nil {
		t.Fatalf("RestoreConfigDefault() error = %v", err)
	}
	restored, err := binding.GetLogLevel()
	if err != nil {
		t.Fatalf("GetLogLevel() after restore error = %v", err)
	}
	if restored.EffectiveLevel != "info" || restored.SavedLevel != "info" || restored.RestartRequired {
		t.Fatalf("restored log level = %+v, want info without restart", restored)
	}
	if err := binding.RestoreConfigDefault(RestoreConfigDefaultRequest{Property: "app.version"}); err == nil {
		t.Fatal("RestoreConfigDefault() error = nil, want invalid property error")
	}
}

// TestConfigBindingsExposeThemeAndPersistenceFailures 验证主题读取、保存、失败回滚和恢复默认。
func TestConfigBindingsExposeThemeAndPersistenceFailures(t *testing.T) {
	host := &platform.FakeDesktopHost{RunFunc: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }}
	application, cancel := newDesktopTestApplication(t, host, nil)
	defer cancel()
	binding := application.Bindings()

	initial, err := binding.GetTheme()
	if err != nil || initial.Theme != string(config.ThemePeach) {
		t.Fatalf("GetTheme() = %+v, error = %v, want peach", initial, err)
	}
	saved, err := binding.SaveTheme(SaveThemeRequest{Theme: string(config.ThemeMint)})
	if err != nil || saved.Theme != string(config.ThemeMint) {
		t.Fatalf("SaveTheme() = %+v, error = %v, want mint", saved, err)
	}

	application.mu.Lock()
	oldPath := application.config.ResolvedPaths.ConfigPath
	application.config.ResolvedPaths.ConfigPath = filepath.Join(t.TempDir(), "missing", "config.yaml")
	application.mu.Unlock()
	_, err = binding.SaveTheme(SaveThemeRequest{Theme: string(config.ThemeSky)})
	var persistenceErr *ConfigPersistenceError
	if !errors.As(err, &persistenceErr) || FormatError(err).Code != "STORAGE" {
		t.Fatalf("SaveTheme() failure = %v, formatted = %+v, want ConfigPersistenceError/STORAGE", err, FormatError(err))
	}
	afterFailure, err := binding.GetTheme()
	if err != nil || afterFailure.Theme != string(config.ThemeMint) {
		t.Fatalf("GetTheme() after failed save = %+v, error = %v, want old mint", afterFailure, err)
	}

	application.mu.Lock()
	application.config.ResolvedPaths.ConfigPath = filepath.Join(t.TempDir(), "config.yaml")
	application.mu.Unlock()
	if err := binding.RestoreConfigDefault(RestoreConfigDefaultRequest{Property: string(config.ConfigPropertyTheme)}); err != nil {
		t.Fatalf("RestoreConfigDefault(theme) error = %v", err)
	}
	restored, err := binding.GetTheme()
	if err != nil || restored.Theme != string(config.ThemePeach) {
		t.Fatalf("GetTheme() after restore = %+v, error = %v, want peach", restored, err)
	}

	application.mu.Lock()
	application.config.ResolvedPaths.ConfigPath = oldPath
	application.mu.Unlock()
	if err := application.StopFromHost(context.Background()); err != nil {
		t.Fatalf("StopFromHost() error = %v", err)
	}
	if _, err := binding.SaveTheme(SaveThemeRequest{Theme: string(config.ThemeLavender)}); err == nil || FormatError(err).Code != string(domain.RuntimeStopping) {
		t.Fatalf("SaveTheme() after stop = %v, formatted = %+v, want RUNTIME_STOPPING", err, FormatError(err))
	}
}

// TestApplyWindowThemeBindingValidatesAndDelegates 验证原生标题栏颜色校验、转换和错误传播。
func TestApplyWindowThemeBindingValidatesAndDelegates(t *testing.T) {
	host := &platform.FakeDesktopHost{RunFunc: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }}
	theme := &platform.FakeWindowThemeController{}
	location := time.FixedZone("CST", 8*60*60)
	application, cancel := newRunningDesktopTestApplication(t, DesktopDependencies{
		Host:          host,
		WindowTheme:   theme,
		Notifications: &platform.FakeNotificationCenter{},
		Clock:         desktopTestClock{snapshot: domain.TimeSnapshot{Now: time.Date(2026, 1, 1, 8, 0, 0, 0, location), Location: location}},
	})
	defer cancel()

	request := ApplyWindowThemeRequest{Surface: "#326D55", Text: "#293F35"}
	if err := application.Bindings().ApplyWindowTheme(request); err != nil {
		t.Fatalf("ApplyWindowTheme() error = %v", err)
	}
	colors := theme.AppliedColors()
	wantColors := platform.WindowThemeColors{TitleBar: platform.RGBColor{R: 0x32, G: 0x6d, B: 0x55}, TitleText: platform.RGBColor{R: 0x29, G: 0x3f, B: 0x35}}
	if len(colors) != 1 || colors[0] != wantColors {
		t.Fatalf("applied colors = %+v, want converted mint colors %+v", colors, wantColors)
	}

	for _, invalid := range []ApplyWindowThemeRequest{
		{Surface: "#326D5", Text: "#293F35"},
		{Surface: "#GG6D55", Text: "#293F35"},
		{Surface: "#326D55", Text: "transparent"},
	} {
		if err := application.Bindings().ApplyWindowTheme(invalid); err == nil {
			t.Fatalf("ApplyWindowTheme(%+v) error = nil, want validation error", invalid)
		}
	}
	if theme.Calls() != 1 {
		t.Fatalf("theme Apply() calls = %d, want invalid requests not delegated", theme.Calls())
	}

	wantErr := errors.New("DWM unavailable")
	theme.ApplyFunc = func(context.Context, platform.WindowThemeColors) error { return wantErr }
	if err := application.Bindings().ApplyWindowTheme(request); !errors.Is(err, wantErr) {
		t.Fatalf("ApplyWindowTheme() native error = %v, want %v", err, wantErr)
	}
}

// TestReminderQueryBindingsUseSQLiteStore 验证提醒查询绑定复用运行时 Service 和真实 SQLite 状态。
func TestReminderQueryBindingsUseSQLiteStore(t *testing.T) {
	host := &platform.FakeDesktopHost{RunFunc: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }}
	store := openDesktopTestStore(t)
	application, cancel := newRunningDesktopTestApplicationWithStore(t, DesktopDependencies{
		Host:          host,
		Notifications: &platform.FakeNotificationCenter{},
		Clock: desktopTestClock{snapshot: domain.TimeSnapshot{
			Now:      time.Date(2026, 1, 1, 8, 0, 0, 0, time.FixedZone("CST", 8*60*60)),
			Location: time.FixedZone("CST", 8*60*60),
		}},
	}, store)
	defer cancel()

	start := time.Date(2026, 1, 1, 9, 0, 0, 0, time.FixedZone("CST", 8*60*60)).Unix()
	created, err := application.Bindings().CreateSchedule(`{"title":"Focus","start_unix":` + formatInt(start) + `,"end_unix":` + formatInt(start+30*60) + `}`)
	if err != nil {
		t.Fatalf("CreateSchedule() error = %v", err)
	}
	identity := domain.OccurrenceIdentity{ScheduleID: domain.ScheduleID(created.ID), OriginalDate: "2026-01-01"}
	if claimed, err := store.ClaimReminderAttempt(context.Background(), identity, time.Unix(start, 0).UTC()); err != nil || !claimed {
		t.Fatalf("ClaimReminderAttempt() = (%t, %v), want claimed", claimed, err)
	}
	if err := store.SetReminderAttemptStatus(context.Background(), identity, app.ReminderAttemptSubmitted); err != nil {
		t.Fatalf("SetReminderAttemptStatus() error = %v", err)
	}

	page, err := application.Bindings().ListPendingReminders(app.ListPendingRemindersRequest{Limit: 10})
	if err != nil {
		t.Fatalf("ListPendingReminders() error = %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ScheduleID != created.ID || page.Items[0].OriginalDate != identity.OriginalDate {
		t.Fatalf("pending page = %+v, want one stable identity", page)
	}
	detail, err := application.Bindings().GetReminderDetail(created.ID, identity.OriginalDate)
	if err != nil {
		t.Fatalf("GetReminderDetail() error = %v", err)
	}
	if detail.Status != app.ReminderStatusPending || detail.ContentStatus != app.ReminderContentAvailable || detail.Title == nil || *detail.Title != "Focus" || detail.StartUnix == nil || *detail.StartUnix != start {
		t.Fatalf("reminder detail = %+v, want rebuilt content", detail)
	}
}

// newReminderBindingFixture 创建带真实 SQLite 提醒尝试的运行时绑定测试夹具。
func newReminderBindingFixture(t *testing.T) (*DesktopApplication, *sqlite.Store, domain.OccurrenceIdentity) {
	t.Helper()
	location := time.FixedZone("CST", 8*60*60)
	store := openDesktopTestStore(t)
	application, _ := newRunningDesktopTestApplicationWithStore(t, DesktopDependencies{
		Host:          &platform.FakeDesktopHost{RunFunc: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }},
		Notifications: &platform.FakeNotificationCenter{},
		Clock: desktopTestClock{snapshot: domain.TimeSnapshot{
			Now:      time.Date(2026, 1, 1, 8, 0, 0, 0, location),
			Location: location,
		}},
	}, store)
	start := time.Date(2026, 1, 1, 9, 0, 0, 0, location).Unix()
	created, err := application.Bindings().CreateSchedule(`{"title":"Focus","start_unix":` + formatInt(start) + `,"end_unix":` + formatInt(start+30*60) + `}`)
	if err != nil {
		t.Fatalf("CreateSchedule() error = %v", err)
	}
	return application, store, domain.OccurrenceIdentity{ScheduleID: domain.ScheduleID(created.ID), OriginalDate: "2026-01-01"}
}

// seedReminderAttempt 写入测试所需的提醒发送结果，不绕过 SQLite 状态转换。
func seedReminderAttempt(t *testing.T, store *sqlite.Store, identity domain.OccurrenceIdentity, status app.ReminderAttemptStatus) {
	t.Helper()
	if claimed, err := store.ClaimReminderAttempt(context.Background(), identity, time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)); err != nil || !claimed {
		t.Fatalf("ClaimReminderAttempt() = (%t, %v), want claimed", claimed, err)
	}
	if status == app.ReminderAttemptClaimed {
		return
	}
	if err := store.SetReminderAttemptStatus(context.Background(), identity, status); err != nil {
		t.Fatalf("SetReminderAttemptStatus() error = %v", err)
	}
}

// TestConfirmReminderBindingIsIdempotent 验证重复确认只首次写入并从待处理列表消失。
func TestConfirmReminderBindingIsIdempotent(t *testing.T) {
	application, store, identity := newReminderBindingFixture(t)
	defer func() { _ = application.StopFromHost(context.Background()) }()
	seedReminderAttempt(t, store, identity, app.ReminderAttemptSubmitted)

	first, err := application.Bindings().ConfirmReminder(int64(identity.ScheduleID), identity.OriginalDate)
	if err != nil || !first.Changed {
		t.Fatalf("first ConfirmReminder() = %+v, %v", first, err)
	}
	second, err := application.Bindings().ConfirmReminder(int64(identity.ScheduleID), identity.OriginalDate)
	if err != nil || second.Changed || !second.ProcessedAtUTC.Equal(first.ProcessedAtUTC) {
		t.Fatalf("second ConfirmReminder() = %+v, %v, want idempotent result", second, err)
	}
	pending, err := application.Bindings().ListPendingReminders(app.ListPendingRemindersRequest{Limit: 10})
	if err != nil || len(pending.Items) != 0 {
		t.Fatalf("pending after confirmation = %+v, %v", pending, err)
	}
}

// TestConfirmReminderBindingPreservesErrorCategories 验证目标不存在和不可处理状态不被绑定层改写。
func TestConfirmReminderBindingPreservesErrorCategories(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		application, _, identity := newReminderBindingFixture(t)
		defer func() { _ = application.StopFromHost(context.Background()) }()
		_, err := application.Bindings().ConfirmReminder(int64(identity.ScheduleID), identity.OriginalDate)
		if err == nil || FormatError(err).Code != string(domain.NotFound) {
			t.Fatalf("missing confirmation error = %v, formatted = %+v", err, FormatError(err))
		}
	})
	t.Run("not processable", func(t *testing.T) {
		application, store, identity := newReminderBindingFixture(t)
		defer func() { _ = application.StopFromHost(context.Background()) }()
		seedReminderAttempt(t, store, identity, app.ReminderAttemptFailed)
		_, err := application.Bindings().ConfirmReminder(int64(identity.ScheduleID), identity.OriginalDate)
		if err == nil || FormatError(err).Code != string(domain.ReminderNotProcessable) {
			t.Fatalf("not processable confirmation error = %v, formatted = %+v", err, FormatError(err))
		}
	})
}

// TestConfirmReminderBindingRejectsStoppingAndStorageFailures 验证停止准入和底层失败均可观察。
func TestConfirmReminderBindingRejectsStoppingAndStorageFailures(t *testing.T) {
	t.Run("stopping", func(t *testing.T) {
		application, store, identity := newReminderBindingFixture(t)
		seedReminderAttempt(t, store, identity, app.ReminderAttemptSubmitted)
		if err := application.StopFromHost(context.Background()); err != nil {
			t.Fatalf("StopFromHost() error = %v", err)
		}
		_, err := application.Bindings().ConfirmReminder(int64(identity.ScheduleID), identity.OriginalDate)
		if err == nil || FormatError(err).Code != string(domain.RuntimeStopping) {
			t.Fatalf("stopping confirmation error = %v, formatted = %+v", err, FormatError(err))
		}
	})
	t.Run("storage failure", func(t *testing.T) {
		application, store, identity := newReminderBindingFixture(t)
		seedReminderAttempt(t, store, identity, app.ReminderAttemptSubmitted)
		if err := store.Close(); err != nil {
			t.Fatalf("Store.Close() error = %v", err)
		}
		_, err := application.Bindings().ConfirmReminder(int64(identity.ScheduleID), identity.OriginalDate)
		if err == nil || FormatError(err).Code != "INTERNAL" {
			t.Fatalf("storage confirmation error = %v, formatted = %+v", err, FormatError(err))
		}
	})
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

// TestScheduleHiddenEventHandshakeAndDelivery 验证前端握手前暂存、按 ID 去重、就绪后即时发送和停止丢弃。
func TestScheduleHiddenEventHandshakeAndDelivery(t *testing.T) {
	frontend := &platform.FakeFrontendEvents{}
	application := &DesktopApplication{
		deps:                  DesktopDependencies{Frontend: frontend},
		state:                 DesktopStateRunning,
		runtimeContext:        context.Background(),
		pendingScheduleHidden: make(map[domain.ScheduleID]struct{}),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan app.ReminderSchedulerEvent, 4)
	go application.forwardReminderEvents(ctx, events)
	event := app.ReminderSchedulerEvent{Type: app.ReminderSchedulerEventScheduleHidden, ScheduleID: 7}
	events <- event
	events <- event
	waitForDesktopCondition(t, func() bool {
		application.mu.Lock()
		defer application.mu.Unlock()
		return len(application.pendingScheduleHidden) == 1
	})
	if frontend.EventCount() != 0 {
		t.Fatalf("frontend events before ready = %d, want 0", frontend.EventCount())
	}
	if _, err := application.FrontendReady(context.Background(), FrontendReadyRequest{}); err != nil {
		t.Fatalf("FrontendReady() error = %v", err)
	}
	if frontend.EventCount() != 1 {
		t.Fatalf("frontend events after ready = %d, want one deduplicated event", frontend.EventCount())
	}
	if payload, ok := frontend.Events[0].Payload.(platform.ScheduleHiddenEvent); !ok || payload.ScheduleID != 7 {
		t.Fatalf("hidden event payload = %#v", frontend.Events[0].Payload)
	}

	events <- app.ReminderSchedulerEvent{Type: app.ReminderSchedulerEventScheduleHidden, ScheduleID: 8}
	waitForDesktopCondition(t, func() bool { return frontend.EventCount() == 2 })
	if payload, ok := frontend.Events[1].Payload.(platform.ScheduleHiddenEvent); !ok || payload.ScheduleID != 8 {
		t.Fatalf("immediate hidden event payload = %#v", frontend.Events[1].Payload)
	}

	frontend.EmitFunc = func(context.Context, string, any) error { return errors.New("frontend unavailable") }
	events <- app.ReminderSchedulerEvent{Type: app.ReminderSchedulerEventScheduleHidden, ScheduleID: 9}
	waitForDesktopCondition(t, func() bool { return frontend.EventCount() == 3 })
	application.mu.Lock()
	application.state = DesktopStateStopping
	application.mu.Unlock()
	events <- app.ReminderSchedulerEvent{Type: app.ReminderSchedulerEventScheduleHidden, ScheduleID: 10}
	time.Sleep(10 * time.Millisecond)
	if frontend.EventCount() != 3 {
		t.Fatalf("frontend events after stopping = %d, want unchanged", frontend.EventCount())
	}
}

// TestPendingRemindersChangedEventHandshakeAndDelivery 验证列表变化信号在前端握手前合并、就绪后发送且不阻塞停止。
func TestPendingRemindersChangedEventHandshakeAndDelivery(t *testing.T) {
	frontend := &platform.FakeFrontendEvents{}
	application := &DesktopApplication{
		deps:                  DesktopDependencies{Frontend: frontend},
		state:                 DesktopStateRunning,
		runtimeContext:        context.Background(),
		pendingScheduleHidden: make(map[domain.ScheduleID]struct{}),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan app.ReminderSchedulerEvent, 4)
	go application.forwardReminderEvents(ctx, events)
	event := app.ReminderSchedulerEvent{Type: app.ReminderSchedulerEventPendingRemindersChanged}
	events <- event
	events <- event
	waitForDesktopCondition(t, func() bool {
		application.mu.Lock()
		defer application.mu.Unlock()
		return application.pendingRemindersChanged
	})
	if frontend.EventCount() != 0 {
		t.Fatalf("frontend events before ready = %d, want 0", frontend.EventCount())
	}
	if _, err := application.FrontendReady(context.Background(), FrontendReadyRequest{}); err != nil {
		t.Fatalf("FrontendReady() error = %v", err)
	}
	if frontend.EventCount() != 1 {
		t.Fatalf("frontend events after ready = %d, want one deduplicated event", frontend.EventCount())
	}
	if frontend.Events[0].Name != platform.FrontendEventPendingRemindersChanged {
		t.Fatalf("frontend event name = %q", frontend.Events[0].Name)
	}
	if _, ok := frontend.Events[0].Payload.(platform.PendingRemindersChangedEvent); !ok {
		t.Fatalf("pending reminders changed payload = %#v", frontend.Events[0].Payload)
	}

	events <- event
	waitForDesktopCondition(t, func() bool { return frontend.EventCount() == 2 })
	if frontend.Events[1].Name != platform.FrontendEventPendingRemindersChanged {
		t.Fatalf("immediate frontend event name = %q", frontend.Events[1].Name)
	}

	frontend.EmitFunc = func(context.Context, string, any) error { return errors.New("frontend unavailable") }
	events <- event
	waitForDesktopCondition(t, func() bool { return frontend.EventCount() == 3 })
	application.mu.Lock()
	application.state = DesktopStateStopping
	application.mu.Unlock()
	events <- event
	time.Sleep(10 * time.Millisecond)
	if frontend.EventCount() != 3 {
		t.Fatalf("frontend events after stopping = %d, want unchanged", frontend.EventCount())
	}
}

// TestStopFromHostResourceCloseFailureIsRetryable 验证宿主关闭失败会停在 CleanupFailed，成功重试后请求宿主退出。
func TestStopFromHostResourceCloseFailureIsRetryable(t *testing.T) {
	host := &platform.FakeDesktopHost{RunFunc: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }}
	var mu sync.Mutex
	closeCalls := 0
	wantErr := errors.New("tray close failed")
	tray := &platform.FakeTrayPort{CloseFunc: func() error {
		mu.Lock()
		defer mu.Unlock()
		closeCalls++
		if closeCalls == 1 {
			return wantErr
		}
		return nil
	}}
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
	if err := application.StopFromHost(context.Background()); !errors.Is(err, wantErr) {
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

// TestStopRequestsHostQuitDespiteTrayCloseFailure 验证托盘退出不会因托盘关闭错误遗留主窗口。
func TestStopRequestsHostQuitDespiteTrayCloseFailure(t *testing.T) {
	host := &platform.FakeDesktopHost{RunFunc: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }}
	wantErr := errors.New("tray close failed")
	tray := &platform.FakeTrayPort{CloseFunc: func() error { return wantErr }}
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
	if err := application.Stop(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("Stop() error = %v, want tray close error", err)
	}
	if host.QuitCalls() != 1 {
		t.Fatalf("host Quit count = %d, want 1", host.QuitCalls())
	}
}
