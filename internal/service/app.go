package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/config"
	logger "github.com/dengweitian0-svg/TimeCue/internal/log"
	"github.com/dengweitian0-svg/TimeCue/internal/model"
)

// ErrNotPeriodSchedule 表示提醒所属日程不是可推进的周期日程。
var ErrNotPeriodSchedule = errors.New("schedule is not periodic")

// ErrInvalidSchedule 表示创建请求中的日程规则无效。
var ErrInvalidSchedule = errors.New("invalid schedule")

// ErrScheduleNotFound 表示日程不存在或已删除。
var ErrScheduleNotFound = errors.New("schedule not found")

// App 统筹所有服务
type App struct {
	Schedule   ScheduleRepo
	Notify     NotifyRepo
	NextNotify NextNotify
	Config     *config.Manager
	Scheduler  *Scheduler
	Delivery   *DeliveryService
	Recovery   *RecoveryService
	operations sync.Mutex // 正式写入、开始冻结和回填共用操作边界，避免提交后回调竞争。
}

// NewApp 使用依赖注入的配置与 Repo 接口组装应用服务。
func NewApp(manager *config.Manager, schedule ScheduleRepo, notify NotifyRepo) (*App, error) {
	if manager == nil {
		return nil, fmt.Errorf("[app] config manager is nil")
	}
	if schedule == nil {
		return nil, fmt.Errorf("[app] schedule repo is nil")
	}
	if notify == nil {
		return nil, fmt.Errorf("[app] notification repo is nil")
	}
	nextNotify, err := NewNextNotifyService(schedule, notify)
	if err != nil {
		return nil, err
	}
	return &App{Schedule: schedule, Notify: notify, NextNotify: nextNotify, Config: manager}, nil
}

// CreateSchedule 校验规则，并原子创建日程和首条提醒。
func (a *App) CreateSchedule(ctx context.Context, schedule model.Schedule) (*model.Schedule, *model.Notification, error) {
	result, err := a.WriteSchedule(ctx, schedule, 0, "")
	if err != nil {
		return nil, nil, err
	}
	return result.Schedule, result.Notification, nil
}

// ListSchedules 查询未删除日程。
func (a *App) ListSchedules(ctx context.Context, options ...model.QueryOption) ([]*model.Schedule, error) {
	return a.Schedule.GetAllSchedule(ctx, options...)
}

// GetSchedule 查询一条未删除日程。
func (a *App) GetSchedule(ctx context.Context, id int) (*model.Schedule, error) {
	schedule, err := a.Schedule.GetScheduleByID(ctx, id)
	if errors.Is(err, model.ErrRecordNotFound) {
		return nil, ErrScheduleNotFound
	}
	return schedule, err
}

// DeleteSchedule 软删除一条日程。
func (a *App) DeleteSchedule(ctx context.Context, id int) error {
	_, err := a.DeleteScheduleWithResult(ctx, id)
	return err
}

// DeleteScheduleWithResult 返回正式删除事务的状态变化，供调用方观察。
func (a *App) DeleteScheduleWithResult(ctx context.Context, id int) (*model.ScheduleWrite, error) {
	a.operations.Lock()
	defer a.operations.Unlock()
	result, err := a.Schedule.DeleteScheduleByID(ctx, id)
	if errors.Is(err, model.ErrRecordNotFound) {
		return nil, ErrScheduleNotFound
	}
	if err == nil && a.Scheduler != nil {
		a.Scheduler.ScheduleChanged(ctx, result)
	}
	return result, err
}

// AdvanceNotification 只允许周期日程以已处理提醒的计划时间推进下一条提醒。
func (a *App) AdvanceNotification(ctx context.Context, notification model.Notification) (*model.Notification, error) {
	schedule, err := a.Schedule.GetScheduleByID(ctx, notification.ScheduleID)
	if err != nil {
		return nil, fmt.Errorf("[app] cannot read schedule for notification, notification_id=%d, schedule_id=%d: %w", notification.ID, notification.ScheduleID, err)
	}
	if schedule == nil {
		return nil, fmt.Errorf("[app] schedule repo returned nil for notification, notification_id=%d, schedule_id=%d", notification.ID, notification.ScheduleID)
	}
	if schedule.Type != model.PeriodSchedule {
		return nil, fmt.Errorf("[app] cannot advance notification, notification_id=%d, schedule_id=%d, type=%q: %w", notification.ID, notification.ScheduleID, schedule.Type, ErrNotPeriodSchedule)
	}
	return a.NextNotify.Next(ctx, notification.ScheduleID, notification.NotifyTime)
}

