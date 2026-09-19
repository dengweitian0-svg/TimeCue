package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/dengweitian0-svg/TimeCue/internal/bootstrap"
	"github.com/dengweitian0-svg/TimeCue/internal/config"
	"github.com/dengweitian0-svg/TimeCue/internal/platform"
	"github.com/dengweitian0-svg/TimeCue/internal/schedule/app"
	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// Run 组装并启动桌面应用，主机运行路径保持同步且 scheduler 只启动一个 goroutine。
func (a *DesktopApplication) Run(ctx context.Context, cfg config.Config, logger *slog.Logger, store bootstrap.Store) error {
	if a == nil {
		return errors.New("desktop application is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if logger == nil {
		return a.failStart(errors.New("desktop application logger is nil"))
	}
	desktopStore, ok := store.(DesktopStore)
	if !ok || desktopStore == nil {
		return a.failStart(errors.New("bootstrap store does not implement desktop store"))
	}
	if err := a.assemble(cfg, logger, desktopStore); err != nil {
		return a.failStart(err)
	}

	var hostErr error
	if a.deps.Host != nil {
		// DesktopHost.Run 必须同步执行，具体 Wails 实现不得把主循环再放入后台。
		hostErr = a.deps.Host.Run(ctx)
	} else {
		<-ctx.Done()
		hostErr = ctx.Err()
	}
	stopErr := a.stop(context.WithoutCancel(ctx), false)
	if hostErr != nil && !errors.Is(hostErr, context.Canceled) && !errors.Is(hostErr, context.DeadlineExceeded) {
		return errors.Join(hostErr, stopErr)
	}
	return stopErr
}

// OnStartup 保存桌面宿主提供的运行时 context；此阶段不调用窗口或通知 Runtime API。
func (a *DesktopApplication) OnStartup(ctx context.Context) error {
	if a == nil {
		return errors.New("desktop application is nil")
	}
	if ctx == nil {
		return errors.New("desktop startup context is nil")
	}
	a.mu.Lock()
	a.runtimeContext = ctx
	a.mu.Unlock()
	return nil
}

// OnDomReady 延迟初始化桌面资源，并在前端可用后启动唯一 scheduler 和事件转发器。
func (a *DesktopApplication) OnDomReady(ctx context.Context) error {
	if a == nil {
		return errors.New("desktop application is nil")
	}
	if ctx == nil {
		return errors.New("desktop DOM-ready context is nil")
	}
	a.mu.Lock()
	if a.domReady {
		a.mu.Unlock()
		return nil
	}
	if a.resourcesClosed || a.state == DesktopStateStopping || a.state == DesktopStateCleanupFailed {
		// 早到的取消可能先于 DOM ready 进入停止；只保存 context 供清理失败页面重试，不得重启资源。
		a.runtimeContext = ctx
		a.domReady = true
		a.mu.Unlock()
		return nil
	}
	if a.scheduler == nil {
		a.mu.Unlock()
		return errors.New("desktop application is not assembled")
	}
	a.runtimeContext = ctx
	a.domReady = true
	// 资源初始化与 scheduler 启动必须发生在 DOM ready 之后，Wails Runtime context 才有效。
	runCtx, cancel := context.WithCancel(ctx)
	a.schedulerCancel = cancel
	a.eventCancel = cancel
	a.starting = true
	a.startupDone = make(chan struct{})
	a.startupCancel = cancel
	scheduler := a.scheduler
	a.mu.Unlock()
	if err := a.startResources(runCtx); err != nil {
		if runCtx.Err() != nil {
			a.finishStartup()
			return nil
		}
		cancel()
		startErr := a.failStart(err)
		a.finishStartup()
		return startErr
	}
	if runCtx.Err() != nil {
		a.finishStartup()
		return nil
	}
	schedulerDone := make(chan error, 1)
	a.mu.Lock()
	a.schedulerDone = schedulerDone
	a.schedulerJoined = false
	a.mu.Unlock()
	// 先建立 scheduler 事件转发器，再启动唯一调度循环，避免提交成功事件
	// 在运行时尚未开始消费时发生生产者竞态。
	a.startEventForwarders(runCtx)
	a.mu.Lock()
	startupCancelled := runCtx.Err() != nil
	if !startupCancelled {
		a.state = DesktopStateRunning
	}
	a.mu.Unlock()
	go func() {
		// scheduler 是唯一的提醒循环；其他 goroutine 只负责转发外部事件。
		schedulerDone <- scheduler.Run(runCtx)
	}()
	a.finishStartup()
	return nil
}

// assemble 从同一个 Store 创建并保存唯一的应用服务、协调器和提醒调度器。
func (a *DesktopApplication) assemble(cfg config.Config, logger *slog.Logger, store DesktopStore) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.service != nil || a.scheduler != nil {
		return errors.New("desktop application has already been assembled")
	}
	if a.deps.Clock == nil {
		return errors.New("desktop application clock is not configured")
	}
	if a.deps.Notifications == nil {
		return errors.New("desktop application notification center is not configured")
	}
	coordinator := app.NewMutationCoordinator()
	service := app.NewServiceWithCoordinator(store, a.deps.Clock, coordinator)
	scheduler := app.NewReminderScheduler(store, store, a.deps.Notifications, a.deps.Clock, a.deps.TimerFactory, logger, coordinator)
	scheduler.SetRecoveryEventHandler(a.publishRecoveryEvent)
	a.config = &cfg
	a.effectiveLogLevel = cfg.Log.Level
	a.logger = logger
	a.coordinator = coordinator
	a.service = service
	a.scheduler = scheduler
	a.bindings = &WailsBindings{application: a}
	a.pendingScheduleHidden = make(map[domain.ScheduleID]struct{})
	a.pendingRemindersChanged = false
	a.resourcesClosed = false
	return nil
}

