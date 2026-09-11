package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	_ "modernc.org/sqlite"
)

// Store 保存 SQLite 数据库连接和存储资源。
type Store struct {
	db     *sql.DB
	logger *slog.Logger
}

// OpenStore 打开 SQLite 数据库并完成初始迁移。
func OpenStore(ctx context.Context, path string, logger *slog.Logger) (*Store, error) {
	if path == "" {
		return nil, errors.New("sqlite database path must not be empty")
	}
	if logger == nil {
		logger = slog.Default()
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	// 单连接同时简化 SQLite 的锁行为和本 Issue 中显式事务的可观察性。
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		pingErr := fmt.Errorf("ping sqlite database: %w", err)
		if closeErr := db.Close(); closeErr != nil {
			return nil, errors.Join(pingErr, fmt.Errorf("close failed sqlite database: %w", closeErr))
		}
		return nil, pingErr
	}
	// 外键表示关联事实，必须由数据库约束保护而不能依赖调用方自觉维护。
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		pragmaErr := fmt.Errorf("enable sqlite foreign keys: %w", err)
		if closeErr := db.Close(); closeErr != nil {
			return nil, errors.Join(pragmaErr, fmt.Errorf("close failed sqlite database: %w", closeErr))
		}
		return nil, pragmaErr
	}
	if err := migrate(ctx, db); err != nil {
		closeErr := db.Close()
		if closeErr != nil {
			return nil, errors.Join(fmt.Errorf("migrate sqlite database: %w", err), fmt.Errorf("close failed sqlite database: %w", closeErr))
		}
		return nil, fmt.Errorf("migrate sqlite database: %w", err)
	}

	logger.Info("sqlite store ready")
	return &Store{db: db, logger: logger}, nil
}

// Close 关闭 SQLite 数据库连接，资源所有权属于 Store。
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("close sqlite database: %w", err)
	}
	if s.logger != nil {
		s.logger.Info("sqlite store closed")
	}
	return nil
}
