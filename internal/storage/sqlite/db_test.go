package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// testBackupDir 返回 SQLite 测试默认使用的独立备份目录。
func testBackupDir(databasePath string) string {
	return filepath.Join(filepath.Dir(databasePath), "backups")
}

// TestOpenStoreRejectsNilLogger 验证存储层不会用默认 logger 绕过统一日志配置。
func TestOpenStoreRejectsNilLogger(t *testing.T) {
	path := filepath.Join(t.TempDir(), "timecue.db")
	if _, err := OpenStore(context.Background(), path, testBackupDir(path), nil); err == nil {
		t.Fatal("OpenStore() error = nil for nil logger")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("database file state = %v, want file not created", err)
	}
}

// TestOpenStoreKeepsForeignKeysAfterConnectionRecreation 验证物理连接重建后外键仍启用并继续级联删除。
func TestOpenStoreKeepsForeignKeysAfterConnectionRecreation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "timecue.db")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store, err := OpenStore(context.Background(), path, testBackupDir(path), logger)
	if err != nil {
		t.Fatalf("OpenStore() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Store.Close() error = %v", err)
		}
	})

	created, err := store.CreateSchedule(context.Background(), createTestSchedule())
	if err != nil {
		t.Fatalf("CreateSchedule() error = %v", err)
	}
	if _, err := store.db.Exec("INSERT INTO occurrence_overrides(schedule_id, occurrence_local, open) VALUES (?, '2026-01-02T10:00:00+08:00', 0)", created.ID); err != nil {
		t.Fatalf("insert override error = %v", err)
	}
	if _, err := store.db.Exec("INSERT INTO reminder_attempts(schedule_id, occurrence_local, attempted_at_utc, status) VALUES (?, '2026-01-02T10:00:00+08:00', '2026-01-02T02:00:00Z', 'submitted')", created.ID); err != nil {
		t.Fatalf("insert reminder attempt error = %v", err)
	}

	conn, err := store.db.Conn(context.Background())
	if err != nil {
		t.Fatalf("DB.Conn() error = %v", err)
	}
	if err := conn.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
		t.Fatalf("Conn.Raw() error = %v, want driver.ErrBadConn", err)
	}
	if err := conn.Close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
		t.Fatalf("Conn.Close() error = %v", err)
	}

	var foreignKeys int
	if err := store.db.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatalf("read foreign_keys pragma error = %v", err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys = %d, want 1 after connection recreation", foreignKeys)
	}
	if _, err := store.DeleteSchedule(context.Background(), created.ID, created.Revision); err != nil {
		t.Fatalf("DeleteSchedule() error = %v", err)
	}
	for _, table := range []string{"occurrence_overrides", "reminder_attempts"} {
		var count int
		if err := store.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			t.Fatalf("count %s error = %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s count = %d, want 0 after cascade", table, count)
		}
	}
}

// TestStoreOperationsFailAfterClose 验证 Store 关闭后不会继续提供数据库操作。
func TestStoreOperationsFailAfterClose(t *testing.T) {
	store := openTestStore(t)
	if err := store.Close(); err != nil {
		t.Fatalf("Store.Close() error = %v", err)
	}
	if _, err := store.GetSchedule(context.Background(), 1); err == nil {
		t.Fatal("GetSchedule() error = nil after Store.Close()")
	}
}

// TestStoreCloseAcceptsNilAndEmptyStores 验证 nil Store 和未初始化 Store 的关闭边界幂等且安全。
func TestStoreCloseAcceptsNilAndEmptyStores(t *testing.T) {
	var nilStore *Store
	if err := nilStore.Close(); err != nil {
		t.Fatalf("nil Store.Close() error = %v", err)
	}
	if err := (&Store{}).Close(); err != nil {
		t.Fatalf("empty Store.Close() error = %v", err)
	}
}
