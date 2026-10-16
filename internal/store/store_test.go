package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	application "github.com/dengweitian0-svg/TimeCue/internal/app"
	"github.com/dengweitian0-svg/TimeCue/internal/config"
	"github.com/dengweitian0-svg/TimeCue/internal/model"
	"github.com/dengweitian0-svg/TimeCue/internal/service/notification"
	schedulesvc "github.com/dengweitian0-svg/TimeCue/internal/service/schedule"
)

// 编译期确认两个存储实现直接满足服务层接口。
var (
	_ schedulesvc.Repo  = (*ScheduleStore)(nil)
	_ notification.Repo = (*NotificationStore)(nil)
)

func requireBeijingTime(t *testing.T, value time.Time) {
	t.Helper()
	_, offset := value.Zone()
	if offset != 8*60*60 {
		t.Fatalf("时间应带 +08:00 偏移: %v", value)
	}
}

// newMigratedStore 从独立 SQL 文件建立测试数据库，生产连接不会执行迁移。
func newMigratedStore(t *testing.T) (*Sqlite, *ScheduleStore, *NotificationStore) {
	t.Helper()
	ctx := context.Background()
	db := NewSqlite()
	if err := db.Connect(ctx, filepath.Join(t.TempDir(), "timecue.db")); err != nil {
		t.Fatalf("连接数据库: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	var count int
	if err := db.db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'schedules'`).Scan(&count); err != nil {
		t.Fatalf("检查迁移前表结构: %v", err)
	}
	if count != 0 {
		t.Fatal("Connect 不应自动执行迁移")
	}
	script, err := os.ReadFile(filepath.Join("..", "..", "migrations", "001_initial.up.sql"))
	if err != nil {
		t.Fatalf("读取独立迁移: %v", err)
	}
	if _, err := db.db.ExecContext(ctx, string(script)); err != nil {
		t.Fatalf("应用测试迁移: %v", err)
	}
	script2, err := os.ReadFile(filepath.Join("..", "..", "migrations", "002_schedule_changes.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(string(script2)); err != nil {
		t.Fatal(err)
	}
	schedules, err := NewScheduleStore(db)
	if err != nil {
		t.Fatalf("创建日程存储: %v", err)
	}
	notifications, err := NewNotificationStore(db)
	if err != nil {
		t.Fatalf("创建提醒存储: %v", err)
	}
	return db, schedules, notifications
}

// TestSoftDeletePreservesHistory 验证删除日程时已发生提醒仍可查询。
func TestSoftDeletePreservesHistory(t *testing.T) {
	_, schedules, notifications := newMigratedStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Minute)
	schedule, err := schedules.CreateSchedule(ctx, model.Schedule{
		Name: "周期日程", Type: model.PeriodSchedule,
		Period: &model.Period{Open: true, Cron: []string{"0 9 * * *"}},
	})
	if err != nil {
		t.Fatalf("创建日程: %v", err)
	}
	past, err := notifications.CreateNotification(ctx, schedule.ID, now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("创建历史提醒: %v", err)
	}
	if err := notifications.SetDeliveryResult(ctx, past.ID, model.NotifySent); err != nil {
		t.Fatalf("记录投递结果: %v", err)
	}
	if err := notifications.ConfirmNotification(ctx, past.ID, now); err != nil {
		t.Fatalf("确认提醒: %v", err)
	}
	future, err := notifications.CreateNotification(ctx, schedule.ID, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("创建未来提醒: %v", err)
	}
	if _, err := schedules.DeleteScheduleByID(ctx, schedule.ID); err != nil {
		t.Fatalf("软删除日程: %v", err)
	}
	stored, err := schedules.GetScheduleByID(ctx, schedule.ID, model.WithScheduleIncludeDeleted(true))
	if err != nil || stored == nil || !stored.Deleted {
		t.Fatalf("已删除日程仍应可按 ID 查询: %#v, %v", stored, err)
	}
	if _, err := schedules.GetScheduleByID(ctx, schedule.ID,
		model.WithScheduleIncludeDeleted(true), model.WithScheduleIncludeDeleted(false)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("后传入的软删除选项应覆盖前一个: %v", err)
	}
	if _, err := schedules.GetScheduleByID(ctx, schedule.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("普通查询应隐藏已删除日程，得到 %v", err)
	}
	if _, err := schedules.GetScheduleByID(ctx, 0); err == nil {
		t.Fatal("按 ID 查询必须提供有效的日程 ID")
	}
	visible, err := schedules.GetAllSchedule(ctx)
	if err != nil || len(visible) != 0 {
		t.Fatalf("普通列表应隐藏日程: %#v, %v", visible, err)
	}
	withDeleted, err := schedules.GetAllSchedule(ctx, model.WithScheduleIncludeDeleted(true))
	if err != nil || len(withDeleted) != 1 || withDeleted[0].ID != schedule.ID {
		t.Fatalf("包含软删除选项应返回已删除日程: %#v, %v", withDeleted, err)
	}
	history, err := notifications.GetNotificationHistoryByScheduleID(ctx, schedule.ID)
	if err != nil || len(history) != 2 {
		t.Fatalf("提醒历史应保留两条记录: %#v, %v", history, err)
	}
	storedPast, err := notifications.GetNotificationByID(ctx, past.ID)
	if err != nil || storedPast.Deleted || storedPast.AcknowledgedAt == nil || storedPast.Status != model.NotifySent {
		t.Fatalf("已发生提醒状态丢失: %#v, %v", storedPast, err)
	}
	requireBeijingTime(t, storedPast.NotifyTime)
	requireBeijingTime(t, *storedPast.AcknowledgedAt)
	storedFuture, err := notifications.GetNotificationByID(ctx, future.ID)
	if err != nil || !storedFuture.Deleted {
		t.Fatalf("未来待处理提醒应被隐藏: %#v, %v", storedFuture, err)
	}
	if _, err := notifications.CreateNotification(ctx, schedule.ID, now.Add(2*time.Hour)); !errors.Is(err, ErrScheduleDeleted) {
		t.Fatalf("已删除日程应拒绝新提醒，得到 %v", err)
	}
}

// TestNotificationCreationAndTransitions 验证幂等创建、暂停及独立状态转换。
func TestNotificationCreationAndTransitions(t *testing.T) {
	_, schedules, notifications := newMigratedStore(t)
	ctx := context.Background()
	notifyAt := time.Now().UTC().Add(time.Hour).Truncate(time.Minute).Truncate(time.Minute)
	schedule, err := schedules.CreateSchedule(ctx, model.Schedule{
		Name: "单次日程", Type: model.SingleSchedule,
		Single: &model.Single{NotifyTime: notifyAt},
	})
	if err != nil {
		t.Fatalf("创建单次日程: %v", err)
	}
	first, err := notifications.CreateNotification(ctx, schedule.ID, notifyAt)
	if err != nil {
		t.Fatalf("创建提醒: %v", err)
	}
	again, err := notifications.CreateNotification(ctx, schedule.ID, notifyAt)
	if err != nil || again.ID != first.ID {
		t.Fatalf("重复创建未保持幂等: %#v, %v", again, err)
	}
	if _, err := notifications.CreateNotification(ctx, schedule.ID, notifyAt.Add(time.Minute)); err == nil {
		t.Fatal("单次日程不应接受其他提醒时间")
	}
	if err := notifications.ConfirmNotification(ctx, first.ID, notifyAt.Add(-time.Minute)); !errors.Is(err, model.ErrInvalidAcknowledgement) {
		t.Fatalf("未来提醒确认应失败，得到 %v", err)
	}
	if err := notifications.SetDeliveryResult(ctx, first.ID, model.NotifyFailed); err != nil {
		t.Fatalf("记录失败投递: %v", err)
	}
	if err := notifications.SetDeliveryResult(ctx, first.ID, model.NotifySent); err != nil {
		t.Fatalf("记录重试成功: %v", err)
	}
	if err := notifications.ConfirmNotification(ctx, first.ID, notifyAt); err != nil {
		t.Fatalf("确认提醒: %v", err)
	}
	if err := notifications.SetDeliveryResult(ctx, first.ID, model.NotifyFailed); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("成功投递后不应回退，得到 %v", err)
	}
	period, err := schedules.CreateSchedule(ctx, model.Schedule{
		Name: "暂停日程", Type: model.PeriodSchedule,
		Period: &model.Period{Open: false, Cron: []string{"0 9 * * *"}},
	})
	if err != nil {
		t.Fatalf("创建暂停日程: %v", err)
	}
	if _, err := notifications.CreateNotification(ctx, period.ID, notifyAt); !errors.Is(err, ErrSchedulePaused) {
		t.Fatalf("暂停日程应拒绝新提醒，得到 %v", err)
	}
	if err := schedules.SetScheduleOpen(ctx, period.ID, true); err != nil {
		t.Fatalf("恢复周期日程: %v", err)
	}
	periodNotification, err := notifications.CreateNotification(ctx, period.ID, notifyAt)
	if err != nil {
		t.Fatalf("恢复后创建提醒: %v", err)
	}
	if err := schedules.CancelScheduleByID(ctx, period.ID); err != nil {
		t.Fatalf("终止周期日程: %v", err)
	}
	stored, err := notifications.GetNotificationByID(ctx, periodNotification.ID)
	if err != nil || !stored.Deleted {
		t.Fatalf("终止日程应隐藏未来提醒: %#v, %v", stored, err)
	}
	if _, err := notifications.CreateNotification(ctx, period.ID, notifyAt.Add(time.Hour)); !errors.Is(err, ErrScheduleCancelled) {
		t.Fatalf("已终止日程应拒绝新提醒，得到 %v", err)
	}
}

// TestStoreErrorFormat 验证公开错误的英文前缀、参数和可判别性。
func TestStoreErrorFormat(t *testing.T) {
	ctx := context.Background()
	var disconnected Sqlite
	chineseText := regexp.MustCompile(`\p{Han}`)
	check := func(err error, parameter string) {
		t.Helper()
		if err == nil || !strings.HasPrefix(err.Error(), "[store] ") || !strings.Contains(err.Error(), parameter) {
			t.Fatalf("存储错误缺少前缀或参数 %q: %v", parameter, err)
		}
		if chineseText.MatchString(err.Error()) {
			t.Fatalf("存储错误包含中文说明: %v", err)
		}
	}
	check(disconnected.Connect(ctx, " "), `path=" "`)
	closeErr := disconnected.Close()
	check(closeErr, "database is not connected")
	if !errors.Is(closeErr, ErrNotConnected) {
		t.Fatalf("关闭错误应保留 ErrNotConnected: %v", closeErr)
	}
	_, schedules, notifications := newMigratedStore(t)
	_, err := schedules.CreateSchedule(ctx, model.Schedule{Name: ""})
	check(err, `name=""`)
	id := 999
	_, err = schedules.GetScheduleByID(ctx, id)
	check(err, "schedule_id=999")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("查询错误应保留 ErrNotFound: %v", err)
	}
	_, err = notifications.CreateNotification(ctx, id, time.Now().UTC())
	check(err, "schedule_id=999")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("提醒创建错误应保留 ErrNotFound: %v", err)
	}
	err = notifications.DeleteNotificationByID(ctx, id)
	check(err, "notification_id=999")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("删除错误应保留 ErrNotFound: %v", err)
	}
}

// TestGetScheduleByIDAppliesOptionalFilters 验证单条查询会使用所有已设置条件。
func TestGetScheduleByIDAppliesOptionalFilters(t *testing.T) {
	_, schedules, _ := newMigratedStore(t)
	ctx := context.Background()
	schedule, err := schedules.CreateSchedule(ctx, model.Schedule{
		Name: "查询日程", Type: model.SingleSchedule,
		Single: &model.Single{NotifyTime: time.Now().UTC().Add(time.Hour).Truncate(time.Minute)},
	})
	if err != nil {
		t.Fatalf("创建日程: %v", err)
	}
	query := []model.QueryOption{
		model.WithScheduleID(schedule.ID), model.WithScheduleName("查询"),
		model.WithScheduleType(model.SingleSchedule), model.WithScheduleLimit(1), model.WithScheduleOffset(0),
	}
	got, err := schedules.GetScheduleByID(ctx, schedule.ID, query...)
	if err != nil || got == nil || got.ID != schedule.ID {
		t.Fatalf("组合条件应返回日程: %#v, %v", got, err)
	}
	otherID := schedule.ID + 1
	if _, err := schedules.GetScheduleByID(ctx, schedule.ID, append(query, model.WithScheduleID(otherID))...); !errors.Is(err, ErrNotFound) {
		t.Fatalf("可选 ID 条件不匹配应返回 ErrNotFound: %v", err)
	}
	if _, err := schedules.GetScheduleByID(ctx, schedule.ID, append(query, model.WithScheduleName("不匹配"))...); !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), `name="不匹配"`) {
		t.Fatalf("名称条件不匹配应返回 ErrNotFound: %v", err)
	}
	if _, err := schedules.GetScheduleByID(ctx, schedule.ID, append(query, model.WithScheduleOffset(1))...); !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "offset=1") {
		t.Fatalf("offset 应参与查询: %v", err)
	}
	if _, err := schedules.GetScheduleByID(ctx, schedule.ID, append(query, model.WithScheduleLimit(0))...); err == nil || !strings.Contains(err.Error(), "limit=0") {
		t.Fatalf("非法 limit 应返回带参数的错误: %v", err)
	}
}

// TestGetAllScheduleAcceptsOptionalFilters 验证列表查询没有必填参数且会组合已设置条件。
func TestGetAllScheduleAcceptsOptionalFilters(t *testing.T) {
	_, schedules, _ := newMigratedStore(t)
	ctx := context.Background()
	first, err := schedules.CreateSchedule(ctx, model.Schedule{
		Name: "早间会议", Type: model.SingleSchedule,
		Single: &model.Single{NotifyTime: time.Now().UTC().Add(time.Hour).Truncate(time.Minute)},
	})
	if err != nil {
		t.Fatalf("创建单次日程: %v", err)
	}
	second, err := schedules.CreateSchedule(ctx, model.Schedule{
		Name: "晚间复盘", Type: model.PeriodSchedule,
		Period: &model.Period{Open: true, Cron: []string{"0 20 * * *"}},
	})
	if err != nil {
		t.Fatalf("创建周期日程: %v", err)
	}
	all, err := schedules.GetAllSchedule(ctx)
	if err != nil || len(all) != 2 {
		t.Fatalf("空查询应返回所有可见日程: %#v, %v", all, err)
	}
	filtered, err := schedules.GetAllSchedule(ctx,
		model.WithScheduleID(second.ID), model.WithScheduleName("晚间"),
		model.WithScheduleType(model.PeriodSchedule), model.WithScheduleLimit(1), model.WithScheduleOffset(0))
	if err != nil || len(filtered) != 1 || filtered[0].ID != second.ID {
		t.Fatalf("组合条件应返回周期日程: %#v, %v", filtered, err)
	}
	byID, err := schedules.GetAllSchedule(ctx, model.WithScheduleID(first.ID))
	if err != nil || len(byID) != 1 || byID[0].ID != first.ID {
		t.Fatalf("仅 ID 条件应返回指定日程: %#v, %v", byID, err)
	}
	byLastID, err := schedules.GetAllSchedule(ctx, model.WithScheduleID(first.ID), nil, model.WithScheduleID(second.ID))
	if err != nil || len(byLastID) != 1 || byLastID[0].ID != second.ID {
		t.Fatalf("空选项应忽略，后传入的 ID 应覆盖前一个: %#v, %v", byLastID, err)
	}
	byType, err := schedules.GetAllSchedule(ctx, model.WithScheduleType(model.PeriodSchedule))
	if err != nil || len(byType) != 1 || byType[0].ID != second.ID {
		t.Fatalf("仅类型条件应返回周期日程: %#v, %v", byType, err)
	}
	page, err := schedules.GetAllSchedule(ctx, model.WithScheduleLimit(1), model.WithScheduleOffset(1))
	if err != nil || len(page) != 1 || page[0].ID != first.ID {
		t.Fatalf("分页条件应跳过第一条日程: %#v, %v", page, err)
	}
	filtered, err = schedules.GetAllSchedule(ctx, model.WithScheduleName("不存在"))
	if err != nil || len(filtered) != 0 {
		t.Fatalf("名称条件应参与列表过滤: %#v, %v", filtered, err)
	}
	for _, test := range []struct {
		name   string
		option model.QueryOption
		detail string
	}{
		{name: "ID", option: model.WithScheduleID(0), detail: "schedule_id=0"},
		{name: "offset", option: model.WithScheduleOffset(-1), detail: "offset=-1"},
		{name: "type", option: model.WithScheduleType("unknown"), detail: `type="unknown"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := schedules.GetAllSchedule(ctx, test.option); err == nil || !strings.Contains(err.Error(), test.detail) {
				t.Fatalf("非法查询条件应返回带参数的错误: %v", err)
			}
		})
	}
}

