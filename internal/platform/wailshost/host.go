// Package wailshost 将 Wails 生命周期收敛为 platform 的三个窄端口。
//
// Wails 具体函数由平台构造器注入；核心状态机不依赖 GUI，因此可以在没有
// WebView 的环境中验证关闭、生命周期和取消语义。
package wailshost

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/dengweitian0-svg/TimeCue/internal/platform"
)

var (
	// ErrRunAlreadyCalled 表示同一个 Host 被重复启动。
	ErrRunAlreadyCalled = errors.New("wails host run already called")
	// ErrRuntimeNotReady 表示 Wails 尚未完成 DOM ready 回调。
	ErrRuntimeNotReady = errors.New("wails runtime is not ready")
)

// CloseBehavior 是窗口关闭策略的最小平台无关表示。
type CloseBehavior string

const (
	// CloseBehaviorTray 表示拦截关闭并隐藏窗口。
	CloseBehaviorTray CloseBehavior = "tray"
	// CloseBehaviorExit 表示拦截关闭并进入正式 Stop。
	CloseBehaviorExit CloseBehavior = "exit"
)

// LifecycleCallbacks 是宿主转发给 Runtime 的生命周期端口。
type LifecycleCallbacks struct {
	OnStartup     func(context.Context) error
	OnDomReady    func(context.Context) error
	Stop          func(context.Context) error
	CloseBehavior func(context.Context) (CloseBehavior, error)
	OnSuspend     func() error
	OnResume      func() error
	OnShutdown    func(context.Context)
}

// RuntimeCallbacks 是 Wails Runtime API 的可注入最小集合。
type RuntimeCallbacks struct {
	Quit           func(context.Context) error
	WindowShow     func(context.Context) error
	WindowHide     func(context.Context) error
	WindowMinimise func(context.Context) error
	WindowActivate func(context.Context) error
	EventsEmit     func(context.Context, string, any) error
	OpenDirectory  func(context.Context, string, string) (string, error)
}

// RunCallbacks 是 Wails Run 需要注册的回调集合。
type RunCallbacks struct {
	OnStartup      func(context.Context)
	OnDomReady     func(context.Context)
	OnBeforeClose  func(context.Context) bool
	OnShutdown     func(context.Context)
	OnSuspend      func()
	OnResume       func()
	ErrorFormatter func(error) any
}

// Runner 同步执行 Wails 主循环；实现不得把主循环再次放入 goroutine。
type Runner func(RunCallbacks) error

// HostOptions 描述 Wails 宿主的注入依赖。
type HostOptions struct {
	Runner         Runner
	Runtime        RuntimeCallbacks
	Lifecycle      LifecycleCallbacks
	Logger         *slog.Logger
	ErrorFormatter func(error) any
}

// Host 实现 DesktopHost，并持有窗口和前端事件端口的共享生命周期状态。
type Host struct {
	runner    Runner
	runtime   RuntimeCallbacks
	lifecycle LifecycleCallbacks
	logger    *slog.Logger
	formatter func(error) any
	state     lifecycleState
	window    windowController
	events    frontendEvents

	runMu        sync.Mutex
	runCalled    bool
	stopOnce     sync.Once
	shutdownOnce sync.Once
	quitMu       sync.Mutex
	quitCalled   bool
	quitPending  bool
	lastErrMu    sync.Mutex
	lastErr      error
	terminalErr  error
}

// New 创建通过函数 seam 驱动的 Wails 宿主及其两个边界端口。
func New(options HostOptions) *Host {
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}
	host := &Host{runner: options.Runner, runtime: options.Runtime, lifecycle: options.Lifecycle, logger: logger, formatter: options.ErrorFormatter}
	host.window = windowController{state: &host.state, runtime: host.runtime}
	host.events = frontendEvents{state: &host.state, runtime: host.runtime}
	return host
}

// Window 返回使用宿主生命周期 context 的窗口控制端口。
func (h *Host) Window() platform.WindowController {
	if h == nil {
		return windowController{}
	}
	return h.window
}

