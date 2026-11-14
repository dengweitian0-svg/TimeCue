package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/app"
	logger "github.com/dengweitian0-svg/TimeCue/internal/log"
	"github.com/dengweitian0-svg/TimeCue/internal/model"
	"github.com/dengweitian0-svg/TimeCue/internal/service/notification"
	"github.com/dengweitian0-svg/TimeCue/internal/store"
)

// TestHostStartsAppBeforeLoopAndServesWhenLoopFails 验证绑定端口、AppReady 与降级后实际 HTTP 服务。
func TestHostStartsAppBeforeLoopAndServesWhenLoopFails(t *testing.T) {
	dataDir := t.TempDir()
	port := unusedHostPort(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	options := startupOptions{
		dataDir: dataDir,
		port:    port,
	}
	host, err := newHost(options, &logger.Logger{})
	if err != nil {
		t.Fatal(err)
	}
	host.newLoop = func(ctx context.Context, application *app.App) (agentLoop, error) {
		if application.Scheduler == nil || application.Recovery == nil {
			t.Error("App 尚未完成装配")
		}
		listener, err := net.Listen("tcp", host.address())
		if err == nil {
			listener.Close()
			t.Error("宿主尚未绑定端口")
		}
		return nil, errors.New("injected Loop failure")
	}
	ready := 0
	host.onReady = func() error {
		ready++
		checkHostStatus(t, host.address(), "/health", http.StatusOK)
		checkHostStatus(t, host.address(), "/api/v1/scheduler/schedules", http.StatusOK)
		checkHostStatus(t, host.address(), "/v1/model", http.StatusNotFound)
		cancel()
		return nil
	}
	if err := host.run(ctx, time.Now()); err != nil || ready != 1 {
		t.Fatalf("仅 App 启动失败: ready=%d, err=%v", ready, err)
	}
}

func unusedHostPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func checkHostStatus(t *testing.T, address, path string, want int) {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get("http://" + address + path)
	if err != nil {
		t.Error(err)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != want {
		t.Errorf("%s: HTTP %d, want %d", path, response.StatusCode, want)
	}
}

// TestRequestTrackerWaitsAndRejectsNewRequests 验证退出先等待已接收请求。
func TestRequestTrackerWaitsAndRejectsNewRequests(t *testing.T) {
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	tracker := &requestTracker{handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) { close(started); <-release })}
	go func() {
		tracker.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
		close(finished)
	}()
	<-started
	tracker.stop()
	response := httptest.NewRecorder()
	tracker.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatal("关闭入口后仍接收请求")
	}
	joined := make(chan struct{})
	go func() { tracker.active.Wait(); close(joined) }()
	select {
	case <-joined:
		t.Fatal("活跃请求未结束便完成等待")
	default:
	}
	close(release)
	<-joined
	<-finished
}

// TestHostRejectsLateLoop 验证超时立即开放 App，迟到结果只清理且退出等待实际 LoopDone。
func TestHostRejectsLateLoop(t *testing.T) {
	h := testHost(t)
	h.loopStartup = 400 * time.Millisecond
	ready, initExpired := make(chan struct{}), make(chan struct{})
	release, cleaned := make(chan struct{}), make(chan struct{})
	var unblock sync.Once
	defer unblock.Do(func() { close(release) })
	var activated atomic.Bool
	h.newLoop = func(ctx context.Context, _ *app.App) (agentLoop, error) {
		<-ctx.Done()
		close(initExpired)
		<-release
		return &testLoop{
			activate: func(context.Context) error { activated.Store(true); return nil },
			shutdown: func(context.Context) { close(cleaned) },
		}, nil
	}
	h.onReady = func() error { close(ready); return nil }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	started := time.Now()
	go func() { done <- h.run(ctx, started) }()
	awaitHostSignal(t, initExpired)
	awaitHostSignal(t, ready)
	checkHostStatus(t, h.address(), "/v1/model", http.StatusNotFound)
	checkHostStatus(t, h.address(), "/api/v1/scheduler/schedules", http.StatusOK)
	cancel()
	if err := h.db.CheckHealth(context.Background()); err != nil {
		t.Fatalf("Loop 初始化仍在运行时关闭了数据库: %v", err)
	}
	unblock.Do(func() { close(release) })
	awaitHostSignal(t, cleaned)
	if err := awaitHostResult(t, done); err != nil || activated.Load() {
		t.Fatalf("迟到 Loop 获得推进资格或阻塞退出: %v", err)
	}
	if err := h.db.CheckHealth(context.Background()); !errors.Is(err, store.ErrDatabaseUnavailable) {
		t.Fatalf("共享数据库未关闭: %v", err)
	}
}

