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

// TestPrepareSchema 验证首次初始化、保留历史升级、备份和重复启动。
func TestPrepareSchema(t *testing.T) {
	for _, fixture := range []string{"empty", "v1", "v2-legacy", "v2"} {
		t.Run(fixture, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "中文 数据.db")
			db := NewSqlite()
			if err := db.Connect(ctx, path); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			if fixture != "empty" {
				script := migrations.Initial
				if fixture != "v2" {
					script = strings.ReplaceAll(script, "'loaded', ", "")
				}
				if _, err := db.db.ExecContext(ctx, script); err != nil {
					t.Fatal(err)
				}
				if _, err := db.db.ExecContext(ctx, `INSERT INTO schedules(id,name,type,single_notify_time,created_at,updated_at) VALUES(42,'旧日程','single',60000000000,0,0); INSERT INTO notifications(id,schedule_id,status,notify_time,acknowledged_at) VALUES(99,42,'sent',60000000000,70000000000);`); err != nil {
					t.Fatal(err)
				}
				if fixture != "v1" {
					if _, err := db.db.ExecContext(ctx, migrations.ScheduleChanges); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := db.PrepareSchema(ctx); err != nil {
				t.Fatal(err)
			}
			if err := db.PrepareSchema(ctx); err != nil {
				t.Fatal(err)
			}
			if err := db.CheckHealth(ctx); err != nil {
				t.Fatal(err)
			}
			if fixture != "empty" {
				var status, name string
				var acknowledgement int64
				if err := db.db.QueryRowContext(ctx, `SELECT n.status,s.name,n.acknowledged_at FROM notifications n JOIN schedules s ON s.id=n.schedule_id WHERE n.id=99`).Scan(&status, &name, &acknowledgement); err != nil {
					t.Fatal(err)
				}
				if status != "sent" || name != "旧日程" || acknowledgement != 70000000000 {
					t.Fatal(status, name, acknowledgement)
				}
			}
			backups, err := filepath.Glob(path + ".before-upgrade-*.bak")
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if fixture != "empty" {
				want = 1
			}
			if len(backups) != want {
				t.Fatalf("备份数 %d，期望 %d", len(backups), want)
			}
		})
	}
}

// TestPrepareRejectsUnknown 保证 Wails 和未知结构不会被自动覆盖。
func TestPrepareRejectsUnknown(t *testing.T) {
	db := NewSqlite()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "unknown.db")
	if err := db.Connect(ctx, path); err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.db.ExecContext(ctx, `CREATE TABLE schema_migrations(version INTEGER); INSERT INTO schema_migrations VALUES(8); CREATE TABLE schedules(id TEXT); INSERT INTO schedules VALUES('保留');`); err != nil {
		t.Fatal(err)
	}
	if err := db.PrepareSchema(ctx); !errors.Is(err, ErrSchemaInvalid) {
		t.Fatal(err)
	}
	var value string
	if err := db.db.QueryRowContext(ctx, `SELECT id FROM schedules`).Scan(&value); err != nil || value != "保留" {
		t.Fatal(value, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

// TestUpgradeRollsBack 验证复制失败不会留下半升级表或改变迁移版本。
func TestUpgradeRollsBack(t *testing.T) {
	db := NewSqlite()
	ctx := context.Background()
	if err := db.Connect(ctx, filepath.Join(t.TempDir(), "bad.db")); err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.db.ExecContext(ctx, strings.ReplaceAll(migrations.Initial, "'loaded', ", "")+migrations.ScheduleChanges); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, `PRAGMA ignore_check_constraints=ON; INSERT INTO schedules(id,name,type,single_notify_time,created_at,updated_at) VALUES(1,'坏数据','single',1,0,0); INSERT INTO notifications(schedule_id,status,notify_time) VALUES(1,'invalid',1); PRAGMA ignore_check_constraints=OFF;`); err != nil {
		t.Fatal(err)
	}
	if err := db.PrepareSchema(ctx); err == nil {
		t.Fatal("非法旧数据应拒绝升级")
	}
	var status string
	if err := db.db.QueryRowContext(ctx, `SELECT status FROM notifications`).Scan(&status); err != nil || status != "invalid" {
		t.Fatal(status, err)
	}
	var count int
	if err := db.db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE name='notifications_upgrade'`).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}
