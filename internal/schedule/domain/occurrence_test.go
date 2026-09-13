package domain

import (
	"testing"
	"time"
)

// TestValidateOccurrenceOverrideAllowsFutureTime 验证例外不受单次日程一年范围限制但仍校验端点。
func TestValidateOccurrenceOverrideAllowsFutureTime(t *testing.T) {
	location := time.FixedZone("CST", 8*60*60)
	snapshot := TimeSnapshot{Now: time.Date(2026, 9, 1, 8, 0, 0, 0, location), Location: location}
	candidate, err := ValidateOccurrenceOverride(snapshot, "2026-09-07", unixMinute(time.Date(2030, 1, 2, 10, 0, 0, 0, location)), unixMinute(time.Date(2030, 1, 2, 11, 30, 0, 0, location)), "moved", "note")
	if err != nil {
		t.Fatalf("ValidateOccurrenceOverride() error = %v", err)
	}
	if candidate.OriginalDate != "2026-09-07" || candidate.DurationMinutes != 90 || candidate.Title != "moved" || candidate.Notes != "note" {
		t.Fatalf("candidate = %+v", candidate)
	}
}

// TestMergeOccurrencesReplacesByOriginalDate 验证例外只替换同一稳定身份并保留其他基础发生。
func TestMergeOccurrencesReplacesByOriginalDate(t *testing.T) {
	location := time.FixedZone("CST", 8*60*60)
	base := []Occurrence{
		{ScheduleID: 1, OriginalDate: "2026-09-07", StartLocal: time.Date(2026, 9, 7, 10, 0, 0, 0, location), EndLocal: time.Date(2026, 9, 7, 11, 0, 0, 0, location), Title: "base", Notes: "old"},
		{ScheduleID: 1, OriginalDate: "2026-09-14", StartLocal: time.Date(2026, 9, 14, 10, 0, 0, 0, location), EndLocal: time.Date(2026, 9, 14, 11, 0, 0, 0, location), Title: "base", Notes: "old"},
	}
	overrides := []OccurrenceOverride{{
		ScheduleID: 1, OriginalDate: "2026-09-07", StartLocal: time.Date(2026, 9, 20, 10, 0, 0, 0, location), DurationMinutes: 90,
		Title: "moved", Notes: "new", Open: true, SnapshotPresent: true,
	}}
	got := MergeOccurrences(base, overrides)
	if len(got) != 2 {
		t.Fatalf("merged length = %d, want 2", len(got))
	}
	if got[0].OriginalDate != "2026-09-14" || got[1].OriginalDate != "2026-09-07" || got[1].StartLocal.Day() != 20 || got[1].EndLocal.Sub(got[1].StartLocal) != 90*time.Minute {
		t.Fatalf("merged occurrences = %+v", got)
	}
}

// TestNextMergedOccurrenceFindsExceptionAfterCutoff 验证基础规则截止后仍能找到未来例外。
func TestNextMergedOccurrenceFindsExceptionAfterCutoff(t *testing.T) {
	location := time.FixedZone("CST", 8*60*60)
	cron := "0 10 * * *"
	until := "2026-09-14"
	schedule := Schedule{ID: 1, Cron: &cron, UntilDateLocal: &until, FirstStartLocal: time.Date(2026, 9, 7, 10, 0, 0, 0, location), DurationMinutes: 60, Title: "base"}
	override := OccurrenceOverride{
		ScheduleID: 1, OriginalDate: "2026-09-07", StartLocal: time.Date(2026, 9, 20, 10, 0, 0, 0, location), DurationMinutes: 60,
		Title: "moved", Open: true, SnapshotPresent: true,
	}
	got, ok, err := NextMergedOccurrence(schedule, []OccurrenceOverride{override}, time.Date(2026, 9, 15, 0, 0, 0, 0, location), location)
	if err != nil || !ok {
		t.Fatalf("NextMergedOccurrence() = %+v, %v, %v", got, ok, err)
	}
	if got.OriginalDate != "2026-09-07" || got.StartLocal.Day() != 20 {
		t.Fatalf("next occurrence = %+v", got)
	}
}

// TestExpandMergedOccurrencesUsesActualInterval 验证移动出窗口的例外仍替换原位置且窗口内其他发生不丢失。
func TestExpandMergedOccurrencesUsesActualInterval(t *testing.T) {
	location := time.FixedZone("CST", 8*60*60)
	cron := "0 10 * * *"
	until := "2026-09-14"
	schedule := Schedule{ID: 1, Cron: &cron, UntilDateLocal: &until, FirstStartLocal: time.Date(2026, 9, 7, 10, 0, 0, 0, location), DurationMinutes: 60, Title: "base"}
	override := OccurrenceOverride{
		ScheduleID: 1, OriginalDate: "2026-09-07", StartLocal: time.Date(2026, 9, 20, 10, 0, 0, 0, location), DurationMinutes: 60,
		Title: "moved", Open: true, SnapshotPresent: true,
	}
	got, err := ExpandMergedOccurrences(schedule, []OccurrenceOverride{override}, time.Date(2026, 9, 7, 0, 0, 0, 0, location), time.Date(2026, 9, 15, 0, 0, 0, 0, location), location)
	if err != nil {
		t.Fatalf("ExpandMergedOccurrences() error = %v", err)
	}
	if len(got) != 7 || got[0].OriginalDate != "2026-09-08" || got[len(got)-1].OriginalDate != "2026-09-14" {
		t.Fatalf("merged window = %+v", got)
	}
}

// TestMergeOccurrencesKeepsSameTimeDifferentIdentities 验证同刻发生按稳定身份分别保留。
func TestMergeOccurrencesKeepsSameTimeDifferentIdentities(t *testing.T) {
	location := time.FixedZone("CST", 8*60*60)
	start := time.Date(2026, 9, 20, 10, 0, 0, 0, location)
	got := MergeOccurrences(nil, []OccurrenceOverride{
		{ScheduleID: 2, OriginalDate: "2026-09-07", StartLocal: start, DurationMinutes: 60, Open: true, SnapshotPresent: true},
		{ScheduleID: 1, OriginalDate: "2026-09-21", StartLocal: start, DurationMinutes: 60, Open: true, SnapshotPresent: true},
	})
	if len(got) != 2 || got[0].ScheduleID != 1 || got[1].ScheduleID != 2 {
		t.Fatalf("same-time occurrences = %+v", got)
	}
}

// unixMinute 将本地时间转换为分钟精度的 Unix 秒，供测试构造输入。
func unixMinute(value time.Time) int64 {
	return value.Unix()
}
