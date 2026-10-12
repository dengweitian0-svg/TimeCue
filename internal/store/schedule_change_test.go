package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/model"
	"github.com/dengweitian0-svg/TimeCue/internal/service"
)

func rule(name, cron string, open bool, id int) model.Schedule {
	return model.Schedule{ID: id, Name: name, Type: model.PeriodSchedule, Period: &model.Period{Open: open, Cron: []string{cron}}}
}

// TestRuleChangesPreserveHistoryAndPauseBoundary 覆盖设计中的 13:00 → 17:00 与暂停、重启。
func TestRuleChangesPreserveHistoryAndPauseBoundary(t *testing.T) {
	db, schedules, notifications := newMigratedStore(t)
	ctx := context.Background()
	now := time.Date(2030, 10, 8, 11, 0, 0, 0, model.BeijingLocation())
	schedules.now = func() time.Time { return now }
	app := &service.App{Schedule: schedules, Notify: notifications}
	created, err := app.WriteSchedule(ctx, rule("旧名称", "0 13 * * *", true, 0), 0, "create")
	if err != nil {
		t.Fatal(err)
	}
	oldID := created.Notification.ID
	if err = notifications.SetDeliveryResult(ctx, oldID, model.NotifyFailed); err != nil {
		t.Fatal(err)
	}
	now = now.Add(3*time.Hour + 32*time.Minute)
	changed, err := app.WriteSchedule(ctx, rule("新名称", "0 17 * * *", true, created.Schedule.ID), 1, "edit")
	if err != nil || changed.Schedule.Revision != 2 || changed.Notification.NotifyTime.Hour() != 17 {
		t.Fatalf("更新失败: %#v %v", changed, err)
	}
	if changed.Before == nil || changed.Before.Revision != 1 || changed.Before.Name != "旧名称" || changed.Before.Period.Cron[0] != "0 13 * * *" || changed.Replayed {
		t.Fatalf("事务内旧状态丢失: %#v", changed)
	}
	var receipt string
	if err = db.db.QueryRow(`SELECT result_json FROM schedule_write_receipts WHERE operation_key = 'edit'`).Scan(&receipt); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(receipt, `"Before"`) || strings.Contains(receipt, `"Replayed"`) {
		t.Fatalf("调用元数据进入原回执: %s", receipt)
	}
	state, err := app.QueryScheduleState(ctx, "", now)
	if err != nil || len(state.Occurrences.Items) != 2 || state.Occurrences.Items[0].Name != "旧名称" || state.Occurrences.Items[1].Name != "新名称" {
		t.Fatalf("旧记录或未来投影错误: %#v %v", state, err)
	}
	replay, err := app.WriteSchedule(ctx, rule("新名称", "0 17 * * *", true, created.Schedule.ID), 1, "edit")
	if err != nil || !replay.Replayed || replay.Before != nil || replay.Schedule.Revision != 2 || !replay.EffectiveAt.Equal(changed.EffectiveAt) {
		t.Fatalf("幂等重放失败: %#v %v", replay, err)
	}
	if _, err = app.WriteSchedule(ctx, rule("另一个草稿", "0 18 * * *", true, created.Schedule.ID), 1, "other"); !errors.Is(err, model.ErrScheduleConflict) {
		t.Fatalf("过期草稿未拒绝: %v", err)
	}
	if _, err = app.WriteSchedule(ctx, rule("不同内容", "0 17 * * *", true, created.Schedule.ID), 2, "edit"); !errors.Is(err, model.ErrIdempotencyConflict) {
		t.Fatalf("复用标识未拒绝: %v", err)
	}
	paused, err := app.WriteSchedule(ctx, rule("新名称", "0 17 * * *", false, created.Schedule.ID), 2, "pause")
	if err != nil || paused.Notification != nil {
		t.Fatalf("暂停产生提醒: %#v %v", paused, err)
	}
	now = time.Date(2030, 10, 10, 17, 0, 0, 0, model.BeijingLocation())
	resumed, err := app.WriteSchedule(ctx, rule("新名称", "0 17 * * *", true, created.Schedule.ID), 3, "resume")
	if err != nil || !resumed.Notification.NotifyTime.Equal(now.Add(24*time.Hour)) {
		t.Fatalf("恢复没有严格从未来继续: %#v %v", resumed, err)
	}
	history, err := notifications.GetNotificationByID(ctx, oldID)
	if err != nil || !history.DeliveryStopped || history.Status != model.NotifyFailed || *history.NameSnapshot != "旧名称" {
		t.Fatalf("旧投递被重新激活: %#v %v", history, err)
	}
	var intervals int
	if err = db.db.QueryRow(`SELECT count(*) FROM schedule_versions WHERE schedule_id = ?`, created.Schedule.ID).Scan(&intervals); err != nil || intervals != 4 {
		t.Fatalf("启停区间丢失: %d %v", intervals, err)
	}
	var duringPause int
	if err = db.db.QueryRow(`SELECT count(*) FROM notifications WHERE schedule_id = ? AND deleted = 0 AND notify_time > ? AND notify_time <= ?`, created.Schedule.ID, paused.EffectiveAt.UnixNano(), resumed.EffectiveAt.UnixNano()).Scan(&duringPause); err != nil || duringPause != 0 {
		t.Fatalf("暂停区间有有效提醒: %d %v", duringPause, err)
	}
	var file string
	var seq int
	var databaseName string
	if err = db.db.QueryRow(`PRAGMA database_list`).Scan(&seq, &databaseName, &file); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	if err = db.Connect(ctx, filepath.Clean(file)); err != nil {
		t.Fatal(err)
	}
	due, err := notifications.GetNotificationByStartTime(ctx, created.EffectiveAt, now)
	if err != nil || len(due) != 0 {
		t.Fatalf("重启重新激活旧投递: %#v %v", due, err)
	}
	if err = app.DeleteSchedule(ctx, created.Schedule.ID); err != nil {
		t.Fatal(err)
	}
	detail, err := app.GetNotification(ctx, oldID, now)
	if err != nil || detail.Editable || detail.Name != "旧名称" {
		t.Fatalf("删除隐藏历史: %#v %v", detail, err)
	}
	if _, err = app.AcknowledgeNotifications(ctx, []int{oldID}, now); err != nil {
		t.Fatal(err)
	}
	if _, err = app.WriteSchedule(ctx, rule("恢复删除", "0 17 * * *", true, created.Schedule.ID), 5, "invalid"); !errors.Is(err, service.ErrScheduleNotFound) {
		t.Fatalf("删除可恢复: %v", err)
	}
}

