package store

import (
	"context"
	"errors"
	"testing"
	"time"

	application "github.com/dengweitian0-svg/TimeCue/internal/app"
	"github.com/dengweitian0-svg/TimeCue/internal/model"
	"github.com/dengweitian0-svg/TimeCue/internal/service/schedule"
)

// TestUnacknowledgedScopes 用真实 SQLite 验证范围先于分页、端点及确认事实。
func TestUnacknowledgedScopes(t *testing.T) {
	db, schedules, notifications := newMigratedStore(t)
	ctx := context.Background()
	now := time.Date(2030, 10, 8, 12, 0, 0, 0, model.BeijingLocation())
	midnight := time.Date(2030, 10, 8, 0, 0, 0, 0, model.BeijingLocation())
	app := &application.App{Schedule: schedules, Notify: notifications}
	add := func(at time.Time, status string, deleted, acknowledged bool) int {
		t.Helper()
		result, err := db.db.Exec(`INSERT INTO schedules(name,type,single_notify_time,created_at,updated_at,revision) VALUES ('范围夹具','single',?,?,?,1)`, at.UnixNano(), now.AddDate(0, 0, -3).UnixNano(), now.AddDate(0, 0, -3).UnixNano())
		if err != nil {
			t.Fatal(err)
		}
		sid, _ := result.LastInsertId()
		var ack any
		if acknowledged {
			ack = now.Add(-time.Hour).UnixNano()
		}
		result, err = db.db.Exec(`INSERT INTO notifications(schedule_id,status,notify_time,deleted,acknowledged_at) VALUES (?,?,?,?,?)`, sid, status, at.UnixNano(), deleted, ack)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := result.LastInsertId()
		return int(id)
	}
	statuses := []string{"pending", "loaded", "sent", "failed"}
	for i := 0; i < 52; i++ {
		add(midnight.Add(-time.Nanosecond), statuses[i%4], false, false)
		add(midnight, statuses[i%4], false, false)
	}
	atNow := add(now.UTC(), "failed", false, false)
	future := add(now.Add(time.Minute), "pending", false, false)
	add(midnight.Add(-time.Hour), "sent", false, true)
	add(midnight, "sent", true, false)
	// 父日程软删除或暂停不参与到期历史的筛选。
	if _, err := db.db.Exec(`UPDATE schedules SET deleted=1 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`UPDATE schedules SET type='period',single_notify_time=NULL,period_open=0,period_cron='["0 9 * * *"]' WHERE id=3`); err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{"today": 53, "history": 52, "": 105}
	cursors := map[string]string{}
	for scope, expected := range counts {
		seen := map[int]bool{}
		raw := ""
		for {
			page, err := app.QueryUnacknowledgedScope(ctx, scope, raw, now.UTC())
			if err != nil || page.Total != expected || !page.AsOf.Equal(now) {
				t.Fatalf("范围 %q: %#v %v", scope, page, err)
			}
			for _, item := range page.Items {
				id := *item.NotificationID
				if seen[id] || id == future || item.AcknowledgedAt != nil || item.NotifyTime.After(now) {
					t.Fatalf("重复或非法记录: %#v", item)
				}
				if scope == "today" && item.NotifyTime.Before(midnight) || scope == "history" && !item.NotifyTime.Before(midnight) {
					t.Fatalf("范围错误: %#v", item)
				}
				seen[id] = true
			}
			if raw == "" {
				cursors[scope] = page.NextCursor
			}
			raw = page.NextCursor
			if raw == "" {
				break
			}
		}
		if len(seen) != expected {
			t.Fatalf("范围 %q 遗漏: %d", scope, len(seen))
		}
	}
	for _, scope := range []string{"today", "history", ""} {
		for other, cursor := range cursors {
			if other == scope {
				continue
			}
			if _, err := app.QueryUnacknowledgedScope(ctx, scope, cursor, now); !errors.Is(err, schedule.ErrInvalidQuery) {
				t.Fatalf("接受混用游标 %q/%q: %v", scope, other, err)
			}
		}
	}
	for _, scope := range []string{"today", "history"} {
		if _, err := app.QueryUnacknowledgedScope(ctx, scope, cursors[scope], midnight.AddDate(0, 0, 1)); !errors.Is(err, schedule.ErrInvalidQuery) {
			t.Fatalf("接受跨日游标: %v", err)
		}
		page, err := app.QueryUnacknowledgedScope(ctx, scope, cursors[scope], now.Add(time.Minute))
		if err != nil || page.Total != counts[scope] || !page.AsOf.Equal(now) {
			t.Fatalf("续页未冻结 asOf: %#v %v", page, err)
		}
	}
	page, err := app.QueryUnacknowledgedScope(ctx, "today", "", now.Add(time.Minute))
	if err != nil || page.Total != 54 {
		t.Fatalf("首分页未纳入新到期事实: %#v %v", page, err)
	}
	first, err := app.AcknowledgeNotifications(ctx, []int{1}, now)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := app.AcknowledgeNotifications(ctx, []int{1}, now.Add(time.Minute))
	if err != nil || !repeated[0].AcknowledgedAt.Equal(*first[0].AcknowledgedAt) || repeated[0].Status != model.NotifyPending || !repeated[0].NotifyTime.Equal(midnight.Add(-time.Nanosecond)) {
		t.Fatalf("确认改写事实: %#v %v", repeated, err)
	}
	if _, err = app.AcknowledgeNotifications(ctx, []int{atNow, future}, now); err == nil {
		t.Fatal("非法集合应原子拒绝")
	}
	value, _ := notifications.GetNotificationByID(ctx, atNow)
	if value.AcknowledgedAt != nil {
		t.Fatal("失败确认改变今日事实")
	}
	history, _ := app.QueryUnacknowledgedScope(ctx, "history", "", now)
	today, _ := app.QueryUnacknowledgedScope(ctx, "today", "", now)
	combined, err := app.QueryScheduleState(ctx, "2030-10-08", now)
	if err != nil || history.Total != 51 || today.Total != 53 || combined.Unacknowledged.Total != 104 {
		t.Fatalf("确认联动范围/组合快照错误: %v", err)
	}
}

// TestScopedAcknowledgementKeepsRecurringToday 只确认历史那次触发，保留同周期后续事实。
func TestScopedAcknowledgementKeepsRecurringToday(t *testing.T) {
	_, schedules, notifications := newMigratedStore(t)
	ctx := context.Background()
	now := time.Date(2030, 10, 8, 12, 0, 0, 0, model.BeijingLocation())
	schedules.now = func() time.Time { return now.AddDate(0, 0, -7) }
	rule, err := schedules.CreateSchedule(ctx, model.Schedule{Name: "同周期独立确认", Type: model.PeriodSchedule, Period: &model.Period{Open: true, Cron: []string{"0 9 * * *"}}})
	if err != nil {
		t.Fatal(err)
	}
	app := &application.App{Schedule: schedules, Notify: notifications}
	values := []*model.Notification{}
	for _, day := range []int{7, 8, 9} {
		value, err := notifications.CreateNotification(ctx, rule.ID, time.Date(2030, 10, day, 9, 0, 0, 0, model.BeijingLocation()))
		if err != nil {
			t.Fatal(err)
		}
		values = append(values, value)
	}
	if _, err := app.AcknowledgeNotifications(ctx, []int{values[0].ID}, now); err != nil {
		t.Fatal(err)
	}
	page, err := app.QueryUnacknowledgedScope(ctx, "today", "", now)
	if err != nil || page.Total != 1 || *page.Items[0].NotificationID != values[1].ID || *page.Items[0].DeliveryStatus != values[1].Status {
		t.Fatalf("历史确认影响同周期今日: %#v %v", page, err)
	}
	tomorrow := time.Date(2030, 10, 9, 9, 0, 0, 0, model.BeijingLocation())
	page, err = app.QueryUnacknowledgedScope(ctx, "today", "", tomorrow)
	if err != nil || page.Total != 1 || *page.Items[0].NotificationID != values[2].ID {
		t.Fatalf("历史确认阻塞同周期后续触发: %#v %v", page, err)
	}
}
