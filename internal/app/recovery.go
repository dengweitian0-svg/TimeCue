package app

import (
	"context"
	"fmt"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/model"
	"github.com/dengweitian0-svg/TimeCue/internal/service/notification"
	"github.com/dengweitian0-svg/TimeCue/internal/service/recovery"
)

func (a *App) recoverStepLocked(ctx context.Context) (bool, error) {
	a.syncServices()
	if a.Recovery == nil {
		return false, nil
	}
	now := a.Recovery.Now().In(model.BeijingLocation())
	var windows []model.NotificationWindow
	if a.Scheduler != nil {
		windows = a.Scheduler.TakeRecoveryWindows(now.Truncate(time.Minute))
	}
	return a.Recovery.RunStep(ctx, now, windows, a.notificationService)
}

func (a *App) prepareRecoveryLocked(ctx context.Context, now time.Time) error {
	a.syncServices()
	if a.Recovery == nil {
		return nil
	}
	var windows []model.NotificationWindow
	if a.Scheduler != nil {
		windows = a.Scheduler.TakeRecoveryWindows(now.Truncate(time.Minute))
	}
	return a.Recovery.Prepare(ctx, now, windows, a.notificationService)
}

// RecoverStep 提交一个恢复批次，便于运行时与确定性调用复用。
func (a *App) RecoverStep(ctx context.Context) (bool, error) {
	return a.recoverStep(ctx, ctx)
}

func (a *App) recoverStep(polling, work context.Context) (bool, error) {
	if err := a.operations.LockContext(polling); err != nil {
		return false, err
	}
	defer a.operations.Unlock()
	if a.Recovery == nil {
		return false, fmt.Errorf("recovery is not configured")
	}
	return a.recoverStepLocked(work)
}

// RunRecovery 持续整理历史遗漏，不阻塞实时调度。
func (a *App) RunRecovery(ctx context.Context) error {
	return a.runRecovery(ctx, ctx)
}

func (a *App) runRecovery(polling, work context.Context) error {
	if a.Recovery == nil {
		return fmt.Errorf("recovery is not configured")
	}
	for {
		if err := polling.Err(); err != nil {
			return err
		}
		worked, err := a.recoverStep(polling, work)
		if polling.Err() != nil {
			return polling.Err()
		}
		delay := time.Second
		if worked && err == nil {
			delay = time.Millisecond
		}
		if err != nil {
			a.Recovery.ReportError(err)
		}
		if err := a.Recovery.Wait(polling, delay); err != nil {
			return err
		}
	}
}

// RecoverySnapshot 返回只读恢复进度。
func (a *App) RecoverySnapshot(ctx context.Context, now time.Time) (*model.RecoveryView, error) {
	if err := a.operations.LockContext(ctx); err != nil {
		return nil, err
	}
	defer a.operations.Unlock()
	if a.Recovery == nil {
		return nil, nil
	}
	a.syncServices()
	return a.Recovery.Snapshot(ctx, a.notificationService, now)
}

// configureRecovery 按存储端口创建后台恢复器。
func (a *App) configureRecovery(options notification.SchedulerOptions) error {
	if a.Recovery != nil {
		return nil
	}
	repo, ok := a.Notify.(recovery.Repo)
	if !ok {
		return fmt.Errorf("notification repo does not support recovery")
	}
	value, err := recovery.NewService(repo, options)
	if err != nil {
		return err
	}
	a.Recovery = value
	return nil
}