// TestAppCreatesAndAdvancesNotifications 验证创建用例、暂停状态与重复推进的持久化结果。
func TestAppCreatesAndAdvancesNotifications(t *testing.T) {
	_, schedules, notifications := newMigratedStore(t)
	manager, err := config.NewManager()
	if err != nil {
		t.Fatalf("创建配置管理器: %v", err)
	}
	app, err := application.NewApp(manager, schedules, notifications)
	if err != nil {
		t.Fatalf("组装服务: %v", err)
	}
	ctx := context.Background()
	firstTime := time.Now().UTC().Add(time.Hour).Truncate(time.Minute)
	singleInput := model.Schedule{
		Name: "单次", Type: model.SingleSchedule, Single: &model.Single{NotifyTime: firstTime},
	}
	single, first, err := app.CreateSchedule(ctx, singleInput)
	if err != nil || single == nil || first == nil || first.ScheduleID != single.ID || !first.NotifyTime.Equal(firstTime) {
		t.Fatalf("创建单次日程及提醒: %#v, %#v, %v", single, first, err)
	}
	if singleInput.Single.NotifyTime.Location() != time.UTC {
		t.Fatalf("创建不应改变调用方的输入时区: %v", singleInput.Single.NotifyTime)
	}
	requireBeijingTime(t, single.Single.NotifyTime)
	requireBeijingTime(t, single.CreatedAt)
	requireBeijingTime(t, single.UpdatedAt)
	requireBeijingTime(t, first.NotifyTime)
	loadedSchedule, err := schedules.GetScheduleByID(ctx, single.ID)
	if err != nil {
		t.Fatalf("读取单次日程: %v", err)
	}
	requireBeijingTime(t, loadedSchedule.Single.NotifyTime)
	requireBeijingTime(t, loadedSchedule.CreatedAt)
	loadedNotification, err := notifications.GetNotificationByID(ctx, first.ID)
	if err != nil {
		t.Fatalf("读取首条提醒: %v", err)
	}
	requireBeijingTime(t, loadedNotification.NotifyTime)
	if next, err := app.AdvanceNotification(ctx, *first); next != nil || !errors.Is(err, application.ErrNotPeriodSchedule) {
		t.Fatalf("单次提醒推进应返回类型错误: %#v, %v", next, err)
	}
	if _, _, err := app.CreateSchedule(ctx, model.Schedule{
		Name: "过期", Type: model.SingleSchedule, Single: &model.Single{NotifyTime: time.Now().UTC().Add(-time.Hour)},
	}); err == nil {
		t.Fatal("过期单次日程应在写入前被拒绝")
	}
	if _, _, err := app.CreateSchedule(ctx, model.Schedule{
		Name: "非法规则", Type: model.PeriodSchedule, Period: &model.Period{Open: true, Cron: []string{"0 9 1 * 1"}},
	}); err == nil {
		t.Fatal("日期与星期混用应在写入前被拒绝")
	}
	period, initial, err := app.CreateSchedule(ctx, model.Schedule{
		Name: "周期", Type: model.PeriodSchedule, Period: &model.Period{Open: true, Cron: []string{"0 9 30,31 * *"}},
	})
	if err != nil || initial == nil || !initial.NotifyTime.After(time.Now().UTC()) {
		t.Fatalf("创建周期日程及首提醒: %#v, %#v, %v", period, initial, err)
	}
	requireBeijingTime(t, initial.NotifyTime)
	next, err := app.AdvanceNotification(ctx, *initial)
	if err != nil || next == nil || !next.NotifyTime.After(initial.NotifyTime) {
		t.Fatalf("周期提醒应严格向后推进: %#v, %v", next, err)
	}
	again, err := app.AdvanceNotification(ctx, *initial)
	if err != nil || again == nil || again.ID != next.ID {
		t.Fatalf("重复推进应复用同一提醒: %#v, %v", again, err)
	}
	history, err := notifications.GetNotificationHistoryByScheduleID(ctx, period.ID)
	if err != nil || len(history) != 2 {
		t.Fatalf("重复推进不应增加提醒数: %#v, %v", history, err)
	}
	paused, pausedInitial, err := app.CreateSchedule(ctx, model.Schedule{
		Name: "暂停", Type: model.PeriodSchedule, Period: &model.Period{Open: false, Cron: []string{"0 9 * * 7"}},
	})
	if err != nil || pausedInitial != nil {
		t.Fatalf("暂停日程不应生成首提醒: %#v, %v", pausedInitial, err)
	}
	nextNotify, err := notification.NewNextNotifyService(schedules, notifications)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := nextNotify.Next(ctx, paused.ID, time.Now().UTC()); err != nil || got != nil {
		t.Fatalf("暂停日程不应推进: %#v, %v", got, err)
	}
	if err := schedules.SetScheduleOpen(ctx, paused.ID, true); err != nil {
		t.Fatalf("恢复日程: %v", err)
	}
	if got, err := nextNotify.Next(ctx, paused.ID, time.Now().UTC()); err != nil || got == nil {
		t.Fatalf("恢复后应可推进: %#v, %v", got, err)
	}
	if err := schedules.CancelScheduleByID(ctx, period.ID); err != nil {
		t.Fatalf("取消日程: %v", err)
	}
	if got, err := app.AdvanceNotification(ctx, *next); err != nil || got != nil {
		t.Fatalf("取消日程不应继续推进: %#v, %v", got, err)
	}
	all, err := schedules.GetAllSchedule(ctx)
	if err != nil || len(all) != 3 {
		t.Fatalf("失败的创建不应留下日程: %#v, %v", all, err)
	}
}

