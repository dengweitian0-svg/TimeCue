package app

import (
	"context"
	"fmt"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/service/notification"
)

// Lifecycle 提供 App 就绪、全局退出和 Loop 实际退出的协调端口。
type Lifecycle struct {
	Ready           chan<- *App
	LoopDone        <-chan struct{}
	Fail            func(error)
	ShutdownBudget  time.Duration
	BackgroundError func(error)
}

// Run 启动提醒调度和历史恢复，并在收到全局退出及 LoopDone 后收尾。
func (a *App) Run(ctx context.Context, lifecycle Lifecycle) error {
	options := notification.SchedulerOptions{OnError: lifecycle.BackgroundError}
	if a.Scheduler == nil {
		repo, ok := a.Notify.(notification.NotificationSchedulerRepo)
		if !ok {
			return fmt.Errorf("notification repo does not support scheduling")
		}
		var err error
		a.Scheduler, err = notification.NewScheduler(repo, options)
		if err != nil {
			return err
		}
	}
	if err := a.configureRecovery(options); err != nil {
		return err
	}
	a.syncServices()

	// 收尾时停止开始新批次，并给已开始的批次保留独立的完成窗口。
	work, cancelWork := context.WithCancel(context.Background())
	defer cancelWork()
	polling, stopPolling := context.WithCancel(work)
	defer stopPolling()
	started := make(chan struct{})
	done := make(chan struct{}, 2)
	start := func(name string, run func(context.Context, context.Context) error) {
		go func() {
			defer func() { done <- struct{}{} }()
			started <- struct{}{}
			err := run(polling, work)
			if polling.Err() == nil {
				if err == nil {
					err = fmt.Errorf("background task stopped unexpectedly")
				}
				lifecycle.Fail(fmt.Errorf("%s: %w", name, err))
			}
		}()
	}
	start("notification scheduler", a.Scheduler.RunUntil)
	start("notification recovery", a.runRecovery)
	<-started
	<-started
	select {
	case lifecycle.Ready <- a:
	case <-ctx.Done():
	}

	<-ctx.Done()
	<-lifecycle.LoopDone
	shutdown, cancel := context.WithTimeout(context.Background(), lifecycle.ShutdownBudget)
	defer cancel()
	stopDeadline := context.AfterFunc(shutdown, cancelWork)
	defer stopDeadline()
	stopPolling()
	a.StopDelivery()
	<-done
	<-done
	return nil
}
