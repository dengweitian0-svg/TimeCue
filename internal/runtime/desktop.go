package runtime

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/dengweitian0-svg/TimeCue/internal/bootstrap"
	"github.com/dengweitian0-svg/TimeCue/internal/config"
	"github.com/dengweitian0-svg/TimeCue/internal/platform"
	"github.com/dengweitian0-svg/TimeCue/internal/schedule/app"
	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// NewDesktopApplication 创建尚未组装 Store 的桌面运行单元。
func NewDesktopApplication(dependencies DesktopDependencies) *DesktopApplication {
	return &DesktopApplication{deps: dependencies, state: DesktopStateStopped}
}

// Service 返回运行时唯一共享的日程应用服务，主要供组装边界测试使用。
func (a *DesktopApplication) Service() *app.Service {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.service
}

// Coordinator 返回运行时唯一共享的变更协调器。
func (a *DesktopApplication) Coordinator() *app.MutationCoordinator {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.coordinator
}

// Scheduler 返回运行时唯一共享的提醒调度器。
func (a *DesktopApplication) Scheduler() *app.ReminderScheduler {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.scheduler
}

// Bindings 返回运行时唯一共享的绑定对象。
func (a *DesktopApplication) Bindings() *WailsBindings {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.bindings
}

// Logger 返回 bootstrap 注入的统一结构化日志器；组装完成前返回 nil。
func (a *DesktopApplication) Logger() *slog.Logger {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.logger
}

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

// startEventForwarders 将平台事实回流至调度器，并保持失败可观察。
func (a *DesktopApplication) startEventForwarders(ctx context.Context) {
	if a.scheduler != nil {
		go a.forwardReminderEvents(ctx, a.scheduler.Events())
	}
	if a.deps.Activation != nil {
		go a.forwardActivations(ctx, a.deps.Activation.Events())
	}
	if a.deps.Environment != nil {
		a.environmentForwarder.Add(1)
		go func() {
			defer a.environmentForwarder.Done()
			a.forwardEnvironmentEvents(ctx, a.deps.Environment.Events())
		}()
	}
}

// forwardReminderEvents 把调度器应用事件转成前端边界事件。
func (a *DesktopApplication) forwardReminderEvents(ctx context.Context, events app.ReminderSchedulerEvents) {
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			switch event.Type {
			case app.ReminderSchedulerEventScheduleHidden:
				a.publishScheduleHidden(event)
			case app.ReminderSchedulerEventPendingRemindersChanged:
				a.publishPendingRemindersChanged(event)
			}
		}
	}
}

// waitEnvironmentForwarder 等待环境事件转发退出，阻止关闭底层时钟后仍有事件进入调度器。
func (a *DesktopApplication) waitEnvironmentForwarder(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		a.environmentForwarder.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// forwardActivations 为每条激活分配单调序列，并在前端就绪后发送最新事件。
func (a *DesktopApplication) forwardActivations(ctx context.Context, events <-chan platform.Activation) {
	for {
		select {
		case <-ctx.Done():
			return
		case activation, ok := <-events:
			if !ok {
				return
			}
			a.publishActivation(ctx, activation)
		}
	}
}

// forwardEnvironmentEvents 把外部环境事件送入唯一调度器边界。
func (a *DesktopApplication) forwardEnvironmentEvents(ctx context.Context, events <-chan app.RuntimeEvent) {
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			if err := a.scheduler.ApplyRuntimeEvent(ctx, event); err != nil {
				a.recordError(err)
			}
		}
	}
}

// publishActivation 保存最新激活并在前端已注册时发送；发送失败会保留待发送事件。
func (a *DesktopApplication) publishActivation(ctx context.Context, activation platform.Activation) {
	dto := activationDTO(activation)
	a.mu.Lock()
	a.activationSequence++
	dto.Sequence = a.activationSequence
	if activation.Kind == platform.ActivationKindToast {
		// 普通第二实例只表示窗口激活，不得作为前端目标积压或补发。
		a.latestActivation = cloneActivationDTO(&dto)
	}
	ready := a.frontendReady
	frontend := a.deps.Frontend
	window := a.deps.Window
	a.mu.Unlock()
	// 第二实例激活没有目标，也必须先把窗口带到前台；Toast 激活随后再把目标交给前端。
	lifecycleCtx := a.lifecycleContext()
	if window != nil && lifecycleCtx != nil {
		if err := window.Show(lifecycleCtx); err != nil {
			a.recordError(err)
			return
		}
		if err := window.Activate(lifecycleCtx); err != nil {
			a.recordError(err)
			return
		}
	}
	if activation.Kind != platform.ActivationKindToast || !ready || frontend == nil {
		return
	}
	if lifecycleCtx == nil {
		lifecycleCtx = ctx
	}
	if err := frontend.Emit(lifecycleCtx, platform.FrontendEventActivation, dto); err != nil {
		a.recordError(err)
	}
}

