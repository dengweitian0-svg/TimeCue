package domain

import (
	"math"
	"reflect"
	"testing"
	"time"
	_ "time/tzdata"
)

// recurrenceLocation 加载真实时区，使 DST 测试不依赖操作系统时区配置。
func recurrenceLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

// recurrenceSchedule 构造用于纯计算的重复定义。
func recurrenceSchedule(cron, until string, first time.Time) Schedule {
	return Schedule{ID: 7, Cron: &cron, UntilDateLocal: &until, FirstStartLocal: first, DurationMinutes: 90}
}

// TestParseCronStrict 验证仅支持每日和每周的严格五字段规则。
func TestParseCronStrict(t *testing.T) {
	for _, value := range []string{"0 0 * * *", "59 23 * * 0", "30 9 * * 6", "  05\t09 * * *  ", "0 9 1 * *", "0 9 29 * *", "0 9 30 * *", "0 9 31 * *"} {
		if _, err := ParseCron(value); err != nil {
			t.Errorf("%q: %v", value, err)
		}
	}
	for _, value := range []string{"", "0 9 * *", "0 0 9 * * *", "* 9 * * *", "0 * * * *", "60 9 * * *", "0 24 * * *", "0 9 0 * *", "0 9 32 * *", "0 9 1 * 0", "0 9 * 1 *", "0 9 * * 7", "0 9 * * MON", "0 9 * * 1-5", "0 9 * * 1,2", "0 9 1-2 * *", "0 9 1,2 * *", "0 9 */2 * *", "*/5 9 * * *", "+1 9 * * *", "-1 9 * * *", "1.0 9 * * *", "@daily", "0 9 * * ?"} {
		if _, err := ParseCron(value); err == nil {
			t.Errorf("accepted %q", value)
		}
	}
}

// TestRecurringInclusiveAndHistorical 验证首次、截止日期、严格 after 与历史重算。
func TestRecurringInclusiveAndHistorical(t *testing.T) {
	loc := time.FixedZone("UTC+8", 8*3600)
	first := time.Date(2024, 2, 28, 9, 0, 0, 0, loc)
	s := recurrenceSchedule("0 9 * * *", "2024-03-01", first)
	want := []time.Time{first, first.AddDate(0, 0, 1), first.AddDate(0, 0, 2)}
	got, err := Expand(s, first, first.AddDate(0, 0, 10), loc)
	if err != nil || len(got) != len(want) {
		t.Fatalf("Expand = %v, %v", got, err)
	}
	for i, occurrence := range got {
		if !occurrence.StartLocal.Equal(want[i]) || occurrence.EndLocal.Sub(occurrence.StartLocal) != 90*time.Minute || occurrence.ScheduleID != s.ID {
			t.Errorf("occurrence = %+v", occurrence)
		}
	}
	next, ok, err := NextStart(s, first, loc)
	if err != nil || !ok || !next.Equal(want[1]) {
		t.Fatalf("strict next = %v %v %v", next, ok, err)
	}
	if _, ok, err := NextStart(s, want[2], loc); err != nil || ok {
		t.Fatalf("past cutoff = %v %v", ok, err)
	}
	again, err := Expand(s, first, first.AddDate(0, 0, 10), loc)
	if err != nil || !reflect.DeepEqual(got, again) {
		t.Fatal("historical recomputation changed")
	}
	window, err := Expand(s, first, want[1], loc)
	if err != nil || len(window) != 1 {
		t.Fatalf("exclusive end = %v %v", window, err)
	}
}

// TestWeeklyAndFirstThreshold 验证每日每周转换及首次不匹配时向后查找。
func TestWeeklyAndFirstThreshold(t *testing.T) {
	first := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		cron string
		want time.Time
	}{
		{"0 9 * * *", time.Date(2026, 9, 13, 9, 0, 0, 0, time.UTC)},
		{"0 9 * * 6", time.Date(2026, 9, 19, 9, 0, 0, 0, time.UTC)},
		{"0 9 * * 0", time.Date(2026, 9, 13, 9, 0, 0, 0, time.UTC)},
	} {
		s := recurrenceSchedule(tc.cron, "2026-09-19", first)
		got, ok, err := NextStart(s, first.AddDate(-1, 0, 0), time.UTC)
		if err != nil || !ok || !got.Equal(tc.want) {
			t.Errorf("%s: %v %v %v", tc.cron, got, ok, err)
		}
	}
}

