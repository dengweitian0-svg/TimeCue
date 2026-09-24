package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dengweitian0-svg/TimeCue/internal/config"
	logger "github.com/dengweitian0-svg/TimeCue/internal/log"
	"github.com/dengweitian0-svg/TimeCue/internal/service"
	"github.com/dengweitian0-svg/TimeCue/internal/store"
)

func main() {
	log := logger.Default()
	log.Info("[main] Starting TimeCue...")
	app, cleanup, err := initialize(context.Background(), log)
	defer cleanup()
	if err != nil {
		panic(err.Error())
	}
	_ = app
	log.Info("[main] app inited")
}

func initialize(ctx context.Context, log *logger.Logger) (*service.App, func(), error) {
	cleanup := func() {}
	manager, err := config.NewManager()
	if err != nil {
		return nil, cleanup, fmt.Errorf("create config manager: %w", err)
	}
	if err := manager.LoadConfig(); err != nil {
		return nil, cleanup, fmt.Errorf("load config: %w", err)
	}
	config, err := manager.CurrentConfig()
	if err != nil {
		return nil, cleanup, fmt.Errorf("get config: %w", err)
	}
	logFile, err := openLogFile(config.Log.LogPath)
	if err != nil {
		return nil, cleanup, fmt.Errorf("open log file: %w", err)
	}
	cleanup = func() { closeLogFile(logFile) }
	log.Upgrade(logFile)

	db := store.NewSqlite()
	if err := db.Connect(ctx, config.DB.DBPath); err != nil {
		return nil, cleanup, fmt.Errorf("connect database: %w", err)
	}
	cleanup = func() {
		if err := db.Close(); err != nil {
			log.Error("[main] Failed to close database", "error", err)
		}
		closeLogFile(logFile)
	}
	log.Info("[main] sqlite inited")
	scheduleRepo, err := store.NewScheduleStore(db)
	if err != nil {
		return nil, cleanup, fmt.Errorf("create schedule repo: %w", err)
	}
	log.Info("[main] scheduleRepo inited")
	notifyRepo, err := store.NewNotificationStore(db)
	if err != nil {
		return nil, cleanup, fmt.Errorf("create notification repo: %w", err)
	}
	log.Info("[main] notifyRepo inited")
	app, err := service.NewApp(manager, scheduleRepo, notifyRepo)
	if err != nil {
		return nil, cleanup, fmt.Errorf("create app: %w", err)
	}
	return app, cleanup, nil
}

func openLogFile(directory string) (*os.File, error) {
	if directory == "" {
		return nil, fmt.Errorf("[main] log.log_path is empty")
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return nil, fmt.Errorf("[main] create log path error. path: %q, err: %w", directory, err)
	}
	path := filepath.Join(directory, "timecue.jsonl")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("[main] open log file error. path: %q, err: %w", path, err)
	}
	return file, nil
}

func closeLogFile(file *os.File) {
	if err := file.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "[main] Failed to close log file: %v\n", err)
	}
}
