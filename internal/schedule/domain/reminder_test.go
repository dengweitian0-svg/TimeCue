package domain

import (
	"testing"
	"time"
)

// TestNextReminderBatchKeepsMovedSameTimeIdentities 验证同一日程内移动到同刻的身份不会被严格游标漏掉。
func TestNextReminderBatchKeepsMovedSameTimeIdentities(t *testing.T) {
	location := time.FixedZone("CST", 8*60*60)
	cron := "0 9 * * *"
	until := "2026-01-04"
	schedule := Schedule{ID: 1, Cron: &cron, UntilDateLocal: &until, MonthEndPolicy: nil, Open: true,
		FirstStartLocal: time.Date(2026, 1, 1, 9, 0, 0, 0, location), DurationMinutes: 30}
	moved := OccurrenceOverride{ScheduleID: 1, OriginalDate: "2026-01-02", Open: true, SnapshotPresent: true,
		StartLocal: time.Date(2026, 1, 1, 9, 0, 0, 0, location), DurationMinutes: 45}
	batch, ok, err := NextReminderCandidateBatch(schedule, []OccurrenceOverride{moved}, time.Date(2026, 1, 1, 8, 0, 0, 0, location), location)
	if err != nil || !ok {
		t.Fatalf("NextReminderCandidateBatch() = %+v, %v, %v", batch, ok, err)
	}
	if len(batch.Occurrences) != 2 || batch.Occurrences[0].OriginalDate != "2026-01-01" || batch.Occurrences[1].OriginalDate != "2026-01-02" {
		t.Fatalf("batch = %+v", batch.Occurrences)
	}
	next, ok, err := AdvanceReminderCandidateBatch(schedule, []OccurrenceOverride{moved}, batch, location)
	if err != nil || !ok || len(next.Occurrences) != 1 || next.Occurrences[0].OriginalDate != "2026-01-03" {
		t.Fatalf("AdvanceReminderCandidateBatch() = %+v, %v, %v", next, ok, err)
	}
}

// TestNextReminderBatchSkipsCancelledAndRespectsStrictAfter 验证取消只推进计算游标且 now 边界严格排除。
func TestNextReminderBatchSkipsCancelledAndRespectsStrictAfter(t *testing.T) {
	location := time.FixedZone("CST", 8*60*60)
	cron := "0 9 * * *"
	until := "2026-01-03"
	schedule := Schedule{ID: 2, Cron: &cron, UntilDateLocal: &until, Open: true,
		FirstStartLocal: time.Date(2026, 1, 1, 9, 0, 0, 0, location), DurationMinutes: 30}
	cancelled := OccurrenceOverride{ScheduleID: 2, OriginalDate: "2026-01-01", Open: false}
	batch, ok, err := NextReminderBatch(schedule, []OccurrenceOverride{cancelled}, time.Date(2026, 1, 1, 9, 0, 0, 0, location), location)
	if err != nil || !ok || batch.Occurrences[0].OriginalDate != "2026-01-02" {
		t.Fatalf("strict/cancelled result = %+v, %v, %v", batch, ok, err)
	}
}

// TestOccurrenceForIdentitySupportsSingleSchedule 验证一次性日程也使用同一稳定身份校验。
func TestOccurrenceForIdentitySupportsSingleSchedule(t *testing.T) {
	location := time.FixedZone("CST", 8*60*60)
	schedule := Schedule{ID: 3, Open: true, FirstStartLocal: time.Date(2026, 1, 2, 9, 0, 0, 0, location), DurationMinutes: 20}
	occurrence, ok, err := OccurrenceForIdentity(schedule, nil, OccurrenceIdentity{ScheduleID: 3, OriginalDate: "2026-01-02"}, location)
	if err != nil || !ok || occurrence.EndLocal.Sub(occurrence.StartLocal) != 20*time.Minute {
		t.Fatalf("OccurrenceForIdentity() = %+v, %v, %v", occurrence, ok, err)
	}
}
