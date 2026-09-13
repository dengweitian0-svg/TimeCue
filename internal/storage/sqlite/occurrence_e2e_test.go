package sqlite

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/app"
	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// TestIssueFiveOccurrenceLifecycle 验证例外移动、整组编辑保留、截止收缩和重开结果一致。
func TestIssueFiveOccurrenceLifecycle(t *testing.T) {
	ctx := context.Background()
	location := time.FixedZone("UTC+8", 8*60*60)
	clock := &recurringE2EClock{snapshot: domain.TimeSnapshot{
		Now:      time.Date(2026, 9, 1, 12, 0, 0, 0, location),
		Location: location,
	}}
	path := filepath.Join(t.TempDir(), "occurrences.db")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store, err := OpenStore(ctx, path, logger)
	if err != nil {
		t.Fatal(err)
	}
	service := app.NewService(store, clock)
	start := time.Date(2026, 9, 7, 10, 0, 0, 0, location)
	until := time.Date(2026, 9, 30, 12, 0, 0, 0, location)
	startUnix, endUnix, untilUnix := start.Unix(), start.Add(time.Hour).Unix(), until.Unix()
	cron := "0 10 * * *"
	created, err := service.CreateSchedule(ctx, app.CreateScheduleRequest{
		Title: "parent title", Notes: "parent notes", StartUnix: &startUnix, EndUnix: &endUnix,
		Cron: &cron, UntilUnix: &untilUnix, CronPresent: true, UntilUnixPresent: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	id := domain.ScheduleID(created.ID)

	// 09-07 移到 09-20；标题、备注和时长作为例外自己的完整快照保存。
	firstMoved := time.Date(2026, 9, 20, 10, 0, 0, 0, location)
	firstTitle, firstNotes := "special title", "special notes"
	first, err := updateOccurrenceForTest(ctx, service, id, created.Revision, "2026-09-07", firstMoved, nil, &firstTitle, &firstNotes)
	if err != nil {
		t.Fatal(err)
	}

	// 09-21 提前到 09-10，验证只改端点时继承基础标题和备注。
	secondMoved := time.Date(2026, 9, 10, 10, 0, 0, 0, location)
	second, err := updateOccurrenceForTest(ctx, service, id, first.ScheduleRevision, "2026-09-21", secondMoved, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// 两个不同原始身份移动到同一时刻，必须都保留；另一条例外跨月并跨越本地日期边界。
	thirdMoved := firstMoved
	third, err := updateOccurrenceForTest(ctx, service, id, second.ScheduleRevision, "2026-09-25", thirdMoved, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	crossDayStart := time.Date(2026, 10, 5, 23, 30, 0, 0, location)
	crossDayEnd := time.Date(2026, 10, 6, 1, 30, 0, 0, location)
	fourth, err := updateOccurrenceForTest(ctx, service, id, third.ScheduleRevision, "2026-09-28", crossDayStart, &crossDayEnd, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.ScheduleRevision != created.Revision+1 || second.ScheduleRevision != created.Revision+2 || fourth.ScheduleRevision != created.Revision+4 {
		t.Fatalf("occurrence revisions = %d, %d, %d", first.ScheduleRevision, second.ScheduleRevision, fourth.ScheduleRevision)
	}

	overrides, err := store.ListOccurrenceOverrides(ctx, id)
	if err != nil || len(overrides) != 4 {
		t.Fatalf("overrides before recurring edit = %+v, %v", overrides, err)
	}

	// 整组编辑同步改规则、父文本和时长；例外数量应反馈给调用方，且例外快照不漂移。
	newFirst := time.Date(2026, 9, 7, 12, 0, 0, 0, location)
	newEnd := newFirst.Add(2 * time.Hour)
	newCron := "0 12 * * *"
	newTitle, newNotes := "edited parent", "edited parent notes"
	newStartUnix, newEndUnix := newFirst.Unix(), newEnd.Unix()
	edited, err := service.UpdateSchedule(ctx, id, app.UpdateScheduleRequest{
		Title: &newTitle, Notes: &newNotes, Cron: &newCron, StartUnix: &newStartUnix, EndUnix: &newEndUnix,
		ExpectedRevision: fourth.ScheduleRevision,
	})
	if err != nil {
		t.Fatal(err)
	}
	if edited.PreservedOverrideCount != 4 || edited.Title != newTitle || edited.Notes != newNotes || edited.DurationMinutes != 120 {
		t.Fatalf("edited schedule = %+v", edited)
	}
	overrides, err = store.ListOccurrenceOverrides(ctx, id)
	if err != nil || len(overrides) != 4 {
		t.Fatalf("overrides after recurring edit = %+v, %v", overrides, err)
	}
	assertIssueFiveSnapshots(t, overrides, location)

	// 父规则截止日缩短后，位于新截止日之后的例外仍然是有效发生。
	shortUntil := time.Date(2026, 9, 21, 12, 0, 0, 0, location).Unix()
	shortened, err := service.UpdateSchedule(ctx, id, app.UpdateScheduleRequest{UntilUnix: &shortUntil, ExpectedRevision: edited.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if shortened.PreservedOverrideCount != 4 || shortened.UntilDateLocal == nil || *shortened.UntilDateLocal != "2026-09-21" {
		t.Fatalf("shortened schedule = %+v", shortened)
	}

	stored, err := store.GetSchedule(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	overrides, err = store.ListOccurrenceOverrides(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	merged, err := domain.ExpandMergedOccurrences(stored, overrides, time.Date(2026, 9, 1, 0, 0, 0, 0, location), time.Date(2026, 10, 10, 0, 0, 0, 0, location), location)
	if err != nil {
		t.Fatal(err)
	}
	assertIssueFiveMerged(t, merged, location)

	next, ok, err := domain.NextMergedOccurrence(stored, overrides, time.Date(2026, 9, 30, 0, 0, 0, 0, location), location)
	if err != nil || !ok || next.OriginalDate != "2026-09-28" || !next.StartLocal.Equal(crossDayStart) || !next.EndLocal.Equal(crossDayEnd) {
		t.Fatalf("next post-cutoff exception = %+v, %v, %v", next, ok, err)
	}

	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = OpenStore(ctx, path, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	reopened, err := store.GetSchedule(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	reopenedOverrides, err := store.ListOccurrenceOverrides(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	reopenedMerged, err := domain.ExpandMergedOccurrences(reopened, reopenedOverrides, time.Date(2026, 9, 1, 0, 0, 0, 0, location), time.Date(2026, 10, 10, 0, 0, 0, 0, location), location)
	if err != nil {
		t.Fatal(err)
	}
	assertIssueFiveMerged(t, reopenedMerged, location)

	if _, err := store.DeleteSchedule(ctx, id, reopened.Revision); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM occurrence_overrides WHERE schedule_id = ?", id).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("cascaded occurrence overrides = %d, want 0", remaining)
	}
}

// updateOccurrenceForTest 使用补丁语义调用例外更新，并返回新父版本。
func updateOccurrenceForTest(ctx context.Context, service *app.Service, id domain.ScheduleID, revision int64, originalDate string, start time.Time, end *time.Time, title, notes *string) (app.OccurrenceDTO, error) {
	startUnix := start.Unix()
	request := app.UpdateOccurrenceRequest{OriginalDate: originalDate, StartUnix: &startUnix, Title: title, Notes: notes, ExpectedRevision: revision}
	if end != nil {
		endUnix := end.Unix()
		request.EndUnix = &endUnix
	}
	return service.UpdateOccurrence(ctx, id, request)
}

// assertIssueFiveSnapshots 验证父日程编辑没有改写例外自己的完整文本和时长。
func assertIssueFiveSnapshots(t *testing.T, overrides []domain.OccurrenceOverride, location *time.Location) {
	t.Helper()
	byDate := make(map[string]domain.OccurrenceOverride, len(overrides))
	for _, override := range overrides {
		byDate[override.OriginalDate] = override
	}
	first := byDate["2026-09-07"]
	if first.Title != "special title" || first.Notes != "special notes" || first.DurationMinutes != 60 || !first.StartLocal.Equal(time.Date(2026, 9, 20, 10, 0, 0, 0, location)) {
		t.Fatalf("first snapshot drifted = %+v", first)
	}
	second := byDate["2026-09-21"]
	if second.Title != "parent title" || second.Notes != "parent notes" || second.DurationMinutes != 60 || !second.StartLocal.Equal(time.Date(2026, 9, 10, 10, 0, 0, 0, location)) {
		t.Fatalf("second snapshot drifted = %+v", second)
	}
	fourth := byDate["2026-09-28"]
	if fourth.DurationMinutes != 120 || !fourth.StartLocal.Equal(time.Date(2026, 10, 5, 23, 30, 0, 0, location)) {
		t.Fatalf("cross-day snapshot drifted = %+v", fourth)
	}
}

// assertIssueFiveMerged 验证稳定身份去重、同刻排序和跨日跨月例外结果。
func assertIssueFiveMerged(t *testing.T, occurrences []domain.Occurrence, location *time.Location) {
	t.Helper()
	if len(occurrences) != 17 {
		t.Fatalf("merged occurrence count = %d, want 17", len(occurrences))
	}
	var sameTime []domain.Occurrence
	var movedEarly, crossDay bool
	for _, occurrence := range occurrences {
		if occurrence.StartLocal.Equal(time.Date(2026, 9, 20, 10, 0, 0, 0, location)) {
			sameTime = append(sameTime, occurrence)
		}
		if occurrence.OriginalDate == "2026-09-21" && occurrence.StartLocal.Equal(time.Date(2026, 9, 10, 10, 0, 0, 0, location)) {
			movedEarly = true
		}
		if occurrence.OriginalDate == "2026-09-28" && occurrence.EndLocal.Equal(time.Date(2026, 10, 6, 1, 30, 0, 0, location)) {
			crossDay = true
		}
	}
	if len(sameTime) != 2 || sameTime[0].OriginalDate != "2026-09-07" || sameTime[1].OriginalDate != "2026-09-25" {
		t.Fatalf("same-time identities = %+v", sameTime)
	}
	if !movedEarly || !crossDay {
		t.Fatalf("merged exception flags = moved early %v, cross day %v", movedEarly, crossDay)
	}
}
