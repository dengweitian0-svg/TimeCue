package domain

import (
	"testing"
	"time"
)

// conflictingSchedule 构造用于冲突计算测试的开放每日日程定义。
func conflictingSchedule(id ScheduleID, title, cron, until string, start time.Time, durationMinutes int) ConflictSource {
	return ConflictSource{Schedule: Schedule{
		ID: id, Title: title, Cron: &cron, UntilDateLocal: &until, Open: true,
		FirstStartLocal: start, OriginalStartLocal: start, DurationMinutes: durationMinutes, Revision: 1,
	}}
}

// movedOverride 构造把某个原始日期移动到实际时间的完整例外快照。
func movedOverride(id ScheduleID, originalDate string, start time.Time, durationMinutes int) OccurrenceOverride {
	return OccurrenceOverride{
		ScheduleID: id, OriginalDate: originalDate, StartLocal: start,
		DurationMinutes: durationMinutes, Title: "moved", Open: true, SnapshotPresent: true,
	}
}

// conflictWindow 返回目标批次的开始和结束时间。
func conflictWindow(target Occurrence) (time.Time, time.Time) {
	return target.StartLocal, target.EndLocal
}

// TestSelectNextTargetBatchIncludesSameTimeIdentities 验证同刻的每个身份都属于同一批次。
func TestSelectNextTargetBatchIncludesSameTimeIdentities(t *testing.T) {
	location := time.UTC
	until := "2026-01-10"
	first := conflictingSchedule(1, "first", "0 9 * * *", until, time.Date(2026, 1, 1, 9, 0, 0, 0, location), 30)
	second := conflictingSchedule(2, "second", "0 9 * * *", until, time.Date(2026, 1, 1, 9, 0, 0, 0, location), 30)
	sources := ConflictSources{Items: []ConflictSource{first, second}}

	targets, err := SelectNextTargetBatch(sources, time.Date(2026, 1, 1, 8, 0, 0, 0, location), location)
	if err != nil {
		t.Fatalf("SelectNextTargetBatch() error = %v", err)
	}
	if len(targets) != 2 || targets[0].ScheduleID != 1 || targets[1].ScheduleID != 2 {
		t.Fatalf("targets = %+v, want both same-time identities", targets)
	}
	if targets[0].StartLocal != time.Date(2026, 1, 1, 9, 0, 0, 0, location) {
		t.Fatalf("target start = %v", targets[0].StartLocal)
	}

	// 目标是过去发生时，严格晚于 now 的最早批次才是下一批。
	targets, err = SelectNextTargetBatch(sources, time.Date(2026, 1, 1, 9, 30, 0, 0, location), location)
	if err != nil {
		t.Fatalf("SelectNextTargetBatch(past) error = %v", err)
	}
	if len(targets) != 2 || targets[0].StartLocal != time.Date(2026, 1, 2, 9, 0, 0, 0, location) {
		t.Fatalf("past targets = %+v", targets)
	}
}

