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

// TestListOccurrenceOverridesReturnsNotFoundForMissingOverride 验证缺失例外使用业务 NOT_FOUND 分类。
func TestListOccurrenceOverridesReturnsNotFoundForMissingOverride(t *testing.T) {
	store := openTestStore(t)
	_, err := store.GetOccurrenceOverride(context.Background(), 1, "2026-09-07")
	var businessErr *domain.Error
	if !errors.As(err, &businessErr) || businessErr.Code != domain.NotFound {
		t.Fatalf("missing override error = %#v", err)
	}
}
