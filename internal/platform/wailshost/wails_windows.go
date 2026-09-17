//go:build windows

package wailshost

import (
	"context"
	"log/slog"

	"github.com/wailsapp/wails/v2"
	wailslogger "github.com/wailsapp/wails/v2/pkg/logger"
	wailsoptions "github.com/wailsapp/wails/v2/pkg/options"
	wailswindows "github.com/wailsapp/wails/v2/pkg/options/windows"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// WailsHostOptions 描述 Windows Wails 应用选项及平台无关生命周期端口。
type WailsHostOptions struct {
	App            wailsoptions.App
	Bindings       func() []interface{}
	Lifecycle      LifecycleCallbacks
	Logger         *slog.Logger
	LoggerProvider func() *slog.Logger
	ErrorFormatter func(error) any
}

// NewWailsHost 创建 Wails v2.15 的真实宿主，并保留所有可测试依赖的注入边界。
func NewWailsHost(options WailsHostOptions) *Host {
	runtime := RuntimeCallbacks{
		Quit: func(ctx context.Context) error {
			wailsruntime.Quit(ctx)
			return nil
		},
		WindowShow: func(ctx context.Context) error {
			wailsruntime.WindowShow(ctx)
			return nil
		},
		WindowHide: func(ctx context.Context) error {
			wailsruntime.WindowHide(ctx)
			return nil
		},
		WindowMinimise: func(ctx context.Context) error {
			wailsruntime.WindowMinimise(ctx)
			return nil
		},
		// Wails v2 没有独立 focus API；WindowShow 会恢复并前置已有窗口。
		WindowActivate: func(ctx context.Context) error {
			wailsruntime.WindowShow(ctx)
			return nil
		},
		EventsEmit: func(ctx context.Context, name string, payload any) error {
			wailsruntime.EventsEmit(ctx, name, payload)
			return nil
		},
	}
	appOptions := options.App
	runner := func(callbacks RunCallbacks) error {
		if options.Bindings != nil {
			appOptions.Bind = options.Bindings()
		}
		appOptions.OnStartup = callbacks.OnStartup
		appOptions.OnDomReady = callbacks.OnDomReady
		appOptions.OnBeforeClose = callbacks.OnBeforeClose
		appOptions.OnShutdown = callbacks.OnShutdown
		appOptions.ErrorFormatter = callbacks.ErrorFormatter
		logger := options.Logger
		if options.LoggerProvider != nil {
			if provided := options.LoggerProvider(); provided != nil {
				logger = provided
			}
		}
		appOptions.Logger = NewSlogLogger(logger)
		if appOptions.Windows == nil {
			appOptions.Windows = &wailswindows.Options{}
		}
		appOptions.Windows.OnSuspend = callbacks.OnSuspend
		appOptions.Windows.OnResume = callbacks.OnResume
		return wails.Run(&appOptions)
	}
	if options.ErrorFormatter == nil {
		options.ErrorFormatter = func(err error) any { return err.Error() }
	}
	return New(HostOptions{Runner: runner, Runtime: runtime, Lifecycle: options.Lifecycle, Logger: options.Logger, ErrorFormatter: options.ErrorFormatter})
}

var _ wailslogger.Logger = (*SlogLogger)(nil)
