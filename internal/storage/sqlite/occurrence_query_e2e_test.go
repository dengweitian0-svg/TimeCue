package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/app"
	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// TestListOccurrencesSQLiteEndToEnd 验证真实 SQLite 上多年查询、例外合并、边界和稳定分页。
func TestListOccurrencesSQLiteEndToEnd(t *testing.T) {
	ctx := context.Background()
	location := time.FixedZone("UTC+8", 8*60*60)
	now := time.Date(2026, 1, 10, 12, 0, 0, 0, location)
	clock := &recurringE2EClock{snapshot: domain.TimeSnapshot{Now: now, Location: location}}
	store := openTestStore(t)
	service := app.NewService(store, clock)

	dailyStart := time.Date(2022, 1, 1, 23, 30, 0, 0, location)
	dailyUntil := time.Date(2026, 1, 5, 12, 0, 0, 0, location)
	daily := createOccurrenceQuerySchedule(t, ctx, service, "多年跨天日程", dailyStart, 2*time.Hour, "30 23 * * *", dailyUntil)
	weeklyStart := time.Date(2022, 1, 3, 9, 0, 0, 0, location)
	weekly := createOccurrenceQuerySchedule(t, ctx, service, "多年周日程", weeklyStart, time.Hour, "0 9 * * 1", dailyUntil)
	closedStart := time.Date(2025, 12, 1, 12, 0, 0, 0, location)
	closed := createOccurrenceQuerySchedule(t, ctx, service, "关闭整组", closedStart, time.Hour, "0 12 * * *", dailyUntil)

	movedStart := time.Date(2024, 1, 3, 0, 15, 0, 0, location)
	movedEnd := movedStart.Add(2 * time.Hour)
	moved, err := service.UpdateOccurrence(ctx, domain.ScheduleID(daily.ID), app.UpdateOccurrenceRequest{
		OriginalDate:     "2024-01-02",
		StartUnix:        int64Pointer(movedStart.Unix()),
		EndUnix:          int64Pointer(movedEnd.Unix()),
		ExpectedRevision: daily.Revision,
	})
	if err != nil {
		t.Fatalf("UpdateOccurrence() error = %v", err)
	}
	if moved.OriginalDate != "2024-01-02" || moved.StartUnix != movedStart.Unix() {
		t.Fatalf("moved occurrence = %+v", moved)
	}
	if _, err := service.CancelSchedule(ctx, domain.ScheduleID(closed.ID), app.ScheduleActionRequest{ExpectedRevision: closed.Revision}); err != nil {
		t.Fatalf("CancelSchedule() error = %v", err)
	}

	beforeOverrides := countSQLiteRows(t, store, "occurrence_overrides")
	beforeReminders := countSQLiteRows(t, store, "reminder_attempts")
	bounds := domain.CalculateTimeBounds(clock.snapshot)
	from := time.Date(2022, 1, 1, 0, 0, 0, 0, location).Unix()
	to := bounds.QueryMaxExclusive.Unix()

	crossDay, err := service.ListOccurrences(ctx, app.ListOccurrencesRequest{
		FromUnix: time.Date(2024, 1, 2, 0, 0, 0, 0, location).Unix(),
		ToUnix:   time.Date(2024, 1, 2, 2, 0, 0, 0, location).Unix(),
		Limit:    50,
	})
	if err != nil {
		t.Fatalf("cross-day ListOccurrences() error = %v", err)
	}
	if len(crossDay.Items) != 1 || crossDay.Items[0].ScheduleID != daily.ID || crossDay.Items[0].OriginalDate != "2024-01-01" {
		t.Fatalf("cross-day results = %+v", crossDay.Items)
	}

	var all []app.OccurrenceDTO
	cursor := ""
	seen := make(map[string]struct{})
	for page := 0; ; page++ {
		if page > 100 {
			t.Fatal("pagination did not terminate")
		}
		result, queryErr := service.ListOccurrences(ctx, app.ListOccurrencesRequest{
			FromUnix: from, ToUnix: to, Limit: 37, Cursor: cursor,
		})
		if queryErr != nil {
			t.Fatalf("paged ListOccurrences() page %d error = %v", page, queryErr)
		}
		for _, item := range result.Items {
			key := occurrenceQueryKey(item)
			if _, exists := seen[key]; exists {
				t.Fatalf("duplicate occurrence %q", key)
			}
			seen[key] = struct{}{}
			if len(all) > 0 && occurrenceDTOKeyLess(item, all[len(all)-1]) {
				t.Fatalf("unstable ordering: previous=%+v current=%+v", all[len(all)-1], item)
			}
			all = append(all, item)
		}
		if result.NextCursor == "" {
			break
		}
		if len(result.Items) != 37 {
			t.Fatalf("non-terminal page length = %d, want 37", len(result.Items))
		}
		cursor = result.NextCursor
	}

	if len(all) < 1500 {
		t.Fatalf("multi-year result count = %d, want at least 1500", len(all))
	}
	var foundMoved, foundClosed bool
	for _, item := range all {
		if item.ScheduleID == daily.ID && item.OriginalDate == "2024-01-02" {
			foundMoved = item.StartUnix == movedStart.Unix() && item.EndUnix == movedEnd.Unix()
		}
		if item.ScheduleID == closed.ID {
			foundClosed = true
		}
	}
	if !foundMoved {
		t.Fatal("moved exception is missing or has the wrong actual time")
	}
	if foundClosed {
		t.Fatal("closed schedule returned occurrences")
	}
	if countSQLiteRows(t, store, "occurrence_overrides") != beforeOverrides ||
		countSQLiteRows(t, store, "reminder_attempts") != beforeReminders {
		t.Fatal("occurrence query changed persisted exception or reminder facts")
	}

	_, err = service.ListOccurrences(ctx, app.ListOccurrencesRequest{
		FromUnix: from, ToUnix: to + 1, Limit: 50,
	})
	var businessErr *domain.Error
	if !errors.As(err, &businessErr) || businessErr.Code != domain.TimeRangeExceeded ||
		businessErr.Bounds == nil || businessErr.Bounds.QueryMaxExclusive.Unix() != to {
		t.Fatalf("upper-bound error = %#v, want TIME_RANGE_EXCEEDED at %d", err, to)
	}

	first, err := service.ListOccurrences(ctx, app.ListOccurrencesRequest{FromUnix: from, ToUnix: to, Limit: 2})
	if err != nil || first.NextCursor == "" {
		t.Fatalf("cursor seed page = %+v, %v", first, err)
	}
	newNotes := "changed after cursor"
	if _, err := service.UpdateSchedule(ctx, domain.ScheduleID(weekly.ID), app.UpdateScheduleRequest{
		Notes: &newNotes, ExpectedRevision: weekly.Revision,
	}); err != nil {
		t.Fatalf("UpdateSchedule() after cursor error = %v", err)
	}
	_, err = service.ListOccurrences(ctx, app.ListOccurrencesRequest{
		FromUnix: from, ToUnix: to, Limit: 2, Cursor: first.NextCursor,
	})
	if !errors.As(err, &businessErr) || businessErr.Code != domain.StaleCursor {
		t.Fatalf("stale cursor error = %#v, want STALE_CURSOR", err)
	}
}

