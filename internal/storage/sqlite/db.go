package sqlite

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
)

// Store 保存 SQLite 数据库连接和存储资源。
type Store struct {
	db         *sql.DB
	logger     *slog.Logger
	generation atomic.Uint64
}

// OpenStore 打开 SQLite 数据库并完成初始迁移。
func OpenStore(ctx context.Context, path string, logger *slog.Logger) (*Store, error) {
	if path == "" {
		return nil, errors.New("sqlite database path must not be empty")
	}
	if logger == nil {
		return nil, errors.New("sqlite logger must not be nil")
	}

	db, err := sql.Open("sqlite", sqliteDSN(path))
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
	// 连接级 DSN 参数会在每个物理连接建立时执行；这里再读取一次，确保首个连接确实启用了约束。
	var foreignKeys int
	if err := db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		pragmaErr := fmt.Errorf("verify sqlite foreign keys: %w", err)
		if closeErr := db.Close(); closeErr != nil {
			return nil, errors.Join(pragmaErr, fmt.Errorf("close failed sqlite database: %w", closeErr))
		}
		return nil, pragmaErr
	}
	if foreignKeys != 1 {
		pragmaErr := fmt.Errorf("verify sqlite foreign keys: want 1, got %d", foreignKeys)
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

	store := &Store{db: db, logger: logger}
	store.generation.Store(initialGeneration())
	logger.Info("sqlite store ready")
	return store, nil
}

// sqliteDSN 为每个 SQLite 物理连接附加外键约束参数，并保留调用方已有查询参数。
func sqliteDSN(path string) string {
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	return path + separator + "_pragma=foreign_keys(1)"
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

// initialGeneration 返回本次进程唯一的内存 generation，重启后不会复用旧游标版本。
func initialGeneration() uint64 {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err == nil {
		value := binary.LittleEndian.Uint64(raw[:])
		if value != 0 {
			return value
		}
	}
	return uint64(time.Now().UnixNano()) | 1
}
