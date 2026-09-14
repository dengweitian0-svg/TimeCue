package app

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// overlapSchedule 构造用于重叠标记测试的开放每日日程定义。
func overlapSchedule(id domain.ScheduleID, title, cron, until string, start time.Time, durationMinutes int) occurrenceQueryFake {
	return occurrenceQueryFake{generation: 3, sources: []OccurrenceSourceDTO{{Schedule: domain.Schedule{
		ID: id, Title: title, Cron: &cron, UntilDateLocal: &until, Open: true,
		FirstStartLocal: start, DurationMinutes: durationMinutes, Revision: 1,
	}}}}
}

// overlapFake 把多个日程合并为一个查询存储，保持日程 ID 升序。
func overlapFake(fakes ...occurrenceQueryFake) *occurrenceQueryFake {
	merged := &occurrenceQueryFake{generation: 3}
	for _, fake := range fakes {
		merged.sources = append(merged.sources, fake.sources...)
	}
	return merged
}

// occurrenceMarkByKey 收集跨页结果的重叠标记，键使用发生身份。
func occurrenceMarkByKey(t *testing.T, service *Service, request ListOccurrencesRequest) map[string]bool {
	t.Helper()
	marks := make(map[string]bool)
	cursor := ""
	for page := 0; ; page++ {
		if page > 50 {
			t.Fatal("overlap query did not terminate")
		}
		result, err := service.ListOccurrences(context.Background(), ListOccurrencesRequest{
			FromUnix: request.FromUnix, ToUnix: request.ToUnix, Limit: request.Limit, Cursor: cursor,
		})
		if err != nil {
			t.Fatalf("ListOccurrences() page %d error = %v", page, err)
		}
		for _, item := range result.Items {
			key := overlapKey(item)
			if _, exists := marks[key]; exists {
				t.Fatalf("duplicate occurrence %q", key)
			}
			marks[key] = item.Overlaps
		}
		if result.NextCursor == "" {
			return marks
		}
		cursor = result.NextCursor
	}
}

// overlapKey 返回跨页比较使用的稳定发生身份。
func overlapKey(item OccurrenceDTO) string {
	return strconv.FormatInt(item.ScheduleID, 10) + "/" + item.OriginalDate
}

// TestListOccurrencesMarksSameTimeIdentities 验证同刻多个身份互相标记重叠。
func TestListOccurrencesMarksSameTimeIdentities(t *testing.T) {
	location := time.UTC
	now := time.Date(2026, 1, 1, 8, 0, 0, 0, location)
	first := overlapSchedule(1, "first", "0 9 * * *", "2026-01-03", time.Date(2026, 1, 1, 9, 0, 0, 0, location), 30)
	second := overlapSchedule(2, "second", "0 9 * * *", "2026-01-03", time.Date(2026, 1, 1, 9, 0, 0, 0, location), 30)
	service := NewService(overlapFake(first, second), &fakeClock{snapshot: domain.TimeSnapshot{Now: now, Location: location}})

	from := time.Date(2026, 1, 1, 0, 0, 0, 0, location).Unix()
	to := time.Date(2026, 1, 4, 0, 0, 0, 0, location).Unix()
	result, err := service.ListOccurrences(context.Background(), ListOccurrencesRequest{FromUnix: from, ToUnix: to, Limit: 10})
	if err != nil {
		t.Fatalf("ListOccurrences() error = %v", err)
	}
	if len(result.Items) != 6 {
		t.Fatalf("items = %+v", result.Items)
	}
	for _, item := range result.Items {
		if !item.Overlaps {
			t.Fatalf("same-time occurrence not marked: %+v", item)
		}
	}
}