// startResources 按运行时拥有权初始化通知、激活源、环境源和托盘资源。
func (a *DesktopApplication) startResources(ctx context.Context) error {
	if err := a.deps.Notifications.Initialize(ctx); err != nil {
		return fmt.Errorf("initialize notification center: %w", err)
	}
	if a.deps.Activation != nil {
		if err := a.deps.Activation.Start(ctx); err != nil {
			return fmt.Errorf("start activation source: %w", err)
		}
	}
	if a.deps.Environment != nil {
		if err := a.deps.Environment.Start(ctx); err != nil {
			return fmt.Errorf("start environment event source: %w", err)
		}
	}
	if a.deps.Tray != nil {
		if err := a.deps.Tray.Start(ctx, platform.TrayCallbacks{OnOpen: a.onTrayOpen, OnExit: a.onTrayExit}); err != nil {
			return fmt.Errorf("start tray: %w", err)
		}
	}
	return nil
}

// failStart 清理启动阶段已经初始化的资源并把应用恢复为可观察的停止状态。
func (a *DesktopApplication) failStart(primary error) error {
	cleanupErr := errors.Join(closeEnvironment(a.deps.Environment), closeActivation(a.deps.Activation), closeTray(a.deps.Tray), closeNotifications(a.deps.Notifications))
	a.mu.Lock()
	a.state = DesktopStateStopped
	a.resourcesClosed = true
	a.domReady = false
	a.schedulerCancel = nil
	a.schedulerDone = nil
	a.schedulerJoined = false
	a.eventCancel = nil
	a.mu.Unlock()
	if cleanupErr != nil {
		return errors.Join(primary, cleanupErr)
	}
	return primary
}

// finishStartup 结束 DOM ready 启动闸门，并唤醒等待启动完成的停止请求。
func (a *DesktopApplication) finishStartup() {
	a.mu.Lock()
	done := a.startupDone
	a.startupDone = nil
	a.startupCancel = nil
	a.starting = false
	a.mu.Unlock()
	if done != nil {
		close(done)
	}
}

// Stop 进入可重入、可重试的停止状态机，并在成功清理后请求宿主退出。
func (a *DesktopApplication) Stop(ctx context.Context) error {
	return a.stop(ctx, true)
}

// StopFromHost 进入同一停止状态机，但由正在处理关闭的宿主掌握最终 Quit。
func (a *DesktopApplication) StopFromHost(ctx context.Context) error {
	return a.stop(ctx, false)
}

