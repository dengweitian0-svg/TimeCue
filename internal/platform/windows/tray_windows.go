//go:build windows

package windows

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"syscall"
	"unsafe"

	"github.com/dengweitian0-svg/TimeCue/internal/platform"
	"golang.org/x/sys/windows"
)

const (
	trayCallbackMessage = 0x8000 + 14
	trayIconID          = 1
	trayCommandOpen     = 1
	trayCommandExit     = 2

	nimAdd        = 0x00000000
	nimDelete     = 0x00000002
	nimSetVersion = 0x00000004
	nifMessage    = 0x00000001
	nifIcon       = 0x00000002
	nifTip        = 0x00000004
	notifyVersion = 4

	wmContextMenu             = 0x007B
	wmLeftButtonDblClk        = 0x0203
	wmRightButtonUp           = 0x0205
	mfString                  = 0x00000000
	tpmRightButton            = 0x0002
	tpmReturnCommand          = 0x0100
	tpmNoNotify               = 0x0080
	applicationIconResourceID = 3
)

// WindowsTrayOptions 配置托盘使用的共享消息线程及显示文本。
type WindowsTrayOptions struct {
	MessageThread     *MessageThread
	OwnsMessageThread bool
	Tooltip           string
}

// trayMessageThread 是托盘依赖的消息线程最小能力集合。
type trayMessageThread interface {
	Start(context.Context) error
	Close() error
	RegisterHandler(MessageHandler) (MessageHandlerToken, error)
	UnregisterHandler(MessageHandlerToken) error
	waitWindow(context.Context) (windows.Handle, error)
}

// trayShell 封装托盘图标和菜单所需的 Win32 调用，便于确定性测试。
type trayShell interface {
	Add(windows.Handle, string) error
	Delete(windows.Handle) error
	ShowMenu(windows.Handle) (uint16, error)
}

// WindowsTray 在共享 Win32 消息窗口上提供“打开”和“退出”两个托盘动作。
type WindowsTray struct {
	thread     trayMessageThread
	ownsThread bool
	shell      trayShell
	tooltip    string

	mu        sync.Mutex
	callbacks platform.TrayCallbacks
	token     MessageHandlerToken
	window    windows.Handle
	started   bool
	closed    bool
	menuOpen  bool
	inflight  sync.WaitGroup
}

// NewWindowsTray 创建与环境监听共用消息线程的 Windows 托盘。
func NewWindowsTray(options WindowsTrayOptions) (*WindowsTray, error) {
	thread := options.MessageThread
	ownsThread := options.OwnsMessageThread
	if thread == nil {
		var err error
		thread, err = NewMessageThread(nil)
		if err != nil {
			return nil, fmt.Errorf("create tray message thread: %w", err)
		}
		ownsThread = true
	}
	tooltip := options.Tooltip
	if tooltip == "" {
		tooltip = "TimeCue"
	}
	return newWindowsTray(thread, ownsThread, nativeTrayShell{}, tooltip), nil
}

// newWindowsTray 使用注入的消息线程和 Shell 实现创建可测试托盘。
func newWindowsTray(thread trayMessageThread, ownsThread bool, shell trayShell, tooltip string) *WindowsTray {
	return &WindowsTray{thread: thread, ownsThread: ownsThread, shell: shell, tooltip: tooltip}
}

// Start 注册托盘消息处理器，等待共享隐藏窗口就绪并添加通知区域图标。
func (t *WindowsTray) Start(ctx context.Context, callbacks platform.TrayCallbacks) error {
	if t == nil {
		return errors.New("start Windows tray: tray is nil")
	}
	if ctx == nil {
		return errors.New("start Windows tray: context is nil")
	}
	if callbacks.OnOpen == nil || callbacks.OnExit == nil {
		return errors.New("start Windows tray: open and exit callbacks are required")
	}
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return errors.New("start Windows tray: tray is closed")
	}
	if t.started {
		t.mu.Unlock()
		return nil
	}
	t.callbacks = callbacks
	t.mu.Unlock()
	token, err := t.thread.RegisterHandler(t.handleMessage)
	if err != nil {
		return fmt.Errorf("register tray message handler: %w", err)
	}
	if t.ownsThread {
		go func() {
			_ = t.thread.Start(ctx)
		}()
	}
	window, err := t.thread.waitWindow(ctx)
	if err != nil {
		_ = t.thread.UnregisterHandler(token)
		return fmt.Errorf("wait tray message window: %w", err)
	}
	if err := t.shell.Add(window, t.tooltip); err != nil {
		_ = t.thread.UnregisterHandler(token)
		if t.ownsThread {
			_ = t.thread.Close()
		}
		return fmt.Errorf("add tray icon: %w", err)
	}
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		_ = t.shell.Delete(window)
		_ = t.thread.UnregisterHandler(token)
		return errors.New("start Windows tray: tray closed while starting")
	}
	t.token = token
	t.window = window
	t.started = true
	t.mu.Unlock()
	return nil
}

