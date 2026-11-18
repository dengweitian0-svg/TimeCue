package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	application "github.com/dengweitian0-svg/TimeCue/internal/app"
	"github.com/dengweitian0-svg/TimeCue/internal/model"
	"github.com/dengweitian0-svg/TimeCue/internal/service/notification"
	"github.com/dengweitian0-svg/TimeCue/internal/service/recovery"
)

func enableRecovery(t *testing.T, app *application.App, notify *NotificationStore, clock *schedulerClock) {
	t.Helper()
	var err error
	app.Recovery, err = recovery.NewService(notify, notification.SchedulerOptions{Now: clock.now})
	if err != nil {
		t.Fatal(err)
	}
	if app.Delivery == nil {
		app.Delivery, err = notification.NewDeliveryService(notify, clock.now)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func completeRecovery(t *testing.T, app *application.App, clock *schedulerClock) *model.RecoveryView {
	t.Helper()
	for i := 0; i < 200; i++ {
		if _, err := app.RecoverStep(context.Background()); err != nil {
			t.Fatal(err)
		}
		view, err := app.RecoverySnapshot(context.Background(), clock.now())
		if err != nil {
			t.Fatal(err)
		}
		if view == nil || view.State == "completed" {
			return view
		}
	}
	t.Fatal("恢复未在有界批次内结束")
	return nil
}

// TestRecoveryLargeHistoryAndPagination 验证 501 条批次、进度、游标和完整汇总。
func TestRecoveryLargeHistoryAndPagination(t *testing.T) {
	db, _, notify, app, clock := schedulerFixture(t)
	ctx := context.Background()
	r := writeRule(t, app, "不应出现在汇总的名称", "0 9 * * *")
	clock.set(clock.now().AddDate(0, 0, 501).Add(2 * time.Minute))
	enableRecovery(t, app, notify, clock)
	if _, err := app.RecoverStep(ctx); err != nil {
		t.Fatal(err)
	}
	view, err := app.RecoverySnapshot(ctx, clock.now())
	if err != nil || view == nil || view.State != "running" || view.MaterializedCount != 500 || view.MissedTotal != nil {
		t.Fatalf("首批进度不实: %#v %v", view, err)
	}
	if claims, err := app.ClaimDelivery(ctx, 20); err != nil || len(claims) != 0 {
		t.Fatal("恢复未完成提前领取", claims, err)
	}
	page, err := app.QueryDeliveryMembers(ctx, view.DeliveryTaskID, "", "", clock.now())
	if err != nil || len(page.Items) != 50 || page.Total != 500 || page.NextCursor == "" {
		t.Fatalf("首批分页: %#v %v", page, err)
	}
	oldCursor := page.NextCursor
	view = completeRecovery(t, app, clock)
	// 首日及随后 501 日，共 502 个已到期触发。
	if view == nil || view.MissedTotal == nil || *view.MissedTotal != 502 || view.MaterializedCount != 502 {
		t.Fatalf("完整总数不准确: %#v", view)
	}
	if _, err := app.QueryDeliveryMembers(ctx, view.DeliveryTaskID, "", oldCursor, clock.now()); !errors.Is(err, notification.ErrMembersChanged) {
		t.Fatal("成员增加未拒绝旧游标", err)
	}
	seen := make(map[int]bool)
	cursor := ""
	for {
		page, err := app.QueryDeliveryMembers(ctx, view.DeliveryTaskID, "", cursor, clock.now())
		if err != nil || page.Total != 502 || page.Recovery == nil || page.Recovery.MissedTotal == nil {
			t.Fatal("完成后分页错误", page, err)
		}
		for _, v := range page.Items {
			id := *v.NotificationID
			if seen[id] {
				t.Fatal("跨页重复成员", id)
			}
			seen[id] = true
		}
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}
	if len(seen) != 502 {
		t.Fatal("跨页遗漏成员", len(seen))
	}
	claim := claimOne(t, app)
	if claim.TaskType != "recovery" {
		t.Fatal("未创建汇总任务", claim)
	}
	if _, err := app.QueryDeliveryMembers(ctx, claim.DeliveryTaskID, claim.AttemptID, "", clock.now()); !errors.Is(err, notification.ErrDeliveryConflict) {
		t.Fatal("未开始允许查询冻结成员", err)
	}
	if _, err := app.AcknowledgeNotifications(ctx, []int{r.Notification.ID}, clock.now()); err != nil {
		t.Fatal(err)
	}
	start, err := app.StartDelivery(ctx, claim.AttemptID)
	if err != nil || start.MemberCount != 501 || strings.Contains(start.Body, r.Schedule.Name) || start.Body != "有 501 项错过的日程需要处理" {
		t.Fatalf("展示成员/文案错误: %#v %v", start, err)
	}
	attemptPage, err := app.QueryDeliveryMembers(ctx, claim.DeliveryTaskID, claim.AttemptID, "", clock.now())
	if err != nil || attemptPage.Total != 501 {
		t.Fatal("尝试成员不独立", attemptPage, err)
	}
	result, err := app.CompleteDelivery(ctx, claim.AttemptID, model.NotifySent)
	if err != nil || result.MemberCount != 501 {
		t.Fatal("整组结果失败", result, err)
	}
	if again, err := app.CompleteDelivery(ctx, claim.AttemptID, model.NotifySent); err != nil || again != result {
		t.Fatal("重复结果不幂等", again, err)
	}
	if _, err := app.CompleteDelivery(ctx, claim.AttemptID, model.NotifyFailed); !errors.Is(err, notification.ErrDeliveryConflict) {
		t.Fatal("成功结果被降级", err)
	}
	if claims, err := app.ClaimDelivery(ctx, 20); err != nil || len(claims) != 0 {
		t.Fatal("汇总重复领取", claims, err)
	}
	var tables, version int
	if err := db.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name IN ('schedules','notifications','schedule_versions','schedule_write_receipts')`).Scan(&tables); err != nil || tables != 4 {
		t.Fatal("恢复修改数据库结构", tables, err)
	}
	if err := db.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != 3 {
		t.Fatal("恢复修改数据库版本", version, err)
	}
}

// TestRecoveryPauseResumeAndHistoricalText 验证未生成历史的停止依据和旧版本文本。
func TestRecoveryPauseResumeAndHistoricalText(t *testing.T) {
	_, _, notify, app, clock := schedulerFixture(t)
	ctx := context.Background()
	r := writeRule(t, app, "旧名", "0 9 * * *")
	clock.set(clock.now().AddDate(0, 0, 2).Add(2 * time.Minute))
	paused, err := app.WriteSchedule(ctx, rule("暂停名", "0 9 * * *", false, r.Schedule.ID), 1, "")
	if err != nil {
		t.Fatal(err)
	}
	clock.set(clock.now().AddDate(0, 0, 2))
	resumed, err := app.WriteSchedule(ctx, rule("新名", "0 9 * * *", true, r.Schedule.ID), paused.Schedule.Revision, "")
	if err != nil {
		t.Fatal(err)
	}
	clock.set(clock.now().AddDate(0, 0, 2))
	enableRecovery(t, app, notify, clock)
	view := completeRecovery(t, app, clock)
	if view == nil || view.MissedTotal == nil || *view.MissedTotal != 5 || view.DisplayableCount != 2 {
		t.Fatalf("暂停期间计入遗漏或旧任务复活: %#v", view)
	}
	page, err := app.QueryDeliveryMembers(ctx, view.DeliveryTaskID, "", "", clock.now())
	if err != nil || page.Total != 5 {
		t.Fatal(page, err)
	}
	for i, v := range page.Items {
		n, err := notify.GetNotificationByID(ctx, *v.NotificationID)
		if err != nil {
			t.Fatal(err)
		}
		if i < 3 && (!n.DeliveryStopped || v.Name != "旧名") {
			t.Fatal("历史文本或停止被覆盖", v, n)
		}
		if i >= 3 && (n.DeliveryStopped || v.Name != resumed.Schedule.Name) {
			t.Fatal("恢复后未来触发错误", v, n)
		}
	}
	claim := claimOne(t, app)
	if _, err := app.DeleteScheduleWithResult(ctx, r.Schedule.ID); err != nil {
		t.Fatal(err)
	}
	start, err := app.StartDelivery(ctx, claim.AttemptID)
	if err != nil || start.ShouldDisplay || start.AttemptNumber != 0 {
		t.Fatal("删除后展示或消费预算", start, err)
	}
}

// TestRecoveryPreservesStartedBudgetAndRevokesClaim 验证交接前守卫及已经失败任务的预算。
func TestRecoveryPreservesStartedBudgetAndRevokesClaim(t *testing.T) {
	for _, started := range []bool{false, true} {
		t.Run(fmt.Sprint(started), func(t *testing.T) {
			_, notify, app, clock, _ := deliveryFixture(t, 1)
			ctx := context.Background()
			claim := claimOne(t, app)
			if started {
				if _, err := app.StartDelivery(ctx, claim.AttemptID); err != nil {
					t.Fatal(err)
				}
				if _, err := app.CompleteDelivery(ctx, claim.AttemptID, model.NotifyFailed); err != nil {
					t.Fatal(err)
				}
			}
			enableRecovery(t, app, notify, clock)
			clock.set(clock.now().Add(time.Minute))
			if !started {
				if _, err := app.StartDelivery(ctx, claim.AttemptID); !errors.Is(err, notification.ErrAttemptNotFound) {
					t.Fatal("恢复前旧 claim 抢先展示", err)
				}
			}
			view := completeRecovery(t, app, clock)
			if view == nil || view.MissedTotal == nil || *view.MissedTotal != 1 {
				t.Fatal("遗漏历史不准确", view)
			}
			next := claimOne(t, app)
			if started {
				if next.DeliveryTaskID != claim.DeliveryTaskID || next.TaskType != "minute" {
					t.Fatal("已开始任务被转移", next)
				}
				start, err := app.StartDelivery(ctx, next.AttemptID)
				if err != nil || start.AttemptNumber != 2 {
					t.Fatal("失败预算被重置", start, err)
				}
			} else if next.TaskType != "recovery" {
				t.Fatal("未开始任务未转汇总", next)
			}
		})
	}
}

// TestRecoveryBatchRollbackAndRestart 验证事务失败及重启去重与旧身份失效。
func TestRecoveryBatchRollbackAndRestart(t *testing.T) {
	db, _, notify, app, clock := schedulerFixture(t)
	ctx := context.Background()
	r := writeRule(t, app, "恢复", "0 9 * * *")
	clock.set(clock.now().AddDate(0, 0, 3).Add(2 * time.Minute))
	enableRecovery(t, app, notify, clock)
	if _, err := db.db.Exec(`CREATE TRIGGER reject_recovery BEFORE INSERT ON notifications WHEN NEW.notify_time > 0 BEGIN SELECT RAISE(ABORT, 'reject recovery'); END`); err != nil {
		t.Fatal(err)
	}
	before, _ := notify.GetNotificationByID(ctx, r.Notification.ID)
	if _, err := app.RecoverStep(ctx); err == nil {
		t.Fatal("未触发事务回滚")
	}
	after, _ := notify.GetNotificationByID(ctx, r.Notification.ID)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("失败事务改变既有事实")
	}
	view, err := app.RecoverySnapshot(ctx, clock.now())
	if err != nil || view.State != "failed" || view.MaterializedCount != 0 || view.MissedTotal != nil {
		t.Fatal("失败推进了位置/总数", view, err)
	}
	if _, err := db.db.Exec(`DROP TRIGGER reject_recovery`); err != nil {
		t.Fatal(err)
	}
	view = completeRecovery(t, app, clock)
	claim := claimOne(t, app)
	if _, err := app.StartDelivery(ctx, claim.AttemptID); err != nil {
		t.Fatal(err)
	}
	if _, err := app.CompleteDelivery(ctx, claim.AttemptID, model.NotifyFailed); err != nil {
		t.Fatal(err)
	}
	restarted := &application.App{Schedule: app.Schedule, Notify: notify}
	enableRecovery(t, restarted, notify, clock)
	newView := completeRecovery(t, restarted, clock)
	if newView.MaterializedCount != view.MaterializedCount || newView.DeliveryTaskID == view.DeliveryTaskID {
		t.Fatal("重启身份或去重错误", newView)
	}
	if _, err := restarted.QueryDeliveryMembers(ctx, view.DeliveryTaskID, "", "", clock.now()); !errors.Is(err, notification.ErrTaskNotFound) {
		t.Fatal("重启保留旧任务", err)
	}
	if _, err := restarted.StartDelivery(ctx, claim.AttemptID); !errors.Is(err, notification.ErrAttemptNotFound) {
		t.Fatal("重启保留旧尝试", err)
	}
	newClaim := claimOne(t, restarted)
	start, err := restarted.StartDelivery(ctx, newClaim.AttemptID)
	if err != nil || start.AttemptNumber != 1 {
		t.Fatal("重启未重新计数", start, err)
	}
	if _, err := restarted.CompleteDelivery(ctx, newClaim.AttemptID, model.NotifySent); err != nil {
		t.Fatal(err)
	}
	third := &application.App{Schedule: app.Schedule, Notify: notify}
	enableRecovery(t, third, notify, clock)
	thirdView := completeRecovery(t, third, clock)
	if thirdView.MissedTotal == nil || *thirdView.MissedTotal != 0 {
		t.Fatal("sent 重新计入遗漏", thirdView)
	}
	if claims, err := third.ClaimDelivery(ctx, 20); err != nil || len(claims) != 0 {
		t.Fatal("sent 重新投递", claims, err)
	}
}

// TestRecoveryDeleteDuringBatches 验证整理未完成时删除仍保存完整旧历史。
func TestRecoveryDeleteDuringBatches(t *testing.T) {
	_, _, notify, app, clock := schedulerFixture(t)
	ctx := context.Background()
	r := writeRule(t, app, "删除前历史", "0 9 * * *")
	clock.set(clock.now().AddDate(0, 0, 501).Add(2 * time.Minute))
	enableRecovery(t, app, notify, clock)
	if _, err := app.RecoverStep(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := app.DeleteScheduleWithResult(ctx, r.Schedule.ID); err != nil {
		t.Fatal(err)
	}
	view := completeRecovery(t, app, clock)
	if view.MissedTotal == nil || *view.MissedTotal != 502 || view.DisplayableCount != 0 {
		t.Fatal("删除丢失尚未整理的历史或复活展示", view)
	}
	page, err := app.QueryDeliveryMembers(ctx, view.DeliveryTaskID, "", "", clock.now())
	if err != nil || page.Total != 502 {
		t.Fatal("删除后历史不可查", page, err)
	}
	if claims, err := app.ClaimDelivery(ctx, 20); err != nil || len(claims) != 0 {
		t.Fatal("全部停止仍领取汇总", claims, err)
	}
	if _, err := app.AcknowledgeNotifications(ctx, []int{*page.Items[0].NotificationID}, clock.now()); err != nil {
		t.Fatal("删除后历史不可确认", err)
	}
}

// TestRecoveryConcurrentStopAndLateResults 验证正式边界及实际尝试快照。
func TestRecoveryConcurrentStopAndLateResults(t *testing.T) {
	for i := 0; i < 8; i++ {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			_, notify, app, clock, writes := deliveryFixture(t, 1)
			ctx := context.Background()
			enableRecovery(t, app, notify, clock)
			clock.set(clock.now().Add(time.Minute))
			completeRecovery(t, app, clock)
			claim := claimOne(t, app)
			var start notification.DeliveryStart
			var startErr, stopErr error
			var wg sync.WaitGroup
			wg.Add(2)
			go func() { defer wg.Done(); start, startErr = app.StartDelivery(ctx, claim.AttemptID) }()
			go func() { defer wg.Done(); _, stopErr = app.DeleteScheduleWithResult(ctx, writes[0].Schedule.ID) }()
			wg.Wait()
			if startErr != nil || stopErr != nil {
				t.Fatal(startErr, stopErr)
			}
			if start.ShouldDisplay {
				app.DeliveryExecutorExited()
				if _, err := app.CompleteDelivery(ctx, claim.AttemptID, model.NotifySent); err != nil {
					t.Fatal("停止后迟到真实结果无法回流", err)
				}
				n, _ := notify.GetNotificationByID(ctx, writes[0].Notification.ID)
				if n.Status != model.NotifySent || !n.DeliveryStopped || n.AcknowledgedAt != nil {
					t.Fatal("真实结果覆盖停止或确认", n)
				}
			} else if start.AttemptNumber != 0 {
				t.Fatal("停止先完成仍消费次数", start)
			}
		})
	}
}

// TestRecoveryRetryBudgetAndFrozenMembers 验证汇总四次预算及每次成员冻结。
func TestRecoveryRetryBudgetAndFrozenMembers(t *testing.T) {
	_, notify, app, clock, writes := deliveryFixture(t, 2)
	ctx := context.Background()
	enableRecovery(t, app, notify, clock)
	clock.set(clock.now().Add(time.Minute))
	completeRecovery(t, app, clock)
	claim := claimOne(t, app)
	oldAttempt := claim.AttemptID
	for attempt := 1; attempt <= 4; attempt++ {
		start, err := app.StartDelivery(ctx, claim.AttemptID)
		if err != nil || start.AttemptNumber != attempt {
			t.Fatal("汇总预算错误", start, err)
		}
		if _, err := app.CompleteDelivery(ctx, claim.AttemptID, model.NotifyFailed); err != nil {
			t.Fatal(err)
		}
		if attempt == 1 {
			if _, err := app.AcknowledgeNotifications(ctx, []int{writes[0].Notification.ID}, clock.now()); err != nil {
				t.Fatal(err)
			}
		}
		delay := []time.Duration{5 * time.Second, 30 * time.Second, 120 * time.Second, 120 * time.Second}[attempt-1]
		if claims, err := app.ClaimDelivery(ctx, 20); err != nil || len(claims) != 0 {
			t.Fatal("等待中重新展示", claims, err)
		}
		clock.set(clock.now().Add(delay))
		if attempt < 4 {
			claim = claimOne(t, app)
			if _, err := app.RecoverStep(ctx); err != nil {
				t.Fatal(err)
			}
		}
	}
	if claims, err := app.ClaimDelivery(ctx, 20); err != nil || len(claims) != 0 {
		t.Fatal("超过四次上限", claims, err)
	}
	page, err := app.QueryDeliveryMembers(ctx, claim.DeliveryTaskID, oldAttempt, "", clock.now())
	if err != nil || page.Total != 2 {
		t.Fatal("旧尝试成员被新资格覆盖", page, err)
	}
	page, err = app.QueryDeliveryMembers(ctx, claim.DeliveryTaskID, claim.AttemptID, "", clock.now())
	if err != nil || page.Total != 1 {
		t.Fatal("新尝试未排除确认成员", page, err)
	}
}

// TestRecoveryRepeatedGapKeepsHistoricalCounts 验证回拨重扫不重复累计前一轮遗漏。
func TestRecoveryRepeatedGapKeepsHistoricalCounts(t *testing.T) {
	_, notify, app, clock, _ := deliveryFixture(t, 1)
	ctx := context.Background()
	enableRecovery(t, app, notify, clock)
	clock.set(clock.now().Add(time.Minute))
	first := completeRecovery(t, app, clock)
	if first.MissedTotal == nil || *first.MissedTotal != 1 {
		t.Fatal(first)
	}
	// 重新构建分钟组模拟回拨后对同一触发的重扫；同运行仍由原汇总拥有成员。
	clock.set(clock.now().Add(-time.Minute))
	app.Scheduler, _ = notification.NewScheduler(notify, notification.SchedulerOptions{Now: clock.now})
	if err := app.Scheduler.Step(ctx); err != nil {
		t.Fatal(err)
	}
	clock.set(clock.now().Add(2 * time.Minute))
	second := completeRecovery(t, app, clock)
	if second.MissedTotal == nil || *second.MissedTotal != 1 {
		t.Fatal("重合窗口重复统计旧遗漏", second)
	}
	claim := claimOne(t, app)
	if claim.DeliveryTaskID != second.DeliveryTaskID {
		t.Fatal("未合并旧未开始汇总", claim)
	}
	if _, err := app.QueryDeliveryMembers(ctx, first.DeliveryTaskID, "", "", clock.now()); !errors.Is(err, notification.ErrTaskNotFound) {
		t.Fatal("合并后旧未开始身份仍有效", err)
	}
}

// TestRecoveryCoalescesUnstartedSummaries 验证连续遗漏只保留一个准确汇总。
func TestRecoveryCoalescesUnstartedSummaries(t *testing.T) {
	_, notify, app, clock, _ := deliveryFixture(t, 1)
	ctx := context.Background()
	enableRecovery(t, app, notify, clock)
	clock.set(clock.now().Add(time.Minute))
	first := completeRecovery(t, app, clock)
	oldClaim := claimOne(t, app)
	writeRule(t, app, "第二次遗漏", "2 9 * * *")
	clock.set(clock.now().Add(2 * time.Minute))
	if err := app.Scheduler.Step(ctx); err != nil {
		t.Fatal(err)
	}
	second := completeRecovery(t, app, clock)
	if second.MissedTotal == nil || *second.MissedTotal != 2 || second.DisplayableCount != 2 {
		t.Fatal("连续遗漏统计不完整", second)
	}
	if _, err := app.StartDelivery(ctx, oldClaim.AttemptID); !errors.Is(err, notification.ErrAttemptNotFound) {
		t.Fatal("旧汇总 claim 未撤销", err)
	}
	if _, err := app.QueryDeliveryMembers(ctx, first.DeliveryTaskID, "", "", clock.now()); !errors.Is(err, notification.ErrTaskNotFound) {
		t.Fatal("旧汇总未退役", err)
	}
	claim := claimOne(t, app)
	start, err := app.StartDelivery(ctx, claim.AttemptID)
	if err != nil || start.MemberCount != 2 || start.AttemptNumber != 1 || start.Body != "有 2 项错过的日程需要处理" {
		t.Fatal("未形成唯一完整汇总", start, err)
	}
}

// TestRecoveryDoesNotBlockCurrentMinute 验证历史批间仍允许即时规则和当前分钟投递。
func TestRecoveryDoesNotBlockCurrentMinute(t *testing.T) {
	_, _, notify, app, clock := schedulerFixture(t)
	ctx := context.Background()
	writeRule(t, app, "长历史", "0 9 * * *")
	clock.set(clock.now().AddDate(0, 0, 501).Add(2 * time.Minute))
	enableRecovery(t, app, notify, clock)
	if _, err := app.RecoverStep(ctx); err != nil {
		t.Fatal(err)
	}
	current := writeRule(t, app, "实时新任务", "2 9 * * *")
	clock.set(clock.now().Add(time.Minute))
	if err := app.Scheduler.Step(ctx); err != nil {
		t.Fatal(err)
	}
	claim := claimOne(t, app)
	if claim.TaskType != "minute" {
		t.Fatal("整理未完成阻止实时通道", claim)
	}
	start, err := app.StartDelivery(ctx, claim.AttemptID)
	if err != nil || start.MemberCount != 1 || !strings.Contains(start.Body, current.Schedule.Name) {
		t.Fatal("实时任务未独立开始", start, err)
	}
	view, err := app.RecoverySnapshot(ctx, clock.now())
	if err != nil || view.State != "running" || view.MissedTotal != nil {
		t.Fatal("实时领取改变历史进度", view, err)
	}
	completeRecovery(t, app, clock)
}

// TestRecoveryFindsNewLoadedAfterClockRollback 验证回拨后新事实不会被已核验分钟永久跳过。
func TestRecoveryFindsNewLoadedAfterClockRollback(t *testing.T) {
	_, notify, app, clock, _ := deliveryFixture(t, 1)
	ctx := context.Background()
	enableRecovery(t, app, notify, clock)
	first := claimOne(t, app)
	if _, err := app.StartDelivery(ctx, first.AttemptID); err != nil {
		t.Fatal(err)
	}
	if _, err := app.CompleteDelivery(ctx, first.AttemptID, model.NotifySent); err != nil {
		t.Fatal(err)
	}
	clock.set(clock.now().Add(-30 * time.Second))
	added := writeRule(t, app, "回拨后新建", "0 9 * * *")
	if _, err := app.RecoverStep(ctx); err != nil {
		t.Fatal(err)
	}
	if len(app.Scheduler.Snapshot().Missing) == 0 {
		t.Fatal("未来交接窗口提前丢失")
	}
	if claims, err := app.ClaimDelivery(ctx, 20); err != nil || len(claims) != 0 {
		t.Fatal("回拨提前展示或重复旧通知", claims, err)
	}
	clock.set(clock.now().Add(90 * time.Second))
	if err := app.Scheduler.Step(ctx); err != nil {
		t.Fatal(err)
	}
	view := completeRecovery(t, app, clock)
	// 启动时的空历史轮次先结束，新登记的回拨窗口属于随后一轮。
	if view.MissedTotal != nil && *view.MissedTotal == 0 {
		view = completeRecovery(t, app, clock)
	}
	if view.MissedTotal == nil || *view.MissedTotal != 1 {
		t.Fatal("新 loaded 被永久跳过", view)
	}
	claim := claimOne(t, app)
	start, err := app.StartDelivery(ctx, claim.AttemptID)
	if err != nil || claim.TaskType != "recovery" || start.MemberCount != 1 {
		t.Fatal("新事实未交汇总", claim, start, err)
	}
	ids, err := app.DeliveryAttemptMembers(claim.AttemptID)
	if err != nil || !reflect.DeepEqual(ids, []int{added.Notification.ID}) {
		t.Fatal("恢复重展示旧 sent", ids, err)
	}
}

// TestRecoveryUnacknowledgedCount 验证确认只减少待处理数，不改完整历史与真实投递事实。
func TestRecoveryUnacknowledgedCount(t *testing.T) {
	for _, confirmedBeforeRecovery := range []bool{false, true} {
		t.Run(fmt.Sprint(confirmedBeforeRecovery), func(t *testing.T) {
			_, _, notify, app, clock := schedulerFixture(t)
			ctx := context.Background()
			first := writeRule(t, app, "遗漏一", "0 9 * * *")
			second := writeRule(t, app, "遗漏二", "0 9 * * *")
			clock.set(clock.now().Add(3 * time.Minute))
			if confirmedBeforeRecovery {
				if _, err := app.AcknowledgeNotifications(ctx, []int{first.Notification.ID, second.Notification.ID}, clock.now()); err != nil {
					t.Fatal(err)
				}
			}
			enableRecovery(t, app, notify, clock)
			view := completeRecovery(t, app, clock)
			if view == nil || view.MissedTotal == nil || *view.MissedTotal != 2 {
				t.Fatal("完整历史改变", view)
			}
			if confirmedBeforeRecovery {
				if view.UnacknowledgedCount != 0 || view.DisplayableCount != 0 {
					t.Fatal("已确认历史被提示", view)
				}
			} else {
				if view.UnacknowledgedCount != 2 {
					t.Fatal("未确认历史未统计", view)
				}
				claim := claimOne(t, app)
				if _, err := app.StartDelivery(ctx, claim.AttemptID); err != nil {
					t.Fatal(err)
				}
				if _, err := app.CompleteDelivery(ctx, claim.AttemptID, model.NotifySent); err != nil {
					t.Fatal(err)
				}
				view, _ = app.RecoverySnapshot(ctx, clock.now())
				if view.UnacknowledgedCount != 2 || view.DisplayableCount != 0 {
					t.Fatal("sent 被误当确认", view)
				}
				if _, err := app.AcknowledgeNotifications(ctx, []int{first.Notification.ID}, clock.now()); err != nil {
					t.Fatal(err)
				}
				view, _ = app.RecoverySnapshot(ctx, clock.now())
				if view.UnacknowledgedCount != 1 || *view.MissedTotal != 2 {
					t.Fatal("部分确认计数不准确", view)
				}
				if _, err := app.AcknowledgeNotifications(ctx, []int{second.Notification.ID}, clock.now()); err != nil {
					t.Fatal(err)
				}
				view, _ = app.RecoverySnapshot(ctx, clock.now())
				if view.UnacknowledgedCount != 0 || *view.MissedTotal != 2 {
					t.Fatal("全部确认后仍提示", view)
				}
			}
			page, err := app.QueryDeliveryMembers(ctx, view.DeliveryTaskID, "", "", clock.now())
			if err != nil || page.Total != 2 || page.Recovery.UnacknowledgedCount != 0 {
				t.Fatal("确认后历史丢失", page, err)
			}
			for _, item := range page.Items {
				if item.AcknowledgedAt == nil {
					t.Fatal("确认事实丢失", item)
				}
			}
			if claims, err := app.ClaimDelivery(ctx, 20); err != nil || len(claims) != 0 {
				t.Fatal("已确认提醒重复投递", claims, err)
			}
		})
	}
}
