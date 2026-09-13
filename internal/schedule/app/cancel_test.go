package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// TestServiceCancelOccurrenceCreatesCancellation 验证未修改发生通过空快照取消且只提升一次版本。
func TestServiceCancelOccurrenceCreatesCancellation(t *testing.T) {
	store, clock, schedule := occurrenceServiceFixture()
	store.cancelResult = domain.OccurrenceActionResult{
		Override:        domain.OccurrenceOverride{ScheduleID: schedule.ID, OriginalDate: "2026-09-07", Open: false},
		OverridePresent: true, ScheduleRevision: schedule.Revision + 1, Changed: true,
	}
	result, err := NewService(store, clock).CancelOccurrence(context.Background(), schedule.ID, OccurrenceActionRequest{
		OriginalDate: "2026-09-07", ExpectedRevision: schedule.Revision,
	})
	if err != nil {
		t.Fatalf("CancelOccurrence() error = %v", err)
	}
	if !result.Cancelled || result.SnapshotPresent || result.ScheduleRevision != schedule.Revision+1 {
		t.Fatalf("result = %+v", result)
	}
	if store.cancelCalls != 1 || store.cancelCandidate.OriginalDate != "2026-09-07" || store.cancelCandidate.ExpectedRevision != schedule.Revision {
		t.Fatalf("cancel candidate/calls = %+v/%d", store.cancelCandidate, store.cancelCalls)
	}
}

// TestServiceCancelOccurrenceKeepsExistingSnapshot 验证已修改发生取消时不重新计算或覆盖快照。
func TestServiceCancelOccurrenceKeepsExistingSnapshot(t *testing.T) {
	store, clock, schedule := occurrenceServiceFixture()
	store.overrideErr = nil
	store.override = domain.OccurrenceOverride{
		ScheduleID: schedule.ID, OriginalDate: "2026-09-07",
		StartLocal:      time.Date(2026, 9, 20, 10, 0, 0, 0, clock.snapshot.Location),
		DurationMinutes: 90, Title: "moved", Notes: "notes", Open: true, SnapshotPresent: true,
	}
	store.cancelResult = domain.OccurrenceActionResult{Override: store.override, OverridePresent: true, ScheduleRevision: 4, Changed: true}
	store.cancelResult.Override.Open = false
	got, err := NewService(store, clock).CancelOccurrence(context.Background(), schedule.ID, OccurrenceActionRequest{
		OriginalDate: "2026-09-07", ExpectedRevision: schedule.Revision,
	})
	if err != nil {
		t.Fatalf("CancelOccurrence() error = %v", err)
	}
	if !got.Cancelled || !got.SnapshotPresent || got.Title == nil || *got.Title != "moved" || got.StartUnix == nil {
		t.Fatalf("result = %+v", got)
	}
}

// TestServiceCancelOccurrenceIsIdempotent 验证已取消发生不触发第二次写入版本。
func TestServiceCancelOccurrenceIsIdempotent(t *testing.T) {
	store, clock, schedule := occurrenceServiceFixture()
	store.overrideErr = nil
	store.override = domain.OccurrenceOverride{ScheduleID: schedule.ID, OriginalDate: "2026-09-07", Open: false}
	store.cancelResult = domain.OccurrenceActionResult{Override: store.override, OverridePresent: true, ScheduleRevision: schedule.Revision, Changed: false}
	got, err := NewService(store, clock).CancelOccurrence(context.Background(), schedule.ID, OccurrenceActionRequest{
		OriginalDate: "2026-09-07", ExpectedRevision: schedule.Revision,
	})
	if err != nil {
		t.Fatalf("CancelOccurrence() error = %v", err)
	}
	if !got.Cancelled || got.ScheduleRevision != schedule.Revision || store.cancelCalls != 1 {
		t.Fatalf("result/calls = %+v/%d", got, store.cancelCalls)
	}
}

// TestServiceCancelOccurrenceRejectsMissingBase 验证无例外且原日期不属于规则时不会写入取消事实。
func TestServiceCancelOccurrenceRejectsMissingBase(t *testing.T) {
	store, clock, schedule := occurrenceServiceFixture()
	_, err := NewService(store, clock).CancelOccurrence(context.Background(), schedule.ID, OccurrenceActionRequest{
		OriginalDate: "2026-09-08", ExpectedRevision: schedule.Revision,
	})
	var businessErr *domain.Error
	if !errors.As(err, &businessErr) || businessErr.Code != domain.Validation {
		t.Fatalf("error = %#v, want validation", err)
	}
	if store.cancelCalls != 0 {
		t.Fatalf("cancel calls = %d, want 0", store.cancelCalls)
	}
}
