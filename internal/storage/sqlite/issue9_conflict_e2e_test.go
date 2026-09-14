package sqlite

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/app"
	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// TestIssueNineConflictLifecycle 验证真实 SQLite 上创建、例外恢复、整组恢复和查询重叠标记的完整生命周期。
func TestIssueNineConflictLifecycle(t *testing.T) {
	ctx := context.Background()
	location := time.FixedZone("UTC+8", 8*60*60)
	clock := &recurringE2EClock{snapshot: domain.TimeSnapshot{
		Now: time.Date(2026, 1, 1, 8, 0, 0, 0, location), Location: location,
	}}
	path := filepath.Join(t.TempDir(), "issue9.db")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store, err := OpenStore(ctx, path, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	service := app.NewService(store, clock)

	first := createIssueNineSchedule(t, ctx, service, "first", time.Date(2026, 1, 2, 9, 0, 0, 0, location))
	second := createIssueNineSchedule(t, ctx, service, "second", time.Date(2026, 1, 2, 9, 0, 0, 0, location))
	third := createIssueNineSchedule(t, ctx, service, "third", time.Date(2026, 1, 2, 11, 0, 0, 0, location))
	if second.Conflict == nil || !second.Conflict.Checked || len(second.Conflict.Conflicts) != 1 {
		t.Fatalf("created conflict = %+v", second.Conflict)
	}
	if second.Conflict.Target == nil || second.Conflict.Target.ScheduleID != second.ID {
		t.Fatalf("created target = %+v", second.Conflict.Target)
	}
	pair := second.Conflict.Conflicts[0]
	if !((pair.Target.ScheduleID == first.ID && pair.Other.ScheduleID == second.ID) ||
		(pair.Target.ScheduleID == second.ID && pair.Other.ScheduleID == first.ID)) {
		t.Fatalf("created conflict pair = %+v", pair)
	}

	// 例外移动到另一组的时间，提交后的结果必须读取 SQLite 中已提交的完整快照。
	movedStart := time.Date(2026, 1, 2, 9, 30, 0, 0, location)
	movedEnd := movedStart.Add(time.Hour)
	moved, err := service.UpdateOccurrence(ctx, domain.ScheduleID(third.ID), app.UpdateOccurrenceRequest{
		OriginalDate: "2026-01-02", StartUnix: int64Pointer(movedStart.Unix()), EndUnix: int64Pointer(movedEnd.Unix()),
		ExpectedRevision: third.Revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	if moved.Conflict == nil || !moved.Conflict.Checked || moved.Conflict.Target == nil || moved.Conflict.Target.StartUnix != movedStart.Unix() {
		t.Fatalf("moved conflict = %+v", moved.Conflict)
	}
	if len(moved.Conflict.Conflicts) != 2 {
		t.Fatalf("moved conflicts = %+v", moved.Conflict.Conflicts)
	}

	// 取消例外不产生冲突结果；恢复后重新基于开放事实返回冲突。
	cancelled, err := service.CancelOccurrence(ctx, domain.ScheduleID(third.ID), app.OccurrenceActionRequest{
		OriginalDate: "2026-01-02", ExpectedRevision: moved.ScheduleRevision,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Conflict != nil || !cancelled.Cancelled {
		t.Fatalf("cancelled occurrence = %+v", cancelled)
	}
	restoredOccurrence, err := service.RestoreOccurrence(ctx, domain.ScheduleID(third.ID), app.OccurrenceActionRequest{
		OriginalDate: "2026-01-02", ExpectedRevision: cancelled.ScheduleRevision,
	})
	if err != nil {
		t.Fatal(err)
	}
	if restoredOccurrence.Conflict == nil || !restoredOccurrence.Conflict.Checked || len(restoredOccurrence.Conflict.Conflicts) != 2 {
		t.Fatalf("restored occurrence conflict = %+v", restoredOccurrence.Conflict)
	}

	// 关闭父组时不产生发生；恢复父组后，最终结果重新包含同刻的其他组发生。
	closed, err := service.CancelSchedule(ctx, domain.ScheduleID(second.ID), app.ScheduleActionRequest{ExpectedRevision: second.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if closed.Conflict != nil || closed.Open {
		t.Fatalf("closed schedule = %+v", closed)
	}
	restoredSchedule, err := service.RestoreSchedule(ctx, domain.ScheduleID(second.ID), app.ScheduleActionRequest{ExpectedRevision: closed.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if restoredSchedule.Conflict == nil || !restoredSchedule.Conflict.Checked || len(restoredSchedule.Conflict.Conflicts) < 1 {
		t.Fatalf("restored schedule conflict = %+v", restoredSchedule.Conflict)
	}
	if !conflictContainsSchedulePair(restoredSchedule.Conflict.Conflicts, first.ID, second.ID) {
		t.Fatalf("restored schedule missing first conflict = %+v", restoredSchedule.Conflict.Conflicts)
	}

	// 旧 revision 的正式提交必须失败，并且不能改变已提交日程事实。
	old := restoredSchedule.Revision - 1
	newTitle := "must not commit"
	_, err = service.UpdateSchedule(ctx, domain.ScheduleID(second.ID), app.UpdateScheduleRequest{
		Title: &newTitle, ExpectedRevision: old,
	})
	var businessErr *domain.Error
	if !errors.As(err, &businessErr) || businessErr.Code != domain.StaleRevision {
		t.Fatalf("stale update error = %#v", err)
	}
	storedSecond, err := store.GetSchedule(ctx, domain.ScheduleID(second.ID))
	if err != nil {
		t.Fatal(err)
	}
	if storedSecond.Title == newTitle || storedSecond.Revision != restoredSchedule.Revision {
		t.Fatalf("stale update changed fact = %+v", storedSecond)
	}

	// 列表重叠标记跨分页保持稳定，且查询不写入例外或提醒事实。
	beforeOverrides := countSQLiteRows(t, store, "occurrence_overrides")
	beforeReminders := countSQLiteRows(t, store, "reminder_attempts")
	from := time.Date(2026, 1, 2, 0, 0, 0, 0, location).Unix()
	to := time.Date(2026, 1, 4, 0, 0, 0, 0, location).Unix()
	marks := make(map[string]bool)
	cursor := ""
	for page := 0; ; page++ {
		if page > 20 {
			t.Fatal("issue9 occurrence pagination did not terminate")
		}
		result, queryErr := service.ListOccurrences(ctx, app.ListOccurrencesRequest{FromUnix: from, ToUnix: to, Limit: 1, Cursor: cursor})
		if queryErr != nil {
			t.Fatal(queryErr)
		}
		for _, item := range result.Items {
			marks[occurrenceQueryKey(item)] = item.Overlaps
		}
		if result.NextCursor == "" {
			break
		}
		cursor = result.NextCursor
	}
	if !marks[occurrenceIdentityKey(first.ID, "2026-01-02")] || !marks[occurrenceIdentityKey(second.ID, "2026-01-02")] || !marks[occurrenceIdentityKey(third.ID, "2026-01-02")] {
		t.Fatalf("same-time occurrence marks = %v", marks)
	}
	if marks[occurrenceIdentityKey(third.ID, "2026-01-03")] {
		t.Fatalf("unrelated occurrence marked = %v", marks)
	}
	if countSQLiteRows(t, store, "occurrence_overrides") != beforeOverrides || countSQLiteRows(t, store, "reminder_attempts") != beforeReminders {
		t.Fatal("occurrence query changed persisted facts")
	}
}

// createIssueNineSchedule 创建 Issue #9 生命周期测试使用的每日重复日程。
func createIssueNineSchedule(t *testing.T, ctx context.Context, service *app.Service, title string, start time.Time) app.ScheduleDTO {
	t.Helper()
	end := start.Add(time.Hour)
	until := time.Date(2026, 1, 4, 12, 0, 0, 0, start.Location())
	startUnix, endUnix, untilUnix := start.Unix(), end.Unix(), until.Unix()
	cron := "0 " + strconv.Itoa(start.Hour()) + " * * *"
	result, err := service.CreateSchedule(ctx, app.CreateScheduleRequest{
		Title: title, StartUnix: &startUnix, EndUnix: &endUnix, Cron: &cron, UntilUnix: &untilUnix,
		CronPresent: true, UntilUnixPresent: true,
	})
	if err != nil {
		t.Fatalf("CreateSchedule(%q): %v", title, err)
	}
	return result
}

// occurrenceIdentityKey 返回测试中使用的发生身份键。
func occurrenceIdentityKey(scheduleID int64, originalDate string) string {
	return string(rune(scheduleID)) + "/" + originalDate
}

// conflictContainsSchedulePair 判断冲突结果是否包含指定的两个父日程身份。
func conflictContainsSchedulePair(conflicts []app.ConflictDTO, left, right int64) bool {
	for _, conflict := range conflicts {
		if (conflict.Target.ScheduleID == left && conflict.Other.ScheduleID == right) ||
			(conflict.Target.ScheduleID == right && conflict.Other.ScheduleID == left) {
			return true
		}
	}
	return false
}
