package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/app"
	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// conversionClock 为跨层转换测试提供可变但每次只返回一个环境快照的时钟。
type conversionClock struct {
	snapshot domain.TimeSnapshot
}

// Snapshot 返回当前测试环境快照。
func (c *conversionClock) Snapshot(context.Context) (domain.TimeSnapshot, error) {
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

// TestScheduleTypeConversionLifecycle 验证真实 SQLite 中带例外和提醒事实的类型来回转换生命周期。
func TestScheduleTypeConversionLifecycle(t *testing.T) {
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
	// 第二个日程提供一个已提交的冲突事实，转换响应不能只使用预读结果。
	otherStart := time.Date(2026, 1, 3, 12, 0, 0, 0, location).Unix()
	otherEnd := time.Date(2026, 1, 3, 13, 0, 0, 0, location).Unix()
	if _, err := service.CreateSchedule(context.Background(), app.CreateScheduleRequest{Title: "other", StartUnix: &otherStart, EndUnix: &otherEnd}); err != nil {
		t.Fatalf("CreateSchedule(other) error = %v", err)
	}

	beforeEpoch := coordinator.Epoch()
	converted, err := service.UpdateSchedule(context.Background(), domain.ScheduleID(created.ID), app.UpdateScheduleRequest{
		CronPresent: true, ExpectedRevision: created.Revision,
	})
	if err != nil {
		t.Fatalf("recurring to single error = %v", err)
	}
	if converted.Cron != nil || !converted.Open || converted.FirstStartLocal != "2026-01-03T12:00:00Z" ||
		converted.PreservedOverrideCount != 3 || coordinator.Epoch() != beforeEpoch+1 {
		t.Fatalf("converted schedule = %+v, epoch %d -> %d", converted, beforeEpoch, coordinator.Epoch())
	}
	if converted.Conflict == nil || !converted.Conflict.Checked || len(converted.Conflict.Conflicts) == 0 {
		t.Fatalf("converted conflict = %+v", converted.Conflict)
	}
	source, err := store.GetOccurrenceSource(context.Background(), domain.ScheduleID(created.ID))
	if err != nil {
		t.Fatalf("GetOccurrenceSource() error = %v", err)
	}
	occurrences, err := domain.ExpandMergedOccurrences(source.Schedule, source.Overrides,
		time.Date(2026, 1, 1, 0, 0, 0, 0, location), time.Date(2026, 1, 5, 0, 0, 0, 0, location), location)
	if err != nil || len(occurrences) != 1 || occurrences[0].OriginalDate != "2026-01-03" || occurrences[0].StartLocal.Hour() != 12 {
		t.Fatalf("single occurrences = %+v, error = %v", occurrences, err)
	}
	if countConversionRows(t, store, "occurrence_overrides", domain.ScheduleID(created.ID)) != 3 || countConversionRows(t, store, "reminder_attempts", domain.ScheduleID(created.ID)) != 3 {
		t.Fatal("conversion dropped related facts")
	}
	for _, date := range []string{"2026-01-01", "2026-01-02", "2026-01-03"} {
		claimed, err := store.ClaimReminderAttempt(context.Background(), domain.OccurrenceIdentity{ScheduleID: domain.ScheduleID(created.ID), OriginalDate: date}, clock.snapshot.Now)
		if err != nil || claimed {
			t.Fatalf("ClaimReminderAttempt(%s) = %v, %v; existing terminal fact was resent", date, claimed, err)
		}
	}

	backCron := "0 9 * * *"
	backUntil := time.Date(2026, 1, 15, 12, 0, 0, 0, location).Unix()
	back, err := service.UpdateSchedule(context.Background(), domain.ScheduleID(created.ID), app.UpdateScheduleRequest{
		Cron: &backCron, UntilUnix: &backUntil, ExpectedRevision: converted.Revision,
	})
	if err != nil {
		t.Fatalf("single to recurring error = %v", err)
	}
	if back.Cron == nil || *back.Cron != backCron || back.UntilDateLocal == nil || *back.UntilDateLocal != "2026-01-15" || !back.Open {
		t.Fatalf("recurring schedule after round trip = %+v", back)
	}
	if countConversionRows(t, store, "occurrence_overrides", domain.ScheduleID(created.ID)) != 3 || countConversionRows(t, store, "reminder_attempts", domain.ScheduleID(created.ID)) != 3 {
		t.Fatal("round trip dropped related facts")
	}
	if back.Conflict == nil || !back.Conflict.Checked {
		t.Fatalf("round trip conflict = %+v", back.Conflict)
	}
}

// TestScheduleTypeConversionWithoutCandidateDoesNotWrite 验证过期定义转换失败时原日程保持不变且无关联副作用。
func TestScheduleTypeConversionWithoutCandidateDoesNotWrite(t *testing.T) {
	store := openTestStore(t)
	location := time.UTC
	clock := &conversionClock{snapshot: domain.TimeSnapshot{Now: time.Date(2026, 1, 1, 8, 0, 0, 0, location), Location: location}}
	service := conversionService(t, store, clock, app.NewMutationCoordinator())
	cron := "0 9 * * *"
	start := time.Date(2026, 1, 1, 9, 0, 0, 0, location).Unix()
	end := time.Date(2026, 1, 1, 10, 0, 0, 0, location).Unix()
	until := time.Date(2026, 1, 3, 12, 0, 0, 0, location).Unix()
	created, err := service.CreateSchedule(context.Background(), app.CreateScheduleRequest{Title: "expired", StartUnix: &start, EndUnix: &end, Cron: &cron, UntilUnix: &until})
	if err != nil {
		t.Fatalf("CreateSchedule() error = %v", err)
	}
	clock.snapshot.Now = time.Date(2026, 1, 4, 8, 0, 0, 0, location)
	if _, err := service.UpdateSchedule(context.Background(), domain.ScheduleID(created.ID), app.UpdateScheduleRequest{CronPresent: true, ExpectedRevision: created.Revision}); err == nil {
		t.Fatal("expired conversion error = nil")
	}
	got, err := store.GetSchedule(context.Background(), domain.ScheduleID(created.ID))
	if err != nil {
		t.Fatalf("GetSchedule() after rejected conversion error = %v", err)
	}
	if got.Cron == nil || got.Revision != created.Revision || got.UntilDateLocal == nil || *got.UntilDateLocal != "2026-01-03" {
		t.Fatalf("rejected conversion changed schedule = %+v", got)
	}
	clock.snapshot.Now = time.Date(2026, 1, 1, 8, 0, 0, 0, location)
	converted, err := service.UpdateSchedule(context.Background(), domain.ScheduleID(created.ID), app.UpdateScheduleRequest{CronPresent: true, ExpectedRevision: created.Revision})
	if err != nil {
		t.Fatalf("conversion without exceptions error = %v", err)
	}
	if converted.Cron != nil || converted.PreservedOverrideCount != 0 || countConversionRows(t, store, "occurrence_overrides", domain.ScheduleID(created.ID)) != 0 || countConversionRows(t, store, "reminder_attempts", domain.ScheduleID(created.ID)) != 0 {
		t.Fatalf("conversion without exceptions = %+v", converted)
	}
	backCron := "0 9 * * *"
	backUntil := time.Date(2026, 1, 5, 12, 0, 0, 0, location).Unix()
	if _, err := service.UpdateSchedule(context.Background(), domain.ScheduleID(created.ID), app.UpdateScheduleRequest{Cron: &backCron, UntilUnix: &backUntil, ExpectedRevision: converted.Revision}); err != nil {
		t.Fatalf("round trip without exceptions error = %v", err)
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