// FrontendReady 完成激活握手，并补发前端尚未消费的最新激活。
func (a *DesktopApplication) FrontendReady(ctx context.Context, request FrontendReadyRequest) (FrontendReadyResponse, error) {
	if a == nil {
		return FrontendReadyResponse{}, errors.New("desktop application is nil")
	}
	a.mu.Lock()
	a.frontendReady = true
	latest := cloneActivationDTO(a.latestActivation)
	latestRecovery := cloneRecoveryEvent(a.latestRecovery)
	pendingHidden := make([]domain.ScheduleID, 0, len(a.pendingScheduleHidden))
	for scheduleID := range a.pendingScheduleHidden {
		pendingHidden = append(pendingHidden, scheduleID)
	}
	a.pendingScheduleHidden = make(map[domain.ScheduleID]struct{})
	pendingRemindersChanged := a.pendingRemindersChanged
	a.pendingRemindersChanged = false
	deliverHidden := a.state == DesktopStateRunning || a.starting
	frontend := a.deps.Frontend
	a.mu.Unlock()
	if deliverHidden && frontend != nil {
		for _, scheduleID := range pendingHidden {
			if err := frontend.Emit(ctx, platform.FrontendEventScheduleHidden, platform.ScheduleHiddenEvent{ScheduleID: int64(scheduleID)}); err != nil {
				// 隐藏事件只是数据库事实的即时投影，发送失败不能阻断调度或回滚已提交状态。
				a.recordError(err)
			}
		}
	}
	if deliverHidden && pendingRemindersChanged && frontend != nil {
		if err := frontend.Emit(ctx, platform.FrontendEventPendingRemindersChanged, platform.PendingRemindersChangedEvent{}); err != nil {
			// 列表变化事件只是数据库事实的即时投影，发送失败不能阻断调度或回滚已提交状态。
			a.recordError(err)
		}
	}
	if frontend != nil && latestRecovery != nil {
		if err := frontend.Emit(ctx, platform.FrontendEventRecovery, *latestRecovery); err != nil {
			a.recordError(err)
			return FrontendReadyResponse{}, fmt.Errorf("emit pending recovery event: %w", err)
		}
	}
	if latest == nil || latest.Sequence <= request.LastSequence || frontend == nil {
		return FrontendReadyResponse{}, nil
	}
	if err := frontend.Emit(ctx, platform.FrontendEventActivation, *latest); err != nil {
		a.recordError(err)
		return FrontendReadyResponse{}, fmt.Errorf("emit pending activation: %w", err)
	}
	return FrontendReadyResponse{Activation: latest}, nil
}

// publishRecoveryEvent 转发恢复状态；前端未 ready 时只保留完成或失败事件，running 状态由状态查询补足。
func (a *DesktopApplication) publishRecoveryEvent(event app.RecoveryEvent) {
	if a == nil {
		return
	}
	a.mu.Lock()
	copyEvent := event
	frontend := a.deps.Frontend
	ready := a.frontendReady
	ctx := a.runtimeContext
	if !ready && event.Status != app.RecoveryStatusRunning {
		a.latestRecovery = &copyEvent
	}
	a.mu.Unlock()
	if !ready || frontend == nil || ctx == nil {
		return
	}
	if err := frontend.Emit(ctx, platform.FrontendEventRecovery, event); err != nil {
		a.recordError(err)
	}
}

