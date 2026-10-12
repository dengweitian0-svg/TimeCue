package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/dengweitian0-svg/TimeCue/migrations"
)

// PrepareSchema 初始化空库或升级可识别的 Flutter 旧库；不转换 Wails 数据。
func (s *Sqlite) PrepareSchema(ctx context.Context) error {
	db, err := s.handle()
	if err != nil {
		return err
	}
	var count, version int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`).Scan(&count); err != nil {
		return err
	}
	if err = db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if count == 0 && version != 0 {
		return fmt.Errorf("%w: empty database has version %d", ErrSchemaInvalid, version)
	}
	if count != 0 {
		if version != 1 && version != 2 {
			return fmt.Errorf("%w: unsupported database; Wails databases require an explicit data conversion", ErrSchemaInvalid)
		}
		// 在备份和任何写操作之前辨认可升级的列集合，拒绝不明结构。
		for table, columns := range map[string][]string{
			"schedules":     {"id", "name", "type", "description", "period_open", "period_cron", "single_notify_time", "cancelled", "deleted", "created_at", "updated_at"},
			"notifications": {"id", "schedule_id", "status", "notify_time", "acknowledged_at", "deleted"},
		} {
			if version == 2 {
				if table == "schedules" {
					columns = append(columns, "revision")
				} else {
					columns = append(columns, "delivery_stopped", "snapshot_taken", "name_snapshot", "description_snapshot")
				}
			}
			rows, queryErr := db.QueryContext(ctx, "PRAGMA table_info("+table+")")
			if queryErr != nil {
				return queryErr
			}
			found := map[string]bool{}
			for rows.Next() {
				var cid, notNull, pk int
				var name, kind string
				var defaultValue sql.NullString
				if err = rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &pk); err != nil {
					rows.Close()
					return err
				}
				found[name] = true
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			if len(found) != len(columns) {
				return fmt.Errorf("%w: unsupported %s columns", ErrSchemaInvalid, table)
			}
			for _, column := range columns {
				if !found[column] {
					return fmt.Errorf("%w: missing %s.%s", ErrSchemaInvalid, table, column)
				}
			}
		}
		if err = checkNotificationUniqueKey(ctx, db); err != nil {
			return err
		}
		if version == 2 {
			if err = checkSchema(ctx, db, true); err != nil {
				return err
			}
			if s.CheckHealth(ctx) == nil {
				return nil
			}
		}
		// SQLite 自身生成一致性备份；失败即停止升级，原库保持不变。
		var name, path string
		var sequence int
		if err = db.QueryRowContext(ctx, `PRAGMA database_list`).Scan(&sequence, &name, &path); err != nil {
			return err
		}
		if path != "" {
			backup := path + ".before-upgrade-" + time.Now().Format("20060102-150405.000000000") + ".bak"
			if _, err = db.ExecContext(ctx, `VACUUM INTO ?`, backup); err != nil {
				return fmt.Errorf("backup database: %w", err)
			}
		}
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// 首次写操作取得锁，后续检查和表转换在同一事务内完成。
	if count != 0 {
		if _, err = tx.ExecContext(ctx, `UPDATE schedules SET name=name WHERE 0`); err != nil {
			return err
		}
	}
	var current int
	if err = tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&current); err != nil {
		return err
	}
	if current != version {
		return fmt.Errorf("%w: database changed during upgrade; retry startup", ErrSchemaInvalid)
	}
	if count == 0 {
		if _, err = tx.ExecContext(ctx, migrationBody(migrations.Initial)); err != nil {
			return err
		}
		version = 1
	}
	if version == 1 {
		if _, err = tx.ExecContext(ctx, migrationBody(migrations.ScheduleChanges)); err != nil {
			return err
		}
	}
	var ddl string
	if err = tx.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type='table' AND name='notifications'`).Scan(&ddl); err != nil {
		return err
	}
	if !strings.Contains(strings.ToLower(ddl), "'loaded'") {
		// 保存已有索引和触发器，重建 CHECK 时保留所有提醒事实和主键。
		rows, queryErr := tx.QueryContext(ctx, `SELECT sql FROM sqlite_master WHERE tbl_name='notifications' AND type IN ('index','trigger') AND sql IS NOT NULL`)
		if queryErr != nil {
			return queryErr
		}
		var objects []string
		for rows.Next() {
			var object string
			if err = rows.Scan(&object); err != nil {
				rows.Close()
				return err
			}
			objects = append(objects, object)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `CREATE TABLE notifications_upgrade (
			id INTEGER PRIMARY KEY, schedule_id INTEGER NOT NULL REFERENCES schedules(id),
			status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','loaded','sent','failed')),
			notify_time INTEGER NOT NULL, acknowledged_at INTEGER,
			deleted INTEGER NOT NULL DEFAULT 0 CHECK(deleted IN (0,1)),
			delivery_stopped INTEGER NOT NULL DEFAULT 0 CHECK(delivery_stopped IN (0,1)),
			snapshot_taken INTEGER NOT NULL DEFAULT 0 CHECK(snapshot_taken IN (0,1)),
			name_snapshot TEXT, description_snapshot TEXT, UNIQUE(schedule_id,notify_time));
			INSERT INTO notifications_upgrade SELECT id,schedule_id,status,notify_time,acknowledged_at,deleted,delivery_stopped,snapshot_taken,name_snapshot,description_snapshot FROM notifications;
			DROP TABLE notifications;
			ALTER TABLE notifications_upgrade RENAME TO notifications;`)
		if err != nil {
			return err
		}
		for _, object := range objects {
			if _, err = tx.ExecContext(ctx, object); err != nil {
				return err
			}
		}
	}
	if err = checkSchema(ctx, tx, false); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	return s.CheckHealth(ctx)
}

// migrationBody 将独立 SQL 的事务边界交给初始化事务统一管理。
func migrationBody(script string) string {
	script = strings.ReplaceAll(script, "BEGIN TRANSACTION;", "")
	return strings.ReplaceAll(script, "COMMIT;", "")
}
