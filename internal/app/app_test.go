package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/config"
	logger "github.com/dengweitian0-svg/TimeCue/internal/log"
	"github.com/dengweitian0-svg/TimeCue/internal/model"
	"github.com/dengweitian0-svg/TimeCue/internal/service/notification"
	"github.com/dengweitian0-svg/TimeCue/internal/service/recovery"
)

const testRuntimePort = 1145

type failingChatStartup struct{}

// Start 在聊天启动故障边界返回明确错误，验证端口冲突优先阻止恢复。
func (*failingChatStartup) Start(context.Context) error {
	return errors.New("injected chat startup failure")
}

// Shutdown 满足生命周期故障注入契约，不持有实际资源。
func (*failingChatStartup) Shutdown(context.Context) {}

type deadlineChatShutdown struct{}

// Start 在退出期限故障边界装配可控生命周期。
func (*deadlineChatShutdown) Start(context.Context) error { return nil }

// Shutdown 消耗宿主全部退出窗口，验证 HTTP 不会另获一个五秒窗口。
func (*deadlineChatShutdown) Shutdown(ctx context.Context) { <-ctx.Done() }

// TestRuntimeReservesPortBeforeChatStartup 验证端口冲突时不会先进入聊天恢复。
func TestRuntimeReservesPortBeforeChatStartup(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	application, _ := runtimeTestApp(t)
	application.ChatRuntime = &failingChatStartup{}
	db := &startupRuntimeDB{drainRuntimeDB: drainRuntimeDB{closed: make(chan struct{})}}
	err = application.Run(context.Background(), RuntimeOptions{Database: db, Handler: http.NotFoundHandler(), Port: listener.Addr().(*net.TCPAddr).Port})
	if err == nil || !strings.Contains(err.Error(), "listen on") {
		t.Fatalf("端口冲突之前进入了聊天启动: %v", err)
	}
}