// RuntimeDatabase 是 App 运行时使用的连接端口，业务层不依赖具体 SQLite 实现。
type RuntimeDatabase interface {
	Connect(context.Context, string) error
	CheckHealth(context.Context) error
	Close() error
}

// RuntimeOptions 注入外部适配器；路由在入口装配，App 不解释 HTTP 路由。
type RuntimeOptions struct {
	Database RuntimeDatabase
	Handler  http.Handler
	Log      *logger.Logger
}

// Run 管理配置、日志、数据库、HTTP 与调度生命周期，退出后才释放依赖资源。
func (a *App) Run(ctx context.Context, options RuntimeOptions) error {
	if a.Config == nil || options.Database == nil || options.Handler == nil {
		return fmt.Errorf("[app] runtime configuration, database and handler are required")
	}
	log := options.Log
	if log == nil {
		log = logger.Default()
	}
	if err := a.Config.LoadConfig(); err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	cfg, err := a.Config.CurrentConfig()
	if err != nil {
		return fmt.Errorf("get config: %w", err)
	}
	logFile, err := openLogFile(cfg.Log.LogPath)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	defer func() {
		if err := logFile.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "close log file: %v\n", err)
		}
	}()
	log.Upgrade(logFile)
	if err = options.Database.Connect(ctx, cfg.DB.DBPath); err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	defer func() {
		if err := options.Database.Close(); err != nil {
			log.Error("[app] failed to close database", "error", err)
		}
	}()
	if err = options.Database.CheckHealth(ctx); err != nil {
		return fmt.Errorf("check database schema: %w", err)
	}
	if a.Scheduler == nil {
		repo, ok := a.Notify.(NotificationSchedulerRepo)
		if !ok {
			return fmt.Errorf("[app] notification repo does not support scheduling")
		}
		a.Scheduler, err = NewScheduler(repo, SchedulerOptions{OnError: func(err error) { log.Error("[scheduler] background operation failed", "error", err) }})
		if err != nil {
			return err
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:10086")
	if err != nil {
		return fmt.Errorf("listen on 127.0.0.1:10086: %w", err)
	}
	if a.Recovery == nil {
		repo, ok := a.Notify.(RecoveryRepo)
		if !ok {
			_ = listener.Close()
			return fmt.Errorf("[app] notification repo does not support recovery")
		}
		a.Recovery, err = NewRecoveryService(repo, SchedulerOptions{OnError: func(err error) { log.Error("[recovery] background operation failed", "error", err) }})
		if err != nil {
			_ = listener.Close()
			return err
		}
	}
	runCtx, stop := context.WithCancel(ctx)
	schedulerDone := make(chan error, 1)
	go func() { schedulerDone <- a.Scheduler.Run(runCtx) }()
	recoveryDone := make(chan error, 1)
	go func() { recoveryDone <- a.RunRecovery(runCtx) }()
	requests := &requestTracker{handler: options.Handler}
	// 请求上下文由 HTTP 连接管理；退出只取消后台工作，已接收回填保留优雅窗口。
	server := &http.Server{
		Handler:           requests,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20,
	}
	defer func() {
		stop()
		a.StopDelivery()
		requests.stop()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
		}
		requests.active.Wait()
		<-schedulerDone
		<-recoveryDone
	}()
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- server.Serve(listener) }()
	log.Info("[app] HTTP server listening", "address", listener.Addr().String())
	select {
	case <-ctx.Done():
		return nil
	case err := <-serveErrors:
		if err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("serve HTTP: %w", err)
		}
		return nil
	}
}

func openLogFile(directory string) (*os.File, error) {
	if directory == "" {
		return nil, fmt.Errorf("log.log_path is empty")
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return nil, err
	}
	return os.OpenFile(filepath.Join(directory, "timecue.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
}

// requestTracker 关闭请求入口后才允许等待，避免退出与新请求交错。
type requestTracker struct {
	handler http.Handler
	mu      sync.Mutex
	closing bool
	active  sync.WaitGroup
}

func (t *requestTracker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	t.mu.Lock()
	if t.closing {
		t.mu.Unlock()
		http.Error(w, "service stopping", http.StatusServiceUnavailable)
		return
	}
	t.active.Add(1)
	t.mu.Unlock()
	defer t.active.Done()
	t.handler.ServeHTTP(w, r)
}

func (t *requestTracker) stop() { t.mu.Lock(); t.closing = true; t.mu.Unlock() }
