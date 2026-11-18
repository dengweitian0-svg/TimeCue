package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	application "github.com/dengweitian0-svg/TimeCue/internal/app"
	"github.com/dengweitian0-svg/TimeCue/internal/model"
)

// TestVersionedDeleteReplaysReceiptAndPreservesHistory 验证正式删除的提醒联动与原回执复用。
func TestVersionedDeleteReplaysReceiptAndPreservesHistory(t *testing.T) {
	_, schedules, notifications := newMigratedStore(t)
	ctx := context.Background()
	now := time.Date(2030, 10, 28, 9, 0, 0, 0, model.BeijingLocation())
	schedules.now = func() time.Time { return now }
	app := &application.App{
		Schedule: schedules,
		Notify:   notifications,
	}
	created, err := app.WriteSchedule(ctx, rule("待删除", "0 10 * * *", true, 0), 0, "")
	if err != nil {
		t.Fatal(err)
	}
	id := created.Schedule.ID
	// 无回执的核对必须只读，不能先删除再判断是否命中。
	if receipt, err := app.ReadScheduleDeleteReceipt(ctx, id, 1, "delete"); err != nil || receipt != nil {
		t.Fatalf("未执行却有回执: %+v %v", receipt, err)
	}
	if _, err := app.GetSchedule(ctx, id); err != nil {
		t.Fatalf("只读核对删除了目标: %v", err)
	}
	now = now.Add(2 * time.Hour)
	deleted, err := app.DeleteScheduleVersion(ctx, id, 1, "delete")
	if err != nil || deleted.Before == nil || !deleted.Schedule.Deleted || deleted.Schedule.Revision != 2 {
		t.Fatalf("删除失败: %+v %v", deleted, err)
	}
	if _, err := app.GetSchedule(ctx, id); !errors.Is(err, application.ErrScheduleNotFound) {
		t.Fatalf("软删除仍可见: %v", err)
	}
	history, err := notifications.GetNotificationByID(ctx, created.Notification.ID)
	if err != nil || !history.DeliveryStopped || history.Deleted || !history.SnapshotTaken || history.NameSnapshot == nil || *history.NameSnapshot != "待删除" {
		t.Fatalf("历史提醒未保留: %+v %v", history, err)
	}
	for _, lookup := range []bool{false, true} {
		var receipt *model.ScheduleWrite
		if lookup {
			receipt, err = app.ReadScheduleDeleteReceipt(ctx, id, 1, "delete")
		} else {
			receipt, err = app.DeleteScheduleVersion(ctx, id, 1, "delete")
		}
		if err != nil || receipt == nil || !receipt.Replayed || receipt.Before != nil || receipt.Schedule.Revision != 2 || !receipt.EffectiveAt.Equal(deleted.EffectiveAt) {
			t.Fatalf("未复用原回执: %+v %v", receipt, err)
		}
	}
	if _, err := app.DeleteScheduleVersion(ctx, id, 2, "delete"); !errors.Is(err, model.ErrIdempotencyConflict) {
		t.Fatalf("同键不同请求未拒绝: %v", err)
	}
	if _, err := app.DeleteScheduleVersion(ctx, id, 1, "another"); !errors.Is(err, application.ErrScheduleNotFound) {
		t.Fatalf("新删除未报告不存在: %v", err)
	}
}

