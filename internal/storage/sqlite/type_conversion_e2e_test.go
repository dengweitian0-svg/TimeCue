package sqlite

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/app"
	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// conversionClock 为跨层转换测试提供可变但每次只返回一个环境快照的时钟。
type conversionClock struct {
	snapshot domain.TimeSnapshot
	calls    int
}

// Snapshot 返回当前测试环境快照。
func (c *conversionClock) Snapshot(context.Context) (domain.TimeSnapshot, error) {
	c.calls++
	return c.snapshot, nil
}

// conversionService 创建使用真实 SQLite 和共享变更协调器的应用服务。
func conversionService(t *testing.T, store *Store, clock *conversionClock, coordinator *app.MutationCoordinator) *app.Service {
	t.Helper()
	return app.NewServiceWithCoordinator(store, clock, coordinator)
}

// openConversionStore 打开重启回归所需的真实 SQLite Store，并统一使用测试日志器。
func openConversionStore(t *testing.T, path string) *Store {
	t.Helper()
	store, err := OpenStore(context.Background(), path, testBackupDir(path), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("OpenStore(%s) error = %v", path, err)
	}
	return store
}

// insertConversionFacts 写入测试所需的取消例外、移动例外和提醒终态事实。
func insertConversionFacts(t *testing.T, store *Store, scheduleID domain.ScheduleID, location *time.Location) {
	t.Helper()
	if _, err := store.db.Exec("INSERT INTO occurrence_overrides(schedule_id, occurrence_local, open) VALUES (?, '2026-01-01', 0)", scheduleID); err != nil {
		t.Fatalf("insert first cancellation error = %v", err)
	}
	if _, err := store.db.Exec("INSERT INTO occurrence_overrides(schedule_id, occurrence_local, open) VALUES (?, '2026-01-02', 0)", scheduleID); err != nil {
		t.Fatalf("insert second cancellation error = %v", err)
	}
	if _, err := store.db.Exec(`INSERT INTO occurrence_overrides(schedule_id, occurrence_local, open, actual_start_local, duration_minutes, title, notes)
		VALUES (?, '2026-01-03', 1, ?, 60, 'moved', 'moved note')`, scheduleID, domain.FormatLocalTime(time.Date(2026, 1, 3, 12, 0, 0, 0, location))); err != nil {
		t.Fatalf("insert moved override error = %v", err)
	}
	for _, fact := range []struct {
		date, attemptedAt, status string
	}{
		{date: "2026-01-01", attemptedAt: "2025-12-31T23:00:00Z", status: "submitted"},
		{date: "2026-01-02", attemptedAt: "2026-01-01T23:00:00Z", status: "failed"},
		{date: "2026-01-03", attemptedAt: "2026-01-02T23:00:00Z", status: "skipped"},
	} {
		if _, err := store.db.Exec("INSERT INTO reminder_attempts(schedule_id, occurrence_local, attempted_at_utc, status) VALUES (?, ?, ?, ?)", scheduleID, fact.date, fact.attemptedAt, fact.status); err != nil {
			t.Fatalf("insert reminder fact %s error = %v", fact.date, err)
		}
	}
}

// countConversionRows 返回指定日程关联事实的行数。
func countConversionRows(t *testing.T, store *Store, table string, scheduleID domain.ScheduleID) int {
	t.Helper()
	var count int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE schedule_id = ?", int64(scheduleID)).Scan(&count); err != nil {
		t.Fatalf("count %s error = %v", table, err)
	}
	return count
}

// assertConversionFactsUnchanged 验证类型拒绝没有改变日程定义或关联事实。
func assertConversionFactsUnchanged(t *testing.T, store *Store, before, after domain.Schedule) {
	t.Helper()
	assertSameSchedule(t, after, before)
	for _, table := range []string{"occurrence_overrides", "reminder_attempts"} {
		if countConversionRows(t, store, table, before.ID) != 3 {
			t.Fatalf("rejected conversion changed %s", table)
		}
	}
}

