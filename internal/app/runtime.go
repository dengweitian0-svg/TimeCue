package app

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

	logger "github.com/dengweitian0-svg/TimeCue/internal/log"
	"github.com/dengweitian0-svg/TimeCue/internal/service/notification"
	"github.com/dengweitian0-svg/TimeCue/internal/service/recovery"
)

// Run 管理配置、日志、数据库、HTTP 与调度生命周期，退出后才释放依赖资源。
func (a *App) Run(ctx context.Context, options RuntimeOptions) error {
	if a.Config == nil || options.Database == nil || options.Handler == nil {
		return fmt.Errorf("[app] runtime configuration, database and handler are required")
	}
	if ctx.Err() != nil {
		return nil
	}
	defer a.Config.Close()
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
	logFile, err := openLogFile(a.Config.ResolvePath(cfg.Log.LogPath))
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	defer func() {
		a.Config.Close()
		if err := logFile.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "close log file: %v\n", err)
		}
	}()
	log.Upgrade(logFile)
	if err = options.Database.Connect(ctx, a.Config.ResolveDatabasePath(cfg.DB.DBPath)); err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	defer func() {
		if err := options.Database.Close(); err != nil {
			log.Error("[app] failed to close database", "error", err)
		}
	}()
	if options.PrepareDatabase != nil {
		if err = options.PrepareDatabase(ctx); err != nil {
			return fmt.Errorf("prepare database schema: %w", err)
		}
	}
	if err = options.Database.CheckHealth(ctx); err != nil {
		return fmt.Errorf("check database schema: %w", err)
	}
	if ctx.Err() != nil {
		return nil
	}
	if a.Scheduler == nil {
		repo, ok := a.Notify.(notification.NotificationSchedulerRepo)
		if !ok {
			return fmt.Errorf("[app] notification repo does not support scheduling")
		}
		a.Scheduler, err = notification.NewScheduler(repo, notification.SchedulerOptions{OnError: func(err error) { log.Error("[scheduler] background operation failed", "error", err) }})
		if err != nil {
			return err
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:10086")
	if err != nil {
		return fmt.Errorf("listen on 127.0.0.1:10086: %w", err)
	}
	if a.Recovery == nil {
		repo, ok := a.Notify.(recovery.Repo)
		if !ok {
			_ = listener.Close()
			return fmt.Errorf("[app] notification repo does not support recovery")
		}
		a.Recovery, err = recovery.NewService(repo, notification.SchedulerOptions{OnError: func(err error) { log.Error("[recovery] background operation failed", "error", err) }})
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
			log.Error("[app] HTTP shutdown exceeded graceful window", "error", err)
			_ = server.Close()
		}
		requests.active.Wait()
		<-schedulerDone
		<-recoveryDone
	}()
	serveErrors := make(chan error, 1)
	serving := &readyListener{Listener: listener, started: make(chan struct{})}
	go func() { serveErrors <- server.Serve(serving) }()
	select {
	case <-ctx.Done():
		return nil
	case err := <-serveErrors:
		return serveError(err)
	case <-serving.started:
	}
	// Serve 已进入接收循环；再次检查故障和取消，避免为失败的启动发送握手。
	select {
	case err := <-serveErrors:
		return serveError(err)
	default:
	}
	if ctx.Err() != nil {
		return nil
	}
	if options.OnReady != nil {
		if err := options.OnReady(); err != nil {
			return fmt.Errorf("announce ready: %w", err)
		}
	}
	log.Info("[app] HTTP server listening", "address", listener.Addr().String())
	select {
	case <-ctx.Done():
		log.Info("[app] runtime stop requested", "reason", ctx.Err())
		return nil
	case err := <-serveErrors:
		return serveError(err)
	}
}

func serveError(err error) error {
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve HTTP: %w", err)
	}
	return nil
}

// readyListener 以 Serve 的首次接收调用确认循环接入，不以固定等待推测就绪。
type readyListener struct {
	net.Listener
	started chan struct{}
	once    sync.Once
}

// Accept 在首次等待连接时通知 HTTP 服务已进入接收循环。
func (l *readyListener) Accept() (net.Conn, error) {
	l.once.Do(func() { close(l.started) })
	return l.Listener.Accept()
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

// ServeHTTP 跟踪正在处理的请求，并在停止接入后拒绝新请求。
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