// publishScheduleHidden 在前端握手前按 ID 暂存事件，握手后即时发送；发送失败只记录错误。
func (a *DesktopApplication) publishScheduleHidden(event app.ReminderSchedulerEvent) {
	if a == nil || event.Type != app.ReminderSchedulerEventScheduleHidden || event.ScheduleID <= 0 {
		return
	}
	a.mu.Lock()
	if a.state == DesktopStateStopping || a.state == DesktopStateStopped || a.state == DesktopStateCleanupFailed {
		a.mu.Unlock()
		return
	}
	frontend := a.deps.Frontend
	ctx := a.runtimeContext
	if !a.frontendReady {
		if a.pendingScheduleHidden == nil {
			a.pendingScheduleHidden = make(map[domain.ScheduleID]struct{})
		}
		a.pendingScheduleHidden[event.ScheduleID] = struct{}{}
		a.mu.Unlock()
		return
	}
	a.mu.Unlock()
	if frontend == nil || ctx == nil {
		return
	}
	if err := frontend.Emit(ctx, platform.FrontendEventScheduleHidden, platform.ScheduleHiddenEvent{ScheduleID: int64(event.ScheduleID)}); err != nil {
		a.recordError(err)
	}
}

// publishPendingRemindersChanged 在前端握手前合并列表变化，握手后即时发送无状态刷新信号。
func (a *DesktopApplication) publishPendingRemindersChanged(event app.ReminderSchedulerEvent) {
	if a == nil || event.Type != app.ReminderSchedulerEventPendingRemindersChanged {
		return
	}
	a.mu.Lock()
	if a.state == DesktopStateStopping || a.state == DesktopStateStopped || a.state == DesktopStateCleanupFailed {
		a.mu.Unlock()
		return
	}
	frontend := a.deps.Frontend
	ctx := a.runtimeContext
	if !a.frontendReady {
		a.pendingRemindersChanged = true
		a.mu.Unlock()
		return
	}
	a.mu.Unlock()
	if frontend == nil || ctx == nil {
		return
	}
	if err := frontend.Emit(ctx, platform.FrontendEventPendingRemindersChanged, platform.PendingRemindersChangedEvent{}); err != nil {
		a.recordError(err)
	}
}

// onTrayOpen 将托盘打开动作转换为窗口显示和激活。
func (a *DesktopApplication) onTrayOpen() {
	if a.deps.Window == nil {
		return
	}
	ctx := a.lifecycleContext()
	if ctx == nil {
		return
	}
	if err := a.deps.Window.Show(ctx); err != nil {
		a.recordError(err)
		return
	}
	if err := a.deps.Window.Activate(ctx); err != nil {
		a.recordError(err)
	}
}