// TestScheduleTypeConversionRejectedByApplication 验证应用层在读取时钟和写入前拒绝重复转单次。
func TestScheduleTypeConversionRejectedByApplication(t *testing.T) {
	store := openTestStore(t)
	location := time.UTC
	clock := &conversionClock{snapshot: domain.TimeSnapshot{Now: time.Date(2026, 1, 1, 8, 0, 0, 0, location), Location: location}}
	coordinator := app.NewMutationCoordinator()
	service := conversionService(t, store, clock, coordinator)
	cron := "0 9 * * *"
	start := time.Date(2026, 1, 1, 9, 0, 0, 0, location).Unix()
	end := time.Date(2026, 1, 1, 10, 0, 0, 0, location).Unix()
	until := time.Date(2026, 1, 10, 12, 0, 0, 0, location).Unix()
	created, err := service.CreateSchedule(context.Background(), app.CreateScheduleRequest{
		Title: "conversion", StartUnix: &start, EndUnix: &end, Cron: &cron, UntilUnix: &until,
	})
	if err != nil {
		t.Fatalf("CreateSchedule() error = %v", err)
	}
	insertConversionFacts(t, store, domain.ScheduleID(created.ID), location)
	before, err := store.GetSchedule(context.Background(), domain.ScheduleID(created.ID))
	if err != nil {
		t.Fatalf("GetSchedule(before) error = %v", err)
	}
	beforeEpoch := coordinator.Epoch()
	clock.calls = 0
	_, err = service.UpdateSchedule(context.Background(), domain.ScheduleID(created.ID), app.UpdateScheduleRequest{
		CronPresent: true, ExpectedRevision: created.Revision,
	})
	assertConversionValidation(t, err)
	if clock.calls != 0 || coordinator.Epoch() != beforeEpoch {
		t.Fatalf("rejected conversion side effects: clock=%d, epoch %d -> %d", clock.calls, beforeEpoch, coordinator.Epoch())
	}
	after, err := store.GetSchedule(context.Background(), domain.ScheduleID(created.ID))
	if err != nil {
		t.Fatalf("GetSchedule(after) error = %v", err)
	}
	assertConversionFactsUnchanged(t, store, before, after)
}

// TestScheduleTypeConversionRejectedBothDirections 验证单次转重复也在读取时钟和写入前被拒绝。
func TestScheduleTypeConversionRejectedBothDirections(t *testing.T) {
	store := openTestStore(t)
	location := time.UTC
	clock := &conversionClock{snapshot: domain.TimeSnapshot{Now: time.Date(2026, 1, 1, 8, 0, 0, 0, location), Location: location}}
	coordinator := app.NewMutationCoordinator()
	service := conversionService(t, store, clock, coordinator)
	start := time.Date(2026, 1, 1, 9, 0, 0, 0, location).Unix()
	end := time.Date(2026, 1, 1, 10, 0, 0, 0, location).Unix()
	created, err := service.CreateSchedule(context.Background(), app.CreateScheduleRequest{Title: "single", StartUnix: &start, EndUnix: &end})
	if err != nil {
		t.Fatalf("CreateSchedule() error = %v", err)
	}
	insertConversionFacts(t, store, domain.ScheduleID(created.ID), location)
	before, err := store.GetSchedule(context.Background(), domain.ScheduleID(created.ID))
	if err != nil {
		t.Fatalf("GetSchedule(before) error = %v", err)
	}
	beforeGeneration, err := store.QueryGeneration(context.Background())
	if err != nil {
		t.Fatalf("QueryGeneration(before) error = %v", err)
	}
	cron := "0 9 * * *"
	until := time.Date(2026, 1, 3, 12, 0, 0, 0, location).Unix()
	beforeEpoch := coordinator.Epoch()
	clock.calls = 0
	_, err = service.UpdateSchedule(context.Background(), domain.ScheduleID(created.ID), app.UpdateScheduleRequest{Cron: &cron, UntilUnix: &until, ExpectedRevision: created.Revision})
	assertConversionValidation(t, err)
	if clock.calls != 0 || coordinator.Epoch() != beforeEpoch {
		t.Fatalf("rejected conversion side effects: clock=%d, epoch %d -> %d", clock.calls, beforeEpoch, coordinator.Epoch())
	}
	got, err := store.GetSchedule(context.Background(), domain.ScheduleID(created.ID))
	if err != nil {
		t.Fatalf("GetSchedule() after rejected conversion error = %v", err)
	}
	assertConversionFactsUnchanged(t, store, before, got)
	afterGeneration, err := store.QueryGeneration(context.Background())
	if err != nil || afterGeneration != beforeGeneration {
		t.Fatalf("generation = %d, error = %v; want %d", afterGeneration, err, beforeGeneration)
	}
}

