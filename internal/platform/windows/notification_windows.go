//go:build windows

package windows

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/dengweitian0-svg/TimeCue/internal/platform"
	"github.com/dengweitian0-svg/TimeCue/internal/schedule/app"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// WailsNotificationRuntime 是 Wails 通知 API 的窄可注入包装，避免业务代码依赖全局函数。
type WailsNotificationRuntime interface {
	InitializeNotifications(context.Context) error
	SendNotification(context.Context, wailsruntime.NotificationOptions) error
	OnNotificationResponse(context.Context, func(wailsruntime.NotificationResult))
}

// WailsNotificationCenterOptions 配置 Wails 通知中心及激活回流端口。
type WailsNotificationCenterOptions struct {
	Runtime             WailsNotificationRuntime
	Cleaner             NotificationHistoryCleaner
	ActivationPublisher ActivationPublisher
	Logger              *slog.Logger
}

// WailsNotificationCenter 将每条提醒逐条提交到 Wails，并管理通知资源所有权。
type WailsNotificationCenter struct {
	runtime   WailsNotificationRuntime
	cleaner   NotificationHistoryCleaner
	publisher ActivationPublisher
	logger    *slog.Logger

	mu          sync.Mutex
	initialized bool
	accepting   bool
	cleaned     bool
	closed      bool
	inflight    sync.WaitGroup
	responseErr responseErrorState
}

// nativeWailsNotificationRuntime 将 Wails 包级函数收敛到可替换端口。
type nativeWailsNotificationRuntime struct{}

// InitializeNotifications 初始化 Wails 通知能力。
func (nativeWailsNotificationRuntime) InitializeNotifications(ctx context.Context) error {
	return wailsruntime.InitializeNotifications(ctx)
}

// SendNotification 提交一条 Wails 通知。
func (nativeWailsNotificationRuntime) SendNotification(ctx context.Context, options wailsruntime.NotificationOptions) error {
	return wailsruntime.SendNotification(ctx, options)
}

// OnNotificationResponse 注册 Wails 通知响应回调。
func (nativeWailsNotificationRuntime) OnNotificationResponse(ctx context.Context, callback func(wailsruntime.NotificationResult)) {
	wailsruntime.OnNotificationResponse(ctx, callback)
}

