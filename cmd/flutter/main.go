package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/api"
	"github.com/dengweitian0-svg/TimeCue/internal/app"
	"github.com/dengweitian0-svg/TimeCue/internal/config"
	logger "github.com/dengweitian0-svg/TimeCue/internal/log"
	"github.com/dengweitian0-svg/TimeCue/internal/store"
	"github.com/gin-gonic/gin"
)

func main() {
	log := logger.Default()
	if err := run(log); err != nil {
		log.Error("[main] server stopped", "error", err)
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run 只装配适配器并转交生命周期，启动和清理由 app.App 管理。
func run(log *logger.Logger) error {
	options, err := parseStartup(os.Args[1:])
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var control *parentControl
	if options.managed {
		gin.SetMode(gin.ReleaseMode)
		control = startParentControl(os.Stdin, cancel)
		defer control.closeAndWait()
	}
	err = runApp(ctx, options, log)
	if control != nil {
		control.closeAndWait()
		if failure := control.err(); failure != nil {
			return failure
		}
	}
	if ctx.Err() != nil && errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// runApp 沿用 App.Run 的资源所有权，控制管道只请求取消。
func runApp(ctx context.Context, options startupOptions, log *logger.Logger) error {
	if err := os.MkdirAll(options.dataDir, 0o755); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	manager, err := config.NewManager(options.dataDir)
	if err != nil {
		return err
	}
	db := store.NewSqlite()
	schedules, err := store.NewScheduleStore(db)
	if err != nil {
		return err
	}
	notifications, err := store.NewNotificationStore(db)
	if err != nil {
		return err
	}
	application, err := app.NewApp(manager, schedules, notifications)
	if err != nil {
		return err
	}
	// TODO: 接入真实聊天应用服务后，替换临时 Mock。
	application.ChatService = chat.NewMock()
	info := app.NewRuntimeInfo(options.instanceID)
	runtime := app.RuntimeOptions{Database: db, PrepareDatabase: db.PrepareSchema, Handler: api.NewRouter(application, db.CheckHealth, info), Log: log}
	if options.managed {
		runtime.OnReady = func() error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return writeReady(os.Stdout, info)
		}
	}
	return application.Run(ctx, runtime)
}
