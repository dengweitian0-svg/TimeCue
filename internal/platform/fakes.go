package platform

import (
	"context"
	"errors"
	"sync"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/app"
)

// FakeDesktopHost 是桌面宿主契约的可控测试实现。
type FakeDesktopHost struct {
	RunFunc  func(context.Context) error
	QuitFunc func(context.Context) error

	mu        sync.Mutex
	RunCount  int
	QuitCount int
}

// Run 记录主循环调用并执行测试钩子。
func (f *FakeDesktopHost) Run(ctx context.Context) error {
	if f == nil {
		return errors.New("nil fake desktop host")
	}
	f.mu.Lock()
	f.RunCount++
	hook := f.RunFunc
	f.mu.Unlock()
	if hook != nil {
		return hook(ctx)
	}
	return nil
}

// Quit 记录退出请求并执行测试钩子。
func (f *FakeDesktopHost) Quit(ctx context.Context) error {
	if f == nil {
		return errors.New("nil fake desktop host")
	}
	f.mu.Lock()
	f.QuitCount++
	hook := f.QuitFunc
	f.mu.Unlock()
	if hook != nil {
		return hook(ctx)
	}
	return nil
}

// RunCalls 返回主循环调用次数，供并发测试安全读取。
func (f *FakeDesktopHost) RunCalls() int {
	if f == nil {
		return 0
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.RunCount
}

// QuitCalls 返回退出请求次数，供并发测试安全读取。
func (f *FakeDesktopHost) QuitCalls() int {
	if f == nil {
		return 0
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.QuitCount
}

// FakeWindowController 是窗口契约的可控测试实现。
type FakeWindowController struct {
	ShowFunc     func(context.Context) error
	HideFunc     func(context.Context) error
	MinimizeFunc func(context.Context) error
	ActivateFunc func(context.Context) error

	mu            sync.Mutex
	ShowCount     int
	HideCount     int
	MinimizeCount int
	ActivateCount int
}

// Show 记录显示窗口调用并执行测试钩子。
func (f *FakeWindowController) Show(ctx context.Context) error {
	if f == nil {
		return errors.New("nil fake window controller")
	}
	f.mu.Lock()
	f.ShowCount++
	hook := f.ShowFunc
	f.mu.Unlock()
	if hook != nil {
		return hook(ctx)
	}
	return nil
}

// Hide 记录隐藏窗口调用并执行测试钩子。
func (f *FakeWindowController) Hide(ctx context.Context) error {
	if f == nil {
		return errors.New("nil fake window controller")
	}
	f.mu.Lock()
	f.HideCount++
	hook := f.HideFunc
	f.mu.Unlock()
	if hook != nil {
		return hook(ctx)
	}
	return nil
}

// Minimize 记录最小化窗口调用并执行测试钩子。
func (f *FakeWindowController) Minimize(ctx context.Context) error {
	if f == nil {
		return errors.New("nil fake window controller")
	}
	f.mu.Lock()
	f.MinimizeCount++
	hook := f.MinimizeFunc
	f.mu.Unlock()
	if hook != nil {
		return hook(ctx)
	}
	return nil
}

// Activate 记录激活窗口调用并执行测试钩子。
func (f *FakeWindowController) Activate(ctx context.Context) error {
	if f == nil {
		return errors.New("nil fake window controller")
	}
	f.mu.Lock()
	f.ActivateCount++
	hook := f.ActivateFunc
	f.mu.Unlock()
	if hook != nil {
		return hook(ctx)
	}
	return nil
}

// ShowCalls 返回显示窗口调用次数，供并发测试安全读取。
func (f *FakeWindowController) ShowCalls() int {
	if f == nil {
		return 0
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ShowCount
}

// ActivateCalls 返回激活窗口调用次数，供并发测试安全读取。
func (f *FakeWindowController) ActivateCalls() int {
	if f == nil {
		return 0
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ActivateCount
}

// FrontendEvent 记录一次发送给前端的事件及其边界载荷。
type FrontendEvent struct {
	Name    string
	Payload any
}

// FakeFrontendEvents 是前端事件契约的可控测试实现。
type FakeFrontendEvents struct {
	EmitFunc func(context.Context, string, any) error

	mu     sync.Mutex
	Events []FrontendEvent
}

// Emit 记录事件并执行测试钩子。
func (f *FakeFrontendEvents) Emit(ctx context.Context, name string, payload any) error {
	if f == nil {
		return errors.New("nil fake frontend events")
	}
	f.mu.Lock()
	f.Events = append(f.Events, FrontendEvent{Name: name, Payload: payload})
	hook := f.EmitFunc
	f.mu.Unlock()
	if hook != nil {
		return hook(ctx, name, payload)
	}
	return nil
}

// EventCount 返回已记录的前端事件数量，供并发测试安全读取。
func (f *FakeFrontendEvents) EventCount() int {
	if f == nil {
		return 0
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.Events)
}

// FakeActivationSource 是激活事件源的可控测试实现。
type FakeActivationSource struct {
	StartFunc func(context.Context) error
	CloseFunc func() error

	mu      sync.Mutex
	sendMu  sync.Mutex
	EventsC chan Activation
	done    chan struct{}
	Started bool
	Closed  bool
}

// NewFakeActivationSource 创建带缓冲事件流的激活事件源。
func NewFakeActivationSource(buffer int) *FakeActivationSource {
	if buffer < 0 {
		buffer = 0
	}
	return &FakeActivationSource{EventsC: make(chan Activation, buffer), done: make(chan struct{})}
}

// Start 标记激活事件源已启动并执行测试钩子。
func (f *FakeActivationSource) Start(ctx context.Context) error {
	if f == nil {
		return errors.New("nil fake activation source")
	}
	f.mu.Lock()
	f.Started = true
	hook := f.StartFunc
	f.mu.Unlock()
	if hook != nil {
		return hook(ctx)
	}
	return nil
}

// Events 返回激活测试事件流。
func (f *FakeActivationSource) Events() <-chan Activation {
	if f == nil {
		return nil
	}
	return f.EventsC
}

// Emit 向激活测试事件流写入一条事件。
func (f *FakeActivationSource) Emit(event Activation) error {
	if f == nil || f.EventsC == nil {
		return errors.New("fake activation source is not initialized")
	}
	f.sendMu.Lock()
	defer f.sendMu.Unlock()
	f.mu.Lock()
	closed := f.Closed
	f.ensureDoneLocked()
	done := f.done
	events := f.EventsC
	f.mu.Unlock()
	if closed {
		return errors.New("fake activation source is closed")
	}
	select {
	case events <- event:
		return nil
	case <-done:
		return errors.New("fake activation source is closed")
	}
}

// Close 关闭激活测试事件流并执行测试钩子。
func (f *FakeActivationSource) Close() error {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	if f.Closed {
		f.mu.Unlock()
		return nil
	}
	f.Closed = true
	f.ensureDoneLocked()
	close(f.done)
	hook := f.CloseFunc
	events := f.EventsC
	f.mu.Unlock()
	f.sendMu.Lock()
	if events != nil {
		close(events)
	}
	f.sendMu.Unlock()
	if hook != nil {
		return hook()
	}
	return nil
}

// ensureDoneLocked 确保事件源拥有可用于并发关闭通知的内部信号；调用方必须持有 mu。
func (f *FakeActivationSource) ensureDoneLocked() {
	if f.done == nil {
		f.done = make(chan struct{})
	}
}

// FakeEnvironmentEventSource 是运行时环境事件源的可控测试实现。
type FakeEnvironmentEventSource struct {
	StartFunc func(context.Context) error
	CloseFunc func() error

	mu      sync.Mutex
	sendMu  sync.Mutex
	EventsC chan app.RuntimeEvent
	done    chan struct{}
	Started bool
	Closed  bool
}

// NewFakeEnvironmentEventSource 创建带缓冲事件流的环境事件源。
func NewFakeEnvironmentEventSource(buffer int) *FakeEnvironmentEventSource {
	if buffer < 0 {
		buffer = 0
	}
	return &FakeEnvironmentEventSource{EventsC: make(chan app.RuntimeEvent, buffer), done: make(chan struct{})}
}

// Start 标记环境事件源已启动并执行测试钩子。
func (f *FakeEnvironmentEventSource) Start(ctx context.Context) error {
	if f == nil {
		return errors.New("nil fake environment event source")
	}
	f.mu.Lock()
	f.Started = true
	hook := f.StartFunc
	f.mu.Unlock()
	if hook != nil {
		return hook(ctx)
	}
	return nil
}

// Events 返回环境测试事件流。
func (f *FakeEnvironmentEventSource) Events() <-chan app.RuntimeEvent {
	if f == nil {
		return nil
	}
	return f.EventsC
}

// Emit 向环境测试事件流写入一条运行时事件。
func (f *FakeEnvironmentEventSource) Emit(event app.RuntimeEvent) error {
	if f == nil || f.EventsC == nil {
		return errors.New("fake environment event source is not initialized")
	}
	f.sendMu.Lock()
	defer f.sendMu.Unlock()
	f.mu.Lock()
	closed := f.Closed
	f.ensureDoneLocked()
	done := f.done
	events := f.EventsC
	f.mu.Unlock()
	if closed {
		return errors.New("fake environment event source is closed")
	}
	select {
	case events <- event:
		return nil
	case <-done:
		return errors.New("fake environment event source is closed")
	}
}

// Close 关闭环境测试事件流并执行测试钩子。
func (f *FakeEnvironmentEventSource) Close() error {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	if f.Closed {
		f.mu.Unlock()
		return nil
	}
	f.Closed = true
	f.ensureDoneLocked()
	close(f.done)
	hook := f.CloseFunc
	events := f.EventsC
	f.mu.Unlock()
	f.sendMu.Lock()
	if events != nil {
		close(events)
	}
	f.sendMu.Unlock()
	if hook != nil {
		return hook()
	}
	return nil
}

// ensureDoneLocked 确保事件源拥有可用于并发关闭通知的内部信号；调用方必须持有 mu。
func (f *FakeEnvironmentEventSource) ensureDoneLocked() {
	if f.done == nil {
		f.done = make(chan struct{})
	}
}

// FakeTrayPort 是托盘契约的可控测试实现。
type FakeTrayPort struct {
	StartFunc func(context.Context, TrayCallbacks) error
	CloseFunc func() error

	mu        sync.Mutex
	Callbacks TrayCallbacks
	Started   bool
	Closed    bool
}

// Start 记录托盘回调并执行测试钩子。
func (f *FakeTrayPort) Start(ctx context.Context, callbacks TrayCallbacks) error {
	if f == nil {
		return errors.New("nil fake tray port")
	}
	f.mu.Lock()
	f.Callbacks = callbacks
	f.Started = true
	hook := f.StartFunc
	f.mu.Unlock()
	if hook != nil {
		return hook(ctx, callbacks)
	}
	return nil
}

// InvokeOpen 触发已注册的打开回调。
func (f *FakeTrayPort) InvokeOpen() {
	if f == nil {
		return
	}
	f.mu.Lock()
	if f.Closed {
		f.mu.Unlock()
		return
	}
	callback := f.Callbacks.OnOpen
	f.mu.Unlock()
	if callback != nil {
		callback()
	}
}

// InvokeExit 触发已注册的退出回调。
func (f *FakeTrayPort) InvokeExit() {
	if f == nil {
		return
	}
	f.mu.Lock()
	if f.Closed {
		f.mu.Unlock()
		return
	}
	callback := f.Callbacks.OnExit
	f.mu.Unlock()
	if callback != nil {
		callback()
	}
}

// Close 标记托盘关闭并执行测试钩子。
func (f *FakeTrayPort) Close() error {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	if f.Closed {
		f.mu.Unlock()
		return nil
	}
	f.Closed = true
	hook := f.CloseFunc
	f.mu.Unlock()
	if hook != nil {
		return hook()
	}
	return nil
}

// FakeNotificationCenter 是通知契约的可控测试实现。
type FakeNotificationCenter struct {
	InitializeFunc func(context.Context) error
	SubmitFunc     func(context.Context, app.ReminderNotification) error
	CleanupFunc    func(context.Context) error
	CloseFunc      func() error

	mu           sync.Mutex
	Initialized  bool
	Closed       bool
	Submissions  []app.ReminderNotification
	CleanupCount int
}

// Initialize 标记通知中心已初始化并执行测试钩子。
func (f *FakeNotificationCenter) Initialize(ctx context.Context) error {
	if f == nil {
		return errors.New("nil fake notification center")
	}
	f.mu.Lock()
	f.Initialized = true
	hook := f.InitializeFunc
	f.mu.Unlock()
	if hook != nil {
		return hook(ctx)
	}
	return nil
}

// Submit 记录一条提醒提交并执行测试钩子。
func (f *FakeNotificationCenter) Submit(ctx context.Context, notification app.ReminderNotification) error {
	if f == nil {
		return errors.New("nil fake notification center")
	}
	f.mu.Lock()
	if f.Closed {
		f.mu.Unlock()
		return errors.New("fake notification center is closed")
	}
	f.Submissions = append(f.Submissions, notification)
	hook := f.SubmitFunc
	f.mu.Unlock()
	if hook != nil {
		return hook(ctx, notification)
	}
	return nil
}

// Cleanup 记录一次系统通知清理并执行测试钩子。
func (f *FakeNotificationCenter) Cleanup(ctx context.Context) error {
	if f == nil {
		return errors.New("nil fake notification center")
	}
	f.mu.Lock()
	f.CleanupCount++
	hook := f.CleanupFunc
	f.mu.Unlock()
	if hook != nil {
		return hook(ctx)
	}
	return nil
}

// Close 标记通知中心关闭并执行测试钩子。
func (f *FakeNotificationCenter) Close() error {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	if f.Closed {
		f.mu.Unlock()
		return nil
	}
	f.Closed = true
	hook := f.CloseFunc
	f.mu.Unlock()
	if hook != nil {
		return hook()
	}
	return nil
}