// TestCreateScheduleWithNotificationRollsBack 验证首条提醒写入失败会撤销日程写入。
func TestCreateScheduleWithNotificationRollsBack(t *testing.T) {
	db, schedules, _ := newMigratedStore(t)
	ctx := context.Background()
	if _, err := db.db.ExecContext(ctx, `CREATE TRIGGER fail_initial_notification BEFORE INSERT ON notifications
		BEGIN SELECT RAISE(FAIL, 'blocked notification'); END`); err != nil {
		t.Fatalf("创建故障触发器: %v", err)
	}
	notifyAt := time.Now().UTC().Add(time.Hour).Truncate(time.Minute)
	created, notification, err := schedules.CreateScheduleWithNotification(ctx, model.Schedule{
		Name: "应回滚", Type: model.SingleSchedule, Single: &model.Single{NotifyTime: notifyAt},
	}, &notifyAt)
	if err == nil || created != nil || notification != nil {
		t.Fatalf("首提醒写入失败应返回错误: %#v, %#v, %v", created, notification, err)
	}
	var count int
	if err := db.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schedules`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("失败事务不应留下日程: count=%d, err=%v", count, err)
	}
}

func queryApp(t *testing.T, schedules *ScheduleStore, notifications *NotificationStore) *application.App {
	t.Helper()
	manager, err := config.NewManager()
	if err != nil {
		t.Fatal(err)
	}
	app, err := application.NewApp(manager, schedules, notifications)
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func TestNotificationConfirmationAtomicAndPersistent(t *testing.T) {
	db, schedules, notifications := newMigratedStore(t)
	app := queryApp(t, schedules, notifications)
	ctx := context.Background()
	now := time.Date(2030, 10, 8, 14, 32, 0, 0, model.BeijingLocation())
	schedule, err := schedules.CreateSchedule(ctx, model.Schedule{Name: "同日多次", Type: model.PeriodSchedule, Period: &model.Period{Open: true, Cron: []string{"0 9 * * *", "0 17 * * *"}}})
	if err != nil {
		t.Fatal(err)
	}
	first, err := notifications.CreateNotification(ctx, schedule.ID, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	second, err := notifications.CreateNotification(ctx, schedule.ID, now.Add(-2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	future, err := notifications.CreateNotification(ctx, schedule.ID, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err = notifications.SetDeliveryResult(ctx, second.ID, model.NotifyFailed); err != nil {
		t.Fatal(err)
	}
	if _, err = app.AcknowledgeNotifications(ctx, []int{first.ID, future.ID}, now); !errors.Is(err, model.ErrInvalidAcknowledgement) {
		t.Fatalf("未来成员应拒绝整批: %v", err)
	}
	first, _ = notifications.GetNotificationByID(ctx, first.ID)
	if first.AcknowledgedAt != nil {
		t.Fatal("失败批次不应部分确认")
	}
	if _, err = app.AcknowledgeNotifications(ctx, []int{first.ID, 999999}, now); !errors.Is(err, model.ErrRecordNotFound) {
		t.Fatalf("不存在成员应拒绝: %v", err)
	}
	values, err := app.AcknowledgeNotifications(ctx, []int{first.ID, second.ID, first.ID}, now)
	if err != nil || len(values) != 2 {
		t.Fatalf("明确集合去重: %v %v", values, err)
	}
	values, err = app.AcknowledgeNotifications(ctx, []int{first.ID, second.ID}, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range values {
		if !n.AcknowledgedAt.Equal(now) {
			t.Fatal("重复确认应保留首次时间")
		}
	}
	if values[0].Status != model.NotifyPending || values[1].Status != model.NotifyFailed {
		t.Fatal("确认不能覆写投递结果")
	}
	if err = notifications.SetDeliveryResult(ctx, first.ID, model.NotifySent); err != nil {
		t.Fatal(err)
	}
	first, _ = notifications.GetNotificationByID(ctx, first.ID)
	if !first.AcknowledgedAt.Equal(now) {
		t.Fatal("投递回填不能重置确认")
	}
	var path string
	if err = db.db.QueryRow(`SELECT file FROM pragma_database_list WHERE name = 'main'`).Scan(&path); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	if err = db.Connect(ctx, path); err != nil {
		t.Fatal(err)
	}
	second, err = notifications.GetNotificationByID(ctx, second.ID)
	if err != nil || !second.AcknowledgedAt.Equal(now) || second.Status != model.NotifyFailed {
		t.Fatalf("重启丢失业务事实: %#v %v", second, err)
	}
}

func TestScheduleQueriesHistoryMidnightProjectionAndPagination(t *testing.T) {
	_, schedules, notifications := newMigratedStore(t)
	app := queryApp(t, schedules, notifications)
	ctx := context.Background()
	now := time.Date(2030, 10, 8, 0, 0, 0, 0, model.BeijingLocation())
	schedule, err := schedules.CreateSchedule(ctx, model.Schedule{Name: "每日两次", Type: model.PeriodSchedule, Period: &model.Period{Open: true, Cron: []string{"0 9 * * *", "0 17 * * *"}}})
	if err != nil {
		t.Fatal(err)
	}
	var ids []int
	for i := 0; i < 61; i++ {
		n, err := notifications.CreateNotification(ctx, schedule.ID, now.Add(-time.Duration(i)*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, n.ID)
	}
	// 持久化未来 pending 与同一投影只能显示一行。
	_, err = notifications.CreateNotification(ctx, schedule.ID, now.Add(9*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := app.QueryScheduleState(ctx, "", now)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Date != "2030-10-08" || len(snapshot.Occurrences.Items) != 3 || snapshot.Unacknowledged.Total != 61 || len(snapshot.Unacknowledged.Items) != 50 {
		t.Fatalf("日期、独立触发或分页错误: %#v", snapshot)
	}
	before, err := app.QueryOccurrences(ctx, "2030-10-07", "", now)
	if err != nil {
		t.Fatal(err)
	}
	if before.Total != 60 {
		t.Fatalf("不能用新规则重建过去，应仅有 60 个事实: %d", before.Total)
	}
	next, err := app.QueryUnacknowledged(ctx, snapshot.Unacknowledged.NextCursor, now)
	if err != nil || len(next.Items) != 11 {
		t.Fatalf("待确认续页: %#v %v", next, err)
	}
	if _, err = app.QueryOccurrences(ctx, "2030-10-08", snapshot.Unacknowledged.NextCursor, now); !errors.Is(err, schedulesvc.ErrInvalidQuery) {
		t.Fatal("跨用途游标应被拒绝")
	}
	calendar, err := app.QueryCalendar(ctx, "2030-10", now)
	if err != nil || len(calendar) != 25 {
		t.Fatalf("月份应含历史7日及8日以后安排: %v %v", calendar, err)
	}
	if _, err = app.QueryCalendar(ctx, "2030-10-08", now); !errors.Is(err, schedulesvc.ErrInvalidQuery) {
		t.Fatal("月份格式必须严格")
	}
	if _, err = app.QueryScheduleState(ctx, "2030-02-30", now); !errors.Is(err, schedulesvc.ErrInvalidQuery) {
		t.Fatal("非法日期必须拒绝")
	}
	// 直接设软删除以使用确定时刻测试历史查询；实际删除转换另有存储测试。
	db, _ := notifications.db.handle()
	if _, err = db.Exec(`UPDATE schedules SET deleted=1 WHERE id=?`, schedule.ID); err != nil {
		t.Fatal(err)
	}
	history, err := app.GetNotification(ctx, ids[1], now)
	if err != nil || history.Editable {
		t.Fatalf("删除后历史应只读可见: %#v %v", history, err)
	}
	after, err := app.QueryScheduleState(ctx, "", now)
	if err != nil || after.Unacknowledged.Total != 61 || len(after.Occurrences.Items) != 1 {
		t.Fatalf("删除父日程不应隐藏到期历史: %#v %v", after, err)
	}
	if _, err = app.AcknowledgeNotifications(ctx, []int{ids[1]}, now); err != nil {
		t.Fatal(err)
	}
	// 保留原有日程、提醒、规则版本及写入回执四张表，运行状态不扩展数据库。
	var tableCount int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`).Scan(&tableCount); err != nil || tableCount != 4 {
		t.Fatalf("业务表结构数量不符，表数量=%d，错误=%v", tableCount, err)
	}
}
