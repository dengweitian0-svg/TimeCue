package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/model"
	"github.com/dengweitian0-svg/TimeCue/internal/service"
)

type schedulerClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *schedulerClock) now() time.Time   { c.mu.Lock(); defer c.mu.Unlock(); return c.at }
func (c *schedulerClock) set(at time.Time) { c.mu.Lock(); defer c.mu.Unlock(); c.at = at }

func schedulerFixture(t *testing.T) (*Sqlite, *ScheduleStore, *NotificationStore, *service.App, *schedulerClock) {
	t.Helper()
	db, schedules, notifications := newMigratedStore(t)
	clock := &schedulerClock{at: time.Date(2030, 10, 9, 8, 59, 0, 0, model.BeijingLocation())}
	schedules.now = clock.now
	scheduler, err := service.NewScheduler(notifications, service.SchedulerOptions{Now: clock.now, OnError: func(err error) { t.Errorf("调度错误: %v", err) }})
	if err != nil {
		t.Fatal(err)
	}
	return db, schedules, notifications, &service.App{Schedule: schedules, Notify: notifications, Scheduler: scheduler}, clock
}

func writeRule(t *testing.T, app *service.App, name, cron string) *model.ScheduleWrite {
	t.Helper()
	r, err := app.WriteSchedule(context.Background(), rule(name, cron, true, 0), 0, "")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestSchedulerImmediateLoadAndMinuteGroup 验证窗口之外的近期提交及到点完整分组。
func TestSchedulerImmediateLoadAndMinuteGroup(t *testing.T) {
	_, _, notifications, app, clock := schedulerFixture(t)
	ctx := context.Background()
	near := writeRule(t, app, "恰好一分钟", "0 9 * * *")
	far := writeRule(t, app, "更远", "1 9 * * *")
	n, _ := notifications.GetNotificationByID(ctx, near.Notification.ID)
	if n.Status != model.NotifyLoaded || n.SnapshotTaken {
		t.Fatalf("近期提醒应预加载但不冻结: %#v", n)
	}
	n, _ = notifications.GetNotificationByID(ctx, far.Notification.ID)
	if n.Status != model.NotifyPending {
		t.Fatalf("远期提醒提前加载: %#v", n)
	}
	if err := app.Scheduler.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if len(app.Scheduler.Snapshot().Ready) != 0 {
		t.Fatal("未来提醒提前开放")
	}
	clock.set(clock.now().Add(50 * time.Second))
	second := writeRule(t, app, "扫描之间新增", "0 9 * * *")
	clock.set(clock.now().Add(10 * time.Second))
	if err := app.Scheduler.Step(ctx); err != nil {
		t.Fatal(err)
	}
	view := app.Scheduler.Snapshot()
	if len(view.Ready) != 1 || len(view.Ready[0].MemberIDs) != 2 {
		t.Fatalf("分钟未完整合并: %#v", view)
	}
	for _, id := range []int{near.Notification.ID, second.Notification.ID} {
		n, _ = notifications.GetNotificationByID(ctx, id)
		if !n.SnapshotTaken || n.NameSnapshot == nil || n.Status != model.NotifyLoaded {
			t.Fatalf("到期事实未冻结: %#v", n)
		}
	}
	view.Ready[0].MemberIDs[0] = -1
	if app.Scheduler.Snapshot().Ready[0].MemberIDs[0] == -1 {
		t.Fatal("视图泄露内部切片")
	}
	if err := app.Scheduler.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if len(app.Scheduler.Snapshot().Ready) != 1 {
		t.Fatal("重复核验拆组")
	}
}

// TestSchedulerRuleChangesAndSelectiveStop 验证改名快照及停止不影响同组其他日程。
func TestSchedulerRuleChangesAndSelectiveStop(t *testing.T) {
	_, _, notifications, app, clock := schedulerFixture(t)
	ctx := context.Background()
	a := writeRule(t, app, "旧名称", "0 9 * * *")
	b := writeRule(t, app, "保留成员", "0 9 * * *")
	clock.set(clock.now().Add(30 * time.Second))
	changed, err := app.WriteSchedule(ctx, rule("新名称", "0 9 * * *", true, a.Schedule.ID), 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if changed.Notification.ID != a.Notification.ID {
		t.Fatal("未来唯一键未复用")
	}
	clock.set(clock.now().Add(30 * time.Second))
	if err = app.Scheduler.Step(ctx); err != nil {
		t.Fatal(err)
	}
	n, _ := notifications.GetNotificationByID(ctx, a.Notification.ID)
	if n.NameSnapshot == nil || *n.NameSnapshot != "新名称" {
		t.Fatalf("冻结预加载旧文: %#v", n)
	}
	clock.set(clock.now().Add(20 * time.Second))
	if _, err = app.WriteSchedule(ctx, rule("再次改名", "0 9 * * *", false, a.Schedule.ID), 2, ""); err != nil {
		t.Fatal(err)
	}
	groups := app.Scheduler.Snapshot().Ready
	if len(groups) != 1 || !reflect.DeepEqual(groups[0].MemberIDs, []int{b.Notification.ID}) {
		t.Fatalf("停止误删其他成员: %#v", groups)
	}
	n, _ = notifications.GetNotificationByID(ctx, a.Notification.ID)
	if !n.DeliveryStopped || *n.NameSnapshot != "新名称" || n.Status != model.NotifyLoaded {
		t.Fatalf("停止抹去历史: %#v", n)
	}
	if _, err = app.AcknowledgeNotifications(ctx, []int{b.Notification.ID}, clock.now()); err != nil {
		t.Fatal(err)
	}
	if len(app.Scheduler.Snapshot().Ready) != 0 {
		t.Fatal("确认后仍可展示")
	}
}

// TestMinuteGenerationUsesHistoricalVersion 覆盖整点切换、迟到生成及暂停恢复。
func TestMinuteGenerationUsesHistoricalVersion(t *testing.T) {
	_, _, notifications, app, clock := schedulerFixture(t)
	ctx := context.Background()
	r := writeRule(t, app, "旧文", "0 9 * * *")
	minute := clock.now().Add(time.Minute)
	clock.set(minute)
	// 绕过入队钩子模拟调度尚未开始，规则事务仍是正式操作。
	app.Scheduler = nil
	if _, err := app.WriteSchedule(ctx, rule("新文", "1 9 * * *", true, r.Schedule.ID), 1, ""); err != nil {
		t.Fatal(err)
	}
	batch, err := notifications.PrepareNotifications(ctx, model.NotificationWindow{Start: minute, End: minute.Add(time.Minute), AsOf: clock.now(), FreezeDue: true}, service.PrepareNotificationCandidates)
	if err != nil || len(batch.EligibleIDs) != 1 {
		t.Fatalf("整点旧版本触发丢失: %#v %v", batch, err)
	}
	n := batch.Notifications[0]
	if n.NameSnapshot == nil || *n.NameSnapshot != "旧文" {
		t.Fatalf("到期文案版本错误: %#v", n)
	}
	clock.set(minute.Add(20 * time.Second))
	if _, err = app.WriteSchedule(ctx, rule("暂停", "1 9 * * *", false, r.Schedule.ID), 2, ""); err != nil {
		t.Fatal(err)
	}
	clock.set(minute.Add(30 * time.Second))
	if _, err = app.WriteSchedule(ctx, rule("恢复", "1 9 * * *", true, r.Schedule.ID), 3, ""); err != nil {
		t.Fatal(err)
	}
	// 删除尚未补造的另一旧分钟事实，模拟恢复仅剩版本依据。
	if _, err = notifications.db.db.Exec(`DELETE FROM notifications WHERE id = ?`, r.Notification.ID); err != nil {
		t.Fatal(err)
	}
	batch, err = notifications.PrepareNotifications(ctx, model.NotificationWindow{Start: minute, End: minute.Add(time.Minute), AsOf: clock.now(), FreezeDue: true}, service.PrepareNotificationCandidates)
	if err != nil || len(batch.EligibleIDs) != 0 || len(batch.Notifications) != 1 || !batch.Notifications[0].DeliveryStopped {
		t.Fatalf("恢复复活旧触发: %#v %v", batch, err)
	}
}

// TestSchedulerRestartFailureAndGaps 验证已加载重建、失败不重置和运行间隙交接。
func TestSchedulerRestartFailureAndGaps(t *testing.T) {
	_, _, notifications, app, clock := schedulerFixture(t)
	ctx := context.Background()
	r := writeRule(t, app, "重建", "0 9 * * *")
	restarted, err := service.NewScheduler(notifications, service.SchedulerOptions{Now: clock.now})
	if err != nil {
		t.Fatal(err)
	}
	clock.set(clock.now().Add(40 * time.Second))
	if err = restarted.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(restarted.Snapshot().WaitingIDs, []int{r.Notification.ID}) {
		t.Fatal("旧 loaded 被跳过")
	}
	if err = notifications.SetDeliveryResult(ctx, r.Notification.ID, model.NotifyFailed); err != nil {
		t.Fatal(err)
	}
	clock.set(clock.now().Add(20 * time.Second))
	if err = restarted.Step(ctx); err != nil {
		t.Fatal(err)
	}
	n, _ := notifications.GetNotificationByID(ctx, r.Notification.ID)
	if n.Status != model.NotifyFailed || len(restarted.Snapshot().Ready) != 0 {
		t.Fatal("重扫清除了失败或重置预算")
	}
	clock.set(clock.now().Add(3 * time.Minute))
	if err = restarted.Step(ctx); err != nil {
		t.Fatal(err)
	}
	view := restarted.Snapshot()
	if len(view.Missing) == 0 {
		t.Fatal("长时间扫描间隙丢失")
	}
	clock.set(clock.now().Add(-time.Minute))
	if err = restarted.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if len(restarted.Snapshot().Ready) != 0 {
		t.Fatal("时钟回拨重复开放")
	}
}

// TestMinuteGenerationLargeGroupAndRollback 验证大组无分页截断和中途错误原子回滚。
func TestMinuteGenerationLargeGroupAndRollback(t *testing.T) {
	db, schedules, notifications, _, clock := schedulerFixture(t)
	ctx := context.Background()
	app := &service.App{Schedule: schedules, Notify: notifications}
	for i := 0; i < 501; i++ {
		writeRule(t, app, fmt.Sprintf("成员%d", i), "0 9 * * *")
	}
	clock.set(clock.now().Add(time.Minute))
	window := model.NotificationWindow{Start: clock.now(), End: clock.now().Add(time.Minute), AsOf: clock.now(), FreezeDue: true}
	if _, err := db.db.Exec(`CREATE TRIGGER fail_snapshot BEFORE UPDATE OF snapshot_taken ON notifications WHEN NEW.schedule_id = 2 BEGIN SELECT RAISE(ABORT, 'test rollback'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := notifications.PrepareNotifications(ctx, window, service.PrepareNotificationCandidates); err == nil {
		t.Fatal("应触发整分钟回滚")
	}
	var count int
	if err := db.db.QueryRow(`SELECT count(*) FROM notifications WHERE snapshot_taken = 1 OR status = 'loaded'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("部分事实提交: %d %v", count, err)
	}
	if _, err := db.db.Exec(`DROP TRIGGER fail_snapshot`); err != nil {
		t.Fatal(err)
	}
	scheduler, _ := service.NewScheduler(notifications, service.SchedulerOptions{Now: clock.now})
	if err := scheduler.Step(ctx); err != nil {
		t.Fatal(err)
	}
	groups := scheduler.Snapshot().Ready
	if len(groups) != 1 || len(groups[0].FactIDs) != 501 || len(groups[0].MemberIDs) != 501 {
		t.Fatalf("大组截断: groups=%d", len(groups))
	}
}

// TestLoadedSchemaPreservesFactsAndConstraints 验证原表的 loaded 约束与事实隔离。
func TestLoadedSchemaPreservesFactsAndConstraints(t *testing.T) {
	db := NewSqlite()
	if err := db.Connect(context.Background(), filepath.Join(t.TempDir(), "upgrade.db")); err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, name := range []string{"001_initial.up.sql", "002_schedule_changes.up.sql"} {
		sqlText, err := os.ReadFile(filepath.Join("..", "..", "migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.db.Exec(string(sqlText)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.db.Exec(`INSERT INTO schedules (id, name, type, period_open, period_cron, created_at, updated_at) VALUES (7, '历史', 'period', 1, '["0 9 * * *"]', 1, 1);
		INSERT INTO notifications (id, schedule_id, status, notify_time, acknowledged_at, deleted, delivery_stopped, snapshot_taken, name_snapshot, description_snapshot) VALUES
		(41, 7, 'sent', 60000000000, 12, 0, 1, 1, '旧名', '旧文'), (42, 7, 'failed', 120000000000, NULL, 1, 1, 1, '失败', NULL)`); err != nil {
		t.Fatal(err)
	}
	notifications, _ := NewNotificationStore(db)
	before, _ := notifications.GetNotificationByID(context.Background(), 41)
	if err := db.CheckHealth(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`UPDATE notifications SET status = 'loaded' WHERE id = 42`); err != nil {
		t.Fatal(err)
	}
	after, _ := notifications.GetNotificationByID(context.Background(), 41)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("加载改写其他提醒事实")
	}
	if _, err := db.db.Exec(`UPDATE notifications SET status = 'invalid' WHERE id = 42`); err == nil {
		t.Fatal("状态约束丢失")
	}
	if _, err := db.db.Exec(`INSERT INTO notifications (schedule_id, notify_time) VALUES (7, 60000000000)`); err == nil {
		t.Fatal("唯一键丢失")
	}
	if _, err := db.db.Exec(`INSERT INTO notifications (schedule_id, notify_time) VALUES (999, 60000000000)`); err == nil {
		t.Fatal("外键丢失")
	}
	var count int
	if err := db.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table'`).Scan(&count); err != nil || count != 4 {
		t.Fatalf("不应新增业务或中间表: %d %v", count, err)
	}
	if err := db.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name IN ('idx_notifications_due', 'idx_notifications_schedule')`).Scan(&count); err != nil || count != 2 {
		t.Fatal("业务索引丢失")
	}
}

// TestSchedulerClockRollbackLoadsUnseenMinute 验证回拨不能冻结未处理分钟。
func TestSchedulerClockRollbackLoadsUnseenMinute(t *testing.T) {
	_, _, notifications, app, clock := schedulerFixture(t)
	ctx := context.Background()
	clock.set(clock.now().Add(time.Hour + time.Minute))
	if err := app.Scheduler.Step(ctx); err != nil {
		t.Fatal(err)
	}
	clock.set(clock.now().Add(-time.Hour))
	if err := app.Scheduler.Step(ctx); err != nil {
		t.Fatal(err)
	}
	r := writeRule(t, app, "回拨后新增", "1 9 * * *")
	if !reflect.DeepEqual(app.Scheduler.Snapshot().WaitingIDs, []int{r.Notification.ID}) {
		t.Fatal("回拨后近期任务没有入队")
	}
	clock.set(clock.now().Add(time.Minute))
	if err := app.Scheduler.Step(ctx); err != nil {
		t.Fatal(err)
	}
	view := app.Scheduler.Snapshot()
	if len(view.Ready) != 1 || view.Ready[0].MemberIDs[0] != r.Notification.ID {
		t.Fatalf("回拨冻结了新分钟: %#v", view)
	}
	n, _ := notifications.GetNotificationByID(ctx, r.Notification.ID)
	if !n.SnapshotTaken {
		t.Fatal("回拨后未冻结事实")
	}
}

// TestMinuteGroupExcludesLegacySeconds 验证完整事实集合也排除旧秒级记录。
func TestMinuteGroupExcludesLegacySeconds(t *testing.T) {
	db, _, _, app, clock := schedulerFixture(t)
	ctx := context.Background()
	r := writeRule(t, app, "正常", "0 9 * * *")
	minute := clock.now().Add(time.Minute)
	if _, err := db.db.Exec(`INSERT INTO notifications (schedule_id, notify_time) VALUES (?, ?)`, r.Schedule.ID, minute.Add(time.Second).UnixNano()); err != nil {
		t.Fatal(err)
	}
	clock.set(minute.Add(20 * time.Second))
	if err := app.Scheduler.Step(ctx); err != nil {
		t.Fatal(err)
	}
	group := app.Scheduler.Snapshot().Ready[0]
	if !reflect.DeepEqual(group.FactIDs, []int{r.Notification.ID}) {
		t.Fatalf("秒级记录进入分钟分组: %#v", group)
	}
}

// TestStoppedFutureIsNotDueFact 验证到期前撤销的候选不计入完整触发数量。
func TestStoppedFutureIsNotDueFact(t *testing.T) {
	_, _, _, app, clock := schedulerFixture(t)
	ctx := context.Background()
	a := writeRule(t, app, "提前暂停", "0 9 * * *")
	b := writeRule(t, app, "正常到期", "0 9 * * *")
	clock.set(clock.now().Add(30 * time.Second))
	if _, err := app.WriteSchedule(ctx, rule("提前暂停", "0 9 * * *", false, a.Schedule.ID), 1, ""); err != nil {
		t.Fatal(err)
	}
	clock.set(clock.now().Add(30 * time.Second))
	if err := app.Scheduler.Step(ctx); err != nil {
		t.Fatal(err)
	}
	groups := app.Scheduler.Snapshot().Ready
	if len(groups) != 1 || !reflect.DeepEqual(groups[0].FactIDs, []int{b.Notification.ID}) {
		t.Fatalf("撤销候选计入到期数量: %#v", groups)
	}
}

// TestLegacyVersionTwoStatusRejected 验证旧版本号不能绕过 loaded 约束检查。
func TestLegacyVersionTwoStatusRejected(t *testing.T) {
	db := NewSqlite()
	ctx := context.Background()
	if err := db.Connect(ctx, filepath.Join(t.TempDir(), "legacy.db")); err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, name := range []string{"001_initial.up.sql", "002_schedule_changes.up.sql"} {
		body, err := os.ReadFile(filepath.Join("..", "..", "migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		legacy := strings.Replace(string(body), "'pending', 'loaded', 'sent', 'failed'", "'pending', 'sent', 'failed'", 1)
		if _, err = db.db.ExecContext(ctx, legacy); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.CheckHealth(ctx); !errors.Is(err, ErrSchemaInvalid) || !strings.Contains(err.Error(), "loaded") {
		t.Fatalf("旧约束错误不明确: %v", err)
	}
}

// TestScheduleCallbacksIgnoreOlderRevision 验证旧暂停回调不撤销新恢复任务。
func TestScheduleCallbacksIgnoreOlderRevision(t *testing.T) {
	_, _, _, app, clock := schedulerFixture(t)
	ctx := context.Background()
	r := writeRule(t, app, "回调乱序", "0 9 * * *")
	scheduler := app.Scheduler
	app.Scheduler = nil
	clock.set(clock.now().Add(20 * time.Second))
	paused, err := app.WriteSchedule(ctx, rule("回调乱序", "0 9 * * *", false, r.Schedule.ID), 1, "")
	if err != nil {
		t.Fatal(err)
	}
	clock.set(clock.now().Add(10 * time.Second))
	resumed, err := app.WriteSchedule(ctx, rule("回调乱序", "0 9 * * *", true, r.Schedule.ID), 2, "")
	if err != nil {
		t.Fatal(err)
	}
	scheduler.ScheduleChanged(ctx, resumed)
	scheduler.ScheduleChanged(ctx, paused)
	if !reflect.DeepEqual(scheduler.Snapshot().WaitingIDs, []int{resumed.Notification.ID}) {
		t.Fatal("旧暂停回调清掉新任务")
	}
}

// TestMinuteStopRace 验证正式暂停与到点核验的两种竞争顺序。
func TestMinuteStopRace(t *testing.T) {
	for i := 0; i < 12; i++ {
		t.Run(fmt.Sprintf("竞争%d", i), func(t *testing.T) {
			_, _, notifications, app, clock := schedulerFixture(t)
			r := writeRule(t, app, "停止竞争", "0 9 * * *")
			clock.set(clock.now().Add(time.Minute + 20*time.Second))
			errors := make(chan error, 2)
			go func() { errors <- app.Scheduler.Step(context.Background()) }()
			go func() {
				_, err := app.WriteSchedule(context.Background(), rule("停止", "0 9 * * *", false, r.Schedule.ID), 1, "")
				errors <- err
			}()
			for j := 0; j < 2; j++ {
				if err := <-errors; err != nil {
					t.Fatal(err)
				}
			}
			if len(app.Scheduler.Snapshot().Ready) != 0 {
				t.Fatal("暂停后仍在就绪组")
			}
			n, _ := notifications.GetNotificationByID(context.Background(), r.Notification.ID)
			if !n.DeliveryStopped || !n.SnapshotTaken || n.NameSnapshot == nil || *n.NameSnapshot != "停止竞争" {
				t.Fatalf("竞争破坏到期事实: %#v", n)
			}
		})
	}
}
