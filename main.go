//go:build !bindings

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/dengweitian0-svg/TimeCue/internal/bootstrap"
	"github.com/dengweitian0-svg/TimeCue/internal/config"
	"github.com/dengweitian0-svg/TimeCue/internal/platform"
	platformfilesystem "github.com/dengweitian0-svg/TimeCue/internal/platform/filesystem"
	"github.com/dengweitian0-svg/TimeCue/internal/platform/wailshost"
	platformwindows "github.com/dengweitian0-svg/TimeCue/internal/platform/windows"
	appRuntime "github.com/dengweitian0-svg/TimeCue/internal/runtime"
	"github.com/dengweitian0-svg/TimeCue/internal/utils"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	wailswindows "github.com/wailsapp/wails/v2/pkg/options/windows"
)

const applicationName = "TimeCue"

// main 创建进程级取消上下文，运行 Windows 桌面应用并处理最终退出码。
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err := runDesktop(ctx)
	exitCode := reportError(os.Stderr, err)
	if exitCode != utils.ExitOK {
		showFatalError(applicationName, err)
		os.Exit(int(exitCode))
	}
}

// runDesktop 先创建单实例 broker，并把 Store、日志和锁的最终所有权留给 bootstrap。
func runDesktop(ctx context.Context) error {
	broker, err := platformwindows.NewWindowsInstanceBroker(applicationName)
	if err != nil {
		return utils.Wrap(utils.StartupError, "create Windows instance broker", err)
	}
	return normalizeDesktopStartError(bootstrap.Start(ctx, desktopEntrypoint{broker: broker}, broker))
}

// normalizeDesktopStartError 将无主实例的过期 Toast 冷激活视为无操作退出，避免弹出致命错误 UI。
func normalizeDesktopStartError(err error) error {
	if errors.Is(err, platformwindows.ErrActivationWithoutInstance) {
		return nil
	}
	return err
}

// desktopEntrypoint 延迟创建桌面资源，确保 Bootstrap 取得主实例资格后才创建消息线程、托盘和 Wails。
type desktopEntrypoint struct {
	broker *platformwindows.WindowsInstanceBroker
}

