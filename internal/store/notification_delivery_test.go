package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/model"
	"github.com/dengweitian0-svg/TimeCue/internal/service"
)

func deliveryFixture(t *testing.T, count int) (*Sqlite, *NotificationStore, *service.App, *schedulerClock, []*model.ScheduleWrite) {
	t.Helper()
	db, _, notify, app, clock := schedulerFixture(t)
	d, err := service.NewDeliveryService(notify, clock.now)
	if err != nil {
		t.Fatal(err)
	}
	app.Delivery = d
	writes := make([]*model.ScheduleWrite, 0, count)
	for i := 0; i < count; i++ {
		writes = append(writes, writeRule(t, app, fmt.Sprintf("成员%d", i), "0 9 * * *"))
	}
	clock.set(clock.now().Add(time.Minute))
	if err := app.Scheduler.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	return db, notify, app, clock, writes
}

func claimOne(t *testing.T, app *service.App) service.DeliveryClaim {
	t.Helper()
	claims, err := app.ClaimDelivery(context.Background(), 20)
	if err != nil || len(claims) != 1 {
		t.Fatalf("领取完整任务: %#v %v", claims, err)
	}
	return claims[0]
}

// TestDeliveryFreezeReplayAndStop 验证停止先于开始及开始后保留真实结果。
func TestDeliveryFreezeReplayAndStop(t *testing.T) {
	_, notify, app, clock, writes := deliveryFixture(t, 2)
	ctx := context.Background()
	claim := claimOne(t, app)
	if again := claimOne(t, app); again != claim {
		t.Fatal("重复领取改变身份")
	}
	if _, err := app.CompleteDelivery(ctx, claim.AttemptID, model.NotifySent); !errors.Is(err, service.ErrDeliveryConflict) {
		t.Fatal("未开始允许回填", err)
	}
	if _, err := app.AcknowledgeNotifications(ctx, []int{writes[0].Notification.ID}, clock.now()); err != nil {
		t.Fatal(err)
	}
	start, err := app.StartDelivery(ctx, claim.AttemptID)
	if err != nil || start.MemberCount != 1 || !start.ShouldDisplay || start.AttemptNumber != 1 {
		t.Fatalf("开始未剔除确认成员: %#v %v", start, err)
	}
	if start.Body != "成员1 2030-10-09 09:00" {
		t.Fatal("文案未使用冻结快照", start.Body)
	}
	if _, err := app.DeleteScheduleWithResult(ctx, writes[1].Schedule.ID); err != nil {
		t.Fatal(err)
	}
	again, err := app.StartDelivery(ctx, claim.AttemptID)
	if err != nil || again != start {
		t.Fatal("开始重传改变快照或预算", again, err)
	}
	result, err := app.CompleteDelivery(ctx, claim.AttemptID, model.NotifySent)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := app.CompleteDelivery(ctx, claim.AttemptID, model.NotifySent)
	if err != nil || duplicate != result {
		t.Fatal("回填重传不幂等", err)
	}
	if _, err := app.CompleteDelivery(ctx, claim.AttemptID, model.NotifyFailed); !errors.Is(err, service.ErrDeliveryConflict) {
		t.Fatal("矛盾结果未拒绝", err)
	}
	for i, w := range writes {
		n, err := notify.GetNotificationByID(ctx, w.Notification.ID)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 && (n.Status != model.NotifyLoaded || n.AcknowledgedAt == nil) {
			t.Fatal("确认改变投递事实")
		}
		if i == 1 && (n.Status != model.NotifySent || !n.DeliveryStopped || n.AcknowledgedAt != nil) {
			t.Fatalf("停止抹去实际结果: %#v", n)
		}
	}
}

// TestDeliveryRetryBudget 验证固定等待、四次上限及重扫不重置预算。
func TestDeliveryRetryBudget(t *testing.T) {
	_, notify, app, clock, writes := deliveryFixture(t, 1)
	ctx := context.Background()
	waits := []time.Duration{5 * time.Second, 30 * time.Second, 120 * time.Second}
	for number := 1; number <= 4; number++ {
		claim := claimOne(t, app)
		start, err := app.StartDelivery(ctx, claim.AttemptID)
		if err != nil || start.AttemptNumber != number {
			t.Fatalf("预算错误: %#v %v", start, err)
		}
		if _, err := app.CompleteDelivery(ctx, claim.AttemptID, model.NotifyFailed); err != nil {
			t.Fatal(err)
		}
		if _, err := app.CompleteDelivery(ctx, claim.AttemptID, model.NotifyFailed); err != nil {
			t.Fatal(err)
		}
		n, _ := notify.GetNotificationByID(ctx, writes[0].Notification.ID)
		if n.Status != model.NotifyFailed {
			t.Fatal("未保存实际失败")
		}
		if number == 4 {
			break
		}
		clock.set(clock.now().Add(waits[number-1] - time.Nanosecond))
		claims, err := app.ClaimDelivery(ctx, 20)
		if err != nil || len(claims) != 0 {
			t.Fatal("提前重试", err)
		}
		clock.set(clock.now().Add(time.Nanosecond))
		if err := app.Scheduler.Step(ctx); err != nil {
			t.Fatal(err)
		}
	}
	clock.set(clock.now().Add(time.Hour))
	claims, err := app.ClaimDelivery(ctx, 20)
	if err != nil || len(claims) != 0 {
		t.Fatal("产生第五次尝试", err)
	}
	view := app.DeliverySnapshot()
	if len(view) != 1 || view[0].Attempts != 4 {
		t.Fatal("重扫重置预算", view)
	}
	view[0].MemberIDs[0] = -1
	if app.DeliverySnapshot()[0].MemberIDs[0] < 0 {
		t.Fatal("视图泄露成员内存")
	}
}

