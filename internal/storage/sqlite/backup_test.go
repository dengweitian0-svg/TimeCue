package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testLogger 返回不产生外部输出的 SQLite 测试 logger。
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// createVersionOneDatabase 创建一个待升级的版本 1 数据库文件。
func createVersionOneDatabase(t *testing.T, path string, withFailureTrigger bool) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create database directory: %v", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	db.SetMaxOpenConns(1)
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(initialMigrationSQL); err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO schema_migrations(version, applied_at_utc) VALUES (1, '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("record legacy version: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO schedules(title, first_start_local, original_start_local, duration_minutes, created_at_utc, updated_at_utc)
		VALUES ('legacy', '2026-01-01T10:00:00+08:00', '2026-01-01T10:00:00+08:00', 30, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}
	if withFailureTrigger {
		if _, err := db.Exec(`CREATE TRIGGER reject_v2 BEFORE INSERT ON schema_migrations WHEN NEW.version = 2 BEGIN SELECT RAISE(ABORT, 'forced backup migration failure'); END`); err != nil {
			t.Fatalf("create migration failure trigger: %v", err)
		}
	}
}

// readFileMigrationVersion 读取文件数据库的已提交迁移版本。
func readFileMigrationVersion(t *testing.T, path string) int {
	t.Helper()
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatalf("open database for version check: %v", err)
	}
	defer func() { _ = db.Close() }()
	version, err := currentMigrationVersion(context.Background(), db)
	if err != nil {
		t.Fatalf("read migration version: %v", err)
	}
	return version
}

// backupFiles 返回备份目录中按约定命名的数据库备份。
func backupFiles(t *testing.T, dir string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "timecue.db.*.bak"))
	if err != nil {
		t.Fatalf("glob backups: %v", err)
	}
	return files
}

func TestOpenStoreCreatesNewDatabaseAndDirectories(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "data", "timecue.db")
	backupDir := filepath.Join(root, "backups")

	store, err := OpenStore(context.Background(), databasePath, backupDir, testLogger())
	if err != nil {
		t.Fatalf("OpenStore() error = %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Errorf("Store.Close() error = %v", err)
		}
	}()
	if got := readFileMigrationVersion(t, databasePath); got != latestMigrationVersion() {
		t.Fatalf("migration version = %d, want %d", got, latestMigrationVersion())
	}
	if _, err := os.Stat(filepath.Dir(databasePath)); err != nil {
		t.Fatalf("database directory: %v", err)
	}
	if _, err := os.Stat(backupDir); err != nil {
		t.Fatalf("backup directory: %v", err)
	}
	if files := backupFiles(t, backupDir); len(files) != 0 {
		t.Fatalf("new database backups = %d, want 0", len(files))
	}
}

func TestOpenStoreReopensLatestDatabaseWithoutBackup(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "timecue.db")
	backupDir := filepath.Join(root, "backups")
	store, err := OpenStore(context.Background(), databasePath, backupDir, testLogger())
	if err != nil {
		t.Fatalf("first OpenStore() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("first Store.Close() error = %v", err)
	}

	reopened, err := OpenStore(context.Background(), databasePath, backupDir, testLogger())
	if err != nil {
		t.Fatalf("reopen OpenStore() error = %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatalf("reopen Store.Close() error = %v", err)
	}
	if files := backupFiles(t, backupDir); len(files) != 0 {
		t.Fatalf("latest database backups = %d, want 0", len(files))
	}
}

func TestOpenStoreBacksUpVersionOneBeforeUpgrade(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "timecue.db")
	backupDir := filepath.Join(root, "backups")
	createVersionOneDatabase(t, databasePath, false)
	original, err := os.ReadFile(databasePath)
	if err != nil {
		t.Fatalf("read original database: %v", err)
	}

	store, err := OpenStore(context.Background(), databasePath, backupDir, testLogger())
	if err != nil {
		t.Fatalf("OpenStore() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Store.Close() error = %v", err)
	}
	files := backupFiles(t, backupDir)
	if len(files) != 1 {
		t.Fatalf("backup count = %d, want 1", len(files))
	}
	backup, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(backup) != string(original) {
		t.Fatal("backup does not contain the complete pre-migration database")
	}
	if got := readFileMigrationVersion(t, databasePath); got != latestMigrationVersion() {
		t.Fatalf("migration version = %d, want %d", got, latestMigrationVersion())
	}
}

