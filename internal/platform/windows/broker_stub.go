//go:build !windows

package windows

import (
	"context"
	"errors"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/platform"
)

// ErrActivationWithoutInstance 表示 Toast 激活启动时没有可转发的主实例。
var ErrActivationWithoutInstance = errors.New("toast activation has no running instance")

// WindowsInstanceBrokerOptions 配置 Windows 单实例和激活通道。
type WindowsInstanceBrokerOptions struct {
	ApplicationName string
	Args            []string
	DialTimeout     time.Duration
	QueueSize       int
}

// WindowsInstanceBroker 是非 Windows 平台上的不可用实现占位。
type WindowsInstanceBroker struct{}

// NewWindowsInstanceBroker 报告当前平台不支持 Windows 单实例 broker。
func NewWindowsInstanceBroker(string) (*WindowsInstanceBroker, error) {
	return nil, errors.New("create instance broker: Windows is required")
}

// NewWindowsInstanceBrokerWithOptions 报告当前平台不支持 Windows 单实例 broker。
func NewWindowsInstanceBrokerWithOptions(WindowsInstanceBrokerOptions) (*WindowsInstanceBroker, error) {
	return nil, errors.New("create instance broker: Windows is required")
}

// Acquire 报告当前平台不支持 Windows 单实例 broker。
func (*WindowsInstanceBroker) Acquire(context.Context) (func() error, error) {
	return nil, errors.New("acquire instance broker: Windows is required")
}

// Start 报告当前平台不支持 Windows 单实例 broker。
func (*WindowsInstanceBroker) Start(context.Context) error {
	return errors.New("start instance broker: Windows is required")
}

// Events 返回空事件流，供非 Windows 代码保持可编译。
func (*WindowsInstanceBroker) Events() <-chan platform.Activation { return nil }

// Publish 报告当前平台不支持 Windows 激活发布。
func (*WindowsInstanceBroker) Publish(platform.Activation) error {
	return errors.New("publish activation: Windows is required")
}

// Close 在非 Windows 平台上不持有资源。
func (*WindowsInstanceBroker) Close() error { return nil }
