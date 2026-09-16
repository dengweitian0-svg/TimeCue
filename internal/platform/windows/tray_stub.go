//go:build !windows

package windows

import (
	"context"
	"errors"

	"github.com/dengweitian0-svg/TimeCue/internal/platform"
)

// WindowsTrayOptions 保持非 Windows 构建下的托盘构造 API 形状。
type WindowsTrayOptions struct {
	MessageThread     *MessageThread
	OwnsMessageThread bool
	Tooltip           string
}

// WindowsTray 是非 Windows 平台上的不可用托盘占位实现。
type WindowsTray struct{}

// NewWindowsTray 报告当前平台不支持 Windows 托盘。
func NewWindowsTray(WindowsTrayOptions) (*WindowsTray, error) {
	return nil, errors.New("create Windows tray: Windows is required")
}

// Start 报告当前平台不支持 Windows 托盘。
func (*WindowsTray) Start(context.Context, platform.TrayCallbacks) error {
	return errors.New("start Windows tray: Windows is required")
}

// Close 在非 Windows 平台不持有托盘资源。
func (*WindowsTray) Close() error { return nil }
