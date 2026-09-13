package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// TestServiceRestoreOccurrenceReopensSnapshot 验证完整快照取消通过恢复端口重新开放。
func TestServiceRestoreOccurrenceReopensSnapshot(t *testing.T) {
	store, clock, schedule := occurrenceServiceFixture()
	store.overrideErr = nil
	store.override = domain.OccurrenceOverride{
		ScheduleID: schedule.ID, OriginalDate: "2026-09-07",
		StartLocal:      time.Date(2026, 9, 20, 10, 0, 0, 0, clock.snapshot.Location),
		DurationMinutes: 90, Title: "moved", Notes: "notes", Open: false, SnapshotPresent: true,
	}
	store.restoreResult = domain.OccurrenceActionResult{Override: store.override, OverridePresent: true, ScheduleRevision: 4, Changed: true}
	store.restoreResult.Override.Open = true
	got, err := NewService(store, clock).RestoreOccurrence(context.Background(), schedule.ID, OccurrenceActionRequest{
		OriginalDate: "2026-09-07", ExpectedRevision: schedule.Revision,
	})
	if err != nil {
		t.Fatalf("RestoreOccurrence() error = %v", err)
	}
	if got.Cancelled || !got.SnapshotPresent || got.Title == nil || *got.Title != "moved" || store.restoreCandidate.RemoveCancellation {
		t.Fatalf("result/candidate = %+v/%+v", got, store.restoreCandidate)
	}
}

// TestServiceRestoreOccurrenceRemovesRegainedEmptyCancellation 验证规则恢复后空取消例外才允许删除。
func TestServiceRestoreOccurrenceRemovesRegainedEmptyCancellation(t *testing.T) {
	store, clock, schedule := occurrenceServiceFixture()
	store.overrideErr = nil
	store.override = domain.OccurrenceOverride{ScheduleID: schedule.ID, OriginalDate: "2026-09-07", Open: false, SnapshotPresent: false}
	store.restoreResult = domain.OccurrenceActionResult{ScheduleRevision: schedule.Revision + 1, Changed: true}
	got, err := NewService(store, clock).RestoreOccurrence(context.Background(), schedule.ID, OccurrenceActionRequest{
		OriginalDate: "2026-09-07", ExpectedRevision: schedule.Revision,
	})
	if err != nil {
		t.Fatalf("RestoreOccurrence() error = %v", err)
	}
	if got.Cancelled || got.ScheduleRevision != schedule.Revision+1 || !store.restoreCandidate.RemoveCancellation {
		t.Fatalf("result/candidate = %+v/%+v", got, store.restoreCandidate)
	}
}

// TestServiceRestoreOccurrenceKeepsInvalidEmptyCancellation 验证规则仍不包含原位置时保留取消记录。
func TestServiceRestoreOccurrenceKeepsInvalidEmptyCancellation(t *testing.T) {
	store, clock, schedule := occurrenceServiceFixture()
	store.overrideErr = nil
	store.override = domain.OccurrenceOverride{ScheduleID: schedule.ID, OriginalDate: "2026-09-08", Open: false, SnapshotPresent: false}
	_, err := NewService(store, clock).RestoreOccurrence(context.Background(), schedule.ID, OccurrenceActionRequest{
		OriginalDate: "2026-09-08", ExpectedRevision: schedule.Revision,
	})
	var businessErr *domain.Error
	if !errors.As(err, &businessErr) || businessErr.Code != domain.Validation {
		t.Fatalf("error = %#v, want validation", err)
	}
	if store.restoreCalls != 0 {
		t.Fatalf("restore calls = %d, want 0", store.restoreCalls)
	}
}

// TestServiceRestoreOccurrenceIsIdempotent 验证已经开放的例外恢复不重复提升版本。
func TestServiceRestoreOccurrenceIsIdempotent(t *testing.T) {
	store, clock, schedule := occurrenceServiceFixture()
	store.overrideErr = nil
	store.override = domain.OccurrenceOverride{ScheduleID: schedule.ID, OriginalDate: "2026-09-07", Open: true, SnapshotPresent: true, DurationMinutes: 60, Title: "active", StartLocal: clock.snapshot.Now}
	store.restoreResult = domain.OccurrenceActionResult{Override: store.override, OverridePresent: true, ScheduleRevision: schedule.Revision, Changed: false}
	got, err := NewService(store, clock).RestoreOccurrence(context.Background(), schedule.ID, OccurrenceActionRequest{
		OriginalDate: "2026-09-07", ExpectedRevision: schedule.Revision,
	})
	if err != nil {
		t.Fatalf("RestoreOccurrence() error = %v", err)
	}
	if got.Cancelled || got.ScheduleRevision != schedule.Revision || store.restoreCalls != 1 {
		t.Fatalf("result/calls = %+v/%d", got, store.restoreCalls)
	}
}