// Events 返回使用宿主生命周期 context 的前端事件端口。
func (h *Host) Events() platform.FrontendEvents {
	if h == nil {
		return frontendEvents{}
	}
	return h.events
}

// DirectoryChooser 返回使用宿主生命周期 context 的目录选择端口。
func (h *Host) DirectoryChooser() platform.DirectoryChooser {
	if h == nil {
		return directoryChooser{}
	}
	return directoryChooser{state: &h.state, runtime: h.runtime}
}

// SetLogger 在 bootstrap 日志器就绪后替换构造期默认日志器。
func (h *Host) SetLogger(logger *slog.Logger) {
	if h == nil || logger == nil {
		return
	}
	h.lastErrMu.Lock()
	h.logger = logger
	h.lastErrMu.Unlock()
}

// Run 只同步调用一次 Wails 主循环，并监听外部取消进入正式 Stop。
func (h *Host) Run(ctx context.Context) error {
	if h == nil {
		return errors.New("wails host is nil")
	}
	h.runMu.Lock()
	if h.runCalled {
		h.runMu.Unlock()
		return ErrRunAlreadyCalled
	}
	h.runCalled = true
	h.runMu.Unlock()
	if h.runner == nil {
		return errors.New("wails host runner is not configured")
	}
	ctx = nonNilContext(ctx)
	watchStop := make(chan struct{})
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		select {
		case <-ctx.Done():
			h.requestStop(ctx, true)
		case <-watchStop:
		}
	}()
	err := h.runner(h.runCallbacks())
	close(watchStop)
	<-watchDone
	return errors.Join(err, h.terminalError())
}

// Quit 设置允许下一次关闭通过，再调用注入的 Wails runtime.Quit。
func (h *Host) Quit(ctx context.Context) error {
	if h == nil {
		return errors.New("wails host is nil")
	}
	lifecycleCtx, err := h.state.context(true)
	if err != nil {
		if errors.Is(err, ErrRuntimeNotReady) {
			return h.deferQuitUntilReady()
		}
		return err
	}
	h.state.setAllowQuit()
	h.quitMu.Lock()
	if h.quitCalled {
		h.quitMu.Unlock()
		return nil
	}
	h.quitCalled = true
	h.quitPending = false
	h.quitMu.Unlock()
	if h.runtime.Quit == nil {
		h.resetQuit()
		return errors.New("wails runtime quit is not configured")
	}
	if err := h.runtime.Quit(lifecycleCtx); err != nil {
		h.resetQuit()
		return fmt.Errorf("quit Wails host: %w", err)
	}
	return nil
}

// OnStartup 保存 Wails context 后转发启动事实，不调用 Runtime API。
func (h *Host) OnStartup(ctx context.Context) {
	if h == nil {
		return
	}
	ctx = nonNilContext(ctx)
	h.state.saveContext(ctx)
	if h.lifecycle.OnStartup != nil {
		if err := h.lifecycle.OnStartup(ctx); err != nil {
			h.recordTerminalError(fmt.Errorf("start desktop lifecycle: %w", err))
			h.requestStop(ctx, true)
		}
	}
}

// OnDomReady 转发 DOM ready，成功返回后才开放窗口和事件 Runtime API。
func (h *Host) OnDomReady(ctx context.Context) {
	if h == nil {
		return
	}
	ctx = nonNilContext(ctx)
	if h.state.isReady() {
		return
	}
	// Wails Runtime 从 OnDomReady 回调开始可用；先开放门闩，确保回调内初始化通知和事件源合法。
	h.state.markReady(ctx)
	if h.lifecycle.OnDomReady != nil {
		if err := h.lifecycle.OnDomReady(ctx); err != nil {
			h.recordTerminalError(fmt.Errorf("complete desktop DOM-ready lifecycle: %w", err))
			h.requestStop(ctx, true)
		}
	}
	h.runPendingQuit()
}

