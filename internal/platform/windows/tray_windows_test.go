//go:build windows

package windows

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/platform"
	"golang.org/x/sys/windows"
)

// fakeTrayThread 提供不创建真实窗口的托盘消息线程测试替身。
type fakeTrayThread struct {
	mu            sync.Mutex
	handler       MessageHandler
	registers     int
	unregisters   int
	starts        int
	closes        int
	window        windows.Handle
	unregisterErr error
}

// Start 记录消息线程启动。
func (f *fakeTrayThread) Start(context.Context) error {
	f.mu.Lock()
	f.starts++
	f.mu.Unlock()
	return nil
}

// Close 记录消息线程关闭。
func (f *fakeTrayThread) Close() error {
	f.mu.Lock()
	f.closes++
	f.mu.Unlock()
	return nil
}

// RegisterHandler 保存托盘消息处理器。
func (f *fakeTrayThread) RegisterHandler(handler MessageHandler) (MessageHandlerToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.registers++
	f.handler = handler
	return 7, nil
}

// UnregisterHandler 记录处理器移除。
func (f *fakeTrayThread) UnregisterHandler(MessageHandlerToken) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unregisters++
	f.handler = nil
	return f.unregisterErr
}

// waitWindow 返回确定性的隐藏窗口句柄。
func (f *fakeTrayThread) waitWindow(context.Context) (windows.Handle, error) {
	return f.window, nil
}

// dispatch 模拟消息线程已取得处理器副本后分发一条消息。
func (f *fakeTrayThread) dispatch(message uint32, lParam uintptr) error {
	f.mu.Lock()
	handler := f.handler
	f.mu.Unlock()
	if handler == nil {
		return nil
	}
	return handler(message, 0, lParam)
}

// fakeTrayShell 记录图标生命周期并返回预设菜单命令。
type fakeTrayShell struct {
	mu       sync.Mutex
	adds     int
	deletes  int
	menus    int
	menu     uint16
	menuErr  error
	menuFunc func(windows.Handle) (uint16, error)
	tooltips []string
}

// Add 记录托盘图标添加。
func (f *fakeTrayShell) Add(_ windows.Handle, tooltip string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.adds++
	f.tooltips = append(f.tooltips, tooltip)
	return nil
}

// Delete 记录托盘图标删除。
func (f *fakeTrayShell) Delete(windows.Handle) error {
	f.mu.Lock()
	f.deletes++
	f.mu.Unlock()
	return nil
}

// ShowMenu 返回测试指定的唯一菜单命令。
func (f *fakeTrayShell) ShowMenu(window windows.Handle) (uint16, error) {
	f.mu.Lock()
	f.menus++
	menu, menuErr, menuFunc := f.menu, f.menuErr, f.menuFunc
	f.mu.Unlock()
	if menuFunc != nil {
		return menuFunc(window)
	}
	return menu, menuErr
}

// TestWindowsTrayDispatchesOnlyOpenAndExit 验证图标交互只产生两个正式回调。
func TestWindowsTrayDispatchesOnlyOpenAndExit(t *testing.T) {
	thread := &fakeTrayThread{window: 42}
	shell := &fakeTrayShell{}
	tray := newWindowsTray(thread, false, shell, "TimeCue test")
	var mu sync.Mutex
	opened, exited := 0, 0
	if err := tray.Start(context.Background(), platform.TrayCallbacks{
		OnOpen: func() { mu.Lock(); opened++; mu.Unlock() },
		OnExit: func() { mu.Lock(); exited++; mu.Unlock() },
	}); err != nil {
		t.Fatal(err)
	}
	if err := thread.dispatch(trayCallbackMessage, wmLeftButtonDblClk); err != nil {
		t.Fatal(err)
	}
	shell.menu = trayCommandOpen
	if err := thread.dispatch(trayCallbackMessage, wmRightButtonUp); err != nil {
		t.Fatal(err)
	}
	shell.menu = trayCommandExit
	if err := thread.dispatch(trayCallbackMessage, wmContextMenu); err != nil {
		t.Fatal(err)
	}
	if err := thread.dispatch(0x9999, wmRightButtonUp); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if opened != 2 || exited != 1 {
		t.Fatalf("callbacks = open %d, exit %d; want open 2, exit 1", opened, exited)
	}
}

