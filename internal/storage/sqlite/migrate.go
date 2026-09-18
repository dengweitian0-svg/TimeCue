package sqlite

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
)

//go:embed migrations/001_initial.sql
var initialMigrationSQL string

//go:embed migrations/002_recurring_cutoff.sql
var recurringCutoffMigrationSQL string

//go:embed migrations/003_month_end_policy.sql
var monthEndPolicyMigrationSQL string

//go:embed migrations/004_occurrence_snapshots.sql
var occurrenceSnapshotsMigrationSQL string

//go:embed migrations/005_schedule_open_invariant.sql
var scheduleOpenInvariantMigrationSQL string

//go:embed migrations/006_reminder_attempts.sql
var reminderAttemptsMigrationSQL string

//go:embed migrations/007_reminder_processing.sql
var reminderProcessingMigrationSQL string

//go:embed migrations/008_scheduler_recovery.sql
var schedulerRecoveryMigrationSQL string

// migration 描述一个带版本号的 SQL 迁移。
type migration struct {
	version int
	sql     string
}

var migrations = []migration{
	{version: 1, sql: initialMigrationSQL},
	{version: 2, sql: recurringCutoffMigrationSQL},
	{version: 3, sql: monthEndPolicyMigrationSQL},
	{version: 4, sql: occurrenceSnapshotsMigrationSQL},
	{version: 5, sql: scheduleOpenInvariantMigrationSQL},
	{version: 6, sql: reminderAttemptsMigrationSQL},
	{version: 7, sql: reminderProcessingMigrationSQL},
	{version: 8, sql: schedulerRecoveryMigrationSQL},
}

// migrate 执行尚未应用的数据库迁移。
func migrate(ctx context.Context, db *sql.DB) error {
	currentVersion, err := currentMigrationVersion(ctx, db)
	if err != nil {
		return err
	}
	if len(migrations) > 0 && currentVersion > migrations[len(migrations)-1].version {
		return fmt.Errorf("database migration version %d is newer than supported version %d", currentVersion, migrations[len(migrations)-1].version)
	}
	for _, migration := range migrations {
		if migration.version <= currentVersion {
			continue
		}

		// DDL 与版本记录必须在同一事务中原子提交，避免留下半套表结构。
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", migration.version, err)
		}
		if err := applyMigration(ctx, tx, migration); err != nil {
			// 任一步失败都必须回滚，不能把不完整迁移标记为已应用。
			if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
				return errors.Join(fmt.Errorf("apply migration %d: %w", migration.version, err), fmt.Errorf("rollback migration %d: %w", migration.version, rollbackErr))
			}
			return fmt.Errorf("apply migration %d: %w", migration.version, err)
		}
		// 只有 SQL 完整执行后才能提交版本事实。
		if err := tx.Commit(); err != nil {
			commitErr := fmt.Errorf("commit migration %d: %w", migration.version, err)
			if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
				return errors.Join(commitErr, fmt.Errorf("rollback migration %d: %w", migration.version, rollbackErr))
			}
			return commitErr
		}
		currentVersion = migration.version
	}
	return nil
}

// currentMigrationVersion 读取已提交的最高迁移版本；新库尚无版本表时视为零。
func currentMigrationVersion(ctx context.Context, db *sql.DB) (int, error) {
	var exists bool
	if err := db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM sqlite_master
			WHERE type = 'table' AND name = 'schema_migrations'
		)`).Scan(&exists); err != nil {
		return 0, fmt.Errorf("inspect migration table: %w", err)
	}
	if !exists {
		return 0, nil
	}

	var version int
	if err := db.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&version); err != nil {
		return 0, fmt.Errorf("read migration version: %w", err)
	}
	return version, nil
}

// applyMigration 在单个事务中应用一个迁移。
func applyMigration(ctx context.Context, tx *sql.Tx, migration migration) error {
	if _, err := tx.ExecContext(ctx, migration.sql); err != nil {
		return fmt.Errorf("execute migration SQL: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO schema_migrations(version, applied_at_utc)
		VALUES (?, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))`, migration.version); err != nil {
		return fmt.Errorf("record migration version: %w", err)
	}
	return nil
}
