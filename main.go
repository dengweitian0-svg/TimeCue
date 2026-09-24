package main

import (
	"context"

	"github.com/dengweitian0-svg/TimeCue/internal/config"
	logger "github.com/dengweitian0-svg/TimeCue/internal/log"
	"github.com/dengweitian0-svg/TimeCue/internal/service"
	"github.com/dengweitian0-svg/TimeCue/internal/store"
)

func main() {
	logger := logger.New()
	logger.Info("[main] Starting TimeCue...")
	// 其他初始化和启动逻辑
	manager, err := config.NewManager()
	if err != nil {
		logger.Error("[main] Failed to create config manager", "error", err)
		return
	}
	if err := manager.LoadConfig(); err != nil {
		logger.Error("[main] Failed to load config", "error", err)
		return
	}
	ctx := context.Background()
	config, err := manager.CurrentConfig()
	if err != nil {
		logger.Error("[main] Failed to get config", "error", err)
		return
	}
	db := store.NewSqlite()
	if err := db.Connect(ctx, config.DB.DBPath); err != nil {
		logger.Error("[main] Failed to connect database", "error", err)
		return
	}
	defer func() {
		if err := db.Close(); err != nil {
			logger.Error("[main] Failed to close database", "error", err)
		}
	}()
	scheduleRepo, err := store.NewScheduleStore(db)
	if err != nil {
		logger.Error("[main] Failed to create schedule repo", "error", err)
		return
	}
	notifyRepo, err := store.NewNotificationStore(db)
	if err != nil {
		logger.Error("[main] Failed to create notification repo", "error", err)
		return
	}
	app, err := service.NewApp(manager, scheduleRepo, notifyRepo)
	if err != nil {
		logger.Error("[main] Failed to new app", "error", err)
		return
	}
	_ = app
}
