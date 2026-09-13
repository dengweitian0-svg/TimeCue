package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// TestUpdateOccurrencePersistsCompleteSnapshot 验证例外快照和父日程版本在重开后保持一致。
func TestUpdateOccurrencePersistsCompleteSnapshot(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	created, err := store.CreateSchedule(ctx, createTestSchedule())
	if err != nil {
		t.Fatalf("CreateSchedule() error = %v", err)
	}
	start := time.Date(2026, 9, 20, 10, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	result, err := store.UpdateOccurrence(ctx, created.ID, domain.OccurrenceOverrideCandidate{
		OriginalDate:     "2026-09-07",
		StartLocal:       start,
		DurationMinutes:  90,
		Title:            "moved",
		Notes:            "snapshot",
		ExpectedRevision: created.Revision,
		UpdatedAtUTC:     created.UpdatedAtUTC.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("UpdateOccurrence() error = %v", err)
	}
	if result.ScheduleRevision != created.Revision+1 || !result.Override.Open || !result.Override.SnapshotPresent {
		t.Fatalf("update result = %+v", result)
	}
	if result.Override.OriginalDate != "2026-09-07" || !result.Override.StartLocal.Equal(start) || result.Override.DurationMinutes != 90 || result.Override.Title != "moved" || result.Override.Notes != "snapshot" {
		t.Fatalf("override = %+v", result.Override)
	}

	got, err := store.GetOccurrenceOverride(ctx, created.ID, "2026-09-07")
	if err != nil {
		t.Fatalf("GetOccurrenceOverride() error = %v", err)
	}
	if got.Title != "moved" || got.Notes != "snapshot" || got.DurationMinutes != 90 {
		t.Fatalf("stored override = %+v", got)
	}
	overrides, err := store.ListOccurrenceOverrides(ctx, created.ID)
	if err != nil || len(overrides) != 1 {
		t.Fatalf("ListOccurrenceOverrides() = %+v, %v", overrides, err)
	}
}

// TestUpdateOccurrenceUpsertsByStableIdentity 验证重复修改同一原位置只保留一条并再次推进版本。
func TestUpdateOccurrenceUpsertsByStableIdentity(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	created, err := store.CreateSchedule(ctx, createTestSchedule())
	if err != nil {
		t.Fatal(err)
	}
	for revision, title := range []string{"first", "second"} {
		_, err = store.UpdateOccurrence(ctx, created.ID, domain.OccurrenceOverrideCandidate{
			OriginalDate:     "2026-09-07",
			StartLocal:       time.Date(2026, 9, 20+revision, 10, 0, 0, 0, time.UTC),
			DurationMinutes:  60,
			Title:            title,
			ExpectedRevision: int64(revision + 1),
			UpdatedAtUTC:     created.UpdatedAtUTC.Add(time.Duration(revision+1) * time.Minute),
		})
		if err != nil {
			t.Fatalf("UpdateOccurrence(%d) error = %v", revision, err)
		}
	}
	var count int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM occurrence_overrides WHERE schedule_id = ? AND occurrence_local = ?", created.ID, "2026-09-07").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("override count = %d, want 1", count)
	}
	got, err := store.GetOccurrenceOverride(ctx, created.ID, "2026-09-07")
	if err != nil || got.Title != "second" {
		t.Fatalf("updated override = %+v, %v", got, err)
	}
}

// TestUpdateOccurrenceCompetesOnParentRevision 验证同一父版本的并发例外更新只有一个成功。
func TestUpdateOccurrenceCompetesOnParentRevision(t *testing.T) {
	store := openTestStore(t)
	created, err := store.CreateSchedule(context.Background(), createTestSchedule())
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	for _, title := range []string{"first", "second"} {
		title := title
		go func() {
			_, updateErr := store.UpdateOccurrence(context.Background(), created.ID, domain.OccurrenceOverrideCandidate{
				OriginalDate:     "2026-09-07",
				StartLocal:       time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC),
				DurationMinutes:  60,
				Title:            title,
				ExpectedRevision: created.Revision,
				UpdatedAtUTC:     created.UpdatedAtUTC.Add(time.Minute),
			})
			results <- updateErr
		}()
	}
	var success, stale int
	for range 2 {
		updateErr := <-results
		if updateErr == nil {
			success++
			continue
		}
		var businessErr *domain.Error
		if errors.As(updateErr, &businessErr) && businessErr.Code == domain.StaleRevision {
			stale++
		}
	}
	if success != 1 || stale != 1 {
		t.Fatalf("concurrent results = success %d, stale %d", success, stale)
	}
}

