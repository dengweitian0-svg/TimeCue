package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	application "github.com/dengweitian0-svg/TimeCue/internal/app"
	"github.com/dengweitian0-svg/TimeCue/internal/model"
)

// TestOpenScheduleCount 统计全部分页的启用日程，确认不减数，同规则多次触发不重复。
func TestOpenScheduleCount(t *testing.T) {
	_, schedules, notifications := newMigratedStore(t)
	ctx := context.Background()
	now := time.Date(2030, 10, 8, 14, 32, 0, 0, model.BeijingLocation())
	schedules.now = func() time.Time { return now }
	app := &application.App{Schedule: schedules, Notify: notifications}
	empty, err := app.QueryOccurrences(ctx, "2030-10-08", "", now)
	if err != nil || empty.OpenScheduleCount == nil || *empty.OpenScheduleCount != 0 {
		t.Fatalf("空日期计数错误: %#v %v", empty, err)
	}

	multi, err := app.WriteSchedule(ctx, model.Schedule{Name: "两次提醒", Type: model.PeriodSchedule, Period: &model.Period{Open: true, Cron: []string{"0 9 * * *", "0 17 * * *"}}}, 0, "multi")
	if err != nil {
		t.Fatal(err)
	}
	morning, err := notifications.CreateNotification(ctx, multi.Schedule.ID, time.Date(2030, 10, 8, 9, 0, 0, 0, model.BeijingLocation()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = app.AcknowledgeNotifications(ctx, []int{morning.ID}, now); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 54; i++ {
		if _, err = app.WriteSchedule(ctx, model.Schedule{Name: fmt.Sprintf("单次%d", i), Type: model.SingleSchedule, Single: &model.Single{NotifyTime: now.Add(time.Hour)}}, 0, ""); err != nil {
			t.Fatal(err)
		}
	}
	// 已到期并确认的单次也仍是一条当日启用日程。
	later := now
	now = time.Date(2030, 10, 8, 13, 0, 0, 0, model.BeijingLocation())
	completed, err := app.WriteSchedule(ctx, model.Schedule{Name: "已确认单次", Type: model.SingleSchedule, Single: &model.Single{NotifyTime: now.Add(time.Hour)}}, 0, "completed")
	if err != nil {
		t.Fatal(err)
	}
	now = later
	if _, err = app.AcknowledgeNotifications(ctx, []int{completed.Notification.ID}, now); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"暂停", "删除", "取消"} {
		value, e := app.WriteSchedule(ctx, rule(state, "0 18 * * *", true, 0), 0, "")
		if e != nil {
			t.Fatal(e)
		}
		if _, e = notifications.CreateNotification(ctx, value.Schedule.ID, now.Add(-time.Hour)); e != nil {
			t.Fatal(e)
		}
		switch state {
		case "暂停":
			_, err = app.WriteSchedule(ctx, rule(state, "0 18 * * *", false, value.Schedule.ID), 1, "")
		case "删除":
			err = app.DeleteSchedule(ctx, value.Schedule.ID)
		case "取消":
			err = schedules.CancelScheduleByID(ctx, value.Schedule.ID)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	result, err := app.QueryScheduleState(ctx, "2030-10-08", now)
	if err != nil {
		t.Fatal(err)
	}
	page := result.Occurrences
	if page.OpenScheduleCount == nil || *page.OpenScheduleCount != 56 || len(page.Items) != 50 || page.NextCursor == "" {
		t.Fatalf("计数不能使用分页长度或未确认集合: %#v", page)
	}
	next, err := app.QueryOccurrences(ctx, "2030-10-08", page.NextCursor, now)
	if err != nil || next.OpenScheduleCount == nil || *next.OpenScheduleCount != 56 {
		t.Fatalf("续页总数错误: %#v %v", next, err)
	}
	if _, err = app.WriteSchedule(ctx, model.Schedule{ID: multi.Schedule.ID, Name: "两次提醒", Type: model.PeriodSchedule, Period: &model.Period{Open: false, Cron: multi.Schedule.Period.Cron}}, 1, "pause-multi"); err != nil {
		t.Fatal(err)
	}
	paused, err := app.QueryOccurrences(ctx, "2030-10-08", "", now)
	if err != nil || paused.OpenScheduleCount == nil || *paused.OpenScheduleCount != 55 {
		t.Fatalf("暂停未从计数排除: %#v %v", paused, err)
	}
	tomorrow, err := app.QueryOccurrences(ctx, "2030-10-09", "", now)
	if err != nil || tomorrow.OpenScheduleCount == nil || *tomorrow.OpenScheduleCount != 0 {
		t.Fatalf("无安排日期计数错误: %#v %v", tomorrow, err)
	}
}
