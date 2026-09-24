package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	_ "modernc.org/sqlite"
)

const LimitMax = 50

var (
	ErrNotConnected        = errors.New("database is not connected")
	ErrAlreadyConnected    = errors.New("database is already connected")
	ErrNotFound            = errors.New("record not found")
	ErrScheduleDeleted     = errors.New("schedule is deleted")
	ErrScheduleCancelled   = errors.New("schedule is cancelled")
	ErrSchedulePaused      = errors.New("schedule is paused")
	ErrNotificationDeleted = errors.New("notification is deleted")
	ErrInvalidTransition   = errors.New("invalid state transition")
)

// DB 定义数据库连接的生命周期操作。
type DB interface {
	// Connect 连接数据库；建表由独立迁移负责。
	Connect(ctx context.Context, path string) error
	// Close 关闭数据库连接。
	Close() error
}

// Sqlite 保存单个 SQLite 连接池及其生命周期状态。
type Sqlite struct {
	mu sync.RWMutex
	db *sql.DB
}

// NewSqlite 创建 SQLite 连接管理器。
func NewSqlite() *Sqlite {
	return &Sqlite{}
}

// Connect 打开数据库并启用外键约束；不会执行数据库迁移。
func (s *Sqlite) Connect(ctx context.Context, path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db != nil {
		return fmt.Errorf("[store] cannot connect database, path=%q: %w", path, ErrAlreadyConnected)
	}
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("[store] database path must not be empty, path=%q", path)
	}
	if path != ":memory:" && !strings.HasPrefix(path, "file:") {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("[store] failed to create database directory, path=%q: %w", path, err)
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("[store] failed to open database, path=%q: %w", path, err)
	}
	// 单连接保证连接级 PRAGMA 在所有存储操作中生效。
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return fmt.Errorf("[store] failed to connect database, path=%q: %w", path, err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		_ = db.Close()
		return fmt.Errorf("[store] failed to enable foreign keys, path=%q: %w", path, err)
	}
	s.db = db
	return nil
}

// Close 关闭已经连接的数据库。
func (s *Sqlite) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return fmt.Errorf("[store] cannot close database: %w", ErrNotConnected)
	}
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("[store] failed to close database: %w", err)
	}
	s.db = nil
	return nil
}

func (s *Sqlite) handle() (*sql.DB, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return nil, ErrNotConnected
	}
	return s.db, nil
}