// TestUpdateOccurrenceRollsBackSnapshotOnParentFailure 验证父版本更新失败时例外写入一并回滚。
func TestUpdateOccurrenceRollsBackSnapshotOnParentFailure(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	created, err := store.CreateSchedule(ctx, createTestSchedule())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`
		CREATE TRIGGER reject_occurrence_parent_update
		BEFORE UPDATE ON schedules
		BEGIN
			SELECT RAISE(ABORT, 'forced occurrence parent failure');
		END`); err != nil {
		t.Fatal(err)
	}
	_, err = store.UpdateOccurrence(ctx, created.ID, domain.OccurrenceOverrideCandidate{
		OriginalDate:     "2026-09-07",
		StartLocal:       time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC),
		DurationMinutes:  60,
		Title:            "should rollback",
		ExpectedRevision: created.Revision,
		UpdatedAtUTC:     created.UpdatedAtUTC.Add(time.Minute),
	})
	if err == nil {
		t.Fatal("UpdateOccurrence() error = nil")
	}
	var count int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM occurrence_overrides WHERE schedule_id = ?", created.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("override count after rollback = %d, want 0", count)
	}
	got, err := store.GetSchedule(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != created.Revision {
		t.Fatalf("schedule revision after rollback = %d, want %d", got.Revision, created.Revision)
	}
}