// TestDeliveryEmptyAndRestart 验证空任务不消费次数及旧进程身份失效。
func TestDeliveryEmptyAndRestart(t *testing.T) {
	_, notify, app, clock, writes := deliveryFixture(t, 1)
	ctx := context.Background()
	old := claimOne(t, app)
	d, err := service.NewDeliveryService(notify, clock.now)
	if err != nil {
		t.Fatal(err)
	}
	app.Delivery = d
	if _, err := app.StartDelivery(ctx, old.AttemptID); !errors.Is(err, service.ErrAttemptNotFound) {
		t.Fatal("旧 attempt 仍有效", err)
	}
	claim := claimOne(t, app)
	if claim.DeliveryTaskID == old.DeliveryTaskID || claim.AttemptID == old.AttemptID {
		t.Fatal("新进程身份重复")
	}
	if _, err := app.AcknowledgeNotifications(ctx, []int{writes[0].Notification.ID}, clock.now()); err != nil {
		t.Fatal(err)
	}
	start, err := app.StartDelivery(ctx, claim.AttemptID)
	if err != nil || start.ShouldDisplay || start.AttemptNumber != 0 || start.MemberCount != 0 {
		t.Fatal("空任务消费预算", start, err)
	}
	if _, err := app.CompleteDelivery(ctx, claim.AttemptID, model.NotifyFailed); !errors.Is(err, service.ErrDeliveryConflict) {
		t.Fatal("空组伪造失败", err)
	}
}

// TestDeliveryExecutorExitLateResults 验证未知退出和迟到结果不覆盖新执行阶段。
func TestDeliveryExecutorExitLateResults(t *testing.T) {
	for _, late := range []model.NotifyStatus{model.NotifySent, model.NotifyFailed} {
		t.Run(string(late), func(t *testing.T) {
			_, notify, app, clock, writes := deliveryFixture(t, 1)
			ctx := context.Background()
			old := claimOne(t, app)
			if _, err := app.StartDelivery(ctx, old.AttemptID); err != nil {
				t.Fatal(err)
			}
			app.DeliveryExecutorExited()
			app.DeliveryExecutorExited()
			n, _ := notify.GetNotificationByID(ctx, writes[0].Notification.ID)
			if n.Status != model.NotifyLoaded {
				t.Fatal("未知退出伪造失败")
			}
			clock.set(clock.now().Add(5 * time.Second))
			current := claimOne(t, app)
			if current.AttemptID == old.AttemptID {
				t.Fatal("退出后复用旧尝试")
			}
			if _, err := app.StartDelivery(ctx, current.AttemptID); err != nil {
				t.Fatal(err)
			}
			if _, err := app.CompleteDelivery(ctx, old.AttemptID, late); err != nil {
				t.Fatal(err)
			}
			n, _ = notify.GetNotificationByID(ctx, writes[0].Notification.ID)
			want := model.NotifyLoaded
			if late == model.NotifySent {
				want = model.NotifySent
			}
			if n.Status != want {
				t.Fatal("迟到结果破坏新状态", n.Status)
			}
			if _, err := app.CompleteDelivery(ctx, current.AttemptID, model.NotifyFailed); err != nil {
				t.Fatal(err)
			}
			if late == model.NotifySent {
				n, _ = notify.GetNotificationByID(ctx, writes[0].Notification.ID)
				if n.Status != model.NotifySent {
					t.Fatal("已成功被新失败降级")
				}
			}
		})
	}
}

// TestDeliveryConcurrentStartAndConfirmation 验证开始与确认确定先后且预算仅一次。
func TestDeliveryConcurrentStartAndConfirmation(t *testing.T) {
	_, _, app, clock, writes := deliveryFixture(t, 2)
	ctx := context.Background()
	claim := claimOne(t, app)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := app.StartDelivery(ctx, claim.AttemptID); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, err := app.AcknowledgeNotifications(ctx, []int{writes[0].Notification.ID}, clock.now()); err != nil {
			t.Error(err)
		}
	}()
	wg.Wait()
	start, err := app.StartDelivery(ctx, claim.AttemptID)
	if err != nil || start.AttemptNumber != 1 || (start.MemberCount != 1 && start.MemberCount != 2) {
		t.Fatal("开始竞争破坏快照", start, err)
	}
	app.StopDelivery()
	if _, err := app.ClaimDelivery(ctx, 20); !errors.Is(err, service.ErrDeliveryStopping) {
		t.Fatal("退出后仍领取", err)
	}
	if _, err := app.CompleteDelivery(ctx, claim.AttemptID, model.NotifySent); err != nil {
		t.Fatal("停止领取阻止回填", err)
	}
}