// TestDetectOccurrenceConflictsBoundaryTouchIsNotConflict 验证半开区间边界相接不算冲突。
func TestDetectOccurrenceConflictsBoundaryTouchIsNotConflict(t *testing.T) {
	location := time.UTC
	until := "2026-01-10"
	left := conflictingSchedule(1, "left", "0 10 * * *", until, time.Date(2026, 1, 1, 10, 0, 0, 0, location), 60)
	touching := conflictingSchedule(2, "touch", "0 11 * * *", until, time.Date(2026, 1, 1, 11, 0, 0, 0, location), 60)
	overlapping := conflictingSchedule(3, "overlap", "30 10 * * *", until, time.Date(2026, 1, 1, 10, 30, 0, 0, location), 60)

	targets, err := SelectNextTargetBatch(ConflictSources{Items: []ConflictSource{left}}, time.Date(2026, 1, 1, 0, 0, 0, 0, location), location)
	if err != nil {
		t.Fatalf("SelectNextTargetBatch() error = %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("targets = %+v", targets)
	}
	start, end := conflictWindow(targets[0])

	check, err := DetectOccurrenceConflicts(ConflictSources{Items: []ConflictSource{left, touching}}, targets, start, end, time.Hour, location)
	if err != nil {
		t.Fatalf("DetectOccurrenceConflicts() error = %v", err)
	}
	if check.Status != ConflictChecked || len(check.Conflicts) != 0 {
		t.Fatalf("boundary-touching check = %+v", check)
	}

	check, err = DetectOccurrenceConflicts(ConflictSources{Items: []ConflictSource{left, overlapping}}, targets, start, end, time.Hour, location)
	if err != nil {
		t.Fatalf("DetectOccurrenceConflicts() error = %v", err)
	}
	if len(check.Conflicts) != 1 {
		t.Fatalf("overlap conflicts = %+v", check.Conflicts)
	}
	conflict := check.Conflicts[0]
	if conflict.Other.ScheduleID != 3 || conflict.Other.Title != "overlap" {
		t.Fatalf("conflict other = %+v", conflict.Other)
	}
	if conflict.SameGroup || conflict.OverlapUnix != time.Date(2026, 1, 1, 10, 30, 0, 0, location).Unix() {
		t.Fatalf("conflict = %+v", conflict)
	}
	if conflict.Target.StartUnix != start.Unix() || conflict.Target.EndUnix != end.Unix() {
		t.Fatalf("conflict target window = %+v", conflict.Target)
	}
}

// TestDetectOccurrenceConflictsFindsCrossDayInstance 验证已经开始的跨天实例仍参与比较。
func TestDetectOccurrenceConflictsFindsCrossDayInstance(t *testing.T) {
	location := time.UTC
	until := "2026-01-10"
	overnight := conflictingSchedule(1, "overnight", "0 23 * * *", until, time.Date(2026, 1, 1, 23, 0, 0, 0, location), 3*60)
	batch := conflictingSchedule(2, "batch", "0 1 * * *", until, time.Date(2026, 1, 2, 1, 0, 0, 0, location), 30)

	targets, err := SelectNextTargetBatch(ConflictSources{Items: []ConflictSource{batch}}, time.Date(2026, 1, 2, 0, 30, 0, 0, location), location)
	if err != nil {
		t.Fatalf("SelectNextTargetBatch() error = %v", err)
	}
	if len(targets) != 1 || targets[0].OriginalDate != "2026-01-02" {
		t.Fatalf("targets = %+v", targets)
	}
	start, end := conflictWindow(targets[0])

	check, err := DetectOccurrenceConflicts(ConflictSources{Items: []ConflictSource{overnight, batch}}, targets, start, end, 3*time.Hour, location)
	if err != nil {
		t.Fatalf("DetectOccurrenceConflicts() error = %v", err)
	}
	if len(check.Conflicts) != 1 {
		t.Fatalf("cross-day conflicts = %+v", check.Conflicts)
	}
	// 跨天实例的起点在目标批次之前，但它仍然是与目标相交的冲突方。
	if check.Conflicts[0].Target.OriginalDate != "2026-01-01" ||
		check.Conflicts[0].Other.OriginalDate != "2026-01-02" ||
		check.Conflicts[0].Target.EndUnix != time.Date(2026, 1, 2, 2, 0, 0, 0, location).Unix() {
		t.Fatalf("cross-day conflict = %+v", check.Conflicts[0])
	}
	// 重叠起点取两个区间的较晚开始时刻，也就是目标批次自身。
	if check.Conflicts[0].OverlapUnix != start.Unix() {
		t.Fatalf("cross-day overlap = %d, want %d", check.Conflicts[0].OverlapUnix, start.Unix())
	}
}

// TestDetectOccurrenceConflictsKeepsSameGroupOtherOccurrence 验证只排除同一身份而保留同组其他发生。
func TestDetectOccurrenceConflictsKeepsSameGroupOtherOccurrence(t *testing.T) {
	location := time.UTC
	until := "2026-01-10"
	series := conflictingSchedule(1, "series", "0 12 * * *", until, time.Date(2026, 1, 1, 12, 0, 0, 0, location), 60)
	moved := movedOverride(1, "2026-01-02", time.Date(2026, 1, 2, 12, 30, 0, 0, location), 60)
	sources := ConflictSources{Items: []ConflictSource{{Schedule: series.Schedule, Overrides: []OccurrenceOverride{moved}}}}

	targets, err := SelectNextTargetBatch(sources, time.Date(2026, 1, 2, 12, 0, 0, 0, location), location)
	if err != nil {
		t.Fatalf("SelectNextTargetBatch() error = %v", err)
	}
	if len(targets) != 1 || targets[0].OriginalDate != "2026-01-02" ||
		targets[0].StartLocal != time.Date(2026, 1, 2, 12, 30, 0, 0, location) {
		t.Fatalf("targets = %+v", targets)
	}
	start, end := conflictWindow(targets[0])

	// 移动到相邻发生内的例外属于同组冲突，不能被“排除整个系列”跳过；
	// 同刻的原始日期和例外身份属于同一批次，批次自身不互相冲突。
	batch := []Occurrence{targets[0], {ScheduleID: 1, OriginalDate: "2026-01-01",
		StartLocal: targets[0].StartLocal, EndLocal: targets[0].EndLocal, Title: "series"}}
	check, err := DetectOccurrenceConflicts(sources, batch, start, end, time.Hour, location)
	if err != nil {
		t.Fatalf("DetectOccurrenceConflicts() error = %v", err)
	}
	if len(check.Conflicts) != 1 {
		t.Fatalf("same-group conflicts = %+v", check.Conflicts)
	}
	conflict := check.Conflicts[0]
	if !conflict.SameGroup || conflict.Target.ScheduleID != 1 {
		t.Fatalf("same-group conflict = %+v", conflict)
	}
	// 同刻的原始日期和例外身份都属于目标批次，冲突的另一方是批次外的同组发生。
	if conflict.Target.OriginalDate != "2026-01-01" || conflict.Other.OriginalDate != "2026-01-02" {
		t.Fatalf("same-group identities = %+v", conflict)
	}
	if conflict.Other.Title != "moved" {
		t.Fatalf("same-group other title = %+v", conflict.Other)
	}
	if conflict.OverlapUnix != time.Date(2026, 1, 2, 12, 30, 0, 0, location).Unix() {
		t.Fatalf("overlap start = %d", conflict.OverlapUnix)
	}
	if !check.SameScheduleOnly {
		t.Fatalf("SameScheduleOnly = false for a single-schedule batch")
	}

	// 相隔较远的批次不把同系列其他发生当成冲突。
	otherDay, err := SelectNextTargetBatch(sources, time.Date(2026, 1, 1, 0, 0, 0, 0, location), location)
	if err != nil {
		t.Fatalf("SelectNextTargetBatch(other day) error = %v", err)
	}
	if len(otherDay) != 1 || otherDay[0].OriginalDate != "2026-01-01" {
		t.Fatalf("other-day targets = %+v", otherDay)
	}
	start, end = conflictWindow(otherDay[0])
	check, err = DetectOccurrenceConflicts(sources, otherDay, start, end, time.Hour, location)
	if err != nil {
		t.Fatalf("DetectOccurrenceConflicts(other day) error = %v", err)
	}
	if len(check.Conflicts) != 0 {
		t.Fatalf("distant batch conflicts = %+v", check.Conflicts)
	}

	// 目标身份自身不参与比较，即使被移动到与另一目标同刻的位置。
	check, err = DetectOccurrenceConflicts(sources, targets, start, end, time.Hour, location)
	if err != nil {
		t.Fatalf("DetectOccurrenceConflicts(self) error = %v", err)
	}
	for _, conflict := range check.Conflicts {
		if conflict.Other.OriginalDate == "2026-01-02" {
			t.Fatalf("target identity compared with itself: %+v", conflict)
		}
	}
}

// TestDetectOccurrenceConflictsExcludesCancelledOverride 验证取消例外不参与冲突比较。
func TestDetectOccurrenceConflictsExcludesCancelledOverride(t *testing.T) {
	location := time.UTC
	until := "2026-01-10"
	series := conflictingSchedule(1, "series", "0 12 * * *", until, time.Date(2026, 1, 1, 12, 0, 0, 0, location), 60)
	other := conflictingSchedule(2, "other", "30 12 * * *", until, time.Date(2026, 1, 1, 12, 30, 0, 0, location), 30)
	cancelled := OccurrenceOverride{
		ScheduleID: 1, OriginalDate: "2026-01-01", StartLocal: time.Date(2026, 1, 1, 12, 0, 0, 0, location),
		DurationMinutes: 60, Title: "series", Open: false, SnapshotPresent: true,
	}
	targets, err := SelectNextTargetBatch(ConflictSources{Items: []ConflictSource{other}}, time.Date(2026, 1, 1, 0, 0, 0, 0, location), location)
	if err != nil {
		t.Fatalf("SelectNextTargetBatch() error = %v", err)
	}
	start, end := conflictWindow(targets[0])

	check, err := DetectOccurrenceConflicts(
		ConflictSources{Items: []ConflictSource{{Schedule: series.Schedule, Overrides: []OccurrenceOverride{cancelled}}, other}},
		targets, start, end, time.Hour, location)
	if err != nil {
		t.Fatalf("DetectOccurrenceConflicts() error = %v", err)
	}
	for _, conflict := range check.Conflicts {
		if conflict.Other.ScheduleID == 1 {
			t.Fatalf("cancelled occurrence reported as conflict: %+v", conflict)
		}
	}
}

// TestDetectOccurrenceConflictsCoversLongDurationInstances 验证长持续时间实例在两个方向上都被检出。
func TestDetectOccurrenceConflictsCoversLongDurationInstances(t *testing.T) {
	location := time.UTC
	until := "2026-01-05"
	// 同一天的短发生既被前面的长实例覆盖，也覆盖后面的短实例。
	long := conflictingSchedule(1, "long", "0 9 * * *", until, time.Date(2026, 1, 1, 9, 0, 0, 0, location), 24*60)
	middle := conflictingSchedule(3, "middle", "0 20 * * *", until, time.Date(2026, 1, 1, 20, 0, 0, 0, location), 30)

	// 完整覆盖目标批次的长实例必须靠回看下界命中。
	targets, err := SelectNextTargetBatch(ConflictSources{Items: []ConflictSource{middle}}, time.Date(2026, 1, 1, 0, 0, 0, 0, location), location)
	if err != nil {
		t.Fatalf("SelectNextTargetBatch() error = %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("targets = %+v", targets)
	}
	start, end := conflictWindow(targets[0])

	check, err := DetectOccurrenceConflicts(ConflictSources{Items: []ConflictSource{long, middle}}, targets, start, end, 24*time.Hour, location)
	if err != nil {
		t.Fatalf("DetectOccurrenceConflicts(previous long) error = %v", err)
	}
	if len(check.Conflicts) != 1 || check.Conflicts[0].Target.ScheduleID != 1 ||
		check.Conflicts[0].Other.ScheduleID != 3 {
		t.Fatalf("previous long conflicts = %+v", check.Conflicts)
	}
	if check.Conflicts[0].OverlapUnix != start.Unix() {
		t.Fatalf("previous long overlap = %d, want %d", check.Conflicts[0].OverlapUnix, start.Unix())
	}

	// 完整覆盖目标批次的实例即使起点远在目标之前也要被检出。
	wide := conflictingSchedule(2, "wide", "0 10 * * *", until, time.Date(2026, 1, 1, 10, 0, 0, 0, location), 12*60)
	check, err = DetectOccurrenceConflicts(ConflictSources{Items: []ConflictSource{wide, middle}}, targets, start, end, 12*time.Hour, location)
	if err != nil {
		t.Fatalf("DetectOccurrenceConflicts(wide) error = %v", err)
	}
	if len(check.Conflicts) != 1 || check.Conflicts[0].Target.ScheduleID != 2 ||
		check.Conflicts[0].Other.ScheduleID != 3 {
		t.Fatalf("wide conflicts = %+v", check.Conflicts)
	}
	if check.Conflicts[0].Target.StartUnix != time.Date(2026, 1, 1, 10, 0, 0, 0, location).Unix() ||
		check.Conflicts[0].Target.EndUnix != time.Date(2026, 1, 1, 22, 0, 0, 0, location).Unix() {
		t.Fatalf("wide target = %+v", check.Conflicts[0].Target)
	}
}

// TestSelectNextTargetBatchReportsEmptyAndClosedGroups 验证无未来候选和关闭组不产生目标。
func TestSelectNextTargetBatchReportsEmptyAndClosedGroups(t *testing.T) {
	location := time.UTC
	until := "2026-01-05"
	daily := conflictingSchedule(1, "daily", "0 9 * * *", until, time.Date(2026, 1, 1, 9, 0, 0, 0, location), 30)
	// 截止日期之前已经没有发生，关闭组也不能恢复出候选。
	targets, err := SelectNextTargetBatch(ConflictSources{Items: []ConflictSource{daily}}, time.Date(2026, 2, 1, 0, 0, 0, 0, location), location)
	if err != nil {
		t.Fatalf("SelectNextTargetBatch() error = %v", err)
	}
	if len(targets) != 0 {
		t.Fatalf("past-cutoff targets = %+v", targets)
	}

	closed := daily
	closed.Schedule.Open = false
	targets, err = SelectNextTargetBatch(ConflictSources{Items: []ConflictSource{closed}}, time.Date(2026, 1, 1, 0, 0, 0, 0, location), location)
	if err != nil {
		t.Fatalf("SelectNextTargetBatch(closed) error = %v", err)
	}
	if len(targets) != 0 {
		t.Fatalf("closed-group targets = %+v", targets)
	}

	check, err := DetectOccurrenceConflicts(ConflictSources{Items: []ConflictSource{daily}}, nil,
		time.Date(2026, 1, 1, 9, 0, 0, 0, location), time.Date(2026, 1, 1, 9, 30, 0, 0, location), 0, location)
	if err != nil {
		t.Fatalf("DetectOccurrenceConflicts(empty) error = %v", err)
	}
	if check.Status != ConflictNoTarget || len(check.Conflicts) != 0 {
		t.Fatalf("empty check = %+v", check)
	}
}

// TestSelectNextTargetBatchValidatesLocation 验证缺少时区时拒绝计算。
func TestSelectNextTargetBatchValidatesLocation(t *testing.T) {
	if _, err := SelectNextTargetBatch(ConflictSources{}, time.Now(), nil); err == nil {
		t.Fatal("SelectNextTargetBatch(nil location) error = nil")
	}
	if _, err := DetectOccurrenceConflicts(ConflictSources{}, nil, time.Now(), time.Now().Add(time.Hour), 0, nil); err == nil {
		t.Fatal("DetectOccurrenceConflicts(nil location) error = nil")
	}
}
