//go:build !windows

package wailshost

import (
	"errors"
	"log/slog"

	wailsoptions "github.com/wailsapp/wails/v2/pkg/options"
)

// WailsHostOptions 保持非 Windows 构建下的生产宿主构造形状。
type WailsHostOptions struct {
	App            wailsoptions.App
	Bindings       func() []interface{}
	Lifecycle      LifecycleCallbacks
	Logger         *slog.Logger
	LoggerProvider func() *slog.Logger
	ErrorFormatter func(error) any
}

// NewWailsHost 创建在运行时明确报告平台不支持的宿主，供跨平台测试编译。
func NewWailsHost(options WailsHostOptions) *Host {
	return New(HostOptions{
		Runner:         func(RunCallbacks) error { return errors.New("run Wails host: Windows is required") },
		Lifecycle:      options.Lifecycle,
		Logger:         options.Logger,
		ErrorFormatter: options.ErrorFormatter,
	})
}