// OnBeforeClose 根据当前偏好隐藏窗口或异步进入唯一 Stop 路径。
func (h *Host) OnBeforeClose(_ context.Context) bool {
	if h == nil {
		return true
	}
	if h.state.allowQuit() {
		return false
	}
	lifecycleCtx, err := h.state.context(true)
	if err != nil {
		h.recordError(err)
		return true
	}
	if h.lifecycle.CloseBehavior == nil {
		h.recordError(errors.New("close behavior callback is not configured"))
		return true
	}
	behavior, err := h.lifecycle.CloseBehavior(lifecycleCtx)
	if err != nil {
		h.recordError(fmt.Errorf("read close behavior: %w", err))
		return true
	}
	switch behavior {
	case CloseBehaviorTray:
		if err := h.Window().Hide(lifecycleCtx); err != nil {
			h.recordError(err)
		}
		return true
	case CloseBehaviorExit:
		h.requestStop(lifecycleCtx, true)
		return true
	default:
		h.recordError(fmt.Errorf("unsupported close behavior %q", behavior))
		return true
	}
}

// OnShutdown 只触发一次幂等的正式停止兜底，不承担主要可重试清理。
func (h *Host) OnShutdown(ctx context.Context) {
	if h == nil {
		return
	}
	h.shutdownOnce.Do(func() {
		if h.lifecycle.OnShutdown != nil {
			h.lifecycle.OnShutdown(nonCancelled(ctx))
		}
		h.requestStop(ctx, false)
		h.state.markClosed()
	})
}

// OnSuspend 将 Wails Windows 电源回调转发给注入的环境端口。
func (h *Host) OnSuspend() {
	if h == nil || h.lifecycle.OnSuspend == nil {
		return
	}
	if err := h.lifecycle.OnSuspend(); err != nil {
		h.recordError(fmt.Errorf("handle Windows suspend: %w", err))
	}
}

// OnResume 将 Wails Windows 恢复回调转发给注入的环境端口。
func (h *Host) OnResume() {
	if h == nil || h.lifecycle.OnResume == nil {
		return
	}
	if err := h.lifecycle.OnResume(); err != nil {
		h.recordError(fmt.Errorf("handle Windows resume: %w", err))
	}
}

// LastError 返回最近一次生命周期或 Runtime 回调错误。
func (h *Host) LastError() error {
	if h == nil {
		return nil
	}
	h.lastErrMu.Lock()
	defer h.lastErrMu.Unlock()
	return h.lastErr
}

// runCallbacks 将 Host 生命周期方法转换为 Wails 回调形状。
func (h *Host) runCallbacks() RunCallbacks {
	return RunCallbacks{OnStartup: h.OnStartup, OnDomReady: h.OnDomReady, OnBeforeClose: h.OnBeforeClose, OnShutdown: h.OnShutdown, OnSuspend: h.OnSuspend, OnResume: h.OnResume, ErrorFormatter: h.formatter}
}

// requestStop 以非取消 context 异步调用唯一 Stop 路径，并按需请求退出宿主。
func (h *Host) requestStop(ctx context.Context, quitAfter bool) {
	h.stopOnce.Do(func() {
		go func() {
			cleanupCtx := nonCancelled(ctx)
			var err error
			if h.lifecycle.Stop == nil {
				err = errors.New("formal stop callback is not configured")
			} else {
				err = h.lifecycle.Stop(cleanupCtx)
			}
			if err != nil {
				h.recordError(err)
				return
			}
			if quitAfter {
				if err := h.Quit(cleanupCtx); err != nil {
					h.recordError(err)
				}
			}
		}()
	})
}

// resetQuit 允许 Runtime Quit 失败后再次尝试退出。
func (h *Host) resetQuit() {
	h.quitMu.Lock()
	h.quitCalled = false
	h.quitMu.Unlock()
}

// deferQuitUntilReady 在 Wails Runtime 尚未开放时记住退出请求，并防止启动竞态丢失请求。
func (h *Host) deferQuitUntilReady() error {
	h.quitMu.Lock()
	h.quitPending = true
	h.quitMu.Unlock()
	if _, err := h.state.context(true); err == nil {
		h.runPendingQuit()
	}
	return nil
}