// TestScheduleTypeConversionKeepsErrorPriority 验证缺失日程和旧 revision 优先于类型拒绝。
func TestScheduleTypeConversionKeepsErrorPriority(t *testing.T) {
	store := openTestStore(t)
	location := time.UTC
	clock := &conversionClock{snapshot: domain.TimeSnapshot{Now: time.Date(2026, 1, 1, 8, 0, 0, 0, location), Location: location}}
	coordinator := app.NewMutationCoordinator()
	service := conversionService(t, store, clock, coordinator)
	cron := "0 9 * * *"
	start := time.Date(2026, 1, 1, 9, 0, 0, 0, location).Unix()
	end := time.Date(2026, 1, 1, 10, 0, 0, 0, location).Unix()
	until := time.Date(2026, 1, 10, 12, 0, 0, 0, location).Unix()
	created, err := service.CreateSchedule(context.Background(), app.CreateScheduleRequest{
		Title: "priority", StartUnix: &start, EndUnix: &end, Cron: &cron, UntilUnix: &until,
	})
	if err != nil {
		t.Fatalf("CreateSchedule() error = %v", err)
	}

	clock.calls = 0
	_, err = service.UpdateSchedule(context.Background(), 9999, app.UpdateScheduleRequest{CronPresent: true, ExpectedRevision: 1})
	var missingErr *domain.Error
	if !errors.As(err, &missingErr) || missingErr.Code != domain.NotFound {
		t.Fatalf("missing schedule error = %#v, want not found", err)
	}
	if clock.calls != 0 {
		t.Fatalf("missing schedule read clock %d times", clock.calls)
	}

	advancedTitle := "advanced"
	if _, err := store.UpdateSchedule(context.Background(), domain.ScheduleID(created.ID), domain.ScheduleUpdateCandidate{
		Title: &advancedTitle, ExpectedRevision: created.Revision, UpdatedAtUTC: time.Date(2026, 1, 1, 8, 1, 0, 0, location),
	}); err != nil {
		t.Fatalf("advance revision error = %v", err)
	}
	beforeEpoch := coordinator.Epoch()
	clock.calls = 0
	_, err = service.UpdateSchedule(context.Background(), domain.ScheduleID(created.ID), app.UpdateScheduleRequest{
		CronPresent: true, ExpectedRevision: created.Revision,
	})
	var staleErr *domain.Error
	if !errors.As(err, &staleErr) || staleErr.Code != domain.StaleRevision {
		t.Fatalf("stale conversion error = %#v, want stale revision", err)
	}
	if clock.calls != 0 || coordinator.Epoch() != beforeEpoch {
		t.Fatalf("stale conversion side effects: clock=%d, epoch %d -> %d", clock.calls, beforeEpoch, coordinator.Epoch())
	}
}

// TestScheduleSameTypeEditStillSucceeds 验证禁止类型转换后同类型编辑仍提交并保留关联事实。
func TestScheduleSameTypeEditStillSucceeds(t *testing.T) {
	store := openTestStore(t)
	location := time.UTC
	clock := &conversionClock{snapshot: domain.TimeSnapshot{Now: time.Date(2026, 1, 1, 8, 0, 0, 0, location), Location: location}}
	service := conversionService(t, store, clock, app.NewMutationCoordinator())
	cron := "0 9 * * *"
	start := time.Date(2026, 1, 1, 9, 0, 0, 0, location).Unix()
	end := time.Date(2026, 1, 1, 10, 0, 0, 0, location).Unix()
	until := time.Date(2026, 1, 10, 12, 0, 0, 0, location).Unix()
	created, err := service.CreateSchedule(context.Background(), app.CreateScheduleRequest{
		Title: "same type", StartUnix: &start, EndUnix: &end, Cron: &cron, UntilUnix: &until,
	})
	if err != nil {
		t.Fatalf("CreateSchedule() error = %v", err)
	}
	insertConversionFacts(t, store, domain.ScheduleID(created.ID), location)
	beforeGeneration, err := store.QueryGeneration(context.Background())
	if err != nil {
		t.Fatalf("QueryGeneration(before) error = %v", err)
	}
	notes := "same type edit"
	updated, err := service.UpdateSchedule(context.Background(), domain.ScheduleID(created.ID), app.UpdateScheduleRequest{
		Notes: &notes, ExpectedRevision: created.Revision,
	})
	if err != nil {
		t.Fatalf("same-type UpdateSchedule() error = %v", err)
	}
	if updated.Notes != notes || updated.Revision != created.Revision+1 || updated.PreservedOverrideCount != 3 {
		t.Fatalf("same-type update = %+v", updated)
	}
	afterGeneration, err := store.QueryGeneration(context.Background())
	if err != nil || afterGeneration <= beforeGeneration {
		t.Fatalf("generation = %d, error = %v; want greater than %d", afterGeneration, err, beforeGeneration)
	}
	if countConversionRows(t, store, "occurrence_overrides", domain.ScheduleID(created.ID)) != 3 || countConversionRows(t, store, "reminder_attempts", domain.ScheduleID(created.ID)) != 3 {
		t.Fatal("same-type edit changed related facts")
	}
}