// NewWailsNotificationCenter 创建生产通知中心；默认清理器清理当前桌面应用的历史。
func NewWailsNotificationCenter(options WailsNotificationCenterOptions) (*WailsNotificationCenter, error) {
	runtime := options.Runtime
	if runtime == nil {
		runtime = nativeWailsNotificationRuntime{}
	}
	cleaner := options.Cleaner
	if cleaner == nil {
		cleaner = NewWinRTNotificationHistoryCleaner()
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &WailsNotificationCenter{runtime: runtime, cleaner: cleaner, publisher: options.ActivationPublisher, logger: logger}, nil
}

var _ platform.NotificationCenter = (*WailsNotificationCenter)(nil)

// SetLogger 在 bootstrap 日志器就绪后替换构造期默认日志器。
func (c *WailsNotificationCenter) SetLogger(logger *slog.Logger) {
	if c == nil || logger == nil {
		return
	}
	c.mu.Lock()
	c.logger = logger
	c.mu.Unlock()
}

// Initialize 在给定 Wails 生命周期上下文中初始化通知并注册一次响应回流。
func (c *WailsNotificationCenter) Initialize(ctx context.Context) error {
	if c == nil {
		return errors.New("initialize notification center: center is nil")
	}
	if ctx == nil {
		return errors.New("initialize notification center: context is nil")
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errors.New("initialize notification center: center is closed")
	}
	if c.initialized {
		c.mu.Unlock()
		return nil
	}
	c.initialized = true
	c.mu.Unlock()
	if err := c.runtime.InitializeNotifications(ctx); err != nil {
		c.mu.Lock()
		c.initialized = false
		c.mu.Unlock()
		return fmt.Errorf("initialize Wails notifications: %w", err)
	}
	c.runtime.OnNotificationResponse(ctx, c.handleResponse)
	c.mu.Lock()
	if !c.closed {
		c.accepting = true
	}
	c.mu.Unlock()
	return nil
}

// Submit 将一个 ReminderNotification 映射为一次 Wails SendNotification 调用。
func (c *WailsNotificationCenter) Submit(ctx context.Context, reminder app.ReminderNotification) error {
	if c == nil {
		return errors.New("submit notification: center is nil")
	}
	if ctx == nil {
		return errors.New("submit notification: context is nil")
	}
	options, err := notificationOptionsForReminder(reminder)
	if err != nil {
		return err
	}
	c.mu.Lock()
	if !c.accepting || c.closed {
		c.mu.Unlock()
		return errors.New("submit notification: notification center is not accepting submissions")
	}
	c.inflight.Add(1)
	c.mu.Unlock()
	defer c.inflight.Done()
	if err := c.runtime.SendNotification(ctx, wailsruntime.NotificationOptions{
		ID: options.ID, Title: options.Title, Body: options.Body, Data: options.Data,
	}); err != nil {
		return fmt.Errorf("send Wails notification for schedule %d on %s: %w", reminder.Identity.ScheduleID, reminder.Identity.OriginalDate, err)
	}
	return nil
}

// Cleanup 停止新提交、等待在途请求，再清理系统通知历史；失败时保持可重试状态。
func (c *WailsNotificationCenter) Cleanup(ctx context.Context) error {
	if c == nil {
		return errors.New("cleanup notification center: center is nil")
	}
	if ctx == nil {
		return errors.New("cleanup notification center: context is nil")
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errors.New("cleanup notification center: center is closed")
	}
	if c.cleaned {
		c.mu.Unlock()
		return nil
	}
	c.accepting = false
	c.mu.Unlock()
	if err := waitNotificationSubmissions(ctx, &c.inflight); err != nil {
		return fmt.Errorf("wait notification submissions: %w", err)
	}
	if err := c.cleaner.Clear(ctx); err != nil {
		return fmt.Errorf("clear notification history: %w", err)
	}
	c.mu.Lock()
	c.cleaned = true
	c.mu.Unlock()
	return nil
}

// Close 停止新提交并等待在途请求，释放中心自身资源且可重复调用。
func (c *WailsNotificationCenter) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.accepting = false
	c.closed = true
	c.mu.Unlock()
	c.inflight.Wait()
	return nil
}

// LastResponseError 返回最近一次异步通知响应转换或回流错误。
func (c *WailsNotificationCenter) LastResponseError() error {
	if c == nil {
		return nil
	}
	return c.responseErr.get()
}

// handleResponse 将 Wails 响应中的严格 UserInfo 转换为 Toast 激活并回流给 broker。
func (c *WailsNotificationCenter) handleResponse(result wailsruntime.NotificationResult) {
	if result.Error != nil {
		c.recordResponseError(fmt.Errorf("Wails notification response: %w", result.Error))
		return
	}
	activation, err := notificationResponseActivation(result.Response.UserInfo)
	if err != nil {
		c.recordResponseError(err)
		return
	}
	if c.publisher == nil {
		return
	}
	if err := c.publisher.Publish(activation); err != nil {
		c.recordResponseError(fmt.Errorf("publish notification activation: %w", err))
	}
}

// recordResponseError 保存并记录无法向调用方同步返回的通知响应错误。
func (c *WailsNotificationCenter) recordResponseError(err error) {
	c.responseErr.set(err)
	c.mu.Lock()
	logger := c.logger
	c.mu.Unlock()
	logger.Warn("notification response could not be processed", "operation", "notification_response", "error", err)
}

// waitNotificationSubmissions 以可取消等待避免关闭上下文永久阻塞。
func waitNotificationSubmissions(ctx context.Context, submissions *sync.WaitGroup) error {
	done := make(chan struct{})
	go func() {
		submissions.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