// stop 执行停止状态机；requestQuit 仅由外部停止请求使用，避免 Run 收尾递归退出。
func (a *DesktopApplication) stop(ctx context.Context, requestQuit bool) error {
	if a == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	a.mu.Lock()
	if a.starting {
		startupDone := a.startupDone
		startupCancel := a.startupCancel
		a.mu.Unlock()
		if startupCancel != nil {
			startupCancel()
		}
		if startupDone != nil {
			select {
			case <-startupDone:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return a.stop(ctx, requestQuit)
	}
	if a.state == DesktopStateStopped && a.resourcesClosed {
		a.mu.Unlock()
		return nil
	}
	if a.state == DesktopStateStopping {
		done := a.stopDone
		a.mu.Unlock()
		if done != nil {
			select {
			case <-done:
				a.mu.Lock()
				err := a.cleanupError
				a.mu.Unlock()
				return err
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		a.mu.Lock()
		err := a.cleanupError
		a.mu.Unlock()
		return err
	}
	a.state = DesktopStateStopping
	done := make(chan struct{})
	a.stopDone = done
	cancel := a.schedulerCancel
	schedulerDone := a.schedulerDone
	coordinator := a.coordinator
	a.mu.Unlock()

	var stopErr error
	// 先取消 scheduler 和环境事件转发，再进入 coordinator 停止转换；否则事件转发
	// 可能持有 coordinator 锁等待存储，而 Stop 同时等待同一把锁形成阻塞环。
	if cancel != nil {
		cancel()
	}
	if err := a.waitEnvironmentForwarder(ctx); err != nil {
		stopErr = errors.Join(stopErr, fmt.Errorf("wait environment event forwarder: %w", err))
	}
	if stopErr == nil && coordinator != nil {
		stopErr = errors.Join(stopErr, coordinator.ApplyRuntimeEvent(app.RuntimeEvent{Type: app.RuntimeEventStop}))
	}
	if schedulerDone != nil {
		// 首次 Stop 可能因调用方 context 超时提前返回；重试仍必须先等 scheduler 真正退出。
		a.mu.Lock()
		joined := a.schedulerJoined
		a.mu.Unlock()
		if !joined {
			select {
			case schedulerErr := <-schedulerDone:
				a.mu.Lock()
				a.schedulerJoined = true
				a.mu.Unlock()
				if schedulerErr != nil && !errors.Is(schedulerErr, context.Canceled) {
					stopErr = errors.Join(stopErr, schedulerErr)
				}
			case <-ctx.Done():
				stopErr = errors.Join(stopErr, ctx.Err())
			}
		}
	}
	// 通知历史只是退出前的尽力清理；失败不能让托盘退出遗留可操作窗口或后台资源。
	a.cleanupNotificationHistory(ctx)
	// 即使前置停止步骤失败，也要继续尝试关闭全部桌面资源，避免资源所有权停在半退出状态。
	stopErr = errors.Join(stopErr, closeEnvironment(a.deps.Environment), closeActivation(a.deps.Activation), closeTray(a.deps.Tray), closeNotifications(a.deps.Notifications))

	a.mu.Lock()
	a.cleanupError = stopErr
	if stopErr == nil {
		a.state = DesktopStateStopped
		a.resourcesClosed = true
	} else {
		a.state = DesktopStateCleanupFailed
	}
	close(done)
	a.mu.Unlock()
	// 托盘退出是终止命令；即使个别桌面资源报告关闭错误，也必须请求宿主关闭窗口，
	// 让进程退出回收剩余的 OS 资源，而不是留下半退出的可操作界面。
	if requestQuit && a.deps.Host != nil {
		if err := a.deps.Host.Quit(ctx); err != nil {
			stopErr = errors.Join(stopErr, fmt.Errorf("quit desktop host: %w", err))
		}
	}
	return stopErr
}

// RetryCleanup 重试失败的通知清理和桌面资源释放。
func (a *DesktopApplication) RetryCleanup(ctx context.Context) (DesktopStatusDTO, error) {
	if err := a.stop(ctx, false); err != nil {
		return a.GetDesktopStatus(), err
	}
	if a.deps.Host != nil {
		lifecycleCtx := a.lifecycleContext()
		if lifecycleCtx == nil {
			return a.GetDesktopStatus(), errors.New("desktop runtime context is not ready")
		}
		if err := a.deps.Host.Quit(lifecycleCtx); err != nil {
			return a.GetDesktopStatus(), fmt.Errorf("quit desktop host after cleanup retry: %w", err)
		}
	}
	return a.GetDesktopStatus(), nil
}
