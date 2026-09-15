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
	schedule := Schedule{ID: 1, Cron: &cron, UntilDateLocal: &until, FirstStartLocal: time.Date(2026, 9, 7, 10, 0, 0, 0, location), DurationMinutes: 60, Title: "base", Open: true}
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
	schedule := Schedule{ID: 1, Cron: &cron, UntilDateLocal: &until, FirstStartLocal: time.Date(2026, 9, 7, 10, 0, 0, 0, location), DurationMinutes: 60, Title: "base", Open: true}
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

// TestClosedMergedScheduleSkipsOverrides 验证关闭整组时基础发生和开放例外都不进入有效结果。
func TestClosedMergedScheduleSkipsOverrides(t *testing.T) {
	location := time.FixedZone("CST", 8*60*60)
	cron := "0 10 * * *"
	until := "2026-09-14"
	schedule := Schedule{ID: 1, Cron: &cron, UntilDateLocal: &until, FirstStartLocal: time.Date(2026, 9, 7, 10, 0, 0, 0, location), DurationMinutes: 60, Open: false}
	override := OccurrenceOverride{ScheduleID: 1, OriginalDate: "2026-09-07", StartLocal: time.Date(2026, 9, 20, 10, 0, 0, 0, location), DurationMinutes: 60, Title: "moved", Open: true, SnapshotPresent: true}
	if occurrence, ok, err := NextMergedOccurrence(schedule, []OccurrenceOverride{override}, time.Date(2026, 9, 15, 0, 0, 0, 0, location), location); err != nil || ok || !occurrence.StartLocal.IsZero() {
		t.Fatalf("NextMergedOccurrence() = %+v, %v, %v", occurrence, ok, err)
	}
	occurrences, err := ExpandMergedOccurrences(schedule, []OccurrenceOverride{override}, time.Date(2026, 9, 7, 0, 0, 0, 0, location), time.Date(2026, 9, 21, 0, 0, 0, 0, location), location)
	if err != nil || len(occurrences) != 0 {
		t.Fatalf("ExpandMergedOccurrences() = %+v, %v", occurrences, err)
	}
}

// TestOccurrenceOnDateMatchesClosedSchedule 验证关闭组仍可按原始规则身份匹配恢复本次例外。
func TestOccurrenceOnDateMatchesClosedSchedule(t *testing.T) {
	location := time.UTC
	cron := "0 10 * * *"
	until := "2026-09-14"
	schedule := Schedule{ID: 1, Cron: &cron, UntilDateLocal: &until, FirstStartLocal: time.Date(2026, 9, 7, 10, 0, 0, 0, location), DurationMinutes: 60, Open: false}
	occurrence, found, err := OccurrenceOnDate(schedule, "2026-09-08", location)
	if err != nil || !found || occurrence.OriginalDate != "2026-09-08" {
		t.Fatalf("OccurrenceOnDate() = %+v, %v, %v", occurrence, found, err)
	}
}

// TestSingleOccurrenceUsesOriginalStartIdentity 验证单次改期后身份仍由原始日期锚点决定。
func TestSingleOccurrenceUsesOriginalStartIdentity(t *testing.T) {
	location := time.FixedZone("CST", 8*60*60)
	schedule := Schedule{
		ID: 7, Open: true,
		FirstStartLocal:    time.Date(2026, 9, 20, 10, 0, 0, 0, location),
		OriginalStartLocal: time.Date(2026, 9, 7, 10, 0, 0, 0, location),
		DurationMinutes:    60,
	}
	override := OccurrenceOverride{
		ScheduleID: 7, OriginalDate: "2026-09-07", Open: true, SnapshotPresent: true,
		StartLocal: time.Date(2026, 9, 20, 10, 0, 0, 0, location), DurationMinutes: 60,
	}

	occurrence, ok, err := NextMergedOccurrence(schedule, []OccurrenceOverride{override}, time.Date(2026, 9, 1, 0, 0, 0, 0, location), location)
	if err != nil || !ok || occurrence.OriginalDate != "2026-09-07" || !occurrence.StartLocal.Equal(override.StartLocal) {
		t.Fatalf("NextMergedOccurrence() = %+v, %v, %v", occurrence, ok, err)
	}
	if _, ok, err := OccurrenceForIdentity(schedule, nil, OccurrenceIdentity{ScheduleID: 7, OriginalDate: "2026-09-20"}, location); err != nil || ok {
		t.Fatalf("OccurrenceForIdentity(moved date) = %v, %v", ok, err)
	}
	if _, ok, err := OccurrenceForIdentity(schedule, nil, OccurrenceIdentity{ScheduleID: 7, OriginalDate: "2026-09-07"}, location); err != nil || !ok {
		t.Fatalf("OccurrenceForIdentity(anchor date) = %v, %v", ok, err)
	}
}

