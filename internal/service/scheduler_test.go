package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/model"
)

type emptySchedulerRepo struct {
	windows      []model.NotificationWindow
	scanFailures int
}

func (r *emptySchedulerRepo) PrepareNotifications(_ context.Context, w model.NotificationWindow, _ model.PrepareNotificationWindow) (*model.NotificationBatch, error) {
	r.windows = append(r.windows, w)
	if !w.FreezeDue && r.scanFailures > 0 {
		r.scanFailures--
		return nil, errors.New("scan failed")
	}
	return &model.NotificationBatch{}, nil
}

// TestRollbackScanFailureRetries 验证回拨后的扫描失败不会等待旧时钟位置。
func TestRollbackScanFailureRetries(t *testing.T) {
	now := beijing(2030, 10, 9, 10, 0)
	repo := &emptySchedulerRepo{}
	scheduler, _ := NewScheduler(repo, SchedulerOptions{Now: func() time.Time { return now }})
	if err := scheduler.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(-time.Hour)
	repo.scanFailures = 1
	if err := scheduler.Step(context.Background()); err == nil {
		t.Fatal("应返回扫描错误")
	}
	now = now.Add(time.Second)
	if err := scheduler.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	scans := 0
	for _, w := range repo.windows {
		if !w.FreezeDue {
			scans++
		}
	}
	if scans != 3 {
		t.Fatalf("回拨扫描未重试: %d", scans)
	}
}

// TestSchedulerRunUsesScanCadenceAndCancels 验证真实循环的定时决策与取消。
func TestSchedulerRunUsesScanCadenceAndCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	now := beijing(2030, 10, 9, 8, 59).Add(10 * time.Second)
	repo := &emptySchedulerRepo{}
	waits := 0
	scheduler, err := NewScheduler(repo, SchedulerOptions{Now: func() time.Time { return now }, Wait: func(ctx context.Context, delay time.Duration, _ <-chan struct{}) error {
		waits++
		if waits == 3 {
			cancel()
			return ctx.Err()
		}
		now = now.Add(delay)
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err = scheduler.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("取消未终止循环: %v", err)
	}
	var scans, minutes []time.Time
	for _, w := range repo.windows {
		if w.FreezeDue {
			minutes = append(minutes, w.Start)
		} else {
			scans = append(scans, w.AsOf)
		}
	}
	if len(scans) != 2 || scans[1].Sub(scans[0]) != 30*time.Second {
		t.Fatalf("扫描节奏错误: %v", scans)
	}
	if len(minutes) != 2 || minutes[1].Minute() != 0 || minutes[1].Hour() != 9 {
		t.Fatalf("到点没有独立核验: %v", minutes)
	}
}

// TestPrepareNotificationVersionBoundaries 验证版本端点、重复规则与旧秒级排除。
func TestPrepareNotificationVersionBoundaries(t *testing.T) {
	minute := beijing(2030, 10, 9, 9, 0)
	old := periodSchedule(true, "0 9 * * *", "0 9 * * *")
	old.ID, old.Name = 1, "旧文"
	newRule := periodSchedule(true, "0 9 * * *")
	newRule.ID, newRule.Name = 1, "新文"
	legacy := model.Schedule{ID: 2, Name: "秒级", Type: model.SingleSchedule, Single: &model.Single{NotifyTime: minute.Add(time.Second)}}
	versions := []model.ScheduleVersion{{Schedule: old, From: minute.Add(-time.Hour), Until: &minute}, {Schedule: newRule, From: minute}, {Schedule: legacy, From: minute.Add(-time.Hour)}}
	items, err := PrepareNotificationCandidates(versions, minute, minute.Add(time.Minute))
	if err != nil || len(items) != 1 || items[0].Name != "旧文" {
		t.Fatalf("版本边界或去重错误: %#v %v", items, err)
	}
}
