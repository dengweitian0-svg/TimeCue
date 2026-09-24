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

	"github.com/dengweitian0-svg/TimeCue/internal/model"
	"github.com/dengweitian0-svg/TimeCue/internal/service"
)

// 编译期确认两个存储实现直接满足服务层接口。
var (
	_ service.ScheduleRepo = (*ScheduleStore)(nil)
	_ service.NotifyRepo   = (*NotificationStore)(nil)
)

// newMigratedStore 从独立 SQL 文件建立测试数据库，生产连接不会执行迁移。
func newMigratedStore(t *testing.T) (*Sqlite, *ScheduleStore, *NotificationStore) {
	t.Helper()
	ctx := context.Background()
	db := &Sqlite{}
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
	return db, NewScheduleStore(db), NewNotificationStore(db)
}

// TestSoftDeletePreservesHistory 验证删除日程时已发生提醒仍可查询。
func TestSoftDeletePreservesHistory(t *testing.T) {
	_, schedules, notifications := newMigratedStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
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
	if err := schedules.DeleteScheduleByID(ctx, schedule.ID); err != nil {
		t.Fatalf("软删除日程: %v", err)
	}
	stored, err := schedules.GetScheduleByID(ctx, schedule.ID, model.QueryOption{IncludeDeleted: true})
	if err != nil || stored == nil || !stored.Deleted {
		t.Fatalf("已删除日程仍应可按 ID 查询: %#v, %v", stored, err)
	}
	if _, err := schedules.GetScheduleByID(ctx, schedule.ID, model.QueryOption{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("普通查询应隐藏已删除日程，得到 %v", err)
	}
	if _, err := schedules.GetScheduleByID(ctx, 0, model.QueryOption{}); err == nil {
		t.Fatal("按 ID 查询必须提供有效的日程 ID")
	}
	visible, err := schedules.GetAllSchedule(ctx, model.QueryOption{})
	if err != nil || len(visible) != 0 {
		t.Fatalf("普通列表应隐藏日程: %#v, %v", visible, err)
	}
	history, err := notifications.GetNotificationHistoryByScheduleID(ctx, schedule.ID)
	if err != nil || len(history) != 2 {
		t.Fatalf("提醒历史应保留两条记录: %#v, %v", history, err)
	}
	storedPast, err := notifications.GetNotificationByID(ctx, past.ID)
	if err != nil || storedPast.Deleted || storedPast.AcknowledgedAt == nil || storedPast.Status != model.NotifySent {
		t.Fatalf("已发生提醒状态丢失: %#v, %v", storedPast, err)
	}
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
	notifyAt := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
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
	if err := notifications.ConfirmNotification(ctx, first.ID, notifyAt); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("投递前确认应失败，得到 %v", err)
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
	_, err = schedules.GetScheduleByID(ctx, id, model.QueryOption{})
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
		Single: &model.Single{NotifyTime: time.Now().UTC().Add(time.Hour)},
	})
	if err != nil {
		t.Fatalf("创建日程: %v", err)
	}
	name, scheduleType, limit, offset := "查询", model.SingleSchedule, 1, 0
	query := model.QueryOption{
		ID: &schedule.ID, Name: &name, Type: &scheduleType,
		Limit: &limit, Offset: &offset,
	}
	got, err := schedules.GetScheduleByID(ctx, schedule.ID, query)
	if err != nil || got == nil || got.ID != schedule.ID {
		t.Fatalf("组合条件应返回日程: %#v, %v", got, err)
	}
	otherID := schedule.ID + 1
	query.ID = &otherID
	if _, err := schedules.GetScheduleByID(ctx, schedule.ID, query); !errors.Is(err, ErrNotFound) {
		t.Fatalf("可选 ID 条件不匹配应返回 ErrNotFound: %v", err)
	}
	query.ID = &schedule.ID
	name = "不匹配"
	if _, err := schedules.GetScheduleByID(ctx, schedule.ID, query); !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), `name="不匹配"`) {
		t.Fatalf("名称条件不匹配应返回 ErrNotFound: %v", err)
	}
	name, offset = "查询", 1
	if _, err := schedules.GetScheduleByID(ctx, schedule.ID, query); !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "offset=1") {
		t.Fatalf("offset 应参与查询: %v", err)
	}
	offset, limit = 0, 0
	if _, err := schedules.GetScheduleByID(ctx, schedule.ID, query); err == nil || !strings.Contains(err.Error(), "limit=0") {
		t.Fatalf("非法 limit 应返回带参数的错误: %v", err)
	}
}

// TestGetAllScheduleAcceptsOptionalFilters 验证列表查询没有必填参数且会组合已设置条件。
func TestGetAllScheduleAcceptsOptionalFilters(t *testing.T) {
	_, schedules, _ := newMigratedStore(t)
	ctx := context.Background()
	first, err := schedules.CreateSchedule(ctx, model.Schedule{
		Name: "早间会议", Type: model.SingleSchedule,
		Single: &model.Single{NotifyTime: time.Now().UTC().Add(time.Hour)},
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
	all, err := schedules.GetAllSchedule(ctx, model.QueryOption{})
	if err != nil || len(all) != 2 {
		t.Fatalf("空查询应返回所有可见日程: %#v, %v", all, err)
	}
	name, scheduleType, limit, offset := "晚间", model.PeriodSchedule, 1, 0
	filtered, err := schedules.GetAllSchedule(ctx, model.QueryOption{
		ID: &second.ID, Name: &name, Type: &scheduleType,
		Limit: &limit, Offset: &offset,
	})
	if err != nil || len(filtered) != 1 || filtered[0].ID != second.ID {
		t.Fatalf("组合条件应返回周期日程: %#v, %v", filtered, err)
	}
	byID, err := schedules.GetAllSchedule(ctx, model.QueryOption{ID: &first.ID})
	if err != nil || len(byID) != 1 || byID[0].ID != first.ID {
		t.Fatalf("仅 ID 条件应返回指定日程: %#v, %v", byID, err)
	}
	byType, err := schedules.GetAllSchedule(ctx, model.QueryOption{Type: &scheduleType})
	if err != nil || len(byType) != 1 || byType[0].ID != second.ID {
		t.Fatalf("仅类型条件应返回周期日程: %#v, %v", byType, err)
	}
	offset = 1
	page, err := schedules.GetAllSchedule(ctx, model.QueryOption{Limit: &limit, Offset: &offset})
	if err != nil || len(page) != 1 || page[0].ID != first.ID {
		t.Fatalf("分页条件应跳过第一条日程: %#v, %v", page, err)
	}
	name = "不存在"
	filtered, err = schedules.GetAllSchedule(ctx, model.QueryOption{Name: &name})
	if err != nil || len(filtered) != 0 {
		t.Fatalf("名称条件应参与列表过滤: %#v, %v", filtered, err)
	}
}