// TestScheduleFactsReadableAfterRestart 验证两类日程、例外和提醒历史在数据库重启后仍可读取。
func TestScheduleFactsReadableAfterRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "restart.db")
	store := openConversionStore(t, path)
	closed := false
	defer func() {
		if !closed {
			if err := store.Close(); err != nil {
				t.Error(err)
			}
		}
	}()

	location := time.FixedZone("UTC+8", 8*60*60)
	cron := "0 10 * * *"
	until := "2026-02-02"
	recurringCandidate := createTestSchedule()
	recurringCandidate.Title = "restart recurring"
	recurringCandidate.Cron = &cron
	recurringCandidate.UntilDateLocal = &until
	recurring, err := store.CreateSchedule(ctx, recurringCandidate)
	if err != nil {
		t.Fatalf("CreateSchedule(recurring) error = %v", err)
	}
	singleCandidate := createTestSchedule()
	singleCandidate.Title = "restart single"
	single, err := store.CreateSchedule(ctx, singleCandidate)
	if err != nil {
		t.Fatalf("CreateSchedule(single) error = %v", err)
	}
	insertConversionFacts(t, store, recurring.ID, location)
	insertConversionFacts(t, store, single.ID, location)
	if err := store.Close(); err != nil {
		t.Fatalf("Store.Close() error = %v", err)
	}
	closed = true

	reopened := openConversionStore(t, path)
	defer func() {
		if err := reopened.Close(); err != nil {
			t.Error(err)
		}
	}()
	for _, expected := range []struct {
		id        domain.ScheduleID
		recurring bool
	}{
		{id: recurring.ID, recurring: true},
		{id: single.ID, recurring: false},
	} {
		got, err := reopened.GetSchedule(ctx, expected.id)
		if err != nil {
			t.Fatalf("GetSchedule(%d) after restart error = %v", expected.id, err)
		}
		if got.IsRecurring() != expected.recurring {
			t.Fatalf("schedule %d recurring = %v, want %v", expected.id, got.IsRecurring(), expected.recurring)
		}
		overrides, err := reopened.ListOccurrenceOverrides(ctx, expected.id)
		if err != nil || len(overrides) != 3 {
			t.Fatalf("schedule %d overrides = %d, error = %v", expected.id, len(overrides), err)
		}
		source, err := reopened.GetOccurrenceSource(ctx, expected.id)
		if err != nil || len(source.Overrides) != 3 || source.Schedule.IsRecurring() != expected.recurring {
			t.Fatalf("schedule %d source = %+v, error = %v", expected.id, source, err)
		}
		for _, fact := range []struct {
			date   string
			status app.ReminderAttemptStatus
		}{
			{date: "2026-01-01", status: app.ReminderAttemptSubmitted},
			{date: "2026-01-02", status: app.ReminderAttemptFailed},
			{date: "2026-01-03", status: app.ReminderAttemptSkipped},
		} {
			record, err := reopened.GetReminderAttempt(ctx, domain.OccurrenceIdentity{ScheduleID: expected.id, OriginalDate: fact.date})
			if err != nil || record.Status != fact.status {
				t.Fatalf("schedule %d reminder %s = %+v, error = %v", expected.id, fact.date, record, err)
			}
		}
	}
}

// assertConversionValidation 检查类型转换返回统一的应用层校验错误。
func assertConversionValidation(t *testing.T, err error) {
	t.Helper()
	var businessErr *domain.Error
	if !errors.As(err, &businessErr) || businessErr.Code != domain.Validation ||
		len(businessErr.Fields) != 1 || businessErr.Fields[0] != "cron" {
		t.Fatalf("conversion error = %#v, want VALIDATION on cron", err)
	}
}

// TestScheduleTypeConversionKeepsMigrationFactsReadable 验证转换测试使用最新迁移创建的事实仍可由应用端口读取。
func TestScheduleTypeConversionKeepsMigrationFactsReadable(t *testing.T) {
	store := openTestStore(t)
	if store == nil {
		t.Fatal("test store was not initialized")
	}
	var version int
	if err := store.db.QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&version); err != nil {
		t.Fatalf("read migration version error = %v", err)
	}
	if version != latestMigrationVersion() {
		t.Fatalf("migration version = %d, want %d", version, latestMigrationVersion())
	}
}
