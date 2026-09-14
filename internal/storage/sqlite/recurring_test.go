package sqlite

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/app"
	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// recurringCandidate 在既有合法候选上补充重复字段。
func recurringCandidate() domain.ScheduleCandidate {
	c := createTestSchedule()
	cron, until := "0 10 * * *", "2026-02-02"
	c.Cron, c.UntilDateLocal = &cron, &until
	c.Open = false
	return c
}

// assertSameSchedule 比较持久化字段，忽略 time.Location 对象的指针差异。
func assertSameSchedule(t *testing.T, got, want domain.Schedule) {
	t.Helper()
	got.FirstStartLocal = got.FirstStartLocal.UTC()
	want.FirstStartLocal = want.FirstStartLocal.UTC()
	got.OriginalStartLocal = got.OriginalStartLocal.UTC()
	want.OriginalStartLocal = want.OriginalStartLocal.UTC()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v; want %+v", got, want)
	}
}

// TestRecurringStoreReopenAndUpdate 验证重复定义重开读取、双向规则切换和字段保留。
func TestRecurringStoreReopenAndUpdate(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "recurring.db")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s, err := OpenStore(ctx, path, testBackupDir(path), logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	created, err := s.CreateSchedule(ctx, recurringCandidate())
	if err != nil {
		t.Fatal(err)
	}
	if created.Cron == nil || *created.Cron != "0 10 * * *" || created.UntilDateLocal == nil || *created.UntilDateLocal != "2026-02-02" {
		t.Fatalf("created = %+v", created)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(ctx, path, testBackupDir(path), logger)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetSchedule(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertSameSchedule(t, got, created)
	for _, cron := range []string{"30 11 * * 1", "45 12 * * *"} {
		updatedAt := got.UpdatedAtUTC.Add(time.Minute)
		updated, err := s.UpdateSchedule(ctx, got.ID, domain.ScheduleUpdateCandidate{Cron: &cron, CronPresent: true, MonthEndPolicyPresent: true, ExpectedRevision: got.Revision, UpdatedAtUTC: updatedAt})
		if err != nil {
			t.Fatal(err)
		}
		want := got
		want.Cron, want.Revision, want.UpdatedAtUTC = &cron, got.Revision+1, updatedAt
		assertSameSchedule(t, updated, want)
		got = updated
	}
	title, until, duration := "new title", "2026-03-01", 120
	first := got.FirstStartLocal.AddDate(0, 0, 1)
	updated, err := s.UpdateSchedule(ctx, got.ID, domain.ScheduleUpdateCandidate{Title: &title, UntilDateLocal: &until, FirstStartLocal: &first, DurationMinutes: &duration, ExpectedRevision: got.Revision, UpdatedAtUTC: got.UpdatedAtUTC.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	want := got
	want.Title, want.UntilDateLocal, want.FirstStartLocal, want.DurationMinutes = title, &until, first, duration
	want.Revision++
	want.UpdatedAtUTC = updated.UpdatedAtUTC
	assertSameSchedule(t, updated, want)
	list, err := s.ListSchedules(ctx, app.ListSchedulesRequest{})
	if err != nil || len(list.Items) != 1 || *list.Items[0].Cron != *got.Cron {
		t.Fatalf("list = %+v %v", list, err)
	}
	assertNoInstances(t, s)
}

// TestRecurringStoreMonthlyPolicySwitch 验证月规则与日规则切换时策略在同一更新事实中清理和恢复。
func TestRecurringStoreMonthlyPolicySwitch(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	monthly := "0 10 31 * *"
	policy := domain.MonthEndPolicyClamp
	candidate := recurringCandidate()
	candidate.Cron = &monthly
	candidate.MonthEndPolicy = &policy
	created, err := store.CreateSchedule(ctx, candidate)
	if err != nil {
		t.Fatal(err)
	}

	daily := "0 10 * * *"
	updated, err := store.UpdateSchedule(ctx, created.ID, domain.ScheduleUpdateCandidate{
		Cron:                  &daily,
		CronPresent:           true,
		MonthEndPolicyPresent: true,
		ExpectedRevision:      created.Revision,
		UpdatedAtUTC:          created.UpdatedAtUTC.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Cron == nil || *updated.Cron != daily || updated.MonthEndPolicy != nil {
		t.Fatalf("month to day = %+v", updated)
	}

	updated, err = store.UpdateSchedule(ctx, updated.ID, domain.ScheduleUpdateCandidate{
		Cron:                  &monthly,
		CronPresent:           true,
		MonthEndPolicy:        &policy,
		MonthEndPolicyPresent: true,
		ExpectedRevision:      updated.Revision,
		UpdatedAtUTC:          updated.UpdatedAtUTC.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Cron == nil || *updated.Cron != monthly || updated.MonthEndPolicy == nil || *updated.MonthEndPolicy != domain.MonthEndPolicyClamp {
		t.Fatalf("day to month = %+v", updated)
	}
}

// assertNoInstances 确认定义操作没有生成实例、提醒尝试或派生字段。
func assertNoInstances(t *testing.T, s *Store) {
	t.Helper()
	for _, table := range []string{"occurrence_overrides", "reminder_attempts"} {
		var count int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s has %d rows", table, count)
		}
	}
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('schedules') WHERE name IN ('frequency', 'next_at', 'end_local')").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("derived columns persisted")
	}
}

// TestRecurringStoreStaleAndRollback 验证旧版本零写入及更新回读失败后的完整回滚。
func TestRecurringStoreStaleAndRollback(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()
	created, err := s.CreateSchedule(ctx, recurringCandidate())
	if err != nil {
		t.Fatal(err)
	}
	cron, until := "30 9 31 * *", "2026-03-03"
	policy := domain.MonthEndPolicyClamp
	patch := domain.ScheduleUpdateCandidate{Cron: &cron, CronPresent: true, MonthEndPolicy: &policy, MonthEndPolicyPresent: true, UntilDateLocal: &until, ExpectedRevision: created.Revision + 1, UpdatedAtUTC: created.UpdatedAtUTC.Add(time.Minute)}
	_, err = s.UpdateSchedule(ctx, created.ID, patch)
	var business *domain.Error
	if !errors.As(err, &business) || business.Code != domain.StaleRevision {
		t.Fatalf("stale = %v", err)
	}
	got, err := s.GetSchedule(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertSameSchedule(t, got, created)
	if _, err := s.db.Exec(`CREATE TRIGGER break_recurring_read AFTER UPDATE ON schedules BEGIN UPDATE schedules SET first_start_local = 'broken' WHERE id = NEW.id; END`); err != nil {
		t.Fatal(err)
	}
	patch.ExpectedRevision = created.Revision
	if _, err := s.UpdateSchedule(ctx, created.ID, patch); err == nil {
		t.Fatal("expected readback failure")
	}
	got, err = s.GetSchedule(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertSameSchedule(t, got, created)
	assertNoInstances(t, s)
}

// TestRecurringCreateRollback 验证重复创建回读失败时连同 cron 和截止日期一起回滚。
func TestRecurringCreateRollback(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.db.Exec(`CREATE TRIGGER break_recurring_insert AFTER INSERT ON schedules BEGIN DELETE FROM schedules WHERE id = NEW.id; END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSchedule(context.Background(), recurringCandidate()); err == nil {
		t.Fatal("expected readback failure")
	}
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM schedules").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("failed creation persisted")
	}
}

// TestMigrateV1ToV2PreservesRows 验证真实版本 1 数据升级时保留字段且截止日期为 NULL。
func TestMigrateV1ToV2PreservesRows(t *testing.T) {
	db := openRawTestDB(t)
	ctx := context.Background()
	if _, err := db.Exec(initialMigrationSQL); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO schema_migrations VALUES (1, '2026-01-01T00:00:00Z'); INSERT INTO schedules(id,title,open,first_start_local,original_start_local,duration_minutes,revision,created_at_utc,updated_at_utc) VALUES (7,'old',1,'2026-01-02T10:00:00+08:00','2026-01-01T10:00:00+08:00',90,4,'2026-01-01T00:00:00Z','2026-01-02T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := db.QueryRow("SELECT json_array(id,title,cron,open,first_start_local,original_start_local,duration_minutes,revision,created_at_utc,updated_at_utc) FROM schedules").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	var after string
	var isNull bool
	if err := db.QueryRow("SELECT json_array(id,title,cron,open,first_start_local,original_start_local,duration_minutes,revision,created_at_utc,updated_at_utc), until_date_local IS NULL FROM schedules").Scan(&after, &isNull); err != nil {
		t.Fatal(err)
	}
	if before != after || !isNull {
		t.Fatalf("migration changed row: %s -> %s, null %v", before, after, isNull)
	}
}

// TestCutoffMigrationRollsBackOnVersionFailure 验证版本记录失败撤销已执行的 ALTER TABLE。
func TestCutoffMigrationRollsBackOnVersionFailure(t *testing.T) {
	db := openRawTestDB(t)
	if _, err := db.Exec(initialMigrationSQL); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO schema_migrations VALUES (1, 'now'); CREATE TRIGGER reject_v2 BEFORE INSERT ON schema_migrations WHEN NEW.version = 2 BEGIN SELECT RAISE(ABORT, 'forced version failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := migrate(context.Background(), db); err == nil {
		t.Fatal("expected migration failure")
	}
	var columns, version int
	if err := db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('schedules') WHERE name='until_date_local'").Scan(&columns); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if columns != 0 || version != 1 {
		t.Fatalf("partial migration: columns=%d version=%d", columns, version)
	}
}
