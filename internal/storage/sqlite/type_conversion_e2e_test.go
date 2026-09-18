package sqlite

import (
	"context"
	"errors"
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
	if after.Cron == nil || after.Revision != before.Revision || after.UntilDateLocal == nil || *after.UntilDateLocal != *before.UntilDateLocal {
		t.Fatalf("rejected conversion changed schedule: before=%+v after=%+v", before, after)
	}
	if countConversionRows(t, store, "occurrence_overrides", domain.ScheduleID(created.ID)) != 3 || countConversionRows(t, store, "reminder_attempts", domain.ScheduleID(created.ID)) != 3 {
		t.Fatal("rejected conversion changed related facts")
	}
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
	if got.Cron != nil || got.Revision != created.Revision {
		t.Fatalf("rejected conversion changed schedule = %+v", got)
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