// TestListOccurrencesMarksLongDurationOverlap 验证长持续时间区间与短发生互相标记。
func TestListOccurrencesMarksLongDurationOverlap(t *testing.T) {
	location := time.UTC
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, location)
	// 长实例跨越两天，会与中间任何一天的发生相交。
	long := overlapSchedule(1, "long", "0 9 * * *", "2026-01-03", time.Date(2026, 1, 1, 9, 0, 0, 0, location), 48*60)
	short := overlapSchedule(2, "short", "0 20 * * *", "2026-01-03", time.Date(2026, 1, 1, 20, 0, 0, 0, location), 30)
	service := NewService(overlapFake(long, short), &fakeClock{snapshot: domain.TimeSnapshot{Now: now, Location: location}})

	from := time.Date(2026, 1, 1, 0, 0, 0, 0, location).Unix()
	to := time.Date(2026, 1, 4, 0, 0, 0, 0, location).Unix()
	marks := occurrenceMarkByKey(t, service, ListOccurrencesRequest{FromUnix: from, ToUnix: to, Limit: 50})
	for key, marked := range marks {
		if !marked {
			t.Fatalf("long-duration overlap not marked: %s", key)
		}
	}
	if len(marks) != 6 {
		t.Fatalf("marks = %v", marks)
	}
}

// TestListOccurrencesMarksOverlapAcrossPages 验证分页结果中的跨分页相交仍被标记。
func TestListOccurrencesMarksOverlapAcrossPages(t *testing.T) {
	location := time.UTC
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, location)
	// 每天 9 点的两小时长实例与每天 10 点的短发生相交，交集落在相邻分页之间。
	long := overlapSchedule(1, "long", "0 9 * * *", "2026-01-05", time.Date(2026, 1, 1, 9, 0, 0, 0, location), 120)
	short := overlapSchedule(2, "short", "0 10 * * *", "2026-01-05", time.Date(2026, 1, 1, 10, 0, 0, 0, location), 15)
	service := NewService(overlapFake(long, short), &fakeClock{snapshot: domain.TimeSnapshot{Now: now, Location: location}})

	from := time.Date(2026, 1, 1, 0, 0, 0, 0, location).Unix()
	to := time.Date(2026, 1, 4, 0, 0, 0, 0, location).Unix()
	single := occurrenceMarkByKey(t, service, ListOccurrencesRequest{FromUnix: from, ToUnix: to, Limit: 50})
	// 使用会切分相交对的页大小重复查询，标记必须与单页结果完全一致。
	for _, limit := range []int{1, 2, 3, 5} {
		paged := occurrenceMarkByKey(t, service, ListOccurrencesRequest{FromUnix: from, ToUnix: to, Limit: limit})
		if len(paged) != len(single) {
			t.Fatalf("limit %d marks = %v, want %v", limit, paged, single)
		}
		for key, marked := range single {
			if paged[key] != marked {
				t.Fatalf("limit %d mark for %s = %v, want %v", limit, key, paged[key], marked)
			}
		}
	}
	for key, marked := range single {
		if !marked {
			t.Fatalf("cross-page overlap not marked: %s", key)
		}
	}
}

// TestListOccurrencesLeavesTouchingBoundariesUnmarked 验证边界相接不算重叠。
func TestListOccurrencesLeavesTouchingBoundariesUnmarked(t *testing.T) {
	location := time.UTC
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, location)
	// 10:00 结束与 10:00 开始的半开区间相接，双方都不应被标记。
	morning := overlapSchedule(1, "morning", "0 9 * * *", "2026-01-04", time.Date(2026, 1, 1, 9, 0, 0, 0, location), 60)
	noon := overlapSchedule(2, "noon", "0 10 * * *", "2026-01-04", time.Date(2026, 1, 1, 10, 0, 0, 0, location), 60)
	service := NewService(overlapFake(morning, noon), &fakeClock{snapshot: domain.TimeSnapshot{Now: now, Location: location}})

	from := time.Date(2026, 1, 1, 0, 0, 0, 0, location).Unix()
	to := time.Date(2026, 1, 4, 0, 0, 0, 0, location).Unix()
	marks := occurrenceMarkByKey(t, service, ListOccurrencesRequest{FromUnix: from, ToUnix: to, Limit: 50})
	for key, marked := range marks {
		if marked {
			t.Fatalf("touching boundary marked as overlap: %s", key)
		}
	}
}

