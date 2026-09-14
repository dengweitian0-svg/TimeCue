package sqlite

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
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

// OpenStore 打开 SQLite 数据库，必要时先备份旧库再完成迁移。
func OpenStore(ctx context.Context, databasePath, backupDir string, logger *slog.Logger) (*Store, error) {
	if databasePath == "" {
		return nil, errors.New("sqlite database path must not be empty")
	}
	if backupDir == "" {
		return nil, errors.New("sqlite backup directory must not be empty")
	}
	if logger == nil {
		return nil, errors.New("sqlite logger must not be nil")
	}
	databasePath = filepath.Clean(databasePath)
	backupDir = filepath.Clean(backupDir)
	if err := os.MkdirAll(filepath.Dir(databasePath), 0o755); err != nil {
		return nil, fmt.Errorf("create sqlite database directory %s: %w", filepath.Dir(databasePath), err)
	}
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return nil, fmt.Errorf("create sqlite backup directory %s: %w", backupDir, err)
	}

	exists, err := databaseExists(databasePath)
	if err != nil {
		return nil, err
	}
	currentVersion := 0
	if exists {
		// 先用独立连接探测版本并关闭它，确保备份发生在任何迁移连接之外。
		currentVersion, err = probeMigrationVersion(ctx, databasePath)
		if err != nil {
			return nil, err
		}
	}
	if exists && currentVersion < latestMigrationVersion() {
		backupPath, err := createDatabaseBackup(databasePath, backupDir)
		if err != nil {
			return nil, err
		}
		logger.Info("sqlite database backed up before migration", "database_path", databasePath, "backup_path", backupPath, "from_version", currentVersion, "to_version", latestMigrationVersion())
	}

	db, err := sql.Open("sqlite", sqliteDSN(databasePath))
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
			return nil, errors.Join(fmt.Errorf("migrate sqlite database %s: %w", databasePath, err), fmt.Errorf("close failed sqlite database %s: %w", databasePath, closeErr))
		}
		return nil, fmt.Errorf("migrate sqlite database %s: %w", databasePath, err)
	}

	store := &Store{db: db, logger: logger}
	store.generation.Store(initialGeneration())
	logger.Info("sqlite store ready")
	return store, nil
}

// latestMigrationVersion 返回当前程序支持的最高数据库版本。
func latestMigrationVersion() int {
	if len(migrations) == 0 {
		return 0
	}
	return migrations[len(migrations)-1].version
}

// databaseExists 检查数据库是否已经存在，并拒绝无法读取的路径。
func databaseExists(path string) (bool, error) {
	info, err := os.Stat(path)
	if err == nil {
		if info.IsDir() {
			return false, fmt.Errorf("inspect sqlite database %q: path is a directory", path)
		}
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("inspect sqlite database %q: %w", path, err)
}

// probeMigrationVersion 在关闭状态下读取已有数据库的迁移版本。
func probeMigrationVersion(ctx context.Context, path string) (version int, probeErr error) {
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		return 0, fmt.Errorf("probe sqlite database %q: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			closeErr = fmt.Errorf("close sqlite probe for %q: %w", path, closeErr)
			if probeErr == nil {
				probeErr = closeErr
			} else {
				probeErr = errors.Join(probeErr, closeErr)
			}
		}
	}()
	if err := db.PingContext(ctx); err != nil {
		return 0, fmt.Errorf("probe sqlite database %q: %w", path, err)
	}
	version, err = currentMigrationVersion(ctx, db)
	if err != nil {
		return 0, fmt.Errorf("probe sqlite database %q: %w", path, err)
	}
	return version, nil
}

// createDatabaseBackup 独占创建、完整复制并刷新关闭数据库备份。
func createDatabaseBackup(databasePath, backupDir string) (backupPath string, backupErr error) {
	backupPath = filepath.Join(backupDir, fmt.Sprintf("%s.%s.bak", filepath.Base(databasePath), time.Now().UTC().Format("20060102T150405.000000000Z")))
	source, err := os.Open(databasePath)
	if err != nil {
		return backupPath, fmt.Errorf("open sqlite database for backup %q: %w", databasePath, err)
	}
	defer func() {
		if closeErr := source.Close(); closeErr != nil {
			closeErr = fmt.Errorf("close sqlite backup source %q: %w", databasePath, closeErr)
			if backupErr == nil {
				backupErr = closeErr
			} else {
				backupErr = errors.Join(backupErr, closeErr)
			}
		}
	}()

	destination, err := os.OpenFile(backupPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return backupPath, fmt.Errorf("create sqlite backup %q: %w", backupPath, err)
	}
	copyErr := error(nil)
	if _, err := io.Copy(destination, source); err != nil {
		copyErr = fmt.Errorf("copy sqlite database to backup %q: %w", backupPath, err)
	}
	if copyErr == nil {
		if err := destination.Sync(); err != nil {
			copyErr = fmt.Errorf("sync sqlite backup %q: %w", backupPath, err)
		}
	}
	closeErr := destination.Close()
	if closeErr != nil {
		closeErr = fmt.Errorf("close sqlite backup %q: %w", backupPath, closeErr)
	}
	if copyErr != nil || closeErr != nil {
		if removeErr := os.Remove(backupPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			copyErr = errors.Join(copyErr, fmt.Errorf("remove incomplete sqlite backup %q: %w", backupPath, removeErr))
		}
		return backupPath, errors.Join(copyErr, closeErr)
	}
	return backupPath, nil
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