// TestHostSkipsLoopWithoutStartupBudget 验证启动预算从 Go 入口开始计时。
func TestHostSkipsLoopWithoutStartupBudget(t *testing.T) {
	h := testHost(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.newLoop = func(context.Context, *app.App) (agentLoop, error) {
		t.Error("用尽启动预算后仍尝试初始化 Loop")
		return nil, errors.New("unexpected Loop startup")
	}
	h.onReady = func() error {
		checkHostStatus(t, h.address(), "/v1/model", http.StatusNotFound)
		cancel()
		return nil
	}
	if err := h.run(ctx, time.Now().Add(-8*time.Second)); err != nil {
		t.Fatal(err)
	}
}

// TestHostAppStartupFailure 验证 App 装配故障退出并释放已绑定的端口和数据库。
func TestHostAppStartupFailure(t *testing.T) {
	h := testHost(t)
	failure := errors.New("injected App startup failure")
	h.newApp = func() (*app.App, error) { return nil, failure }
	h.onReady = func() error { t.Error("App 启动失败后发布 ready"); return nil }
	h.newLoop = func(context.Context, *app.App) (agentLoop, error) {
		t.Error("App 启动失败后初始化 Loop")
		return nil, failure
	}
	if err := h.run(context.Background(), time.Now()); !errors.Is(err, failure) {
		t.Fatalf("错误不符: %v", err)
	}
	if err := h.db.CheckHealth(context.Background()); !errors.Is(err, store.ErrDatabaseUnavailable) {
		t.Fatalf("共享数据库未关闭: %v", err)
	}
	listener, err := net.Listen("tcp", h.address())
	if err != nil {
		t.Fatal(err)
	}
	listener.Close()
}

// TestHostFatalAppFailureDrainsLoopBeforeApp 验证运行期故障触发独立退出窗口和实际完成门禁。
func TestHostFatalAppFailureDrainsLoopBeforeApp(t *testing.T) {
	h := testHost(t)
	h.loopShutdown = 70 * time.Millisecond
	h.appShutdown = 70 * time.Millisecond
	failure := errors.New("injected scheduler failure")
	fatal, ready := make(chan struct{}), make(chan struct{})
	loopExpired, releaseLoop := make(chan struct{}), make(chan struct{})
	recoveryEntered, appExpired, releaseApp := make(chan struct{}), make(chan struct{}), make(chan struct{})
	toolDone := make(chan error, 1)
	var unblockLoop, unblockApp sync.Once
	defer unblockLoop.Do(func() { close(releaseLoop) })
	defer unblockApp.Do(func() { close(releaseApp) })
	newApp := h.newApp
	h.newApp = func() (*app.App, error) {
		application, err := newApp()
		if err != nil {
			return nil, err
		}
		application.Notify = &heldHostRecovery{
			NotificationStore: application.Notify.(*store.NotificationStore),
			entered:           recoveryEntered,
			canceled:          appExpired,
			release:           releaseApp,
		}
		application.Scheduler, err = notification.NewScheduler(application.Notify.(notification.NotificationSchedulerRepo), notification.SchedulerOptions{
			Wait: func(ctx context.Context, _ time.Duration, _ <-chan struct{}) error {
				select {
				case <-fatal:
					return failure
				case <-ctx.Done():
					return ctx.Err()
				}
			},
		})
		return application, err
	}
	h.newLoop = func(_ context.Context, application *app.App) (agentLoop, error) {
		return &testLoop{
			activate: func(context.Context) error { return nil },
			shutdown: func(ctx context.Context) {
				// 恢复正持有操作锁，在途日程工具可用 Loop 的退出期限解除排队。
				_, err := application.WriteSchedule(ctx, model.Schedule{}, 0, "")
				toolDone <- err
				close(loopExpired)
				<-releaseLoop
			},
		}, nil
	}
	h.onReady = func() error { close(ready); return nil }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- h.run(ctx, time.Now()) }()
	awaitHostSignal(t, recoveryEntered)
	awaitHostSignal(t, ready)
	close(fatal)
	awaitHostSignal(t, loopExpired)
	if err := awaitHostResult(t, toolDone); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Loop 期限未取消排队的日程操作: %v", err)
	}
	select {
	case <-appExpired:
		t.Fatal("Loop 尚未完成，App 已开始收尾")
	default:
	}
	if conn, err := net.DialTimeout("tcp", h.address(), time.Second); err == nil {
		conn.Close()
		t.Fatal("Loop 仍在收尾时 HTTP 继续接收新连接")
	}
	unblockLoop.Do(func() { close(releaseLoop) })
	awaitHostSignal(t, appExpired)
	if err := h.db.CheckHealth(context.Background()); err != nil {
		t.Fatalf("App 尚未完成便关闭数据库: %v", err)
	}
	unblockApp.Do(func() { close(releaseApp) })
	if err := awaitHostResult(t, done); !errors.Is(err, failure) {
		t.Fatalf("运行期致命故障未传回入口: %v", err)
	}
}