// TestSingleOccurrenceIgnoresUnrelatedOverride 验证单次日程不会把历史其他身份例外扩展成第二次发生。
func TestSingleOccurrenceIgnoresUnrelatedOverride(t *testing.T) {
	location := time.UTC
	schedule := Schedule{
		ID: 8, Open: true,
		FirstStartLocal:    time.Date(2026, 9, 20, 10, 0, 0, 0, location),
		OriginalStartLocal: time.Date(2026, 9, 7, 10, 0, 0, 0, location),
		DurationMinutes:    60,
	}
	overrides := []OccurrenceOverride{
		{ScheduleID: 8, OriginalDate: "2026-09-01", Open: true, SnapshotPresent: true,
			StartLocal: time.Date(2026, 9, 21, 10, 0, 0, 0, location), DurationMinutes: 60},
	}
	occurrences, err := ExpandMergedOccurrences(schedule, overrides,
		time.Date(2026, 9, 1, 0, 0, 0, 0, location), time.Date(2026, 9, 30, 0, 0, 0, 0, location), location)
	if err != nil || len(occurrences) != 1 || occurrences[0].OriginalDate != "2026-09-07" {
		t.Fatalf("ExpandMergedOccurrences() = %+v, %v", occurrences, err)
	}
}

// TestMergedOccurrenceIteratorIncludesCrossDayBase 验证从窗口前开始但实际跨入窗口的基础发生不会遗漏。
func TestMergedOccurrenceIteratorIncludesCrossDayBase(t *testing.T) {
	location := time.FixedZone("CST", 8*60*60)
	cron := "30 23 * * *"
	until := "2026-09-03"
	schedule := Schedule{ID: 3, Cron: &cron, UntilDateLocal: &until,
		FirstStartLocal: time.Date(2026, 9, 1, 23, 30, 0, 0, location),
		DurationMinutes: 120, Title: "overnight", Open: true}
	iterator, err := NewMergedOccurrenceIterator(schedule, nil,
		time.Date(2026, 9, 2, 0, 30, 0, 0, location),
		time.Date(2026, 9, 2, 2, 0, 0, 0, location), location)
	if err != nil {
		t.Fatalf("NewMergedOccurrenceIterator() error = %v", err)
	}
	got, ok, err := iterator.Next()
	if err != nil || !ok {
		t.Fatalf("Next() = %+v, %v, %v", got, ok, err)
	}
	if got.OriginalDate != "2026-09-01" || got.StartLocal.Hour() != 23 || got.EndLocal.Hour() != 1 {
		t.Fatalf("cross-day occurrence = %+v", got)
	}
	if _, ok, err := iterator.Next(); err != nil || ok {
		t.Fatalf("second Next() = %v, %v", ok, err)
	}
}

// TestMergedOccurrenceIteratorAppliesCancellationAndMove 验证取消先替换基础身份，移动例外再按实际时间参与查询。
func TestMergedOccurrenceIteratorAppliesCancellationAndMove(t *testing.T) {
	location := time.UTC
	cron := "0 10 * * *"
	until := "2026-09-04"
	schedule := Schedule{ID: 4, Cron: &cron, UntilDateLocal: &until,
		FirstStartLocal: time.Date(2026, 9, 1, 10, 0, 0, 0, location),
		DurationMinutes: 60, Title: "base", Open: true}
	overrides := []OccurrenceOverride{
		{ScheduleID: 4, OriginalDate: "2026-09-02", Open: false},
		{ScheduleID: 4, OriginalDate: "2026-09-03", Open: true, SnapshotPresent: true,
			StartLocal: time.Date(2026, 9, 1, 12, 0, 0, 0, location), DurationMinutes: 30, Title: "moved"},
	}
	occurrences, err := ExpandMergedOccurrences(schedule, overrides,
		time.Date(2026, 9, 1, 0, 0, 0, 0, location),
		time.Date(2026, 9, 5, 0, 0, 0, 0, location), location)
	if err != nil {
		t.Fatalf("ExpandMergedOccurrences() error = %v", err)
	}
	if len(occurrences) != 3 || occurrences[0].OriginalDate != "2026-09-01" ||
		occurrences[1].OriginalDate != "2026-09-03" || occurrences[2].OriginalDate != "2026-09-04" {
		t.Fatalf("merged occurrences = %+v", occurrences)
	}
}

// TestMergedOccurrenceIteratorIncludesLongMovedOverride 验证长于父日程的移动例外跨过查询下界时仍被返回。
func TestMergedOccurrenceIteratorIncludesLongMovedOverride(t *testing.T) {
	location := time.UTC
	cron := "0 10 * * *"
	until := "2026-09-03"
	schedule := Schedule{ID: 5, Cron: &cron, UntilDateLocal: &until,
		FirstStartLocal: time.Date(2026, 9, 1, 10, 0, 0, 0, location),
		DurationMinutes: 30, Title: "base", Open: true}
	override := OccurrenceOverride{ScheduleID: 5, OriginalDate: "2026-09-02",
		StartLocal: time.Date(2026, 9, 2, 9, 0, 0, 0, location), DurationMinutes: 120,
		Title: "long moved", Open: true, SnapshotPresent: true}
	iterator, err := NewMergedOccurrenceIterator(schedule, []OccurrenceOverride{override},
		time.Date(2026, 9, 2, 10, 0, 0, 0, location),
		time.Date(2026, 9, 2, 10, 30, 0, 0, location), location)
	if err != nil {
		t.Fatalf("NewMergedOccurrenceIterator() error = %v", err)
	}
	got, ok, err := iterator.Next()
	if err != nil || !ok || got.OriginalDate != "2026-09-02" || !got.StartLocal.Equal(override.StartLocal) {
		t.Fatalf("Next() = %+v, %v, %v", got, ok, err)
	}
}