// TestRuntimeReleasesPortAfterChatStartupFailure 验证取得端口后聊天启动失败仍释放监听资源。
func TestRuntimeReleasesPortAfterChatStartupFailure(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	_ = probe.Close()
	application, _ := runtimeTestApp(t)
	application.ChatRuntime = &failingChatStartup{}
	db := &startupRuntimeDB{drainRuntimeDB: drainRuntimeDB{closed: make(chan struct{})}}
	err = application.Run(context.Background(), RuntimeOptions{Database: db, Handler: http.NotFoundHandler(), Port: port})
	if err == nil || !strings.Contains(err.Error(), "injected chat startup failure") {
		t.Fatalf("错误不符: %v", err)
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("聊天启动失败后端口仍被占用: %v", err)
	}
	_ = listener.Close()
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

type startupRuntimeDB struct {
	drainRuntimeDB
	path  string
	check func() error
}

func (d *startupRuntimeDB) Connect(_ context.Context, path string) error { d.path = path; return nil }
func (d *startupRuntimeDB) CheckHealth(context.Context) error {
	if d.check != nil {
		return d.check()
	}
	return nil
}

func runtimeTestApp(t *testing.T) (*App, string) {
	t.Helper()
	dir := t.TempDir()
	manager, err := config.NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	scheduler, _ := notification.NewScheduler(drainSchedulerRepo{}, notification.SchedulerOptions{})
	recovery, _ := recovery.NewService(emptyRecoveryRepo{}, notification.SchedulerOptions{})
	return &App{Config: manager, Scheduler: scheduler, Recovery: recovery}, dir
}

// TestRuntimeReadyFailureAndCancellation 验证就绪失败、取消及端口冲突均由原生命周期清理。
func TestRuntimeReadyFailureAndCancellation(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:1145")
	if err != nil {
		t.Skip("固定端口已占用")
	}
	_ = probe.Close()
	for _, kind := range []string{"ready-failed", "canceled", "schema-failed", "occupied"} {
		t.Run(kind, func(t *testing.T) {
			app, dir := runtimeTestApp(t)
			db := &startupRuntimeDB{drainRuntimeDB: drainRuntimeDB{closed: make(chan struct{})}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if kind == "canceled" {
				db.check = func() error { cancel(); return nil }
			}
			if kind == "schema-failed" {
				db.check = func() error { return errors.New("invalid schema") }
			}
			if kind == "occupied" {
				listener, err := net.Listen("tcp", "127.0.0.1:1145")
				if err != nil {
					t.Fatal(err)
				}
				defer listener.Close()
			}
			calls := 0
			readyError := errors.New("ready writer failed")
			err := app.Run(ctx, RuntimeOptions{Database: db, Log: &logger.Logger{}, Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), Port: testRuntimePort, OnReady: func() error { calls++; return readyError }})
			if kind == "ready-failed" && (!errors.Is(err, readyError) || calls != 1) {
				t.Fatal(calls, err)
			}
			if kind != "ready-failed" && calls != 0 {
				t.Fatal("失败启动输出 ready", kind)
			}
			if kind == "canceled" && err != nil {
				t.Fatal("取消不是正常退出", err)
			}
			if kind != "canceled" && err == nil {
				t.Fatal("启动失败被吞掉", kind)
			}
			if kind == "occupied" && !strings.Contains(err.Error(), "127.0.0.1:1145") {
				t.Fatal("监听错误未包含实际端口", err)
			}
			select {
			case <-db.closed:
			default:
				t.Fatal("数据库没有关闭")
			}
			if db.path != filepath.Join(dir, "db", "timecue.db") {
				t.Fatal("数据库相对路径基准错误", db.path)
			}
			if _, err := os.Stat(filepath.Join(dir, "log", "timecue.jsonl")); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestRuntimeShutdownTimeoutCancelsConnection 聊天与 HTTP 共用期限，超时后仍等待已接收业务再关闭数据库。
func TestRuntimeShutdownTimeoutCancelsConnection(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:1145")
	if err != nil {
		t.Skip("固定端口已占用")
	}
	_ = probe.Close()
	app, dir := runtimeTestApp(t)
	app.ChatRuntime = &deadlineChatShutdown{}
	db := &drainRuntimeDB{closed: make(chan struct{})}
	ready, entered, connectionCanceled := make(chan struct{}), make(chan struct{}), make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	handler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		close(connectionCanceled)
		<-release
	})
	go func() {
		finished <- app.Run(ctx, RuntimeOptions{Database: db, Handler: handler, Log: &logger.Logger{}, Port: testRuntimePort, OnReady: func() error { close(ready); return nil }})
	}()
	select {
	case <-ready:
	case err := <-finished:
		t.Fatal(err)
	case <-time.After(3 * time.Second):
		t.Fatal("没有 ready")
	}
	clientDone := make(chan struct{})
	go func() {
		defer close(clientDone)
		client := &http.Client{Timeout: 9 * time.Second}
		resp, _ := client.Get("http://127.0.0.1:1145/hold")
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("请求未进入")
	}
	cancel()
	select {
	case <-connectionCanceled:
	case <-time.After(7 * time.Second):
		t.Fatal("超时未关闭连接并取消请求")
	}
	select {
	case <-db.closed:
		t.Fatal("仍有业务使用数据库却提前关闭")
	case <-finished:
		t.Fatal("伪装业务已经退出")
	default:
	}
	unblock()
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	<-clientDone
	log, err := os.ReadFile(filepath.Join(dir, "log", "timecue.jsonl"))
	if err != nil || !strings.Contains(string(log), "HTTP shutdown exceeded graceful window") {
		t.Fatal("缺少退出超时诊断", string(log), err)
	}
}

type heldRuntimeRecovery struct {
	emptyRecoveryRepo
	entered, canceled, release chan struct{}
}

func (r heldRuntimeRecovery) RecoveryStart(ctx context.Context, _ time.Time) (time.Time, error) {
	close(r.entered)
	<-ctx.Done()
	close(r.canceled)
	<-r.release
	return time.Time{}, ctx.Err()
}

// TestRuntimeReadyDoesNotWaitForRecovery 就绪不等待历史整理，退出仍等待恢复不再使用数据库。
func TestRuntimeReadyDoesNotWaitForRecovery(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:1145")
	if err != nil {
		t.Skip("固定端口已占用")
	}
	_ = probe.Close()
	app, _ := runtimeTestApp(t)
	repo := heldRuntimeRecovery{entered: make(chan struct{}), canceled: make(chan struct{}), release: make(chan struct{})}
	var once sync.Once
	release := func() { once.Do(func() { close(repo.release) }) }
	defer release()
	app.Recovery, _ = recovery.NewService(repo, notification.SchedulerOptions{})
	app.Delivery, _ = notification.NewDeliveryService(deliveryTestRepo{}, nil)
	db := &drainRuntimeDB{closed: make(chan struct{})}
	ready, finished := make(chan struct{}), make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		finished <- app.Run(ctx, RuntimeOptions{Database: db, Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), Log: &logger.Logger{}, Port: testRuntimePort, OnReady: func() error { close(ready); return nil }})
	}()
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("ready 等待了历史恢复")
	}
	select {
	case <-repo.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("恢复未接入")
	}
	cancel()
	select {
	case <-repo.canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("恢复未协作取消")
	}
	select {
	case <-db.closed:
		t.Fatal("恢复未结束就关闭数据库")
	default:
	}
	release()
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	select {
	case <-db.closed:
	default:
		t.Fatal("恢复结束后数据库未关闭")
	}
}

type drainRuntimeDB struct{ closed chan struct{} }

func (d *drainRuntimeDB) Connect(context.Context, string) error { return nil }
func (d *drainRuntimeDB) CheckHealth(context.Context) error     { return nil }
func (d *drainRuntimeDB) Close() error                          { close(d.closed); return nil }