// TestWindowsTraySuppressesDuplicateContextMenu 验证同一次右键操作的嵌套菜单消息只打开一次菜单。
func TestWindowsTraySuppressesDuplicateContextMenu(t *testing.T) {
	thread := &fakeTrayThread{window: 42}
	shell := &fakeTrayShell{menu: trayCommandOpen}
	tray := newWindowsTray(thread, false, shell, "TimeCue")
	opened := 0
	if err := tray.Start(context.Background(), platform.TrayCallbacks{
		OnOpen: func() { opened++ },
		OnExit: func() {},
	}); err != nil {
		t.Fatal(err)
	}
	shell.menuFunc = func(windows.Handle) (uint16, error) {
		if err := thread.dispatch(trayCallbackMessage, wmContextMenu); err != nil {
			t.Fatalf("duplicate context menu dispatch error = %v", err)
		}
		return trayCommandOpen, nil
	}
	if err := thread.dispatch(trayCallbackMessage, wmRightButtonUp); err != nil {
		t.Fatalf("initial tray menu dispatch error = %v", err)
	}
	if opened != 1 {
		t.Fatalf("open callbacks = %d, want 1", opened)
	}
	shell.mu.Lock()
	defer shell.mu.Unlock()
	if shell.menus != 1 {
		t.Fatalf("shown menus = %d, want 1", shell.menus)
	}
}

// TestWindowsTrayCloseWaitsForCallback 验证 Close 等待已进入的回调并保持幂等。
func TestWindowsTrayCloseWaitsForCallback(t *testing.T) {
	thread := &fakeTrayThread{window: 42}
	shell := &fakeTrayShell{menu: trayCommandExit}
	tray := newWindowsTray(thread, false, shell, "TimeCue")
	entered := make(chan struct{})
	release := make(chan struct{})
	if err := tray.Start(context.Background(), platform.TrayCallbacks{
		OnOpen: func() {},
		OnExit: func() { close(entered); <-release },
	}); err != nil {
		t.Fatal(err)
	}
	dispatched := make(chan error, 1)
	go func() { dispatched <- thread.dispatch(trayCallbackMessage, wmRightButtonUp) }()
	<-entered
	closed := make(chan error, 1)
	go func() { closed <- tray.Close() }()
	select {
	case err := <-closed:
		t.Fatalf("Close() returned before callback completed: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-dispatched; err != nil {
		t.Fatal(err)
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	if err := tray.Close(); err != nil {
		t.Fatal(err)
	}
	thread.mu.Lock()
	defer thread.mu.Unlock()
	if thread.unregisters != 1 || thread.closes != 0 {
		t.Fatalf("thread cleanup = unregister %d, close %d; want 1, 0", thread.unregisters, thread.closes)
	}
	if shell.deletes != 1 {
		t.Fatalf("icon deletes = %d, want 1", shell.deletes)
	}
}

// TestWindowsTrayReportsMenuFailure 验证菜单错误沿消息线程返回并由线程边界记录。
func TestWindowsTrayReportsMenuFailure(t *testing.T) {
	thread := &fakeTrayThread{window: 42}
	shell := &fakeTrayShell{menuErr: errors.New("menu unavailable")}
	tray := newWindowsTray(thread, false, shell, "TimeCue")
	if err := tray.Start(context.Background(), platform.TrayCallbacks{OnOpen: func() {}, OnExit: func() {}}); err != nil {
		t.Fatal(err)
	}
	if err := thread.dispatch(trayCallbackMessage, wmRightButtonUp); err == nil {
		t.Fatal("tray message error = nil")
	}
}