// TestConcurrentRuleUpdatesAndRollback 验证并发修改只成功一次，写入失败不留下半次变更。
func TestConcurrentRuleUpdatesAndRollback(t *testing.T) {
	db, schedules, notifications := newMigratedStore(t)
	ctx := context.Background()
	app := &service.App{Schedule: schedules, Notify: notifications}
	created, err := app.WriteSchedule(ctx, rule("初始", "0 13 * * *", true, 0), 0, "create")
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	for _, name := range []string{"甲", "乙"} {
		go func(name string) {
			_, e := app.WriteSchedule(ctx, rule(name, "0 17 * * *", true, created.Schedule.ID), 1, name)
			results <- e
		}(name)
	}
	success, conflict := 0, 0
	for range 2 {
		e := <-results
		if e == nil {
			success++
		} else if errors.Is(e, model.ErrScheduleConflict) {
			conflict++
		} else {
			t.Fatal(e)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("并发覆盖: 成功=%d 冲突=%d", success, conflict)
	}
	if _, err = db.db.Exec(`CREATE TRIGGER reject_future BEFORE INSERT ON notifications BEGIN SELECT RAISE(ABORT, 'test rollback'); END`); err != nil {
		t.Fatal(err)
	}
	if result, err := app.WriteSchedule(ctx, rule("回滚", "0 19 * * *", true, created.Schedule.ID), 2, "rollback"); err == nil || result != nil {
		t.Fatal("应触发写入失败")
	}
	after, err := schedules.GetScheduleByID(ctx, created.Schedule.ID)
	if err != nil || after.Revision != 2 || after.Name == "回滚" {
		t.Fatalf("半次更新残留: %#v %v", after, err)
	}
	var receipts int
	if err = db.db.QueryRow(`SELECT count(*) FROM schedule_write_receipts WHERE operation_key = 'rollback'`).Scan(&receipts); err != nil || receipts != 0 {
		t.Fatal("失败留下幂等回执")
	}
}

// TestExpiredSingleAndReusedFuture 验证已到期单次不可再安排，撤销的未来唯一键可合法复用。
func TestExpiredSingleAndReusedFuture(t *testing.T) {
	_, schedules, notifications := newMigratedStore(t)
	ctx := context.Background()
	now := time.Date(2030, 10, 8, 11, 0, 0, 0, model.BeijingLocation())
	schedules.now = func() time.Time { return now }
	app := &service.App{Schedule: schedules, Notify: notifications}
	single := model.Schedule{Name: "单次", Type: model.SingleSchedule, Single: &model.Single{NotifyTime: now.Add(time.Hour)}}
	created, err := app.WriteSchedule(ctx, single, 0, "single")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Hour)
	single.ID = created.Schedule.ID
	single.Name = "说明变更"
	if _, err = app.WriteSchedule(ctx, single, 1, "metadata"); err != nil {
		t.Fatal(err)
	}
	single.Single = &model.Single{NotifyTime: now.Add(time.Hour)}
	if _, err = app.WriteSchedule(ctx, single, 2, "rearrange"); !errors.Is(err, service.ErrInvalidSchedule) {
		t.Fatalf("已到期单次可再安排: %v", err)
	}
	first, err := app.WriteSchedule(ctx, rule("周期", "0 17 * * *", true, 0), 0, "period")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = app.WriteSchedule(ctx, rule("周期", "0 18 * * *", true, first.Schedule.ID), 1, "to18"); err != nil {
		t.Fatal(err)
	}
	reused, err := app.WriteSchedule(ctx, rule("周期", "0 17 * * *", true, first.Schedule.ID), 2, "to17")
	if err != nil || reused.Notification.ID != first.Notification.ID || reused.Notification.Deleted {
		t.Fatalf("未来唯一键未正确复用: %#v %v", reused, err)
	}
}

