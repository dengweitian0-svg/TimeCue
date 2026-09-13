package app

import (
	"context"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// TestServiceGetScheduleReturnsCancelledOccurrences 验证读取日程时返回取消事实而不重新计算原位置。
func TestServiceGetScheduleReturnsCancelledOccurrences(t *testing.T) {
	store, _, schedule := occurrenceServiceFixture()
	start := time.Date(2030, 1, 2, 10, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	store.overrides = []domain.OccurrenceOverride{
		{ScheduleID: schedule.ID, OriginalDate: "2030-01-02", Open: false},
		{ScheduleID: schedule.ID, OriginalDate: "2026-09-07", Open: false, SnapshotPresent: true, StartLocal: start, DurationMinutes: 90, Title: "moved", Notes: "notes"},
		{ScheduleID: schedule.ID, OriginalDate: "2026-09-14", Open: true, SnapshotPresent: true, StartLocal: start, DurationMinutes: 60, Title: "active", Notes: "active notes"},
	}
	got, err := NewService(store, nil).GetSchedule(context.Background(), schedule.ID)
	if err != nil {
		t.Fatalf("GetSchedule() error = %v", err)
	}
	if len(got.CancelledOccurrences) != 2 {
		t.Fatalf("cancelled occurrences = %+v", got.CancelledOccurrences)
	}
	if got.CancelledOccurrences[0].OriginalDate != "2030-01-02" || got.CancelledOccurrences[0].SnapshotPresent {
		t.Fatalf("empty cancellation = %+v", got.CancelledOccurrences[0])
	}
	full := got.CancelledOccurrences[1]
	if !full.SnapshotPresent || full.StartUnix == nil || *full.StartUnix != start.Unix() || full.EndUnix == nil || *full.EndUnix != start.Add(90*time.Minute).Unix() || full.Title == nil || *full.Title != "moved" {
		t.Fatalf("full cancellation = %+v", full)
	}
}

// TestServiceGetSchedulePropagatesOccurrenceReadFailure 验证取消例外读取失败不会被伪装成空列表。
func TestServiceGetSchedulePropagatesOccurrenceReadFailure(t *testing.T) {
	store, _, schedule := occurrenceServiceFixture()
	store.overrideListErr = context.DeadlineExceeded
	_, err := NewService(store, nil).GetSchedule(context.Background(), schedule.ID)
	if err == nil {
		t.Fatal("GetSchedule() error = nil")
	}
}
