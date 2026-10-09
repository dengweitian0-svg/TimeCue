package service

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/config"
	logger "github.com/dengweitian0-svg/TimeCue/internal/log"
	"github.com/dengweitian0-svg/TimeCue/internal/model"
)

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

// TestAppRuntimeDrainsAcceptedResults 验证取消后台工作不取消已接收的平台结果请求。
func TestAppRuntimeDrainsAcceptedResults(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:10086")
	if err != nil {
		t.Skip("本机业务端口正在使用，生命周期集成检查需空闲端口")
	}
	_ = probe.Close()
	t.Chdir(t.TempDir())
	manager, _ := config.NewManager()
	now := time.Now().Truncate(time.Minute)
	clock := func() time.Time { return now }
	delivery, _ := NewDeliveryService(drainDeliveryRepo{}, clock)
	scheduler, _ := NewScheduler(drainSchedulerRepo{}, SchedulerOptions{Now: clock})
	scheduler.groups[now.UnixNano()] = MinuteGroup{Minute: now, FactIDs: []int{1}, MemberIDs: []int{1}}
	recovery, _ := NewRecoveryService(emptyRecoveryRepo{}, SchedulerOptions{Now: clock})
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
		finished <- app.Run(ctx, RuntimeOptions{Database: db, Handler: handler, Log: &logger.Logger{}})
	}()
	response := make(chan error, 1)
	go func() {
		client := &http.Client{Timeout: 3 * time.Second}
		deadline := time.Now().Add(3 * time.Second)
		for {
			r, err := client.Get("http://127.0.0.1:10086/hold")
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