// TestCancelOccurrencePersistsEmptySnapshotAndIsIdempotent 验证未修改发生取消后保存空快照且重复取消不提升版本。
func TestCancelOccurrencePersistsEmptySnapshotAndIsIdempotent(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	created, err := store.CreateSchedule(ctx, createTestSchedule())
	if err != nil {
		t.Fatal(err)
	}
	candidate := domain.OccurrenceCancelCandidate{
		OriginalDate: "2026-01-02", ExpectedRevision: created.Revision, UpdatedAtUTC: created.UpdatedAtUTC.Add(time.Minute),
	}
	result, err := store.CancelOccurrence(ctx, created.ID, candidate)
	if err != nil {
		t.Fatalf("CancelOccurrence() error = %v", err)
	}
	if !result.Changed || !result.OverridePresent || result.Override.Open || result.Override.SnapshotPresent || result.ScheduleRevision != created.Revision+1 {
		t.Fatalf("cancel result = %+v", result)
	}
	schedule, err := store.GetSchedule(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !schedule.Open || schedule.Revision != created.Revision+1 {
		t.Fatalf("schedule after cancellation = %+v", schedule)
	}
	repeated, err := store.CancelOccurrence(ctx, created.ID, domain.OccurrenceCancelCandidate{
		OriginalDate: "2026-01-02", ExpectedRevision: schedule.Revision, UpdatedAtUTC: schedule.UpdatedAtUTC.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("idempotent CancelOccurrence() error = %v", err)
	}
	if repeated.Changed || repeated.ScheduleRevision != schedule.Revision || repeated.Override.SnapshotPresent {
		t.Fatalf("idempotent result = %+v", repeated)
	}
	_, err = store.CancelOccurrence(ctx, created.ID, candidate)
	var businessErr *domain.Error
	if !errors.As(err, &businessErr) || businessErr.Code != domain.StaleRevision {
		t.Fatalf("stale cancellation error = %#v", err)
	}
}

// TestCancelOccurrencePreservesCompleteSnapshot 验证取消已修改发生时只改变 open 而保留完整快照。
func TestCancelOccurrencePreservesCompleteSnapshot(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	created, err := store.CreateSchedule(ctx, createTestSchedule())
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 1, 10, 11, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	updated, err := store.UpdateOccurrence(ctx, created.ID, domain.OccurrenceOverrideCandidate{
		OriginalDate: "2026-01-02", StartLocal: start, DurationMinutes: 75,
		Title: "moved", Notes: "kept", ExpectedRevision: created.Revision, UpdatedAtUTC: created.UpdatedAtUTC.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.CancelOccurrence(ctx, created.ID, domain.OccurrenceCancelCandidate{
		OriginalDate: "2026-01-02", ExpectedRevision: updated.ScheduleRevision, UpdatedAtUTC: created.UpdatedAtUTC.Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatalf("CancelOccurrence() error = %v", err)
	}
	if !result.Changed || result.Override.Open || !result.Override.SnapshotPresent || !result.Override.StartLocal.Equal(start) || result.Override.DurationMinutes != 75 || result.Override.Title != "moved" || result.Override.Notes != "kept" {
		t.Fatalf("cancelled snapshot = %+v", result.Override)
	}
}

// TestCancelOccurrenceRollsBackOnParentFailure 验证父日程版本更新失败时取消例外不会残留。
func TestCancelOccurrenceRollsBackOnParentFailure(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	created, err := store.CreateSchedule(ctx, createTestSchedule())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`
		CREATE TRIGGER reject_cancel_parent_update
		BEFORE UPDATE ON schedules
		BEGIN
			SELECT RAISE(ABORT, 'forced cancellation parent failure');
		END`); err != nil {
		t.Fatal(err)
	}
	_, err = store.CancelOccurrence(ctx, created.ID, domain.OccurrenceCancelCandidate{
		OriginalDate: "2026-01-02", ExpectedRevision: created.Revision, UpdatedAtUTC: created.UpdatedAtUTC.Add(time.Minute),
	})
	if err == nil {
		t.Fatal("CancelOccurrence() error = nil")
	}
	var count int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM occurrence_overrides WHERE schedule_id = ?", created.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("cancelled override count after rollback = %d, want 0", count)
	}
	schedule, err := store.GetSchedule(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if schedule.Revision != created.Revision || !schedule.Open {
		t.Fatalf("schedule after rollback = %+v", schedule)
	}
}

// TestRestoreOccurrenceReopensSnapshotAndRemovesEmptyCancellation 验证完整快照重开与空取消例外删除两种恢复路径。
func TestRestoreOccurrenceReopensSnapshotAndRemovesEmptyCancellation(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	created, err := store.CreateSchedule(ctx, createTestSchedule())
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 1, 10, 11, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	updated, err := store.UpdateOccurrence(ctx, created.ID, domain.OccurrenceOverrideCandidate{
		OriginalDate: "2026-01-02", StartLocal: start, DurationMinutes: 75,
		Title: "moved", Notes: "kept", ExpectedRevision: created.Revision, UpdatedAtUTC: created.UpdatedAtUTC.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := store.CancelOccurrence(ctx, created.ID, domain.OccurrenceCancelCandidate{
		OriginalDate: "2026-01-02", ExpectedRevision: updated.ScheduleRevision, UpdatedAtUTC: created.UpdatedAtUTC.Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	restored, err := store.RestoreOccurrence(ctx, created.ID, domain.OccurrenceRestoreCandidate{
		OriginalDate: "2026-01-02", ExpectedRevision: cancelled.ScheduleRevision, UpdatedAtUTC: created.UpdatedAtUTC.Add(3 * time.Minute),
	})
	if err != nil {
		t.Fatalf("RestoreOccurrence() error = %v", err)
	}
	if !restored.Changed || !restored.OverridePresent || !restored.Override.Open || !restored.Override.SnapshotPresent || restored.Override.Title != "moved" || restored.ScheduleRevision != cancelled.ScheduleRevision+1 {
		t.Fatalf("restored snapshot = %+v", restored)
	}

	emptySchedule, err := store.CreateSchedule(ctx, createTestSchedule())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec("INSERT INTO reminder_attempts(schedule_id, occurrence_local, attempted_at_utc, status) VALUES (?, ?, ?, ?)", emptySchedule.ID, "2026-01-02", "2026-01-02T02:00:00Z", "claimed"); err != nil {
		t.Fatal(err)
	}
	emptyCancelled, err := store.CancelOccurrence(ctx, emptySchedule.ID, domain.OccurrenceCancelCandidate{
		OriginalDate: "2026-01-02", ExpectedRevision: emptySchedule.Revision, UpdatedAtUTC: emptySchedule.UpdatedAtUTC.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	emptyRestored, err := store.RestoreOccurrence(ctx, emptySchedule.ID, domain.OccurrenceRestoreCandidate{
		OriginalDate: "2026-01-02", ExpectedRevision: emptyCancelled.ScheduleRevision, UpdatedAtUTC: emptySchedule.UpdatedAtUTC.Add(2 * time.Minute), RemoveCancellation: true,
	})
	if err != nil {
		t.Fatalf("RestoreOccurrence(empty) error = %v", err)
	}
	if !emptyRestored.Changed || emptyRestored.OverridePresent || emptyRestored.ScheduleRevision != emptyCancelled.ScheduleRevision+1 {
		t.Fatalf("restored empty cancellation = %+v", emptyRestored)
	}
	var overrideCount, reminderCount int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM occurrence_overrides WHERE schedule_id = ?", emptySchedule.ID).Scan(&overrideCount); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow("SELECT COUNT(*) FROM reminder_attempts WHERE schedule_id = ?", emptySchedule.ID).Scan(&reminderCount); err != nil {
		t.Fatal(err)
	}
	if overrideCount != 0 || reminderCount != 1 {
		t.Fatalf("restored related rows = overrides %d, reminders %d", overrideCount, reminderCount)
	}
}

// TestRestoreOccurrenceRollsBackOnParentFailure 验证恢复快照的父版本失败时取消状态保持不变。
func TestRestoreOccurrenceRollsBackOnParentFailure(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	created, err := store.CreateSchedule(ctx, createTestSchedule())
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 1, 10, 11, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	updated, err := store.UpdateOccurrence(ctx, created.ID, domain.OccurrenceOverrideCandidate{
		OriginalDate: "2026-01-02", StartLocal: start, DurationMinutes: 75,
		Title: "moved", Notes: "kept", ExpectedRevision: created.Revision, UpdatedAtUTC: created.UpdatedAtUTC.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := store.CancelOccurrence(ctx, created.ID, domain.OccurrenceCancelCandidate{
		OriginalDate: "2026-01-02", ExpectedRevision: updated.ScheduleRevision, UpdatedAtUTC: created.UpdatedAtUTC.Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`
		CREATE TRIGGER reject_restore_parent_update
		BEFORE UPDATE ON schedules
		BEGIN
			SELECT RAISE(ABORT, 'forced restoration parent failure');
		END`); err != nil {
		t.Fatal(err)
	}
	_, err = store.RestoreOccurrence(ctx, created.ID, domain.OccurrenceRestoreCandidate{
		OriginalDate: "2026-01-02", ExpectedRevision: cancelled.ScheduleRevision, UpdatedAtUTC: created.UpdatedAtUTC.Add(3 * time.Minute),
	})
	if err == nil {
		t.Fatal("RestoreOccurrence() error = nil")
	}
	override, err := store.GetOccurrenceOverride(ctx, created.ID, "2026-01-02")
	if err != nil {
		t.Fatal(err)
	}
	if override.Open || !override.SnapshotPresent {
		t.Fatalf("override after restoration rollback = %+v", override)
	}
	schedule, err := store.GetSchedule(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if schedule.Revision != cancelled.ScheduleRevision {
		t.Fatalf("schedule revision after restoration rollback = %d, want %d", schedule.Revision, cancelled.ScheduleRevision)
	}
}

// TestListOccurrenceOverridesReturnsNotFoundForMissingOverride 验证缺失例外使用业务 NOT_FOUND 分类。
func TestListOccurrenceOverridesReturnsNotFoundForMissingOverride(t *testing.T) {
	store := openTestStore(t)
	_, err := store.GetOccurrenceOverride(context.Background(), 1, "2026-09-07")
	var businessErr *domain.Error
	if !errors.As(err, &businessErr) || businessErr.Code != domain.NotFound {
		t.Fatalf("missing override error = %#v", err)
	}
}
