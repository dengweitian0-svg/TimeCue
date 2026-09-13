package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// TestMonthEndPolicyRoundTripAndClear 验证策略创建、读取、列表、保留和显式清空。
func TestMonthEndPolicyRoundTripAndClear(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	policy := domain.MonthEndPolicyClamp
	candidate := recurringCandidate()
	candidate.MonthEndPolicy = &policy

	created, err := store.CreateSchedule(ctx, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if created.MonthEndPolicy == nil || *created.MonthEndPolicy != domain.MonthEndPolicyClamp {
		t.Fatalf("created policy = %#v", created.MonthEndPolicy)
	}

	title := "preserve policy"
	updated, err := store.UpdateSchedule(ctx, created.ID, domain.ScheduleUpdateCandidate{
		Title:            &title,
		ExpectedRevision: created.Revision,
		UpdatedAtUTC:     created.UpdatedAtUTC.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.MonthEndPolicy == nil || *updated.MonthEndPolicy != domain.MonthEndPolicyClamp {
		t.Fatalf("preserved policy = %#v", updated.MonthEndPolicy)
	}

	cleared, err := store.UpdateSchedule(ctx, created.ID, domain.ScheduleUpdateCandidate{
		MonthEndPolicyPresent: true,
		ExpectedRevision:      updated.Revision,
		UpdatedAtUTC:          updated.UpdatedAtUTC.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if cleared.MonthEndPolicy != nil {
		t.Fatalf("cleared policy = %#v", cleared.MonthEndPolicy)
	}
}

// TestMonthEndPolicyMigrationRollsBackOnVersionFailure 验证策略迁移失败时列和版本记录一起回滚。
func TestMonthEndPolicyMigrationRollsBackOnVersionFailure(t *testing.T) {
	db := openRawTestDB(t)
	ctx := context.Background()
	if _, err := db.Exec(initialMigrationSQL); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(recurringCutoffMigrationSQL); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO schema_migrations VALUES (1, 'now'), (2, 'now');
		CREATE TRIGGER reject_v3 BEFORE INSERT ON schema_migrations
		WHEN NEW.version = 3 BEGIN SELECT RAISE(ABORT, 'forced version failure'); END`); err != nil {
		t.Fatal(err)
	}

	if err := migrate(ctx, db); err == nil {
		t.Fatal("expected migration failure")
	}
	var columns, version int
	if err := db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('schedules') WHERE name='month_end_policy'").Scan(&columns); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if columns != 0 || version != 2 {
		t.Fatalf("partial migration: columns=%d version=%d", columns, version)
	}
}

// TestMonthEndPolicyWriteFailureDoesNotSucceed 验证策略写入失败时不会返回成功或留下更新。
func TestMonthEndPolicyWriteFailureDoesNotSucceed(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	created, err := store.CreateSchedule(ctx, recurringCandidate())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`
		CREATE TRIGGER break_policy_update AFTER UPDATE ON schedules
		BEGIN UPDATE schedules SET month_end_policy = 'broken' WHERE id = NEW.id; END`); err != nil {
		t.Fatal(err)
	}

	policy := domain.MonthEndPolicyClamp
	_, err = store.UpdateSchedule(ctx, created.ID, domain.ScheduleUpdateCandidate{
		MonthEndPolicy:        &policy,
		MonthEndPolicyPresent: true,
		ExpectedRevision:      created.Revision,
		UpdatedAtUTC:          created.UpdatedAtUTC.Add(time.Minute),
	})
	if err == nil {
		t.Fatal("expected policy write failure")
	}

	got, err := store.GetSchedule(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != created.Revision || got.MonthEndPolicy != nil {
		t.Fatalf("failed update changed schedule: %+v", got)
	}
}
