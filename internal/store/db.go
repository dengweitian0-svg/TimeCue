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

	"github.com/dengweitian0-svg/TimeCue/internal/model"
)

const LimitMax = 50

var (
	ErrNotConnected        = errors.New("database is not connected")
	ErrAlreadyConnected    = errors.New("database is already connected")
	ErrNotFound            = model.ErrRecordNotFound
	ErrScheduleDeleted     = errors.New("schedule is deleted")
	ErrScheduleCancelled   = errors.New("schedule is cancelled")
	ErrSchedulePaused      = errors.New("schedule is paused")
	ErrNotificationDeleted = errors.New("notification is deleted")
	ErrInvalidTransition   = errors.New("invalid state transition")
	ErrDatabaseUnavailable = errors.New("database unavailable")
	ErrSchemaInvalid       = errors.New("database schema invalid")
)

// CheckHealth 验证连接可用且数据库采用当前手动迁移的结构。
func (s *Sqlite) CheckHealth(ctx context.Context) error {
	db, err := s.handle()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDatabaseUnavailable, err)
	}
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("%w: %v", ErrDatabaseUnavailable, err)
	}
	return checkSchema(ctx, db, false)
}

// schemaReader 允许提交前通过同一事务验证结构。
type schemaReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func checkSchema(ctx context.Context, db schemaReader, allowLegacyStatus bool) error {
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("%w: %v", ErrDatabaseUnavailable, err)
	}
	if version != 2 {
		return fmt.Errorf("%w: expected version 2, got %d", ErrSchemaInvalid, version)
	}
	var notificationDDL string
	if err := db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'notifications'`).Scan(&notificationDDL); err != nil {
		return fmt.Errorf("%w: notifications table missing: %v", ErrSchemaInvalid, err)
	}
	if !allowLegacyStatus && !strings.Contains(strings.ToLower(notificationDDL), "'loaded'") {
		return fmt.Errorf("%w: notifications status does not support loaded", ErrSchemaInvalid)
	}
	for table, required := range map[string]map[string]string{
		"schedules": {
			"id": "INTEGER", "name": "TEXT", "type": "TEXT", "description": "TEXT",
			"period_open": "INTEGER", "period_cron": "TEXT", "single_notify_time": "INTEGER",
			"cancelled": "INTEGER", "deleted": "INTEGER", "created_at": "INTEGER", "updated_at": "INTEGER", "revision": "INTEGER",
		},
		"schedule_write_receipts": {"operation_key": "TEXT", "request_digest": "TEXT", "result_json": "TEXT"},
		"schedule_versions":       {"schedule_id": "INTEGER", "revision": "INTEGER", "effective_from": "INTEGER", "effective_until": "INTEGER", "name": "TEXT", "type": "TEXT", "description": "TEXT", "period_open": "INTEGER", "period_cron": "TEXT", "single_notify_time": "INTEGER", "cancelled": "INTEGER", "deleted": "INTEGER"},
		"notifications": {
			"id": "INTEGER", "schedule_id": "INTEGER", "status": "TEXT",
			"notify_time": "INTEGER", "acknowledged_at": "INTEGER", "deleted": "INTEGER",
			"delivery_stopped": "INTEGER", "snapshot_taken": "INTEGER", "name_snapshot": "TEXT", "description_snapshot": "TEXT",
		},
	} {
		rows, err := db.QueryContext(ctx, "PRAGMA table_info("+table+")")
		if err != nil {
			return fmt.Errorf("%w: %v", ErrDatabaseUnavailable, err)
		}
		found := make(map[string]string)
		primaryID := false
		for rows.Next() {
			var cid, notNull, primaryKey int
			var name, dataType string
			var defaultValue sql.NullString
			if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
				rows.Close()
				return fmt.Errorf("%w: %v", ErrDatabaseUnavailable, err)
			}
			found[name] = dataType
			if name == "id" && primaryKey == 1 {
				primaryID = true
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return fmt.Errorf("%w: %v", ErrDatabaseUnavailable, err)
		}
		for column, dataType := range required {
			if found[column] != dataType {
				return fmt.Errorf("%w: invalid %s.%s type", ErrSchemaInvalid, table, column)
			}
		}
		if (table == "schedules" || table == "notifications") && !primaryID {
			return fmt.Errorf("%w: missing %s primary key", ErrSchemaInvalid, table)
		}
	}
	if err := checkNotificationUniqueKey(ctx, db); err != nil {
		return err
	}
	return nil
}

// checkNotificationUniqueKey 确认提醒去重所依赖的完整唯一索引仍然存在。
func checkNotificationUniqueKey(ctx context.Context, db schemaReader) error {
	rows, err := db.QueryContext(ctx, `PRAGMA index_list(notifications)`)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDatabaseUnavailable, err)
	}
	type index struct {
		name    string
		unique  bool
		partial bool
	}
	var indexes []index
	for rows.Next() {
		var sequence, unique, partial int
		var name, origin string
		if err := rows.Scan(&sequence, &name, &unique, &origin, &partial); err != nil {
			rows.Close()
			return fmt.Errorf("%w: %v", ErrDatabaseUnavailable, err)
		}
		indexes = append(indexes, index{name: name, unique: unique == 1, partial: partial == 1})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("%w: %v", ErrDatabaseUnavailable, err)
	}
	rows.Close()
	for _, candidate := range indexes {
		if !candidate.unique || candidate.partial {
			continue
		}
		columns, err := db.QueryContext(ctx, `SELECT seqno, cid, name FROM pragma_index_info(?)`, candidate.name)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrDatabaseUnavailable, err)
		}
		var names []string
		for columns.Next() {
			var sequence, columnID int
			var name sql.NullString
			if err := columns.Scan(&sequence, &columnID, &name); err != nil {
				columns.Close()
				return fmt.Errorf("%w: %v", ErrDatabaseUnavailable, err)
			}
			if !name.Valid {
				names = append(names, "")
			} else {
				names = append(names, name.String)
			}
		}
		err = columns.Err()
		columns.Close()
		if err != nil {
			return fmt.Errorf("%w: %v", ErrDatabaseUnavailable, err)
		}
		if len(names) == 2 && names[0] == "schedule_id" && names[1] == "notify_time" {
			return nil
		}
	}
	return fmt.Errorf("%w: missing UNIQUE(notifications.schedule_id, notifications.notify_time)", ErrSchemaInvalid)
}

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
