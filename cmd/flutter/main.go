package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/api"
	"github.com/dengweitian0-svg/TimeCue/internal/config"
	logger "github.com/dengweitian0-svg/TimeCue/internal/log"
	"github.com/dengweitian0-svg/TimeCue/internal/service"
	"github.com/dengweitian0-svg/TimeCue/internal/store"
)

func main() {
	log := logger.Default()
	log.Info("[main] Starting TimeCue...")
	if err := run(log); err != nil {
		log.Error("[main] server stopped", "error", err)
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(log *logger.Logger) error {
	app, db, cleanup, err := initialize(context.Background(), log)
	defer cleanup()
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:10086")
	if err != nil {
		return fmt.Errorf("listen on 127.0.0.1:10086: %w", err)
	}
	server := &http.Server{
		Handler:           api.NewRouter(app, db.CheckHealth),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- server.Serve(listener) }()
	log.Info("[main] HTTP server listening", "address", "127.0.0.1:10086")
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	select {
	case err := <-serveErrors:
		if err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("serve HTTP: %w", err)
		}
	case <-signals:
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			_ = server.Close()
			return fmt.Errorf("shut down HTTP: %w", err)
		}
		if err := <-serveErrors; err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("serve HTTP: %w", err)
		}
	}
	return nil
}

func initialize(ctx context.Context, log *logger.Logger) (*service.App, *store.Sqlite, func(), error) {
	cleanup := func() {}
	manager, err := config.NewManager()
	if err != nil {
		return nil, nil, cleanup, fmt.Errorf("create config manager: %w", err)
	}
	if err := manager.LoadConfig(); err != nil {
		return nil, nil, cleanup, fmt.Errorf("load config: %w", err)
	}
	config, err := manager.CurrentConfig()
	if err != nil {
		return nil, nil, cleanup, fmt.Errorf("get config: %w", err)
	}
	logFile, err := openLogFile(config.Log.LogPath)
	if err != nil {
		return nil, nil, cleanup, fmt.Errorf("open log file: %w", err)
	}
	cleanup = func() { closeLogFile(logFile) }
	log.Upgrade(logFile)

	db := store.NewSqlite()
	if err := db.Connect(ctx, config.DB.DBPath); err != nil {
		return nil, nil, cleanup, fmt.Errorf("connect database: %w", err)
	}
	cleanup = func() {
		if err := db.Close(); err != nil {
			log.Error("[main] Failed to close database", "error", err)
		}
		closeLogFile(logFile)
	}
	log.Info("[main] sqlite inited")
	if err := db.CheckHealth(ctx); err != nil {
		return nil, nil, cleanup, fmt.Errorf("check database schema: %w", err)
	}
	scheduleRepo, err := store.NewScheduleStore(db)
	if err != nil {
		return nil, nil, cleanup, fmt.Errorf("create schedule repo: %w", err)
	}
	log.Info("[main] scheduleRepo inited")
	notifyRepo, err := store.NewNotificationStore(db)
	if err != nil {
		return nil, nil, cleanup, fmt.Errorf("create notification repo: %w", err)
	}
	log.Info("[main] notifyRepo inited")
	app, err := service.NewApp(manager, scheduleRepo, notifyRepo)
	if err != nil {
		return nil, nil, cleanup, fmt.Errorf("create app: %w", err)
	}
	return app, db, cleanup, nil
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
	if file == nil {
		return
	}
	if err := file.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "[main] Failed to close log file: %v\n", err)
	}
}
