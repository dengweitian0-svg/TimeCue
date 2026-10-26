package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dengweitian0-svg/TimeCue/migrations"
)

func TestCheckHealthRejectsNotificationsWithoutUniqueKey(t *testing.T) {
	ctx := context.Background()
	db := NewSqlite()
	if err := db.Connect(ctx, filepath.Join(t.TempDir(), "timecue.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	initial := withoutNotificationUniqueConstraint(migrations.Initial)
	if initial == migrations.Initial {
		t.Fatal("测试迁移未找到通知唯一约束")
	}
	if _, err := db.db.ExecContext(ctx, migrationBody(initial)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, migrationBody(migrations.ScheduleChanges)); err != nil {
		t.Fatal(err)
	}
	if err := db.CheckHealth(ctx); !errors.Is(err, ErrSchemaInvalid) {
		t.Fatalf("缺少提醒唯一键时健康检查应拒绝数据库: %v", err)
	}
	if _, err := db.db.ExecContext(ctx, `INSERT INTO schedules(name,type,period_open,period_cron,created_at,updated_at) VALUES ('测试','period',1,'[]',0,0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, `INSERT INTO notifications(schedule_id,notify_time) VALUES (1,1) ON CONFLICT(schedule_id,notify_time) DO NOTHING`); err == nil {
		t.Fatal("缺少唯一键的库不应接受依赖该唯一键的冲突处理")
	}
	var schemaSQL string
	if err := db.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type='table' AND name='notifications'`).Scan(&schemaSQL); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(schemaSQL), "unique") {
		t.Fatalf("健康检查不应修改未知数据库结构: %s", schemaSQL)
	}
}

func TestCheckHealthAcceptsNonPartialUniqueIndex(t *testing.T) {
	ctx := context.Background()
	db := NewSqlite()
	if err := db.Connect(ctx, filepath.Join(t.TempDir(), "timecue.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	initialSQL := withoutNotificationUniqueConstraint(migrations.Initial)
	if initialSQL == migrations.Initial {
		t.Fatal("测试迁移未找到通知唯一约束")
	}
	if _, err := db.db.ExecContext(ctx, migrationBody(initialSQL)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, migrationBody(migrations.ScheduleChanges)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, `CREATE UNIQUE INDEX uq_notifications_schedule_time ON notifications(schedule_id, notify_time)`); err != nil {
		t.Fatal(err)
	}
	if err := db.PrepareSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.CheckHealth(ctx); err != nil {
		t.Fatalf("显式完整唯一索引应满足健康检查: %v", err)
	}
}

func TestPrepareSchemaRejectsV1WithoutNotificationUniqueKey(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "timecue.db")
	db := NewSqlite()
	if err := db.Connect(ctx, dbPath); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	initial := withoutNotificationUniqueConstraint(migrations.Initial)
	if initial == migrations.Initial {
		t.Fatal("测试迁移未找到通知唯一约束")
	}
	if _, err := db.db.ExecContext(ctx, migrationBody(initial)); err != nil {
		t.Fatal(err)
	}
	var beforeVersion int
	var beforeDDL string
	if err := db.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&beforeVersion); err != nil {
		t.Fatal(err)
	}
	if err := db.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type='table' AND name='notifications'`).Scan(&beforeDDL); err != nil {
		t.Fatal(err)
	}
	if err := db.PrepareSchema(ctx); !errors.Is(err, ErrSchemaInvalid) {
		t.Fatalf("缺少唯一键的 Flutter v1 库应拒绝升级: %v", err)
	}
	var afterVersion int
	var afterDDL string
	if err := db.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&afterVersion); err != nil {
		t.Fatal(err)
	}
	if err := db.db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type='table' AND name='notifications'`).Scan(&afterDDL); err != nil {
		t.Fatal(err)
	}
	if afterVersion != beforeVersion || afterDDL != beforeDDL {
		t.Fatalf("拒绝前后版本或结构发生变化: version %d -> %d, DDL unchanged=%t", beforeVersion, afterVersion, beforeDDL == afterDDL)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".before-upgrade-") {
			t.Fatalf("结构拒绝前不应生成升级备份: %s", entry.Name())
		}
	}
}

func withoutNotificationUniqueConstraint(script string) string {
	lines := strings.Split(strings.ReplaceAll(script, "\r\n", "\n"), "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != "UNIQUE (schedule_id, notify_time)" {
			continue
		}
		if i > 0 {
			lines[i-1] = strings.TrimSuffix(lines[i-1], ",")
		}
		return strings.Join(append(lines[:i], lines[i+1:]...), "\n")
	}
	return script
}