func TestOpenStoreStopsBeforeMigrationWhenBackupPreparationFails(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "timecue.db")
	backupPath := filepath.Join(root, "backup-file")
	createVersionOneDatabase(t, databasePath, false)
	if err := os.WriteFile(backupPath, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("create backup path: %v", err)
	}

	_, err := OpenStore(context.Background(), databasePath, backupPath, testLogger())
	if err == nil {
		t.Fatal("OpenStore() error = nil, want backup directory error")
	}
	if !strings.Contains(err.Error(), backupPath) {
		t.Fatalf("error = %v, want backup path", err)
	}
	if got := readFileMigrationVersion(t, databasePath); got != 1 {
		t.Fatalf("migration version = %d, want unchanged version 1", got)
	}
}

func TestOpenStoreKeepsBackupWhenMigrationFails(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "timecue.db")
	backupDir := filepath.Join(root, "backups")
	createVersionOneDatabase(t, databasePath, true)

	_, err := OpenStore(context.Background(), databasePath, backupDir, testLogger())
	if err == nil {
		t.Fatal("OpenStore() error = nil, want migration error")
	}
	if !strings.Contains(err.Error(), databasePath) {
		t.Fatalf("error = %v, want database path", err)
	}
	if files := backupFiles(t, backupDir); len(files) != 1 {
		t.Fatalf("backup count = %d, want 1 after failed migration", len(files))
	}
	if got := readFileMigrationVersion(t, databasePath); got != 1 {
		t.Fatalf("migration version = %d, want rollback to version 1", got)
	}
}

func TestOpenStoreCanRestoreBackupBeforeRetry(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "timecue.db")
	backupDir := filepath.Join(root, "backups")
	createVersionOneDatabase(t, databasePath, false)

	store, err := OpenStore(context.Background(), databasePath, backupDir, testLogger())
	if err != nil {
		t.Fatalf("first OpenStore() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("first Store.Close() error = %v", err)
	}
	files := backupFiles(t, backupDir)
	if len(files) != 1 {
		t.Fatalf("backup count = %d, want 1", len(files))
	}
	if err := os.Remove(databasePath); err != nil {
		t.Fatalf("remove database for restore: %v", err)
	}
	source, err := os.Open(files[0])
	if err != nil {
		t.Fatalf("open backup for restore: %v", err)
	}
	destination, err := os.OpenFile(databasePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		_ = source.Close()
		t.Fatalf("create restored database: %v", err)
	}
	_, copyErr := io.Copy(destination, source)
	closeDestinationErr := destination.Close()
	closeSourceErr := source.Close()
	if copyErr != nil || closeDestinationErr != nil || closeSourceErr != nil {
		t.Fatalf("restore database backup: %v", errors.Join(copyErr, closeDestinationErr, closeSourceErr))
	}
	if err := os.Chmod(databasePath, 0o600); err != nil {
		t.Fatalf("restore database backup: %v", err)
	}

	restored, err := OpenStore(context.Background(), databasePath, backupDir, testLogger())
	if err != nil {
		t.Fatalf("restored OpenStore() error = %v", err)
	}
	defer func() { _ = restored.Close() }()
	if got := readFileMigrationVersion(t, databasePath); got != latestMigrationVersion() {
		t.Fatalf("restored migration version = %d, want %d", got, latestMigrationVersion())
	}
	var title string
	if err := restored.db.QueryRow("SELECT title FROM schedules WHERE id = 1").Scan(&title); err != nil {
		t.Fatalf("read restored schedule: %v", err)
	}
	if title != "legacy" {
		t.Fatalf("restored title = %q, want legacy", title)
	}
}
