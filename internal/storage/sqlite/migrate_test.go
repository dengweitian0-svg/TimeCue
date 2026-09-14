package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

// openRawTestDB 创建一个仅用于迁移测试的内存数据库。
func openRawTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ExecContext(context.Background(), "PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("enable foreign keys error = %v", err)
	}
	return db
}

// TestMigrateCreatesInitialSchema 验证新库创建版本表和三张业务表。
func TestMigrateCreatesInitialSchema(t *testing.T) {
	db := openRawTestDB(t)
	if err := migrate(context.Background(), db); err != nil {
		t.Fatalf("migrate() error = %v", err)
	}
	for _, table := range []string{"schema_migrations", "schedules", "occurrence_overrides", "reminder_attempts"} {
		var exists bool
		if err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?)", table).Scan(&exists); err != nil {
			t.Fatalf("inspect table %q error = %v", table, err)
		}
		if !exists {
			t.Fatalf("table %q does not exist", table)
		}
	}
}

// TestMigrateIsIdempotent 验证重复迁移不会重复写入版本记录。
func TestMigrateIsIdempotent(t *testing.T) {
	db := openRawTestDB(t)
	ctx := context.Background()
	if err := migrate(ctx, db); err != nil {
		t.Fatalf("first migrate() error = %v", err)
	}
	if err := migrate(ctx, db); err != nil {
		t.Fatalf("second migrate() error = %v", err)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil {
		t.Fatalf("count migrations error = %v", err)
	}
	if count != 6 {
		t.Fatalf("migration count = %d, want 6", count)
	}
}

// TestMigrationVersionIsRecorded 验证迁移版本在事务提交后可读取。
func TestMigrationVersionIsRecorded(t *testing.T) {
	db := openRawTestDB(t)
	if err := migrate(context.Background(), db); err != nil {
		t.Fatalf("migrate() error = %v", err)
	}
	var version int
	if err := db.QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&version); err != nil {
		t.Fatalf("read migration version error = %v", err)
	}
	if version != 6 {
		t.Fatalf("version = %d, want 6", version)
	}
}

