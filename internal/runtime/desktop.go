package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/dengweitian0-svg/TimeCue/internal/platform"
	"github.com/dengweitian0-svg/TimeCue/internal/schedule/app"
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