// TestListOccurrencesIgnoresClosedGroupsForOverlap 验证关闭组不参与重叠标记。
func TestListOccurrencesIgnoresClosedGroupsForOverlap(t *testing.T) {
	location := time.UTC
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, location)
	open := overlapSchedule(1, "open", "0 9 * * *", "2026-01-03", time.Date(2026, 1, 1, 9, 0, 0, 0, location), 30)
	closed := overlapSchedule(2, "closed", "0 9 * * *", "2026-01-03", time.Date(2026, 1, 1, 9, 0, 0, 0, location), 30)
	closed.sources[0].Schedule.Open = false
	service := NewService(overlapFake(open, closed), &fakeClock{snapshot: domain.TimeSnapshot{Now: now, Location: location}})

	from := time.Date(2026, 1, 1, 0, 0, 0, 0, location).Unix()
	to := time.Date(2026, 1, 4, 0, 0, 0, 0, location).Unix()
	result, err := service.ListOccurrences(context.Background(), ListOccurrencesRequest{FromUnix: from, ToUnix: to, Limit: 50})
	if err != nil {
		t.Fatalf("ListOccurrences() error = %v", err)
	}
	if len(result.Items) != 3 {
		t.Fatalf("items = %+v", result.Items)
	}
	for _, item := range result.Items {
		if item.ScheduleID != 1 || item.Overlaps {
			t.Fatalf("closed group affected overlap marks: %+v", item)
		}
	}
}

// TestListOccurrencesKeepsStableOrderAndCursorWithOverlap 验证重叠标记不改变排序和游标语义。
func TestListOccurrencesKeepsStableOrderAndCursorWithOverlap(t *testing.T) {
	location := time.UTC
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, location)
	first := overlapSchedule(1, "first", "0 9 * * *", "2026-01-04", time.Date(2026, 1, 1, 9, 0, 0, 0, location), 90)
	second := overlapSchedule(2, "second", "0 9 * * *", "2026-01-04", time.Date(2026, 1, 1, 9, 0, 0, 0, location), 30)
	service := NewService(overlapFake(first, second), &fakeClock{snapshot: domain.TimeSnapshot{Now: now, Location: location}})

	from := time.Date(2026, 1, 1, 0, 0, 0, 0, location).Unix()
	to := time.Date(2026, 1, 4, 0, 0, 0, 0, location).Unix()
	collected := make([]OccurrenceDTO, 0, 6)
	cursor := ""
	for page := 0; ; page++ {
		if page > 20 {
			t.Fatal("cursor pagination did not terminate")
		}
		result, err := service.ListOccurrences(context.Background(), ListOccurrencesRequest{
			FromUnix: from, ToUnix: to, Limit: 2, Cursor: cursor,
		})
		if err != nil {
			t.Fatalf("ListOccurrences() page %d error = %v", page, err)
		}
		collected = append(collected, result.Items...)
		if result.NextCursor == "" {
			break
		}
		if len(result.Items) != 2 {
			t.Fatalf("non-terminal page = %+v", result.Items)
		}
		cursor = result.NextCursor
	}
	if len(collected) != 6 {
		t.Fatalf("collected = %+v", collected)
	}
	for index := 1; index < len(collected); index++ {
		if occurrenceDTOKeyLess(collected[index], collected[index-1]) {
			t.Fatalf("unstable order: %+v then %+v", collected[index-1], collected[index])
		}
	}
	// 同一批次内两个身份同刻开始，按日程 ID 稳定排序。
	if collected[0].ScheduleID != 1 || collected[1].ScheduleID != 2 || !collected[0].Overlaps || !collected[1].Overlaps {
		t.Fatalf("same-time batch = %+v", collected[:2])
	}
}

// occurrenceDTOKeyLess 判断查询 DTO 是否位于另一个 DTO 之前。
func occurrenceDTOKeyLess(left, right OccurrenceDTO) bool {
	if left.StartUnix != right.StartUnix {
		return left.StartUnix < right.StartUnix
	}
	if left.ScheduleID != right.ScheduleID {
		return left.ScheduleID < right.ScheduleID
	}
	return left.OriginalDate < right.OriginalDate
}