// Close 阻止新回调、删除托盘图标并在拥有消息线程时等待其退出。
func (t *WindowsTray) Close() error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	token := t.token
	window := t.window
	started := t.started
	t.mu.Unlock()
	var closeErr error
	if token != 0 {
		closeErr = errors.Join(closeErr, t.thread.UnregisterHandler(token))
	}
	if started {
		closeErr = errors.Join(closeErr, t.shell.Delete(window))
	}
	t.inflight.Wait()
	if t.ownsThread {
		closeErr = errors.Join(closeErr, t.thread.Close())
	}
	return closeErr
}

// handleMessage 将图标交互转换为唯一的打开或退出回调。
func (t *WindowsTray) handleMessage(message uint32, _, lParam uintptr) error {
	if message != trayCallbackMessage {
		return nil
	}
	t.mu.Lock()
	if t.closed || !t.started {
		t.mu.Unlock()
		return nil
	}
	t.inflight.Add(1)
	window := t.window
	callbacks := t.callbacks
	t.mu.Unlock()
	defer t.inflight.Done()
	notification := uint16(lParam & 0xffff)
	switch notification {
	case wmLeftButtonDblClk:
		callbacks.OnOpen()
	case wmRightButtonUp, wmContextMenu:
		// 同一次右键操作可能在 TrackPopupMenu 的嵌套消息循环中同时送达两种消息；
		// 只允许一个菜单存在，避免第二次调用返回 Popup menu already active。
		t.mu.Lock()
		if t.menuOpen {
			t.mu.Unlock()
			return nil
		}
		t.menuOpen = true
		t.mu.Unlock()
		defer func() {
			t.mu.Lock()
			t.menuOpen = false
			t.mu.Unlock()
		}()
		command, err := t.shell.ShowMenu(window)
		if err != nil {
			return fmt.Errorf("show tray menu: %w", err)
		}
		switch command {
		case trayCommandOpen:
			callbacks.OnOpen()
		case trayCommandExit:
			callbacks.OnExit()
		}
	}
	return nil
}

// nativeTrayShell 使用 Shell_NotifyIcon 和弹出菜单实现托盘资源。
type nativeTrayShell struct{}

// Add 添加图标并切换到 NOTIFYICON_VERSION_4 回调语义。
func (nativeTrayShell) Add(window windows.Handle, tooltip string) error {
	icon, err := loadApplicationIcon()
	if err != nil {
		return err
	}
	data, err := newNotifyIconData(window, icon, tooltip)
	if err != nil {
		return err
	}
	if ok, _, callErr := procShellNotifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(&data))); ok == 0 {
		return fmt.Errorf("Shell_NotifyIcon NIM_ADD: %w", callErr)
	}
	data.Version = notifyVersion
	if ok, _, callErr := procShellNotifyIcon.Call(nimSetVersion, uintptr(unsafe.Pointer(&data))); ok == 0 {
		_, _, _ = procShellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&data)))
		return fmt.Errorf("Shell_NotifyIcon NIM_SETVERSION: %w", callErr)
	}
	return nil
}

// loadApplicationIcon 从当前 exe 的 Wails 资源中读取应用图标，保证托盘与窗口使用同一图标。
func loadApplicationIcon() (windows.Handle, error) {
	var module windows.Handle
	if err := windows.GetModuleHandleEx(0, nil, &module); err != nil {
		return 0, fmt.Errorf("get application module handle: %w", err)
	}
	icon, _, callErr := procLoadIcon.Call(uintptr(module), applicationIconResourceID)
	if icon == 0 {
		return 0, fmt.Errorf("load application icon resource %d: %w", applicationIconResourceID, callErr)
	}
	return windows.Handle(icon), nil
}

