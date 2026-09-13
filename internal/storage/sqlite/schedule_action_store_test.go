package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// createOpenRecurringSchedule 创建可用于整组动作测试的开放重复日程。
func createOpenRecurringSchedule(t *testing.T) (context.Context, *Store, domain.Schedule) {
	t.Helper()
	ctx := context.Background()
	candidate := recurringCandidate()
	candidate.Open = true
	store := openTestStore(t)
	schedule, err := store.CreateSchedule(ctx, candidate)
	if err != nil {
		t.Fatalf("CreateSchedule() error = %v", err)
	}
	return ctx, store, schedule
}

// TestScheduleActionsCancelRestore 验证整组取消/恢复只变更 open、revision 和审计时间。
func TestScheduleActionsCancelRestore(t *testing.T) {
	ctx, store, schedule := createOpenRecurringSchedule(t)
	cancelAt := time.Date(2026, 2, 3, 4, 5, 6, 0, time.FixedZone("local", 8*60*60))
	cancelled, err := store.CancelSchedule(ctx, schedule.ID, domain.ScheduleActionCandidate{ExpectedRevision: schedule.Revision, UpdatedAtUTC: cancelAt})
	if err != nil {
		t.Fatalf("CancelSchedule() error = %v", err)
	}
	if cancelled.Open || cancelled.Revision != schedule.Revision+1 || !cancelled.UpdatedAtUTC.Equal(cancelAt.UTC()) || !cancelled.Changed {
		t.Fatalf("cancelled = %+v", cancelled)
	}

	restored, err := store.RestoreSchedule(ctx, schedule.ID, domain.ScheduleActionCandidate{ExpectedRevision: cancelled.Revision, UpdatedAtUTC: cancelAt.Add(time.Hour)})
	if err != nil {
		t.Fatalf("RestoreSchedule() error = %v", err)
	}
	if !restored.Open || restored.Revision != cancelled.Revision+1 || !restored.UpdatedAtUTC.Equal(cancelAt.Add(time.Hour).UTC()) || !restored.Changed {
		t.Fatalf("restored = %+v", restored)
	}
}

