package app

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/model"
	"github.com/dengweitian0-svg/TimeCue/internal/service/notification"
	"github.com/dengweitian0-svg/TimeCue/internal/service/recovery"
)

type emptyRecoveryRepo struct{}

func (emptyRecoveryRepo) RecoveryStart(context.Context, time.Time) (time.Time, error) {
	return time.Time{}, nil
}
func (emptyRecoveryRepo) RecoverNotifications(context.Context, model.RecoveryWindow, model.PrepareRecoveryVersion) (*model.RecoveryBatch, error) {
	return nil, fmt.Errorf("empty history must not submit recovery batches")
}
func (emptyRecoveryRepo) CountRecoveryMembers(context.Context, []int, time.Time) (int, error) {
	return 0, nil
}

func (emptyRecoveryRepo) CountUnacknowledgedRecoveryMembers(context.Context, []int, time.Time) (int, error) {
	return 0, nil
}

// TestRecoveryRunCancellation 验证空历史不提交批次且取消立即结束等待。
func TestRecoveryRunCancellation(t *testing.T) {
	if err := (&App{}).RunRecovery(context.Background()); err == nil {
		t.Fatal("未配置恢复应返回错误")
	}
	if worked, err := (&App{}).RecoverStep(context.Background()); err == nil || worked {
		t.Fatal("未配置恢复时单步调用应返回错误", worked, err)
	}
	entered := make(chan struct{})
	reported := make(chan error, 1)
	r, err := recovery.NewService(emptyRecoveryRepo{}, notification.SchedulerOptions{
		Wait: func(ctx context.Context, _ time.Duration, _ <-chan struct{}) error {
			close(entered)
			<-ctx.Done()
			return ctx.Err()
		},
		OnError: func(err error) { reported <- err },
	})
	if err != nil {
		t.Fatal(err)
	}
	d, _ := notification.NewDeliveryService(deliveryTestRepo{}, nil)
	app := &App{Recovery: r, Delivery: d}
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() { finished <- app.RunRecovery(ctx) }()
	<-entered
	cancel()
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatal("未响应取消", err)
	}
	select {
	case err := <-reported:
		t.Fatal("正常退出被记录成恢复故障", err)
	default:
	}
}

// TestRecoveryWindowsUseTriggerPoints 验证亚分钟空隙不产生轮次且不会截丢真实触发。
func TestRecoveryWindowsUseTriggerPoints(t *testing.T) {
	m := time.Date(2030, 10, 9, 9, 0, 0, 0, model.BeijingLocation())
	values := recovery.MergeRecoveryWindows([]model.NotificationWindow{
		{Start: m.Add(-90 * time.Second), End: m.Add(-30 * time.Second)},
		{Start: m.Add(-20 * time.Second), End: m.Add(-20*time.Second + time.Nanosecond)},
		{Start: m, End: m.Add(time.Second)},
	}, m)
	if len(values) != 1 || !values[0].Start.Equal(m.Add(-time.Minute)) || !values[0].End.Equal(m) {
		t.Fatal("触发点归属错误或空隙制造了轮次", values)
	}
	values = recovery.MergeRecoveryWindows([]model.NotificationWindow{{Start: m.Add(-time.Nanosecond), End: m.Add(time.Nanosecond)}}, m.Add(time.Minute))
	if len(values) != 1 || !values[0].Start.Equal(m) || !values[0].End.Equal(m.Add(time.Minute)) {
		t.Fatal("秒级窗口跨整分钟时丢失触发", values)
	}
}

func TestRecoveryStepKeepsCurrentMinuteGroup(t *testing.T) {
	minute := time.Date(2030, 10, 9, 10, 0, 0, 0, model.BeijingLocation())
	now := minute.Add(20 * time.Second)
	scheduler, err := notification.NewScheduler(dueSchedulerRepo{at: minute}, notification.SchedulerOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	delivery, err := notification.NewDeliveryService(deliveryTestRepo{}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	recoveryService, err := recovery.NewService(emptyRecoveryRepo{}, notification.SchedulerOptions{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	application := &App{Scheduler: scheduler, Delivery: delivery, Recovery: recoveryService}
	if _, err := application.RecoverStep(context.Background()); err != nil {
		t.Fatal(err)
	}
	claims, err := application.ClaimDelivery(context.Background(), 20)
	if err != nil || len(claims) != 1 || !claims[0].NotifyTime.Equal(minute) {
		t.Fatalf("当前分钟任务被恢复准备丢弃: %#v %v", claims, err)
	}
}