type drainSchedulerRepo struct{}

func (drainSchedulerRepo) PrepareNotifications(context.Context, model.NotificationWindow, model.PrepareNotificationWindow) (*model.NotificationBatch, error) {
	return &model.NotificationBatch{}, nil
}

type drainDeliveryRepo struct{ deliveryTestRepo }

func (drainDeliveryRepo) SaveDeliveryResult(ctx context.Context, _ []int, _ model.NotifyStatus) error {
	return ctx.Err()
}

type deliveryTestRepo struct{}

func (deliveryTestRepo) ReadDeliveryMembers(_ context.Context, ids []int, at time.Time, _ bool) ([]*model.Notification, error) {
	name := "测试成员"
	values := make([]*model.Notification, 0, len(ids))
	for _, id := range ids {
		values = append(values, &model.Notification{ID: id, Status: model.NotifyLoaded, NotifyTime: at, SnapshotTaken: true, NameSnapshot: &name})
	}
	return values, nil
}
func (deliveryTestRepo) SaveDeliveryResult(context.Context, []int, model.NotifyStatus) error {
	return nil
}

type dueSchedulerRepo struct{ at time.Time }

func (r dueSchedulerRepo) PrepareNotifications(_ context.Context, w model.NotificationWindow, _ model.PrepareNotificationWindow) (*model.NotificationBatch, error) {
	if !w.FreezeDue {
		return &model.NotificationBatch{}, nil
	}
	name := "测试成员"
	n := &model.Notification{ID: 1, ScheduleID: 1, Status: model.NotifyLoaded, NotifyTime: r.at, SnapshotTaken: true, NameSnapshot: &name}
	return &model.NotificationBatch{Notifications: []*model.Notification{n}, EligibleIDs: []int{1}}, nil
}

// TestAppRuntimeDrainsAcceptedResults 验证取消后台工作不取消已接收的平台结果请求。
func TestAppRuntimeDrainsAcceptedResults(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:1145")
	if err != nil {
		t.Skip("本机业务端口正在使用，生命周期集成检查需空闲端口")
	}
	_ = probe.Close()
	t.Chdir(t.TempDir())
	manager, _ := config.NewManager()
	now := time.Now().Truncate(time.Minute)
	clock := func() time.Time { return now }
	delivery, _ := notification.NewDeliveryService(drainDeliveryRepo{}, clock)
	scheduler, _ := notification.NewScheduler(dueSchedulerRepo{at: now}, notification.SchedulerOptions{Now: clock})
	if err := scheduler.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	recovery, _ := recovery.NewService(emptyRecoveryRepo{}, notification.SchedulerOptions{Now: clock})
	app := &App{Config: manager, Delivery: delivery, Scheduler: scheduler, Recovery: recovery}
	claims, err := app.ClaimDelivery(context.Background(), 20)
	if err != nil || len(claims) != 1 {
		t.Fatal(claims, err)
	}
	if _, err := app.StartDelivery(context.Background(), claims[0].AttemptID); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan context.Context, 1), make(chan struct{})
	var releaseOnce sync.Once
	releaseRequest := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseRequest()
	db := &drainRuntimeDB{closed: make(chan struct{})}
	handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		entered <- req.Context()
		<-release
		if _, err := app.CompleteDelivery(req.Context(), claims[0].AttemptID, model.NotifySent); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.WriteHeader(200)
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		finished <- app.Run(ctx, RuntimeOptions{Database: db, Handler: handler, Log: &logger.Logger{}, Port: testRuntimePort})
	}()
	response := make(chan error, 1)
	go func() {
		client := &http.Client{Timeout: 3 * time.Second}
		deadline := time.Now().Add(3 * time.Second)
		for {
			r, err := client.Get("http://127.0.0.1:1145/hold")
			if err == nil {
				_ = r.Body.Close()
				if r.StatusCode != 200 {
					err = fmt.Errorf("result response: %d", r.StatusCode)
				}
				response <- err
				return
			}
			if time.Now().After(deadline) {
				response <- err
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	var requestContext context.Context
	select {
	case requestContext = <-entered:
	case err := <-finished:
		t.Fatal("runtime stopped before request", err)
	case <-time.After(4 * time.Second):
		t.Fatal("request was not accepted")
	}
	cancel()
	if requestContext.Err() != nil {
		t.Fatal("后台取消同时取消了已接收回填", requestContext.Err())
	}
	select {
	case <-db.closed:
		t.Fatal("请求未结束即关闭数据库")
	default:
	}
	releaseRequest()
	if err := <-response; err != nil {
		t.Fatal("优雅窗口无法回填", err)
	}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	select {
	case <-db.closed:
	default:
		t.Fatal("运行结束未关闭数据库")
	}
}
