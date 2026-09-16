//go:build !windows

package windows

import (
	"context"
	"errors"
	"log/slog"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/app"
)

// WailsNotificationCenter 是非 Windows 平台上的不可用占位实现。
type WailsNotificationCenter struct{}

// WailsNotificationCenterOptions 保持非 Windows 编译时的构造 API 形状。
type WailsNotificationCenterOptions struct {
	Runtime                any
	Cleaner                NotificationHistoryCleaner
	ActivationPublisher    ActivationPublisher
	ApplicationUserModelID string
	Logger                 *slog.Logger
}

// NewWailsNotificationCenter 报告当前平台不支持 Wails Windows 通知。
func NewWailsNotificationCenter(WailsNotificationCenterOptions) (*WailsNotificationCenter, error) {
	return nil, errors.New("create notification center: Windows is required")
}

// Initialize 报告当前平台不支持 Wails 通知。
func (*WailsNotificationCenter) Initialize(context.Context) error {
	return errors.New("initialize notification center: Windows is required")
}

// Submit 报告当前平台不支持 Wails 通知。
func (*WailsNotificationCenter) Submit(context.Context, app.ReminderNotification) error {
	return errors.New("submit notification: Windows is required")
}

// Cleanup 报告当前平台不支持 Wails 通知。
func (*WailsNotificationCenter) Cleanup(context.Context) error {
	return errors.New("cleanup notification center: Windows is required")
}

// Close 在非 Windows 平台上不持有通知资源。
func (*WailsNotificationCenter) Close() error { return nil }

// LastResponseError 在非 Windows 平台上没有异步通知响应，因此返回空错误。
func (*WailsNotificationCenter) LastResponseError() error { return nil }

// WinRTNotificationHistoryCleaner 是非 Windows 平台上的清理器占位类型。
type WinRTNotificationHistoryCleaner struct{}

// NewWinRTNotificationHistoryCleaner 报告当前平台不支持 WinRT 通知历史清理。
func NewWinRTNotificationHistoryCleaner(string) (*WinRTNotificationHistoryCleaner, error) {
	return nil, errors.New("create notification history cleaner: Windows is required")
}