// TestDeleteReceiptFailureRollsBackScheduleAndNotifications 验证回执落库失败时删除与未来提醒全部回滚。
func TestDeleteReceiptFailureRollsBackScheduleAndNotifications(t *testing.T) {
	db, schedules, notifications := newMigratedStore(t)
	ctx := context.Background()
	app := &application.App{
		Schedule: schedules,
		Notify:   notifications,
	}
	created, err := app.WriteSchedule(ctx, rule("原日程", "0 10 * * *", true, 0), 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`CREATE TRIGGER reject_delete_receipt BEFORE INSERT ON schedule_write_receipts BEGIN SELECT RAISE(ABORT, 'receipt failure'); END`); err != nil {
		t.Fatal(err)
	}
	if result, err := app.DeleteScheduleVersion(ctx, created.Schedule.ID, 1, "rollback-delete"); err == nil || result != nil {
		t.Fatalf("回执失败仍返回删除: %+v %v", result, err)
	}
	current, err := app.GetSchedule(ctx, created.Schedule.ID)
	if err != nil || current.Revision != 1 || current.Deleted {
		t.Fatalf("删除未回滚: %+v %v", current, err)
	}
	future, err := notifications.GetNotificationByID(ctx, created.Notification.ID)
	if err != nil || future.Deleted || future.DeliveryStopped {
		t.Fatalf("提醒未回滚: %+v %v", future, err)
	}
	if receipt, err := app.ReadScheduleDeleteReceipt(ctx, created.Schedule.ID, 1, "rollback-delete"); err != nil || receipt != nil {
		t.Fatalf("回滚残留回执: %+v %v", receipt, err)
	}
	if _, err := db.db.Exec(`DROP TRIGGER reject_delete_receipt`); err != nil {
		t.Fatal(err)
	}
	deleted, err := app.DeleteScheduleVersion(ctx, created.Schedule.ID, 1, "rollback-delete")
	if err != nil || deleted.Schedule.Revision != 2 {
		t.Fatalf("回滚后不能重试: %+v %v", deleted, err)
	}
	future, err = notifications.GetNotificationByID(ctx, created.Notification.ID)
	if err != nil || !future.Deleted || !future.DeliveryStopped {
		t.Fatalf("未来提醒未撤销: %+v %v", future, err)
	}
	// 所有日程更新都被拒绝时，未命中的只读回执核对仍应成功返回空值。
	if _, err := db.db.Exec(`CREATE TRIGGER reject_update BEFORE UPDATE ON schedules BEGIN SELECT RAISE(ABORT, 'read only'); END`); err != nil {
		t.Fatal(err)
	}
	if receipt, err := app.ReadScheduleDeleteReceipt(ctx, created.Schedule.ID, 1, "unknown"); err != nil || receipt != nil {
		t.Fatalf("核对入口尝试写入: %+v %v", receipt, err)
	}
	if receipt, err := app.ReadScheduleDeleteReceipt(ctx, created.Schedule.ID, 1, "rollback-delete"); err != nil || receipt == nil || receipt.Schedule.Revision != 2 {
		t.Fatalf("只读入口不能读取已提交回执: %+v %v", receipt, err)
	}
}

// TestDeleteCompetesWithUpdateAcrossFormalApps 验证两个正式入口竞争同一版本时只有一个生效。
func TestDeleteCompetesWithUpdateAcrossFormalApps(t *testing.T) {
	for range 8 {
		_, schedules, notifications := newMigratedStore(t)
		ctx := context.Background()
		deleter := &application.App{
			Schedule: schedules,
			Notify:   notifications,
		}
		editor := &application.App{
			Schedule: schedules,
			Notify:   notifications,
		}
		created, err := editor.WriteSchedule(ctx, rule("竞争", "0 10 * * *", true, 0), 0, "")
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		var deleteErr, editErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, deleteErr = deleter.DeleteScheduleVersion(ctx, created.Schedule.ID, 1, "race-delete")
		}()
		go func() {
			defer wg.Done()
			_, editErr = editor.WriteSchedule(ctx, rule("更新", "0 11 * * *", true, created.Schedule.ID), 1, "")
		}()
		wg.Wait()
		if editErr == nil {
			if !errors.Is(deleteErr, model.ErrScheduleConflict) {
				t.Fatalf("修改后旧版本仍可删除: %v", deleteErr)
			}
			if receipt, err := deleter.ReadScheduleDeleteReceipt(ctx, created.Schedule.ID, 1, "race-delete"); err != nil || receipt != nil {
				t.Fatalf("冲突留下回执: %+v %v", receipt, err)
			}
		} else if deleteErr != nil || !errors.Is(editErr, application.ErrScheduleNotFound) {
			t.Fatalf("竞争裁决错误: delete=%v edit=%v", deleteErr, editErr)
		}
	}
}