// TestScheduleActionsAreIdempotent 验证相同目标状态不递增版本也不覆盖审计时间。
func TestScheduleActionsAreIdempotent(t *testing.T) {
	ctx, store, schedule := createOpenRecurringSchedule(t)
	at := schedule.UpdatedAtUTC.Add(time.Minute)
	cancelled, err := store.CancelSchedule(ctx, schedule.ID, domain.ScheduleActionCandidate{ExpectedRevision: schedule.Revision, UpdatedAtUTC: at})
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := store.CancelSchedule(ctx, schedule.ID, domain.ScheduleActionCandidate{ExpectedRevision: cancelled.Revision, UpdatedAtUTC: at.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if repeated.Changed || repeated.Open || repeated.Revision != cancelled.Revision || !repeated.UpdatedAtUTC.Equal(cancelled.UpdatedAtUTC) {
		t.Fatalf("repeated cancellation = %+v", repeated)
	}
	restored, err := store.RestoreSchedule(ctx, schedule.ID, domain.ScheduleActionCandidate{ExpectedRevision: repeated.Revision, UpdatedAtUTC: at.Add(2 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	repeatedRestore, err := store.RestoreSchedule(ctx, schedule.ID, domain.ScheduleActionCandidate{ExpectedRevision: restored.Revision, UpdatedAtUTC: at.Add(3 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if repeatedRestore.Changed || !repeatedRestore.Open || repeatedRestore.Revision != restored.Revision || !repeatedRestore.UpdatedAtUTC.Equal(restored.UpdatedAtUTC) {
		t.Fatalf("repeated restoration = %+v", repeatedRestore)
	}
}

// TestScheduleActionsRejectSingleStaleAndMissing 验证单次、旧版本和已删除日程不会改变状态。
func TestScheduleActionsRejectSingleStaleAndMissing(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	single, err := store.CreateSchedule(ctx, createTestSchedule())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CancelSchedule(ctx, single.ID, domain.ScheduleActionCandidate{ExpectedRevision: single.Revision}); err == nil {
		t.Fatal("single cancellation error = nil")
	}
	candidate := recurringCandidate()
	candidate.Open = true
	recurring, err := store.CreateSchedule(ctx, candidate)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CancelSchedule(ctx, recurring.ID, domain.ScheduleActionCandidate{ExpectedRevision: recurring.Revision + 1})
	var businessErr *domain.Error
	if !errors.As(err, &businessErr) || businessErr.Code != domain.StaleRevision {
		t.Fatalf("stale error = %#v", err)
	}
	if _, err := store.RestoreSchedule(ctx, 999, domain.ScheduleActionCandidate{ExpectedRevision: 1}); !errors.As(err, &businessErr) || businessErr.Code != domain.NotFound {
		t.Fatalf("missing restore error = %#v", err)
	}
}

// TestScheduleActionsPreserveOccurrenceFacts 验证整组状态转换不删除例外和提醒事实。
func TestScheduleActionsPreserveOccurrenceFacts(t *testing.T) {
	ctx, store, schedule := createOpenRecurringSchedule(t)
	start := time.Date(2026, 1, 20, 10, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	if _, err := store.UpdateOccurrence(ctx, schedule.ID, domain.OccurrenceOverrideCandidate{OriginalDate: "2026-01-05", StartLocal: start, DurationMinutes: 60, Title: "moved", Notes: "kept", ExpectedRevision: schedule.Revision, UpdatedAtUTC: schedule.UpdatedAtUTC.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	schedule, err := store.GetSchedule(ctx, schedule.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec("INSERT INTO reminder_attempts(schedule_id, occurrence_local, attempted_at_utc, status) VALUES (?, ?, ?, ?)", schedule.ID, "2026-01-05", "2026-01-05T02:00:00Z", "sent"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CancelSchedule(ctx, schedule.ID, domain.ScheduleActionCandidate{ExpectedRevision: schedule.Revision, UpdatedAtUTC: schedule.UpdatedAtUTC.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	var overrideCount, reminderCount int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM occurrence_overrides WHERE schedule_id = ?", schedule.ID).Scan(&overrideCount); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow("SELECT COUNT(*) FROM reminder_attempts WHERE schedule_id = ?", schedule.ID).Scan(&reminderCount); err != nil {
		t.Fatal(err)
	}
	if overrideCount != 1 || reminderCount != 1 {
		t.Fatalf("facts counts = %d/%d", overrideCount, reminderCount)
	}
}

// TestScheduleActionRollsBackOnUpdateFailure 验证状态更新失败时事务不会留下半套状态。
func TestScheduleActionRollsBackOnUpdateFailure(t *testing.T) {
	ctx, store, schedule := createOpenRecurringSchedule(t)
	if _, err := store.db.Exec("CREATE TRIGGER fail_schedule_action BEFORE UPDATE OF open ON schedules WHEN OLD.id = NEW.id BEGIN SELECT RAISE(ABORT, 'forced action failure'); END"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CancelSchedule(ctx, schedule.ID, domain.ScheduleActionCandidate{ExpectedRevision: schedule.Revision, UpdatedAtUTC: schedule.UpdatedAtUTC.Add(time.Minute)}); err == nil {
		t.Fatal("CancelSchedule() error = nil")
	}
	got, err := store.GetSchedule(ctx, schedule.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Open || got.Revision != schedule.Revision || !got.UpdatedAtUTC.Equal(schedule.UpdatedAtUTC) {
		t.Fatalf("rolled back schedule = %+v", got)
	}
}

// TestOccurrenceActionReturnsParentOpen 验证例外动作在事务内返回父日程 Open 状态。
func TestOccurrenceActionReturnsParentOpen(t *testing.T) {
	ctx, store, schedule := createOpenRecurringSchedule(t)
	if _, err := store.CancelSchedule(ctx, schedule.ID, domain.ScheduleActionCandidate{ExpectedRevision: schedule.Revision, UpdatedAtUTC: schedule.UpdatedAtUTC.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	got, err := store.CancelOccurrence(ctx, schedule.ID, domain.OccurrenceCancelCandidate{OriginalDate: "2026-01-05", ExpectedRevision: schedule.Revision + 1, UpdatedAtUTC: schedule.UpdatedAtUTC.Add(2 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if got.ScheduleOpen || got.Override.Open || got.ScheduleRevision != schedule.Revision+2 {
		t.Fatalf("cancel occurrence result = %+v", got)
	}
}

// TestClosedScheduleEditsKeepOpen 验证关闭组的父定义编辑和例外编辑都不改变整组状态。
func TestClosedScheduleEditsKeepOpen(t *testing.T) {
	ctx, store, schedule := createOpenRecurringSchedule(t)
	cancelled, err := store.CancelSchedule(ctx, schedule.ID, domain.ScheduleActionCandidate{ExpectedRevision: schedule.Revision, UpdatedAtUTC: schedule.UpdatedAtUTC.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	title := "edited while closed"
	updated, err := store.UpdateSchedule(ctx, schedule.ID, domain.ScheduleUpdateCandidate{Title: &title, ExpectedRevision: cancelled.Revision, UpdatedAtUTC: cancelled.UpdatedAtUTC.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Open || updated.Revision != cancelled.Revision+1 {
		t.Fatalf("closed schedule edit = %+v", updated)
	}
	start := time.Date(2026, 1, 25, 10, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	if _, err := store.UpdateOccurrence(ctx, schedule.ID, domain.OccurrenceOverrideCandidate{OriginalDate: "2026-01-05", StartLocal: start, DurationMinutes: 60, Title: "exception edit", ExpectedRevision: updated.Revision, UpdatedAtUTC: updated.UpdatedAtUTC.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetSchedule(ctx, schedule.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Open {
		t.Fatalf("closed schedule after exception edit = %+v", got)
	}
}

// TestClosedGroupRestoresOccurrenceOnly 验证父组关闭时恢复本次只移除例外取消，不重新打开父组。
func TestClosedGroupRestoresOccurrenceOnly(t *testing.T) {
	ctx, store, schedule := createOpenRecurringSchedule(t)
	cancelled, err := store.CancelSchedule(ctx, schedule.ID, domain.ScheduleActionCandidate{ExpectedRevision: schedule.Revision, UpdatedAtUTC: schedule.UpdatedAtUTC.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	occurrence, err := store.CancelOccurrence(ctx, schedule.ID, domain.OccurrenceCancelCandidate{OriginalDate: "2026-01-05", ExpectedRevision: cancelled.Revision, UpdatedAtUTC: cancelled.UpdatedAtUTC.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if occurrence.ScheduleOpen || occurrence.ScheduleRevision != cancelled.Revision+1 {
		t.Fatalf("closed occurrence cancellation = %+v", occurrence)
	}
	restored, err := store.RestoreOccurrence(ctx, schedule.ID, domain.OccurrenceRestoreCandidate{OriginalDate: "2026-01-05", ExpectedRevision: occurrence.ScheduleRevision, UpdatedAtUTC: cancelled.UpdatedAtUTC.Add(2 * time.Minute), RemoveCancellation: true})
	if err != nil {
		t.Fatal(err)
	}
	if restored.ScheduleOpen || restored.ScheduleRevision != occurrence.ScheduleRevision+1 || restored.OverridePresent {
		t.Fatalf("closed occurrence restoration = %+v", restored)
	}
	got, err := store.GetSchedule(ctx, schedule.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Open {
		t.Fatalf("parent reopened by occurrence restoration = %+v", got)
	}
}