// runPendingQuit 在 OnDomReady 开放 Runtime API 后执行早到的退出请求。
func (h *Host) runPendingQuit() {
	h.quitMu.Lock()
	pending := h.quitPending
	h.quitPending = false
	h.quitMu.Unlock()
	if !pending {
		return
	}
	if err := h.Quit(context.Background()); err != nil {
		h.recordError(err)
	}
}

// recordError 保存并记录边界错误，避免错误静默消失。
func (h *Host) recordError(err error) {
	if h == nil || err == nil {
		return
	}
	h.lastErrMu.Lock()
	h.lastErr = err
	logger := h.logger
	h.lastErrMu.Unlock()
	logger.Error("Wails desktop host error", "error", err)
}

// recordTerminalError 保存必须返回 Bootstrap 的启动阶段错误，最终 ERROR 由 Bootstrap 统一记录。
func (h *Host) recordTerminalError(err error) {
	if h == nil || err == nil {
		return
	}
	h.lastErrMu.Lock()
	h.lastErr = err
	h.terminalErr = errors.Join(h.terminalErr, err)
	h.lastErrMu.Unlock()
}

// terminalError 返回必须穿过主循环边界的生命周期错误。
func (h *Host) terminalError() error {
	h.lastErrMu.Lock()
	defer h.lastErrMu.Unlock()
	return h.terminalErr
}

// nonNilContext 为生命周期入口提供稳定的 context 默认值。
func nonNilContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// nonCancelled 返回保留 values 但不再响应取消的清理 context。
func nonCancelled(ctx context.Context) context.Context {
	return context.WithoutCancel(nonNilContext(ctx))
}

// lifecycleState 保存 Runtime context、DOM ready 和允许退出门闩。
type lifecycleState struct {
	mu            sync.RWMutex
	ctx           context.Context
	ready         bool
	allowQuitFlag bool
}

// saveContext 保存 Wails OnStartup 提供的 context。
func (s *lifecycleState) saveContext(ctx context.Context) {
	s.mu.Lock()
	s.ctx = ctx
	s.mu.Unlock()
}

// markReady 保存 DOM ready context 并开放 Runtime API。
func (s *lifecycleState) markReady(ctx context.Context) {
	s.mu.Lock()
	s.ctx = ctx
	s.ready = true
	s.mu.Unlock()
}

// markClosed 关闭 Runtime API，阻止前端销毁后的调用。
func (s *lifecycleState) markClosed() {
	s.mu.Lock()
	s.ready = false
	s.mu.Unlock()
}

// isReady 判断 Wails Runtime 是否已完成 DOM ready。
func (s *lifecycleState) isReady() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ready
}

// context 返回可供 Runtime 调用的保存 context。
func (s *lifecycleState) context(requireReady bool) (context.Context, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.ctx == nil || (requireReady && !s.ready) {
		return nil, ErrRuntimeNotReady
	}
	return s.ctx, nil
}

// setAllowQuit 设置关闭门闩，使 Wails runtime.Quit 触发的下一次关闭直接通过。
func (s *lifecycleState) setAllowQuit() {
	s.mu.Lock()
	s.allowQuitFlag = true
	s.mu.Unlock()
}

// allowQuit 读取关闭门闩。
func (s *lifecycleState) allowQuit() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.allowQuitFlag
}

// windowController 将三个窗口动作绑定到共享生命周期 context。
type windowController struct {
	state   *lifecycleState
	runtime RuntimeCallbacks
}

// Show 显示窗口并使用 OnDomReady 保存的 context。
func (w windowController) Show(_ context.Context) error {
	ctx, err := w.state.context(true)
	if err != nil {
		return err
	}
	if w.runtime.WindowShow == nil {
		return errors.New("wails runtime window show is not configured")
	}
	return w.runtime.WindowShow(ctx)
}

// Hide 隐藏窗口并使用 OnDomReady 保存的 context。
func (w windowController) Hide(_ context.Context) error {
	ctx, err := w.state.context(true)
	if err != nil {
		return err
	}
	if w.runtime.WindowHide == nil {
		return errors.New("wails runtime window hide is not configured")
	}
	return w.runtime.WindowHide(ctx)
}

