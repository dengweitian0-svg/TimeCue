package service

import (
	"strings"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/model"
)

func beijing(year int, month time.Month, day, hour, minute int) time.Time {
	return time.Date(year, month, day, hour, minute, 0, 0, model.BeijingLocation())
}

func periodSchedule(open bool, expressions ...string) model.Schedule {
	return model.Schedule{Name: "周期", Type: model.PeriodSchedule, Period: &model.Period{Open: open, Cron: expressions}}
}

// TestNextNotificationTime 验证北京时间的严格推进、星期编号和短月月末行为。
func TestNextNotificationTime(t *testing.T) {
	cases := []struct {
		name     string
		schedule model.Schedule
		after    time.Time
		want     time.Time
		hasNext  bool
	}{
		{"每天恰好命中", periodSchedule(true, "0 9 * * *"), beijing(2025, 1, 1, 9, 0), beijing(2025, 1, 2, 9, 0), true},
		{"UTC 跨日输入", periodSchedule(true, "0 8 * * *"), time.Date(2025, 1, 1, 23, 0, 0, 0, time.UTC), beijing(2025, 1, 2, 8, 0), true},
		{"每周多天", periodSchedule(true, "0 9 * * 1,3,7"), beijing(2025, 1, 4, 9, 0), beijing(2025, 1, 5, 9, 0), true},
		{"星期日后推进", periodSchedule(true, "0 9 * * 1,7"), beijing(2025, 1, 5, 9, 0), beijing(2025, 1, 6, 9, 0), true},
		{"平年二月月底", periodSchedule(true, "0 9 29,30,31 * *"), beijing(2025, 2, 1, 0, 0), beijing(2025, 2, 28, 9, 0), true},
		{"闰年二月月底", periodSchedule(true, "0 9 30,31 * *"), beijing(2024, 2, 1, 0, 0), beijing(2024, 2, 29, 9, 0), true},
		{"短月重复日期只推进一次", periodSchedule(true, "0 9 30,31 * *"), beijing(2025, 4, 30, 9, 0), beijing(2025, 5, 30, 9, 0), true},
		{"多个规则取最早", periodSchedule(true, "0 15 * * *", "0 9 * * 1,7"), beijing(2025, 1, 5, 8, 0), beijing(2025, 1, 5, 9, 0), true},
		{"暂停不推进", periodSchedule(false, "0 9 * * *"), beijing(2025, 1, 1, 0, 0), time.Time{}, false},
		{"取消不推进", model.Schedule{Name: "取消", Type: model.PeriodSchedule, Cancelled: true, Period: &model.Period{Open: true, Cron: []string{"0 9 * * *"}}}, beijing(2025, 1, 1, 0, 0), time.Time{}, false},
		{"单次已过无下一次", model.Schedule{Name: "单次", Type: model.SingleSchedule, Single: &model.Single{NotifyTime: beijing(2025, 1, 1, 9, 0)}}, beijing(2025, 1, 1, 9, 0), time.Time{}, false},
		{"单次 UTC 输入按北京时间返回", model.Schedule{Name: "单次", Type: model.SingleSchedule, Single: &model.Single{NotifyTime: time.Date(2025, 1, 1, 1, 0, 0, 0, time.UTC)}}, beijing(2025, 1, 1, 8, 0), beijing(2025, 1, 1, 9, 0), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, hasNext, err := nextNotificationTime(tc.schedule, tc.after)
			if err != nil || hasNext != tc.hasNext || !got.Equal(tc.want) {
				t.Fatalf("got %v, %t, %v; want %v, %t", got, hasNext, err, tc.want, tc.hasNext)
			}
			if hasNext {
				_, offset := got.Zone()
				if offset != 8*60*60 {
					t.Fatalf("下一次时间应带 +08:00 偏移: %v", got)
				}
			}
		})
	}
}

// TestParseCronRuleRejectsUnsupportedSyntax 防止通用 cron 方言越过当前三种规则形状。
func TestParseCronRuleRejectsUnsupportedSyntax(t *testing.T) {
	invalid := []string{
		"0 9 * * * *", "*/5 9 * * *", "0 9 * 1 *", "0 9 1 * 1",
		"0 9 * * 0", "0 9 * * MON", "0 9 1-3 * *", "@daily",
		"0 24 * * *", "60 9 * * *", "0 9 1,,3 * *", "0 9 * * 8",
	}
	for _, expression := range invalid {
		t.Run(expression, func(t *testing.T) {
			if _, err := parseCronRule(expression); err == nil {
				t.Fatalf("unsupported expression accepted: %q", expression)
			}
		})
	}
	for _, expression := range []string{"0 9 * * *", "0 9 * * 1,2,7", "0 9 1,2,31 * *"} {
		if _, err := parseCronRule(expression); err != nil {
			t.Fatalf("valid expression %q rejected: %v", expression, err)
		}
	}
	_, _, err := nextNotificationTime(periodSchedule(false, "0 9 1 * 1"), beijing(2025, 1, 1, 0, 0))
	if err == nil || !strings.Contains(err.Error(), "day of month and day of week") {
		t.Fatalf("paused schedule must still validate cron syntax: %v", err)
	}
}