// TestDeliveryLargeGroupAtomicResult 验证大组不拆分、失败整组回滚及结构不变。
func TestDeliveryLargeGroupAtomicResult(t *testing.T) {
	db, notify, app, _, writes := deliveryFixture(t, 501)
	ctx := context.Background()
	before := deliverySchema(t, db)
	claim := claimOne(t, app)
	start, err := app.StartDelivery(ctx, claim.AttemptID)
	if err != nil || start.MemberCount != 501 || start.Body != "有 501 项日程需要处理" {
		t.Fatal("大组被拆开", start, err)
	}
	if err := notify.SaveDeliveryResult(ctx, []int{writes[0].Notification.ID, 9999999}, model.NotifySent); err == nil {
		t.Fatal("非法成员没有拒绝")
	}
	n, _ := notify.GetNotificationByID(ctx, writes[0].Notification.ID)
	if n.Status != model.NotifyLoaded {
		t.Fatal("整组事务未回滚")
	}
	if _, err := app.CompleteDelivery(ctx, claim.AttemptID, model.NotifySent); err != nil {
		t.Fatal(err)
	}
	for _, w := range writes {
		n, err := notify.GetNotificationByID(ctx, w.Notification.ID)
		if err != nil || n.Status != model.NotifySent {
			t.Fatal("大组结果未完整提交", err)
		}
	}
	if !reflect.DeepEqual(before, deliverySchema(t, db)) {
		t.Fatal("投递改变数据库结构")
	}
	if err := db.CheckHealth(ctx); err != nil {
		t.Fatal(err)
	}
}

func deliverySchema(t *testing.T, db *Sqlite) []string {
	t.Helper()
	rows, err := db.db.Query(`SELECT name || ':' || sql FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var schema []string
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			t.Fatal(err)
		}
		schema = append(schema, text)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(schema) != 4 {
		t.Fatal("原四张业务表发生变化", schema)
	}
	return schema
}

// TestDeliveryFailedCommitCanBeRetransmitted 验证落库失败不缓存成功回执或建立重试。
func TestDeliveryFailedCommitCanBeRetransmitted(t *testing.T) {
	_, notify, app, _, writes := deliveryFixture(t, 1)
	ctx := context.Background()
	claim := claimOne(t, app)
	if _, err := app.StartDelivery(ctx, claim.AttemptID); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := app.CompleteDelivery(cancelled, claim.AttemptID, model.NotifySent); err == nil {
		t.Fatal("失败落库伪装成功")
	}
	n, err := notify.GetNotificationByID(ctx, writes[0].Notification.ID)
	if err != nil || n.Status != model.NotifyLoaded {
		t.Fatal("取消请求更改实际结果", err)
	}
	if _, err := app.CompleteDelivery(ctx, claim.AttemptID, model.NotifySent); err != nil {
		t.Fatal("原结果不能重传", err)
	}
}

// TestDeliveryRestartCurrentFailed 验证重启当前分钟的 failed 不被 D1 与 D3 交界漏掉。
func TestDeliveryRestartCurrentFailed(t *testing.T) {
	_, notify, app, clock, writes := deliveryFixture(t, 1)
	ctx := context.Background()
	old := claimOne(t, app)
	if _, err := app.StartDelivery(ctx, old.AttemptID); err != nil {
		t.Fatal(err)
	}
	if _, err := app.CompleteDelivery(ctx, old.AttemptID, model.NotifyFailed); err != nil {
		t.Fatal(err)
	}
	scheduler, err := service.NewScheduler(notify, service.SchedulerOptions{Now: clock.now})
	if err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Step(ctx); err != nil {
		t.Fatal(err)
	}
	n, _ := notify.GetNotificationByID(ctx, writes[0].Notification.ID)
	if n.Status != model.NotifyFailed {
		t.Fatal("D1 自行许可失败重试")
	}
	d, err := service.NewDeliveryService(notify, clock.now)
	if err != nil {
		t.Fatal(err)
	}
	restarted := &service.App{Scheduler: scheduler, Notify: notify, Delivery: d}
	claim := claimOne(t, restarted)
	start, err := restarted.StartDelivery(ctx, claim.AttemptID)
	if err != nil || start.AttemptNumber != 1 || !start.ShouldDisplay {
		t.Fatal("重启当前分钟失败提醒丢失", start, err)
	}
	if _, err := restarted.StartDelivery(ctx, old.AttemptID); !errors.Is(err, service.ErrAttemptNotFound) {
		t.Fatal("旧回执身份仍生效")
	}
	ids, err := restarted.DeliveryAttemptMembers(claim.AttemptID)
	if err != nil || len(ids) != 1 {
		t.Fatal("尝试成员快照缺失", err)
	}
	ids[0] = -1
	again, _ := restarted.DeliveryAttemptMembers(claim.AttemptID)
	if again[0] < 0 {
		t.Fatal("成员快照泄漏可变内存")
	}
}