// TestHostFatalLoopFailure 验证 Loop 运行期故障触发协调退出。
func TestHostFatalLoopFailure(t *testing.T) {
	h := testHost(t)
	failure := errors.New("injected Loop failure")
	fatal, cleaned := make(chan struct{}), make(chan struct{})
	h.newLoop = func(context.Context, *app.App) (agentLoop, error) {
		return &testLoop{
			activate: func(context.Context) error { return nil },
			serve: func(ctx context.Context) error {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-fatal:
					return failure
				}
			},
			shutdown: func(context.Context) { close(cleaned) },
		}, nil
	}
	h.onReady = func() error { close(fatal); return nil }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- h.run(ctx, time.Now()) }()
	awaitHostSignal(t, cleaned)
	if err := awaitHostResult(t, done); !errors.Is(err, failure) {
		t.Fatalf("Loop 故障未传回宿主: %v", err)
	}
}

// TestHostHTTPShutdownWaitsForHandler 验证 HTTP 到期强制关闭连接并等待 handler 返回。
func TestHostHTTPShutdownWaitsForHandler(t *testing.T) {
	h := testHost(t)
	h.httpShutdown = 75 * time.Millisecond
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	listener, err := net.Listen("tcp", h.address())
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ready, entered, disconnected, release := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	var unblock sync.Once
	defer unblock.Do(func() { close(release) })
	h.onReady = func() error { close(ready); return nil }
	done := make(chan error, 1)
	go func() {
		done <- h.serveHTTP(ctx, cancel, listener, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			close(entered)
			<-r.Context().Done()
			close(disconnected)
			<-release
		}))
	}()
	awaitHostSignal(t, ready)
	client := &http.Client{Timeout: 3 * time.Second}
	go func() {
		response, err := client.Get("http://" + h.address())
		if err == nil {
			response.Body.Close()
		}
	}()
	awaitHostSignal(t, entered)
	cancel(nil)
	awaitHostSignal(t, disconnected)
	select {
	case <-done:
		t.Fatal("在途 handler 尚未实际返回")
	default:
	}
	unblock.Do(func() { close(release) })
	if err := awaitHostResult(t, done); err != nil {
		t.Fatal(err)
	}
}

type testLoop struct {
	agentLoop
	activate func(context.Context) error
	serve    func(context.Context) error
	shutdown func(context.Context)
}

// Activate 记录宿主是否接纳测试 Loop。
func (l *testLoop) Activate(ctx context.Context) error { return l.activate(ctx) }

// Serve 注入运行期致命故障，或者等待全局退出。
func (l *testLoop) Serve(ctx context.Context) error {
	if l.serve != nil {
		return l.serve(ctx)
	}
	<-ctx.Done()
	return nil
}

// Shutdown 允许测试控制 Loop 实际返回的时机。
func (l *testLoop) Shutdown(ctx context.Context) { l.shutdown(ctx) }

type heldHostRecovery struct {
	*store.NotificationStore
	entered, canceled, release chan struct{}
}

// RecoveryStart 模拟已经开始且需等到独立退出期限的历史恢复批次。
func (r *heldHostRecovery) RecoveryStart(ctx context.Context, _ time.Time) (time.Time, error) {
	close(r.entered)
	<-ctx.Done()
	close(r.canceled)
	<-r.release
	return time.Time{}, ctx.Err()
}

func testHost(t *testing.T) *host {
	t.Helper()
	h, err := newHost(startupOptions{
		dataDir: t.TempDir(),
		port:    unusedHostPort(t),
	}, &logger.Logger{})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func awaitHostSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(4 * time.Second):
		t.Fatal("未收到生命周期信号")
	}
}

func awaitHostResult(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(4 * time.Second):
		t.Fatal("宿主未退出")
		return nil
	}
}