// createOccurrenceQuerySchedule 创建用于发生查询端到端测试的重复日程。
func createOccurrenceQuerySchedule(t *testing.T, ctx context.Context, service *app.Service, title string, start time.Time, duration time.Duration, cron string, until time.Time) app.ScheduleDTO {
	t.Helper()
	startUnix, endUnix, untilUnix := start.Unix(), start.Add(duration).Unix(), until.Unix()
	result, err := service.CreateSchedule(ctx, app.CreateScheduleRequest{
		Title: title, StartUnix: &startUnix, EndUnix: &endUnix, Cron: &cron, UntilUnix: &untilUnix,
		CronPresent: true, UntilUnixPresent: true,
	})
	if err != nil {
		t.Fatalf("CreateSchedule(%q) error = %v", title, err)
	}
	return result
}

// countSQLiteRows 读取查询前后的持久化事实数量。
func countSQLiteRows(t *testing.T, store *Store, table string) int {
	t.Helper()
	var count int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
		t.Fatalf("count %s rows: %v", table, err)
	}
	return count
}

// int64Pointer 返回用于应用补丁请求的整数指针。
func int64Pointer(value int64) *int64 {
	return &value
}

// occurrenceQueryKey 返回发生的稳定身份键。
func occurrenceQueryKey(item app.OccurrenceDTO) string {
	return string(rune(item.ScheduleID)) + "/" + item.OriginalDate
}

// occurrenceDTOKeyLess 判断查询 DTO 是否位于另一个 DTO 之前。
func occurrenceDTOKeyLess(left, right app.OccurrenceDTO) bool {
	if left.StartUnix != right.StartUnix {
		return left.StartUnix < right.StartUnix
	}
	if left.ScheduleID != right.ScheduleID {
		return left.ScheduleID < right.ScheduleID
	}
	return left.OriginalDate < right.OriginalDate
}
