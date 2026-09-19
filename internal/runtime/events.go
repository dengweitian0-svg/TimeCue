package runtime

import (
	"context"
	"errors"
	"fmt"

	"github.com/dengweitian0-svg/TimeCue/internal/platform"
	"github.com/dengweitian0-svg/TimeCue/internal/schedule/app"
	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

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