// Delete 从通知区域移除当前窗口拥有的图标。
func (nativeTrayShell) Delete(window windows.Handle) error {
	data := notifyIconData{Size: uint32(unsafe.Sizeof(notifyIconData{})), Window: window, ID: trayIconID}
	if ok, _, callErr := procShellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&data))); ok == 0 {
		return fmt.Errorf("Shell_NotifyIcon NIM_DELETE: %w", callErr)
	}
	return nil
}

// ShowMenu 在当前指针位置显示仅含打开和退出项的托盘菜单。
func (nativeTrayShell) ShowMenu(window windows.Handle) (uint16, error) {
	menu, _, callErr := procCreatePopupMenu.Call()
	if menu == 0 {
		return 0, fmt.Errorf("create tray menu: %w", callErr)
	}
	defer procDestroyMenu.Call(menu)
	openText, _ := windows.UTF16PtrFromString("打开 TimeCue")
	exitText, _ := windows.UTF16PtrFromString("退出")
	if ok, _, appendErr := procAppendMenu.Call(menu, mfString, trayCommandOpen, uintptr(unsafe.Pointer(openText))); ok == 0 {
		return 0, fmt.Errorf("append tray open command: %w", appendErr)
	}
	if ok, _, appendErr := procAppendMenu.Call(menu, mfString, trayCommandExit, uintptr(unsafe.Pointer(exitText))); ok == 0 {
		return 0, fmt.Errorf("append tray exit command: %w", appendErr)
	}
	var point winPoint
	if ok, _, pointErr := procGetCursorPos.Call(uintptr(unsafe.Pointer(&point))); ok == 0 {
		return 0, fmt.Errorf("read tray cursor position: %w", pointErr)
	}
	procSetForegroundWindow.Call(uintptr(window))
	command, _, trackErr := procTrackPopupMenu.Call(menu, tpmRightButton|tpmReturnCommand|tpmNoNotify, uintptr(point.X), uintptr(point.Y), 0, uintptr(window), 0)
	if command == 0 && trackErr != syscall.Errno(0) {
		return 0, fmt.Errorf("track tray menu: %w", trackErr)
	}
	return uint16(command), nil
}

// newNotifyIconData 构造固定图标身份及受长度限制的提示文字。
func newNotifyIconData(window, icon windows.Handle, tooltip string) (notifyIconData, error) {
	encoded, err := windows.UTF16FromString(tooltip)
	if err != nil {
		return notifyIconData{}, fmt.Errorf("encode tray tooltip: %w", err)
	}
	if len(encoded) > 128 {
		encoded = encoded[:128]
		encoded[127] = 0
	}
	data := notifyIconData{
		Window:          window,
		ID:              trayIconID,
		Flags:           nifMessage | nifIcon | nifTip,
		CallbackMessage: trayCallbackMessage,
		Icon:            icon,
	}
	data.Size = uint32(unsafe.Sizeof(data))
	copy(data.Tip[:], encoded)
	return data, nil
}

// notifyIconData 对应 Windows NOTIFYICONDATAW 的完整当前布局。
type notifyIconData struct {
	Size            uint32
	Window          windows.Handle
	ID              uint32
	Flags           uint32
	CallbackMessage uint32
	Icon            windows.Handle
	Tip             [128]uint16
	State           uint32
	StateMask       uint32
	Info            [256]uint16
	Version         uint32
	InfoTitle       [64]uint16
	InfoFlags       uint32
	GUIDItem        windows.GUID
	BalloonIcon     windows.Handle
}

// winPoint 对应 GetCursorPos 使用的 POINT。
type winPoint struct {
	X int32
	Y int32
}

var (
	shell32                 = syscall.NewLazyDLL("shell32.dll")
	procShellNotifyIcon     = shell32.NewProc("Shell_NotifyIconW")
	procLoadIcon            = user32.NewProc("LoadIconW")
	procCreatePopupMenu     = user32.NewProc("CreatePopupMenu")
	procAppendMenu          = user32.NewProc("AppendMenuW")
	procDestroyMenu         = user32.NewProc("DestroyMenu")
	procGetCursorPos        = user32.NewProc("GetCursorPos")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procTrackPopupMenu      = user32.NewProc("TrackPopupMenu")
)

var _ platform.TrayPort = (*WindowsTray)(nil)
