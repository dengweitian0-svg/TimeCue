//go:build !windows

package windows

import (
	"context"
	"errors"
)

// MessageHandler 保持非 Windows 构建下的消息线程 API 形状。
type MessageHandler func(message uint32, wParam, lParam uintptr) error

// MessageHandlerToken 标识非 Windows 构建下的处理器注册。
type MessageHandlerToken uint64

// MessageThread 是非 Windows 平台上的不可用消息线程占位实现。
type MessageThread struct{}

// NewMessageThread 在非 Windows 平台明确返回不支持错误。
func NewMessageThread(MessageHandler) (*MessageThread, error) {
	return nil, errors.New("create message thread: Windows is required")
}

// RegisterHandler 在非 Windows 平台明确返回不支持错误。
func (*MessageThread) RegisterHandler(MessageHandler) (MessageHandlerToken, error) {
	return 0, errors.New("register message handler: Windows is required")
}

// UnregisterHandler 在非 Windows 平台明确返回不支持错误。
func (*MessageThread) UnregisterHandler(MessageHandlerToken) error {
	return errors.New("unregister message handler: Windows is required")
}

// Start 在非 Windows 平台明确返回不支持错误。
func (*MessageThread) Start(context.Context) error {
	return errors.New("start message thread: Windows is required")
}

// Close 在非 Windows 平台保持资源清理 API 的幂等形状。
func (*MessageThread) Close() error { return nil }