// Run 在配置、日志、Store 和单实例锁就绪后组装并运行桌面应用。
func (entry desktopEntrypoint) Run(ctx context.Context, cfg config.Config, logger *slog.Logger, store bootstrap.Store) error {
	if entry.broker == nil {
		return utils.New(utils.StartupError, "assemble desktop application", "instance broker must not be nil")
	}
	messageThread, err := platformwindows.NewMessageThread(nil)
	if err != nil {
		return utils.Wrap(utils.StartupError, "create Windows message thread", err)
	}
	environment, err := platformwindows.NewEnvironmentClockWithMessageThread(messageThread)
	if err != nil {
		_ = messageThread.Close()
		return utils.Wrap(utils.StartupError, "create Windows environment clock", err)
	}
	tray, err := platformwindows.NewWindowsTray(platformwindows.WindowsTrayOptions{MessageThread: messageThread, OwnsMessageThread: true, Tooltip: applicationName})
	if err != nil {
		_ = environment.Close()
		_ = messageThread.Close()
		return utils.Wrap(utils.StartupError, "create Windows tray", err)
	}
	notificationCenter, err := platformwindows.NewWailsNotificationCenter(platformwindows.WailsNotificationCenterOptions{
		ActivationPublisher: entry.broker,
	})
	if err != nil {
		_ = environment.Close()
		_ = tray.Close()
		return utils.Wrap(utils.StartupError, "create Windows notification center", err)
	}
	windowTheme, err := platformwindows.NewWindowsWindowThemeController(platformwindows.MainWindowClassName)
	if err != nil {
		_ = environment.Close()
		_ = tray.Close()
		_ = notificationCenter.Close()
		return utils.Wrap(utils.StartupError, "create Windows window theme controller", err)
	}

	var application *appRuntime.DesktopApplication
	var host *wailshost.Host
	host = wailshost.NewWailsHost(wailshost.WailsHostOptions{
		App: options.App{
			Title:             applicationName,
			Width:             1180,
			Height:            760,
			MinWidth:          840,
			MinHeight:         600,
			BackgroundColour:  options.NewRGB(245, 242, 235),
			Windows:           &wailswindows.Options{WindowClassName: platformwindows.MainWindowClassName},
			AssetServer:       &assetserver.Options{Assets: frontendAssets()},
			HideWindowOnClose: false,
		},
		Bindings: func() []interface{} {
			if application == nil || application.Bindings() == nil {
				return nil
			}
			return []interface{}{application.Bindings()}
		},
		LoggerProvider: func() *slog.Logger {
			if application == nil {
				return nil
			}
			return application.Logger()
		},
		Lifecycle: wailshost.LifecycleCallbacks{
			OnStartup: func(lifecycleCtx context.Context) error {
				logger := requireDesktop(application).Logger()
				host.SetLogger(logger)
				notificationCenter.SetLogger(logger)
				return requireDesktop(application).OnStartup(lifecycleCtx)
			},
			OnDomReady: func(lifecycleCtx context.Context) error {
				return requireDesktop(application).OnDomReady(lifecycleCtx)
			},
			Stop: func(lifecycleCtx context.Context) error {
				return requireDesktop(application).StopFromHost(lifecycleCtx)
			},
			CloseBehavior: func(context.Context) (wailshost.CloseBehavior, error) {
				bindings := requireDesktop(application).Bindings()
				if bindings == nil {
					return "", errors.New("desktop bindings are not assembled")
				}
				preference, err := bindings.GetCloseBehavior()
				return wailshost.CloseBehavior(preference.Behavior), err
			},
			OnSuspend: environment.OnSuspend,
			OnResume:  environment.OnResume,
		},
		ErrorFormatter: func(err error) any { return (appRuntime.ErrorFormatter{}).Format(err) },
	})
	application = appRuntime.NewDesktopApplication(appRuntime.DesktopDependencies{
		Host:             host,
		Window:           host.Window(),
		WindowTheme:      windowTheme,
		Frontend:         host.Events(),
		Activation:       nonOwningActivationSource{WindowsInstanceBroker: entry.broker},
		Environment:      environment,
		Tray:             tray,
		Notifications:    notificationCenter,
		DirectoryAccess:  platformfilesystem.DirectoryAccess{},
		DirectoryChooser: host.DirectoryChooser(),
		Clock:            environment,
	})
	return application.Run(ctx, cfg, logger, store)
}

// requireDesktop 返回已经接线的桌面应用；生命周期早于接线属于编程错误。
func requireDesktop(application *appRuntime.DesktopApplication) *appRuntime.DesktopApplication {
	if application == nil {
		panic("desktop application lifecycle invoked before wiring completed")
	}
	return application
}

// nonOwningActivationSource 让 Runtime 停止 Pipe 和激活消费，但把 Mutex 的最终释放留给 bootstrap。
type nonOwningActivationSource struct {
	*platformwindows.WindowsInstanceBroker
}

// Close 停止接收新激活；bootstrap 在 Store、日志之后调用 broker.Close 释放 Mutex。
func (source nonOwningActivationSource) Close() error {
	if source.WindowsInstanceBroker == nil {
		return nil
	}
	return source.WindowsInstanceBroker.Stop()
}

// reportError 保持入口的错误文本输出和退出码映射行为，并允许不启动进程进行测试。
func reportError(writer io.Writer, err error) utils.ExitCode {
	if err == nil {
		return utils.ExitOK
	}
	_, _ = fmt.Fprintln(writer, err)
	return utils.ExitCodeFor(err)
}

var _ platform.ActivationSource = nonOwningActivationSource{}