// Minimize 最小化窗口并使用 OnDomReady 保存的 context。
func (w windowController) Minimize(_ context.Context) error {
	ctx, err := w.state.context(true)
	if err != nil {
		return err
	}
	if w.runtime.WindowMinimise == nil {
		return errors.New("wails runtime window minimise is not configured")
	}
	return w.runtime.WindowMinimise(ctx)
}

// Activate 将窗口带到前台，并使用 OnDomReady 保存的 context。
func (w windowController) Activate(_ context.Context) error {
	ctx, err := w.state.context(true)
	if err != nil {
		return err
	}
	if w.runtime.WindowActivate == nil {
		return errors.New("wails runtime window activate is not configured")
	}
	return w.runtime.WindowActivate(ctx)
}

// frontendEvents 将受控事件发送到 Wails Runtime。
type frontendEvents struct {
	state   *lifecycleState
	runtime RuntimeCallbacks
}

// directoryChooser 将目录选择对话框绑定到 Wails 的 Runtime context。
type directoryChooser struct {
	state   *lifecycleState
	runtime RuntimeCallbacks
}

// ChooseDirectory 打开目录选择器；空路径表示用户取消，不产生配置变更。
func (d directoryChooser) ChooseDirectory(_ context.Context, title, defaultDirectory string) (string, error) {
	if d.state == nil {
		return "", errors.New("wails directory chooser is not configured")
	}
	ctx, err := d.state.context(true)
	if err != nil {
		return "", err
	}
	if d.runtime.OpenDirectory == nil {
		return "", errors.New("wails runtime open directory is not configured")
	}
	return d.runtime.OpenDirectory(ctx, title, defaultDirectory)
}

// Emit 发送单个前端 payload，并始终使用保存的生命周期 context。
func (e frontendEvents) Emit(_ context.Context, name string, payload any) error {
	ctx, err := e.state.context(true)
	if err != nil {
		return err
	}
	if e.runtime.EventsEmit == nil {
		return errors.New("wails runtime events emit is not configured")
	}
	return e.runtime.EventsEmit(ctx, name, payload)
}

// SlogLogger 将 Wails logger.Logger 的级别映射到现有 slog。
type SlogLogger struct {
	logger *slog.Logger
}

// NewSlogLogger 创建使用既有结构化日志器的 Wails logger 适配器。
func NewSlogLogger(logger *slog.Logger) *SlogLogger {
	if logger == nil {
		logger = slog.Default()
	}
	return &SlogLogger{logger: logger}
}

// Print 将普通 Wails 日志映射为 INFO。
func (l *SlogLogger) Print(message string) { l.log(slog.LevelInfo, message) }

// Trace 将 Wails trace 映射为 DEBUG。
func (l *SlogLogger) Trace(message string) { l.log(slog.LevelDebug, message) }

// Debug 将 Wails debug 映射为 DEBUG。
func (l *SlogLogger) Debug(message string) { l.log(slog.LevelDebug, message) }

// Info 将 Wails info 映射为 INFO。
func (l *SlogLogger) Info(message string) { l.log(slog.LevelInfo, message) }

// Warning 将 Wails warning 映射为 WARN。
func (l *SlogLogger) Warning(message string) { l.log(slog.LevelWarn, message) }

// Error 将 Wails error 映射为 ERROR。
func (l *SlogLogger) Error(message string) { l.log(slog.LevelError, message) }

// Fatal 将 Wails fatal 映射为 ERROR 并保留 fatal 属性，不直接结束进程。
func (l *SlogLogger) Fatal(message string) {
	if l == nil || l.logger == nil {
		return
	}
	l.logger.LogAttrs(context.Background(), slog.LevelError, message, slog.Bool("fatal", true))
}

// log 使用统一 message 字段写入 Wails 日志。
func (l *SlogLogger) log(level slog.Level, message string) {
	if l == nil || l.logger == nil {
		return
	}
	l.logger.Log(context.Background(), level, message)
}

var (
	_ platform.DesktopHost      = (*Host)(nil)
	_ platform.WindowController = windowController{}
	_ platform.FrontendEvents   = frontendEvents{}
	_ platform.DirectoryChooser = directoryChooser{}
)