// TestRecurringDST 验证缺失后移、重复取早、非整小时跳变和自然日推进。
func TestRecurringDST(t *testing.T) {
	for _, tc := range []struct{ zone, cron, date, want string }{
		{"America/New_York", "30 2 * * *", "2026-03-08", "2026-03-08T03:30:00-04:00"},
		{"America/New_York", "30 1 * * *", "2026-11-01", "2026-11-01T01:30:00-04:00"},
		{"Australia/Lord_Howe", "15 2 * * *", "2026-10-04", "2026-10-04T02:45:00+11:00"},
	} {
		loc := recurrenceLocation(t, tc.zone)
		date, err := time.ParseInLocation(time.DateOnly, tc.date, loc)
		if err != nil {
			t.Fatal(err)
		}
		s := recurrenceSchedule(tc.cron, date.AddDate(0, 0, 1).Format(time.DateOnly), date)
		got, err := Expand(s, date, date.AddDate(0, 0, 2), loc)
		if err != nil || len(got) != 2 {
			t.Fatalf("%s: %v %v", tc.zone, got, err)
		}
		if got[0].StartLocal.Format(time.RFC3339) != tc.want {
			t.Errorf("got %v want %s", got[0], tc.want)
		}
		if got[1].StartLocal.Day() != date.AddDate(0, 0, 1).Day() {
			t.Fatal("did not advance calendar date")
		}
		if got[0].EndLocal.Sub(got[0].StartLocal) != 90*time.Minute {
			t.Fatal("DST changed elapsed duration")
		}
		if tc.date == "2026-11-01" {
			late, err := time.Parse(time.RFC3339, "2026-11-01T01:15:00-05:00")
			if err != nil {
				t.Fatal(err)
			}
			next, ok, err := NextStart(s, late, loc)
			if err != nil || !ok || next.Day() != 2 {
				t.Fatalf("fold emitted twice: %v %v %v", next, ok, err)
			}
		}
	}
}

// TestValidateRecurring 验证截止日期按本地日期包含，并拒绝非法端点和溢出。
func TestValidateRecurring(t *testing.T) {
	loc := recurrenceLocation(t, "Asia/Shanghai")
	start := time.Date(2026, 9, 12, 23, 0, 0, 0, loc)
	snapshot := TimeSnapshot{Now: start.AddDate(1, 0, 0), Location: loc}
	until := "2026-09-12"
	c, err := ValidateRecurring(snapshot, start.Unix(), start.Add(2*time.Hour).Unix(), "0 23 * * *", until)
	if err != nil {
		t.Fatal(err)
	}
	if *c.UntilDateLocal != "2026-09-12" || c.DurationMinutes != 120 || !c.Open || !c.OriginalStartLocal.Equal(start) {
		t.Fatalf("candidate = %+v", c)
	}
	for _, tc := range []struct {
		start, end  int64
		until, cron string
	}{
		{start.Unix() + 1, start.Unix() + 60, until, "0 23 * * *"},
		{start.Unix(), start.Unix(), until, "0 23 * * *"},
		{start.Unix(), start.Unix() + 60, "2026-09-11", "0 23 * * *"},
		{start.Unix(), start.Unix() + 60, "2026-02-30", "0 23 * * *"},
		{start.Unix(), start.Unix() + 60, until, "bad"},
		{math.MinInt64 / 60 * 60, math.MaxInt64 / 60 * 60, until, "0 23 * * *"},
	} {
		if _, err := ValidateRecurring(snapshot, tc.start, tc.end, tc.cron, tc.until); err == nil {
			t.Errorf("accepted %+v", tc)
		}
	}
	if _, err := ValidateRecurring(TimeSnapshot{}, start.Unix(), start.Unix()+60, "0 23 * * *", until); err == nil {
		t.Fatal("accepted nil location")
	}
	s := recurrenceSchedule("0 23 * * *", "2026-09-12", start)
	if _, _, err := NextStart(s, start, nil); err == nil {
		t.Fatal("accepted nil calculation location")
	}
	badDate := "2026-02-30"
	s.UntilDateLocal = &badDate
	if _, _, err := NextStart(s, start, loc); err == nil {
		t.Fatal("accepted invalid calendar date")
	}
}