// onTrayExit 异步汇入统一停止路径，确保托盘消息回调先返回后再关闭托盘资源。
func (a *DesktopApplication) onTrayExit() {
	ctx := a.lifecycleContext()
	if ctx == nil {
		return
	}
	go func() {
		if err := a.Stop(context.WithoutCancel(ctx)); err != nil {
			a.recordError(err)
		}
	}()
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

// recordError 保存最近一次运行时错误，供状态查询和日志边界使用。
func (a *DesktopApplication) recordError(err error) {
	if a == nil || err == nil {
		return
	}
	a.mu.Lock()
	a.lastError = err
	logger := a.logger
	a.mu.Unlock()
	if logger != nil {
		logger.Error("desktop runtime error", "error", err)
	}
}

// cleanupNotificationHistory 尝试删除系统中的 TimeCue 通知，但失败只作为退出告警。
func (a *DesktopApplication) cleanupNotificationHistory(ctx context.Context) {
	if a == nil || a.deps.Notifications == nil {
		return
	}
	if err := a.deps.Notifications.Cleanup(ctx); err != nil {
		a.mu.Lock()
		logger := a.logger
		a.mu.Unlock()
		if logger != nil {
			logger.Warn("notification history cleanup failed before exit", "operation", "clear_notification_history", "error", err)
		}
	}
}

// lifecycleContext 返回 OnStartup/OnDomReady 保存的宿主 context，禁止平台回调自行创建 context。
func (a *DesktopApplication) lifecycleContext() context.Context {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.runtimeContext
}

// bindingContext 返回绑定调用使用的宿主 context。
func (a *DesktopApplication) bindingContext() (context.Context, error) {
	ctx := a.lifecycleContext()
	if ctx == nil {
		return nil, errors.New("desktop runtime context is not ready")
	}
	return ctx, nil
}

// GetDesktopStatus 返回桌面生命周期、清理错误和激活握手状态。
func (a *DesktopApplication) GetDesktopStatus() DesktopStatusDTO {
	if a == nil {
		return DesktopStatusDTO{}
	}
	a.mu.Lock()
	state := a.state
	activationSequence := a.activationSequence
	frontendReady := a.frontendReady
	scheduler := a.scheduler
	cleanupError := a.cleanupError
	lastError := a.lastError
	a.mu.Unlock()
	recoveryStatus := app.RecoveryStatusIdle
	if scheduler != nil {
		recoveryStatus = scheduler.GetRuntimeStatus().RecoveryStatus
	}
	status := DesktopStatusDTO{State: state, ActivationSequence: activationSequence, FrontendReady: frontendReady, RecoveryStatus: recoveryStatus}
	if cleanupError != nil {
		status.CleanupError = cleanupError.Error()
	}
	if lastError != nil {
		status.LastError = lastError.Error()
	}
	return status
}

// GetStorageDirectories 返回日志和备份目录的 effective/saved 对照，不暴露固定数据库路径。
func (a *DesktopApplication) GetStorageDirectories() (StorageDirectoriesDTO, error) {
	if a == nil {
		return StorageDirectoriesDTO{}, errors.New("desktop application is nil")
	}
	a.mu.Lock()
	if a.config == nil || a.service == nil {
		a.mu.Unlock()
		return StorageDirectoriesDTO{}, errors.New("desktop application is not assembled")
	}
	cfg := *a.config
	a.mu.Unlock()
	return storageDirectoriesDTO(cfg)
}

// GetLogLevel 返回日志级别当前生效值与保存值，日志器不会在运行中切换级别。
func (a *DesktopApplication) GetLogLevel() (LogLevelDTO, error) {
	if a == nil {
		return LogLevelDTO{}, errors.New("desktop application is nil")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.config == nil || a.service == nil {
		return LogLevelDTO{}, errors.New("desktop application is not assembled")
	}
	return logLevelDTO(a.effectiveLogLevel, a.config.Log.Level)
}

// GetTheme 返回当前生效的应用主题。
func (a *DesktopApplication) GetTheme() (ThemeDTO, error) {
	if a == nil {
		return ThemeDTO{}, errors.New("desktop application is nil")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.config == nil || a.service == nil {
		return ThemeDTO{}, errors.New("desktop application is not assembled")
	}
	return ThemeDTO{Theme: string(a.config.Theme)}, nil
}

// ApplyWindowTheme 将前端主题的 background/text 颜色应用到原生窗口标题栏。
// 标题栏是非关键桌面副作用，平台失败会被记录并返回给前端，但不改变配置状态。
func (a *DesktopApplication) ApplyWindowTheme(ctx context.Context, request ApplyWindowThemeRequest) error {
	if a == nil {
		return errors.New("desktop application is nil")
	}
	if err := a.ensureWritable(); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	colors, err := windowThemeColorsFromRequest(request)
	if err != nil {
		return fmt.Errorf("apply window theme: %w", err)
	}
	a.mu.Lock()
	controller := a.deps.WindowTheme
	logger := a.logger
	a.mu.Unlock()
	if controller == nil {
		return nil
	}
	if err := controller.Apply(ctx, colors); err != nil {
		wrapped := fmt.Errorf("apply window theme: %w", err)
		if logger != nil {
			logger.Warn("native window theme synchronization failed", "operation", "apply_window_theme", "dependency", "windows_dwm", "error", wrapped)
		}
		return wrapped
	}
	return nil
}

// windowThemeColorsFromRequest 严格解析前端传入的两个不透明十六进制颜色。
func windowThemeColorsFromRequest(request ApplyWindowThemeRequest) (platform.WindowThemeColors, error) {
	titleBar, err := parseWindowThemeColor("background", request.Background)
	if err != nil {
		return platform.WindowThemeColors{}, err
	}
	titleText, err := parseWindowThemeColor("text", request.Text)
	if err != nil {
		return platform.WindowThemeColors{}, err
	}
	return platform.WindowThemeColors{TitleBar: titleBar, TitleText: titleText}, nil
}

// parseWindowThemeColor 将 #RRGGBB 解析为平台无关 RGB 通道，并拒绝透明或畸形值。
func parseWindowThemeColor(name, value string) (platform.RGBColor, error) {
	if len(value) != 7 || value[0] != '#' {
		return platform.RGBColor{}, fmt.Errorf("window theme %s must be #RRGGBB", name)
	}
	decoded, err := hex.DecodeString(value[1:])
	if err != nil {
		return platform.RGBColor{}, fmt.Errorf("window theme %s must be #RRGGBB: %w", name, err)
	}
	return platform.RGBColor{R: decoded[0], G: decoded[1], B: decoded[2]}, nil
}

// SaveLogLevel 保存日志级别；当前日志器保持原级别，重启后加载新值。
func (a *DesktopApplication) SaveLogLevel(ctx context.Context, request SaveLogLevelRequest) (LogLevelDTO, error) {
	if a == nil {
		return LogLevelDTO{}, errors.New("desktop application is nil")
	}
	if err := a.ensureWritable(); err != nil {
		return LogLevelDTO{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return LogLevelDTO{}, err
	}
	a.mu.Lock()
	if a.state != DesktopStateRunning {
		a.mu.Unlock()
		return LogLevelDTO{}, domain.NewRuntimeStopping()
	}
	if a.config == nil {
		a.mu.Unlock()
		return LogLevelDTO{}, errors.New("runtime config is not configured")
	}
	if err := a.config.SaveLogLevel(request.Level); err != nil {
		wrapped := a.newConfigPersistenceError("save log level", "log.level", err)
		a.lastError = wrapped
		logger := a.logger
		a.mu.Unlock()
		if logger != nil {
			logger.Error("log level configuration save failed", "operation", "save_log_level", "error", wrapped)
		}
		return LogLevelDTO{}, wrapped
	}
	result, err := logLevelDTO(a.effectiveLogLevel, a.config.Log.Level)
	logger := a.logger
	a.mu.Unlock()
	if err != nil {
		return LogLevelDTO{}, err
	}
	if logger != nil {
		logger.Info("log level configuration saved", "operation", "save_log_level", "restart_required", result.RestartRequired)
	}
	return result, nil
}

// SaveTheme 保存应用主题；只有配置原子替换成功后才更新运行时内存主题。
func (a *DesktopApplication) SaveTheme(ctx context.Context, request SaveThemeRequest) (ThemeDTO, error) {
	if a == nil {
		return ThemeDTO{}, errors.New("desktop application is nil")
	}
	if err := a.ensureWritable(); err != nil {
		return ThemeDTO{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return ThemeDTO{}, err
	}
	a.mu.Lock()
	if a.state != DesktopStateRunning {
		a.mu.Unlock()
		return ThemeDTO{}, domain.NewRuntimeStopping()
	}
	if a.config == nil {
		a.mu.Unlock()
		return ThemeDTO{}, errors.New("runtime config is not configured")
	}
	if err := a.config.SaveTheme(config.Theme(request.Theme)); err != nil {
		wrapped := a.newConfigPersistenceError("save theme", "theme", err)
		a.lastError = wrapped
		logger := a.logger
		a.mu.Unlock()
		if logger != nil {
			logger.Error("theme configuration save failed", "operation", "save_theme", "error", wrapped)
		}
		return ThemeDTO{}, wrapped
	}
	result := ThemeDTO{Theme: string(a.config.Theme)}
	logger := a.logger
	a.mu.Unlock()
	if logger != nil {
		logger.Info("theme configuration saved", "operation", "save_theme", "theme", result.Theme)
	}
	return result, nil
}

// RestoreConfigDefault 将一个公开配置属性的默认值写回 config.yaml。
func (a *DesktopApplication) RestoreConfigDefault(ctx context.Context, property string) error {
	if a == nil {
		return errors.New("desktop application is nil")
	}
	if err := a.ensureWritable(); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	a.mu.Lock()
	if a.state != DesktopStateRunning {
		a.mu.Unlock()
		return domain.NewRuntimeStopping()
	}
	if a.config == nil {
		a.mu.Unlock()
		return errors.New("runtime config is not configured")
	}
	if err := a.config.RestoreDefault(config.ConfigProperty(property)); err != nil {
		wrapped := a.newConfigPersistenceError("restore config default", property, err)
		a.lastError = wrapped
		logger := a.logger
		a.mu.Unlock()
		if logger != nil {
			logger.Error("config default restore failed", "operation", "restore_config_default", "property", property, "error", wrapped)
		}
		return wrapped
	}
	logger := a.logger
	a.mu.Unlock()
	if logger != nil {
		logger.Info("config default restored", "operation", "restore_config_default", "property", property)
	}
	return nil
}

// SaveStorageDirectory 验证并保存目录配置；当前生效路径保持不变，重启后才加载新路径。
func (a *DesktopApplication) SaveStorageDirectory(ctx context.Context, request SaveStorageDirectoryRequest) (StorageDirectoryDTO, error) {
	if a == nil {
		return StorageDirectoryDTO{}, errors.New("desktop application is nil")
	}
	if err := a.ensureWritable(); err != nil {
		return StorageDirectoryDTO{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return StorageDirectoryDTO{}, err
	}
	kind, err := parseStorageDirectoryKind(request.Kind)
	if err != nil {
		return StorageDirectoryDTO{}, a.configPersistenceError("validate storage directory kind", request.Kind, err)
	}
	a.mu.Lock()
	access := a.deps.DirectoryAccess
	configValue := a.config
	state := a.state
	a.mu.Unlock()
	if state != DesktopStateRunning {
		return StorageDirectoryDTO{}, domain.NewRuntimeStopping()
	}
	if configValue == nil {
		return StorageDirectoryDTO{}, errors.New("runtime config is not configured")
	}
	if access == nil {
		return StorageDirectoryDTO{}, a.configPersistenceError("validate storage directory", string(kind), errors.New("directory access is not configured"))
	}
	normalized, err := access.ValidateDirectory(ctx, request.Path)
	if err != nil {
		return StorageDirectoryDTO{}, a.configPersistenceError("validate storage directory", string(kind), err)
	}
	if err := ctx.Err(); err != nil {
		return StorageDirectoryDTO{}, err
	}

	a.mu.Lock()
	if a.state != DesktopStateRunning {
		a.mu.Unlock()
		return StorageDirectoryDTO{}, domain.NewRuntimeStopping()
	}
	if a.config == nil {
		a.mu.Unlock()
		return StorageDirectoryDTO{}, errors.New("runtime config is not configured")
	}
	if err := a.config.SaveStorageDirectory(kind, normalized); err != nil {
		wrapped := a.newConfigPersistenceError("save storage directory", string(kind), err)
		a.lastError = wrapped
		logger := a.logger
		a.mu.Unlock()
		if logger != nil {
			logger.Error("storage directory save failed", "operation", "save_storage_directory", "kind", string(kind), "error", wrapped)
		}
		return StorageDirectoryDTO{}, wrapped
	}
	directory, err := storageDirectoryDTO(*a.config, kind)
	if err != nil {
		a.mu.Unlock()
		return StorageDirectoryDTO{}, err
	}
	logger := a.logger
	a.mu.Unlock()
	if logger != nil {
		logger.Info("storage directory configuration saved", "operation", "save_storage_directory", "kind", string(kind), "restart_required", directory.RestartRequired)
	}
	return directory, nil
}

// ChooseStorageDirectory 打开目录选择器；取消返回空字符串且不改变任何配置。
func (a *DesktopApplication) ChooseStorageDirectory(ctx context.Context, kindValue string) (string, error) {
	if a == nil {
		return "", errors.New("desktop application is nil")
	}
	kind, err := parseStorageDirectoryKind(kindValue)
	if err != nil {
		return "", a.configPersistenceError("choose storage directory", kindValue, err)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	a.mu.Lock()
	chooser := a.deps.DirectoryChooser
	configValue := a.config
	state := a.state
	a.mu.Unlock()
	if state != DesktopStateRunning {
		return "", domain.NewRuntimeStopping()
	}
	if configValue == nil {
		return "", errors.New("runtime config is not configured")
	}
	if chooser == nil {
		return "", a.configPersistenceError("choose storage directory", string(kind), errors.New("directory chooser is not configured"))
	}
	directory, err := storageDirectoryDTO(*configValue, kind)
	if err != nil {
		return "", err
	}
	defaultDirectory := directory.SavedPath
	if _, statErr := os.Stat(defaultDirectory); statErr != nil {
		defaultDirectory = directory.EffectivePath
	}
	title := "选择日志目录"
	if kind == config.StorageDirectoryBackups {
		title = "选择备份目录"
	}
	selected, err := chooser.ChooseDirectory(ctx, title, defaultDirectory)
	if err != nil {
		return "", a.configPersistenceError("choose storage directory", string(kind), err)
	}
	if selected == "" {
		return "", nil
	}
	return selected, nil
}

// storageDirectoriesDTO 将配置保存值与进程启动时冻结的生效值投影到前端。
func storageDirectoriesDTO(cfg config.Config) (StorageDirectoriesDTO, error) {
	logs, err := storageDirectoryDTO(cfg, config.StorageDirectoryLogs)
	if err != nil {
		return StorageDirectoriesDTO{}, err
	}
	backups, err := storageDirectoryDTO(cfg, config.StorageDirectoryBackups)
	if err != nil {
		return StorageDirectoriesDTO{}, err
	}
	return StorageDirectoriesDTO{Logs: logs, Backups: backups}, nil
}

// logLevelDTO 将日志级别的运行时生效值与配置保存值投影到前端。
func logLevelDTO(effective, saved string) (LogLevelDTO, error) {
	effective = strings.ToLower(strings.TrimSpace(effective))
	saved = strings.ToLower(strings.TrimSpace(saved))
	if effective == "" || saved == "" {
		return LogLevelDTO{}, errors.New("log level must not be empty")
	}
	return LogLevelDTO{EffectiveLevel: effective, SavedLevel: saved, RestartRequired: effective != saved}, nil
}

// storageDirectoryDTO 计算单个目录的生效/保存差异，并按物理目录判断重启需求。
func storageDirectoryDTO(cfg config.Config, kind config.StorageDirectoryKind) (StorageDirectoryDTO, error) {
	saved, err := cfg.SavedStorageDirectory(kind)
	if err != nil {
		return StorageDirectoryDTO{}, err
	}
	var effective string
	switch kind {
	case config.StorageDirectoryLogs:
		effective = filepath.Dir(cfg.ResolvedPaths.LogPath)
	case config.StorageDirectoryBackups:
		effective = cfg.ResolvedPaths.BackupDir
	default:
		return StorageDirectoryDTO{}, fmt.Errorf("invalid storage directory kind %q", kind)
	}
	effective = filepath.Clean(effective)
	saved = filepath.Clean(saved)
	return StorageDirectoryDTO{Kind: string(kind), EffectivePath: effective, SavedPath: saved, RestartRequired: !config.SamePhysicalDirectory(effective, saved)}, nil
}

// parseStorageDirectoryKind 将 Wails 请求限制到两个产品目录类型。
func parseStorageDirectoryKind(value string) (config.StorageDirectoryKind, error) {
	kind := config.StorageDirectoryKind(value)
	if kind != config.StorageDirectoryLogs && kind != config.StorageDirectoryBackups {
		return "", fmt.Errorf("invalid storage directory kind %q: want logs or backups", value)
	}
	return kind, nil
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

// closeEnvironment 关闭环境事件源并保留关闭错误上下文。
func closeEnvironment(source platform.EnvironmentEventSource) error {
	if source == nil {
		return nil
	}
	if err := source.Close(); err != nil {
		return fmt.Errorf("close environment event source: %w", err)
	}
	return nil
}

// closeActivation 关闭激活事件源并保留关闭错误上下文。
func closeActivation(source platform.ActivationSource) error {
	if source == nil {
		return nil
	}
	if err := source.Close(); err != nil {
		return fmt.Errorf("close activation source: %w", err)
	}
	return nil
}

// closeTray 关闭托盘资源并保留关闭错误上下文。
func closeTray(tray platform.TrayPort) error {
	if tray == nil {
		return nil
	}
	if err := tray.Close(); err != nil {
		return fmt.Errorf("close tray: %w", err)
	}
	return nil
}

// closeNotifications 关闭通知资源并保留关闭错误上下文。
func closeNotifications(center platform.NotificationCenter) error {
	if center == nil {
		return nil
	}
	if err := center.Close(); err != nil {
		return fmt.Errorf("close notification center: %w", err)
	}
	return nil
}

// activationDTO 将平台激活事实投影为不暴露内部指针的前端 DTO。
func activationDTO(activation platform.Activation) ActivationDTO {
	result := ActivationDTO{Kind: activation.Kind}
	if activation.Target != nil {
		result.Target = &ActivationTargetDTO{ScheduleID: int64(activation.Target.ScheduleID), OriginalDate: activation.Target.OriginalDate}
	}
	return result
}

// cloneActivationDTO 复制激活 DTO，避免前端或测试持有运行时内部指针。
func cloneActivationDTO(value *ActivationDTO) *ActivationDTO {
	if value == nil {
		return nil
	}
	clone := *value
	if value.Target != nil {
		target := *value.Target
		clone.Target = &target
	}
	return &clone
}

// cloneRecoveryEvent 复制恢复事件，避免运行时内部状态指针泄露到前端边界。
func cloneRecoveryEvent(value *app.RecoveryEvent) *app.RecoveryEvent {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
