package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/loop"
	"github.com/dengweitian0-svg/TimeCue/internal/api"
	"github.com/dengweitian0-svg/TimeCue/internal/app"
	"github.com/dengweitian0-svg/TimeCue/internal/config"
	logger "github.com/dengweitian0-svg/TimeCue/internal/log"
	"github.com/dengweitian0-svg/TimeCue/internal/model"
	"github.com/dengweitian0-svg/TimeCue/internal/store"
)

type agentLoop interface {
	api.ChatService
	Activate(context.Context) error
	Serve(context.Context) error
	Shutdown(context.Context)
}

type host struct {
	manager      *config.Manager
	db           *store.Sqlite
	log          *logger.Logger
	port         int
	info         model.RuntimeInfo
	onReady      func() error
	newApp       func() (*app.App, error)
	newLoop      func(context.Context, *app.App) (agentLoop, error)
	loopStartup  time.Duration
	httpShutdown time.Duration
	loopShutdown time.Duration
	appShutdown  time.Duration
}

func newHost(options startupOptions, log *logger.Logger) (*host, error) {
	if err := os.MkdirAll(options.dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	manager, err := config.NewManager(options.dataDir)
	if err != nil {
		return nil, err
	}
	db := store.NewSqlite()
	h := &host{
		manager:      manager,
		db:           db,
		log:          log,
		port:         options.port,
		info:         newRuntimeInfo(options.instanceID),
		loopStartup:  8 * time.Second,
		httpShutdown: 5 * time.Second,
		loopShutdown: 30 * time.Second,
		appShutdown:  30 * time.Second,
	}
	h.newApp = func() (*app.App, error) {
		schedules, err := store.NewScheduleStore(db)
		if err != nil {
			return nil, err
		}
		notifications, err := store.NewNotificationStore(db)
		if err != nil {
			return nil, err
		}
		return app.NewApp(schedules, notifications)
	}
	h.newLoop = func(ctx context.Context, application *app.App) (agentLoop, error) {
		service, err := loop.New(ctx, store.NewChatStorage(db), manager.ChatModels, application)
		if err != nil {
			return nil, err
		}
		return service, nil
	}
	if options.managed {
		h.onReady = func() error { return writeReady(os.Stdout, h.info, h.port) }
	}
	return h, nil
}

func newRuntimeInfo(instanceID string) model.RuntimeInfo {
	return model.RuntimeInfo{
		InstanceID:   instanceID,
		PID:          os.Getpid(),
		APIVersion:   "v1",
		Capabilities: []string{"delivery-v1", "recovery-v1", "task-members-v1", "managed-lifecycle-v1"},
	}
}

func (h *host) address() string { return fmt.Sprintf("127.0.0.1:%d", h.port) }

func (h *host) run(parent context.Context, started time.Time) error {
	if parent.Err() != nil {
		return nil
	}
	if err := h.manager.LoadConfig(); err != nil {
		h.manager.Close()
		return fmt.Errorf("load config: %w", err)
	}
	var logFile *os.File
	defer func() {
		h.manager.Close()
		if logFile != nil {
			if err := logFile.Close(); err != nil {
				fmt.Fprintf(os.Stderr, "close log file: %v\n", err)
			}
		}
	}()
	cfg, err := h.manager.CurrentConfig()
	if err != nil {
		return fmt.Errorf("get config: %w", err)
	}
	logFile, err = openLogFile(h.manager.ResolvePath(cfg.Log.LogPath))
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	h.log.Upgrade(logFile)
	if err := h.db.Connect(parent, h.manager.ResolveDatabasePath(cfg.DB.DBPath)); err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	defer func() {
		if err := h.db.Close(); err != nil {
			h.log.Error("[host] failed to close database", "error", err)
		}
	}()
	if err := h.db.PrepareSchema(parent); err != nil {
		return fmt.Errorf("prepare database schema: %w", err)
	}
	if err := h.db.CheckHealth(parent); err != nil {
		return fmt.Errorf("check database schema: %w", err)
	}
	if parent.Err() != nil {
		return nil
	}
	listener, err := net.Listen("tcp", h.address())
	if err != nil {
		return fmt.Errorf("listen on %s: %w", h.address(), err)
	}
	defer listener.Close()
	return h.serve(parent, listener, started.Add(h.loopStartup))
}

func (h *host) serve(parent context.Context, listener net.Listener, loopDeadline time.Time) (result error) {
	ctx, stop := context.WithCancelCause(parent)
	appReady := make(chan *app.App)
	appDone := make(chan struct{})
	loopDone := make(chan struct{})
	go func() {
		defer close(appDone)
		application, err := h.newApp()
		if err == nil {
			err = application.Run(ctx, app.Lifecycle{
				Ready:           appReady,
				LoopDone:        loopDone,
				Fail:            func(err error) { stop(fmt.Errorf("app: %w", err)) },
				ShutdownBudget:  h.appShutdown,
				BackgroundError: func(err error) { h.log.Error("[app] background operation failed", "error", err) },
			})
		}
		if err != nil {
			stop(fmt.Errorf("app: %w", err))
		}
	}()
	defer func() {
		stop(result)
		_ = listener.Close()
		<-appDone
		<-loopDone
		if cause := context.Cause(ctx); cause != nil && cause != context.Cause(parent) && result == nil {
			result = cause
		}
	}()

	var application *app.App
	select {
	case <-ctx.Done():
		close(loopDone)
		return nil
	case application = <-appReady:
	}
	initCtx, cancelInit := context.WithDeadline(ctx, loopDeadline)
	defer cancelInit()
	var service agentLoop
	if initCtx.Err() == nil && time.Now().Before(loopDeadline) {
		var err error
		service, err = h.startLoop(ctx, initCtx, stop, loopDone, application)
		cancelInit()
		if err != nil && ctx.Err() == nil {
			h.log.Warn("[host] Agent Loop unavailable; serving App only", "error", err)
		}
	} else {
		close(loopDone)
		if ctx.Err() == nil {
			h.log.Warn("[host] Agent Loop skipped; startup deadline elapsed")
		}
	}
	if ctx.Err() != nil {
		return nil
	}
	handler := api.NewRouterWithPort(application, service, h.db.CheckHealth, h.port, h.info)
	return h.serveHTTP(ctx, stop, listener, handler)
}

// startLoop 仅在期限内接纳 LoopReady；迟到结果由原 goroutine 清理。
func (h *host) startLoop(ctx, initCtx context.Context, stop context.CancelCauseFunc, done chan<- struct{}, application *app.App) (agentLoop, error) {
	type loopReady struct {
		service agentLoop
		err     error
	}
	ready := make(chan loopReady)
	accept := make(chan bool)
	activated := make(chan error, 1)
	go func() {
		defer close(done)
		service, err := h.newLoop(initCtx, application)
		select {
		case ready <- loopReady{
			service: service,
			err:     err,
		}:
			if <-accept {
				err = service.Activate(ctx)
				activated <- err
				if err == nil {
					if err := service.Serve(ctx); ctx.Err() == nil {
						if err == nil {
							err = fmt.Errorf("service stopped unexpectedly")
						}
						stop(fmt.Errorf("agent loop: %w", err))
					}
					shutdown, cancel := context.WithTimeout(context.Background(), h.loopShutdown)
					defer cancel()
					service.Shutdown(shutdown)
					return
				}
			}
		case <-initCtx.Done():
		}
		if service != nil {
			service.Shutdown(initCtx)
		}
	}()
	select {
	case <-initCtx.Done():
		return nil, initCtx.Err()
	case result := <-ready:
		err := initCtx.Err()
		deadline, _ := initCtx.Deadline()
		if err == nil && !time.Now().Before(deadline) {
			err = context.DeadlineExceeded
		}
		accept <- (err == nil && result.err == nil)
		if err != nil {
			return nil, err
		}
		if result.err != nil {
			return nil, result.err
		}
		if err := <-activated; err != nil {
			return nil, err
		}
		return result.service, nil
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