// TestMigrateRejectsNewerVersion 验证程序不会在未知数据库版本上继续运行。
func TestMigrateRejectsNewerVersion(t *testing.T) {
	db := openRawTestDB(t)
	if _, err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at_utc TEXT NOT NULL)`); err != nil {
		t.Fatalf("create migration table error = %v", err)
	}
	if _, err := db.Exec("INSERT INTO schema_migrations(version, applied_at_utc) VALUES (99, 'now')"); err != nil {
		t.Fatalf("insert migration version error = %v", err)
	}
	if err := migrate(context.Background(), db); err == nil {
		t.Fatal("migrate() error = nil for newer schema version")
	}
}

// TestInitialSchemaConstraints 验证日程字段约束和不保存结束时间。
func TestInitialSchemaConstraints(t *testing.T) {
	db := openRawTestDB(t)
	if err := migrate(context.Background(), db); err != nil {
		t.Fatalf("migrate() error = %v", err)
	}
	for name, statement := range map[string]string{
		"empty title":              "INSERT INTO schedules(title, first_start_local, original_start_local, duration_minutes, created_at_utc, updated_at_utc) VALUES (' ', 'a', 'a', 1, 'a', 'a')",
		"closed value":             "INSERT INTO schedules(title, open, first_start_local, original_start_local, duration_minutes, created_at_utc, updated_at_utc) VALUES ('x', 2, 'a', 'a', 1, 'a', 'a')",
		"zero duration":            "INSERT INTO schedules(title, first_start_local, original_start_local, duration_minutes, created_at_utc, updated_at_utc) VALUES ('x', 'a', 'a', 0, 'a', 'a')",
		"zero revision":            "INSERT INTO schedules(title, first_start_local, original_start_local, duration_minutes, revision, created_at_utc, updated_at_utc) VALUES ('x', 'a', 'a', 1, 0, 'a', 'a')",
		"invalid month end policy": "INSERT INTO schedules(title, first_start_local, original_start_local, duration_minutes, created_at_utc, updated_at_utc, month_end_policy) VALUES ('x', 'a', 'a', 1, 'a', 'a', 'invalid')",
	} {
		if _, err := db.Exec(statement); err == nil {
			t.Errorf("%s: Exec() error = nil", name)
		}
	}
	if _, err := db.Exec("INSERT INTO schedules(title, first_start_local, original_start_local, duration_minutes, created_at_utc, updated_at_utc) VALUES ('valid', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', 60, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')"); err != nil {
		t.Fatalf("insert valid schedule error = %v", err)
	}
	for name, statement := range map[string]string{
		"open snapshot missing notes": "INSERT INTO occurrence_overrides(schedule_id, occurrence_local, open, actual_start_local, duration_minutes, title) VALUES (1, '2026-01-02', 1, '2026-01-02T10:00:00+08:00', 60, 'title')",
		"open snapshot missing start": "INSERT INTO occurrence_overrides(schedule_id, occurrence_local, open, duration_minutes, title, notes) VALUES (1, '2026-01-03', 1, 60, 'title', '')",
		"cancel snapshot partial":     "INSERT INTO occurrence_overrides(schedule_id, occurrence_local, open, actual_start_local) VALUES (1, '2026-01-04', 0, '2026-01-04T10:00:00+08:00')",
	} {
		if _, err := db.Exec(statement); err == nil {
			t.Errorf("%s: Exec() error = nil", name)
		}
	}
	var columnCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('schedules') WHERE name = 'end_local'").Scan(&columnCount); err != nil {
		t.Fatalf("inspect end column error = %v", err)
	}
	if columnCount != 0 {
		t.Fatal("schedules unexpectedly contains an end time column")
	}
}

// TestForeignKeysCascade 验证关联表使用外键并在日程删除时级联清理。
func TestForeignKeysCascade(t *testing.T) {
	db := openRawTestDB(t)
	if err := migrate(context.Background(), db); err != nil {
		t.Fatalf("migrate() error = %v", err)
	}
	_, err := db.Exec(`INSERT INTO schedules(title, first_start_local, original_start_local, duration_minutes, created_at_utc, updated_at_utc) VALUES ('x', 'a', 'a', 1, 'a', 'a')`)
	if err != nil {
		t.Fatalf("insert schedule error = %v", err)
	}
	if _, err := db.Exec("INSERT INTO occurrence_overrides(schedule_id, occurrence_local, open) VALUES (1, 'a', 0)"); err != nil {
		t.Fatalf("insert override error = %v", err)
	}
	if _, err := db.Exec("INSERT INTO reminder_attempts(schedule_id, occurrence_local, attempted_at_utc, status) VALUES (1, 'a', 'a', 'submitted')"); err != nil {
		t.Fatalf("insert attempt error = %v", err)
	}
	if _, err := db.Exec("DELETE FROM schedules WHERE id = 1"); err != nil {
		t.Fatalf("delete schedule error = %v", err)
	}
	for _, table := range []string{"occurrence_overrides", "reminder_attempts"} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			t.Fatalf("count %s error = %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s count = %d, want 0", table, count)
		}
	}
	if _, err := db.Exec("INSERT INTO occurrence_overrides(schedule_id, occurrence_local, open) VALUES (999, 'a', 1)"); err == nil {
		t.Fatal("foreign key violation error = nil")
	}
}

// TestFailedMigrationRollsBackSchema 验证迁移 SQL 失败时不会留下半成品表结构。
func TestFailedMigrationRollsBackSchema(t *testing.T) {
	db := openRawTestDB(t)
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx() error = %v", err)
	}
	err = applyMigration(context.Background(), tx, migration{version: 99, sql: `
		CREATE TABLE rollback_probe (id INTEGER PRIMARY KEY);
		CREATE TABLE broken (id INTEGER NOT NULL;
	`})
	if err == nil {
		t.Fatal("applyMigration() error = nil")
	}
	if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
		t.Fatalf("Rollback() error = %v", rollbackErr)
	}
	var exists bool
	if err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'rollback_probe')").Scan(&exists); err != nil {
		t.Fatalf("inspect rollback table error = %v", err)
	}
	if exists {
		t.Fatal("failed migration left rollback_probe table")
	}
}

// TestScheduleOpenInvariantMigrationRepairsLegacySingle 验证 v5 修复遗留单次状态并保留关联事实。
func TestScheduleOpenInvariantMigrationRepairsLegacySingle(t *testing.T) {
	db := openRawTestDB(t)
	ctx := context.Background()
	// 先构造 v4 数据库，模拟 v5 尚未安装时遗留的非法单次状态。
	for _, migration := range migrations[:4] {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("begin migration %d error = %v", migration.version, err)
		}
		if err := applyMigration(ctx, tx, migration); err != nil {
			_ = tx.Rollback()
			t.Fatalf("apply migration %d error = %v", migration.version, err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("commit migration %d error = %v", migration.version, err)
		}
	}
	if _, err := db.Exec("INSERT INTO schedules(title, cron, open, first_start_local, original_start_local, duration_minutes, created_at_utc, updated_at_utc) VALUES ('legacy', NULL, 0, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', 60, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')"); err != nil {
		t.Fatalf("insert legacy schedule error = %v", err)
	}
	if _, err := db.Exec("INSERT INTO occurrence_overrides(schedule_id, occurrence_local, open) VALUES (1, '2026-01-02', 0)"); err != nil {
		t.Fatalf("insert cancellation error = %v", err)
	}
	if _, err := db.Exec("INSERT INTO reminder_attempts(schedule_id, occurrence_local, attempted_at_utc, status) VALUES (1, '2026-01-02', '2026-01-02T00:00:00Z', 'submitted')"); err != nil {
		t.Fatalf("insert reminder attempt error = %v", err)
	}

	if err := migrate(ctx, db); err != nil {
		t.Fatalf("reapply v5 migration error = %v", err)
	}
	var open int
	if err := db.QueryRow("SELECT open FROM schedules WHERE id = 1").Scan(&open); err != nil || open != 1 {
		t.Fatalf("repaired open = %d, error = %v", open, err)
	}
	for _, table := range []string{"occurrence_overrides", "reminder_attempts"} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM " + table + " WHERE schedule_id = 1").Scan(&count); err != nil || count != 1 {
			t.Fatalf("%s count = %d, error = %v", table, count, err)
		}
	}
}

// TestScheduleOpenInvariantRejectsClosedSingle 验证触发器阻止单次日程插入和更新为关闭状态。
func TestScheduleOpenInvariantRejectsClosedSingle(t *testing.T) {
	db := openRawTestDB(t)
	if err := migrate(context.Background(), db); err != nil {
		t.Fatalf("migrate() error = %v", err)
	}
	insert := "INSERT INTO schedules(title, cron, open, first_start_local, original_start_local, duration_minutes, created_at_utc, updated_at_utc) VALUES ('single', NULL, 0, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', 60, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')"
	if _, err := db.Exec(insert); err == nil {
		t.Fatal("closed single insert error = nil")
	}
	if _, err := db.Exec("INSERT INTO schedules(title, cron, first_start_local, original_start_local, duration_minutes, created_at_utc, updated_at_utc) VALUES ('single', NULL, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', 60, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')"); err != nil {
		t.Fatalf("insert valid single error = %v", err)
	}
	if _, err := db.Exec("UPDATE schedules SET open = 0 WHERE id = 1"); err == nil {
		t.Fatal("closed single update error = nil")
	}
	if _, err := db.Exec("INSERT INTO schedules(title, cron, open, first_start_local, original_start_local, duration_minutes, created_at_utc, updated_at_utc) VALUES ('recurring', '0 9 * * *', 0, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', 60, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')"); err != nil {
		t.Fatalf("closed recurring insert error = %v", err)
	}
}
