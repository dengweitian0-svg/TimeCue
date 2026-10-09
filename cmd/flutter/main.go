package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/dengweitian0-svg/TimeCue/internal/api"
	"github.com/dengweitian0-svg/TimeCue/internal/config"
	logger "github.com/dengweitian0-svg/TimeCue/internal/log"
	"github.com/dengweitian0-svg/TimeCue/internal/service"
	"github.com/dengweitian0-svg/TimeCue/internal/store"
)

func main() {
	log := logger.Default()
	if err := run(log); err != nil {
		log.Error("[main] server stopped", "error", err)
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run 只装配适配器并转交生命周期，启动和清理由 service.App 管理。
func run(log *logger.Logger) error {
	manager, err := config.NewManager()
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
	app, err := service.NewApp(manager, schedules, notifications)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return app.Run(ctx, service.RuntimeOptions{Database: db, Handler: api.NewRouter(app, db.CheckHealth), Log: log})
}