// TestLegacySecondsCannotBeSavedAgain 保留旧事实可读性，但新版本禁止保存或执行秒级规则。
func TestLegacySecondsCannotBeSavedAgain(t *testing.T) {
	db, schedules, notifications := newMigratedStore(t)
	ctx := context.Background()
	now := time.Date(2030, 10, 8, 11, 0, 0, 0, model.BeijingLocation())
	schedules.now = func() time.Time { return now }
	at := now.Add(time.Hour + 5*time.Second + 123*time.Millisecond)
	// 用 SQL 模拟旧版数据；当前正式创建接口不再允许此输入。
	if _, err := db.db.Exec(`INSERT INTO schedules (id, name, type, single_notify_time, created_at, updated_at) VALUES (1, '旧数据', 'single', ?, ?, ?)`, at.UnixNano(), now.UnixNano(), now.UnixNano()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`INSERT INTO notifications (schedule_id, notify_time) VALUES (1, ?)`, at.UnixNano()); err != nil {
		t.Fatal(err)
	}
	app := &service.App{Schedule: schedules, Notify: notifications}
	if _, err := app.QueryScheduleState(ctx, "", now); err != nil {
		t.Fatalf("旧事实查询失败: %v", err)
	}
	history, err := app.QueryScheduleState(ctx, "", at.Add(time.Minute))
	if err != nil || len(history.Occurrences.Items) != 1 || !history.Occurrences.Items[0].NotifyTime.Equal(at) {
		t.Fatalf("旧事实被删除或取整: %#v %v", history, err)
	}
	old, err := schedules.GetScheduleByID(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	old.Name = "只改名称"
	if _, err = app.WriteSchedule(ctx, *old, 1, "legacy-metadata"); !errors.Is(err, service.ErrInvalidSchedule) {
		t.Fatalf("新版本原样接受了秒级规则: %v", err)
	}
	old.Single = &model.Single{NotifyTime: at.Truncate(time.Minute)}
	changed, err := app.WriteSchedule(ctx, *old, 1, "minute-rule")
	if err != nil || changed.Schedule.Single.NotifyTime.Second() != 0 {
		t.Fatalf("分钟级未来规则无法保存: %#v %v", changed, err)
	}
}

func TestSingleScheduleUnixNanoBoundaryAndOverflow(t *testing.T) {
	db, schedules, notifications := newMigratedStore(t)
	ctx := context.Background()
	schedules.now = func() time.Time { return time.Date(2200, 1, 1, 0, 0, 0, 0, model.BeijingLocation()) }
	app := &service.App{Schedule: schedules, Notify: notifications}
	maxUnixNano := time.Unix(0, 1<<63-1)
	lastSupportedMinute := maxUnixNano.Truncate(time.Minute)
	if err := model.ValidateUnixNanoTime(lastSupportedMinute); err != nil {
		t.Fatalf("最后一个可存储分钟应有效: %v", err)
	}
	created, err := app.WriteSchedule(ctx, model.Schedule{
		Name: "边界时间", Type: model.SingleSchedule, Single: &model.Single{NotifyTime: lastSupportedMinute},
	}, 0, "boundary")
	if err != nil {
		t.Fatalf("保存边界分钟: %v", err)
	}
	var stored int64
	if err = db.db.QueryRowContext(ctx, `SELECT single_notify_time FROM schedules WHERE id = ?`, created.Schedule.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != lastSupportedMinute.UnixNano() {
		t.Fatalf("边界分钟持久化值错误: got %d want %d", stored, lastSupportedMinute.UnixNano())
	}

	overflow := time.Date(9999, 1, 1, 0, 0, 0, 0, model.BeijingLocation())
	if _, err = app.WriteSchedule(ctx, model.Schedule{
		Name: "溢出时间", Type: model.SingleSchedule, Single: &model.Single{NotifyTime: overflow},
	}, 0, "overflow"); !errors.Is(err, service.ErrInvalidSchedule) {
		t.Fatalf("超出 Unix 纳秒范围的单次应被拒绝: %v", err)
	}
	var scheduleCount, receiptCount int
	if err = db.db.QueryRowContext(ctx, `SELECT count(*) FROM schedules WHERE name = '溢出时间'`).Scan(&scheduleCount); err != nil {
		t.Fatal(err)
	}
	if err = db.db.QueryRowContext(ctx, `SELECT count(*) FROM schedule_write_receipts WHERE operation_key = 'overflow'`).Scan(&receiptCount); err != nil {
		t.Fatal(err)
	}
	if scheduleCount != 0 || receiptCount != 0 {
		t.Fatalf("溢出单次不应落库: schedules=%d receipts=%d", scheduleCount, receiptCount)
	}
}
