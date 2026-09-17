//go:build windows

package windows

import (
	"fmt"
	"syscall"
	"time"
	"unsafe"

	"github.com/dengweitian0-svg/TimeCue/internal/schedule/app"
	"golang.org/x/sys/windows"
)

const (
	wmTimeChange    = 0x001E
	wmSettingChange = 0x001A
	wmQuit          = 0x0012
	hwndMessage     = windows.Handle(^uintptr(2))
	getMessageError = ^uintptr(0)
)

var (
	user32                = syscall.NewLazyDLL("user32.dll")
	procRegisterClassEx   = user32.NewProc("RegisterClassExW")
	procCreateWindowEx    = user32.NewProc("CreateWindowExW")
	procGetMessage        = user32.NewProc("GetMessageW")
	procTranslateMessage  = user32.NewProc("TranslateMessage")
	procDispatchMessage   = user32.NewProc("DispatchMessageW")
	procPostThreadMessage = user32.NewProc("PostThreadMessageW")
	procDestroyWindow     = user32.NewProc("DestroyWindow")
	procUnregisterClass   = user32.NewProc("UnregisterClassW")
	procDefWindowProc     = user32.NewProc("DefWindowProcW")
)

// NewEnvironmentClock 创建使用动态 Windows 时区规则的共享环境时钟。
func NewEnvironmentClock() (*EnvironmentClock, error) {
	thread, err := NewMessageThread(nil)
	if err != nil {
		return nil, fmt.Errorf("create environment message thread: %w", err)
	}
	clock, err := newWindowsEnvironmentClock(thread, true)
	if err != nil {
		_ = thread.Close()
	}
	return clock, err
}

// NewEnvironmentClockWithMessageThread 将环境时钟注册到调用方提供的共享消息线程。
func NewEnvironmentClockWithMessageThread(thread *MessageThread) (*EnvironmentClock, error) {
	if thread == nil {
		return nil, fmt.Errorf("create environment clock: message thread is nil")
	}
	return newWindowsEnvironmentClock(thread, false)
}

// newWindowsEnvironmentClock 创建时钟并将其处理器注册到指定消息线程。
func newWindowsEnvironmentClock(thread *MessageThread, ownsThread bool) (*EnvironmentClock, error) {
	clock, err := newEnvironmentClock(EnvironmentClockOptions{
		Now:            time.Now,
		LoadLocation:   loadDynamicLocation,
		EventQueueSize: 32,
	})
	if err != nil {
		return nil, err
	}
	token, err := thread.RegisterHandler(func(message uint32, _, _ uintptr) error {
		switch message {
		case wmTimeChange:
			return clock.NotifyClockChanged()
		case wmSettingChange:
			// Windows 不保证 WM_SETTINGCHANGE 的 lParam 提供稳定的时区标识；低频设置变化统一刷新动态规则，避免漏掉真实变化。
			return clock.NotifyTimeZoneChanged()
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("register environment message handler: %w", err)
	}
	clock.thread = thread
	clock.threadToken = token
	clock.ownsThread = ownsThread
	return clock, nil
}

// winSystemTime 是 Windows SYSTEMTIME 的布局。
type winSystemTime struct {
	Year         uint16
	Month        uint16
	DayOfWeek    uint16
	Day          uint16
	Hour         uint16
	Minute       uint16
	Second       uint16
	Milliseconds uint16
}

// winTimeZoneInformation 是 Windows TIME_ZONE_INFORMATION 的布局。
type winTimeZoneInformation struct {
	Bias         int32
	StandardName [32]uint16
	StandardDate winSystemTime
	StandardBias int32
	DaylightName [32]uint16
	DaylightDate winSystemTime
	DaylightBias int32
}

// winDynamicTimeZoneInformation 是 Windows DYNAMIC_TIME_ZONE_INFORMATION 的布局。
type winDynamicTimeZoneInformation struct {
	winTimeZoneInformation
	TimeZoneKeyName             [128]uint16
	DynamicDaylightTimeDisabled uint32
}

// newEnvironmentRule 从 Windows API 结构转换为跨平台规则。
func newEnvironmentRule(info winTimeZoneInformation, name string) TimeZoneRule {
	return TimeZoneRule{
		Name: name, Bias: info.Bias, StandardBias: info.StandardBias, DaylightBias: info.DaylightBias,
		StandardDate: SystemTimeRule{Month: info.StandardDate.Month, DayOfWeek: info.StandardDate.DayOfWeek, Day: info.StandardDate.Day, Hour: info.StandardDate.Hour, Minute: info.StandardDate.Minute, Second: info.StandardDate.Second},
		DaylightDate: SystemTimeRule{Month: info.DaylightDate.Month, DayOfWeek: info.DaylightDate.DayOfWeek, Day: info.DaylightDate.Day, Hour: info.DaylightDate.Hour, Minute: info.DaylightDate.Minute, Second: info.DaylightDate.Second},
	}
}

// loadDynamicLocation 读取逐年 Windows 时区规则并生成包含未来转换的 TZif。
func loadDynamicLocation() (*time.Location, error) {
	var dynamic winDynamicTimeZoneInformation
	result, _, callErr := procGetDynamicTimeZoneInformation.Call(uintptr(unsafe.Pointer(&dynamic)))
	if result == ^uintptr(0) {
		return nil, fmt.Errorf("get dynamic time zone information: %w", callErr)
	}
	name := windows.UTF16ToString(dynamic.TimeZoneKeyName[:])
	if name == "" {
		name = "TimeCueWindows"
	}
	rules := make(map[int]TimeZoneRule)
	for year := 1970; year <= 2200; year++ {
		var info winTimeZoneInformation
		ok, _, yearErr := procGetTimeZoneInformationForYear.Call(uintptr(year), uintptr(unsafe.Pointer(&dynamic)), uintptr(unsafe.Pointer(&info)))
		if ok == 0 {
			if yearErr != windows.ERROR_CALL_NOT_IMPLEMENTED && yearErr != windows.ERROR_PROC_NOT_FOUND {
				return nil, fmt.Errorf("get time zone rules for year %d: %w", year, yearErr)
			}
			info = dynamic.winTimeZoneInformation
		}
		rules[year] = newEnvironmentRule(info, name)
	}
	location, err := BuildLocationFromRules(name, rules, 1970, 2200)
	if err != nil {
		return nil, fmt.Errorf("build dynamic time zone %q: %w", name, err)
	}
	return location, nil
}

// wndClassEx 是 RegisterClassExW 所需的最小窗口类结构。
type wndClassEx struct {
	Size        uint32
	Style       uint32
	WindowProc  uintptr
	ClassExtra  int32
	WindowExtra int32
	Instance    windows.Handle
	Icon        windows.Handle
	Cursor      windows.Handle
	Background  windows.Handle
	MenuName    *uint16
	ClassName   *uint16
	IconSmall   windows.Handle
}

// winMessage 是 GetMessageW 使用的消息结构。
type winMessage struct {
	Window  windows.Handle
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	PointX  int32
	PointY  int32
	Private uint32
}

var procGetDynamicTimeZoneInformation = syscall.NewLazyDLL("kernel32.dll").NewProc("GetDynamicTimeZoneInformation")
var procGetTimeZoneInformationForYear = syscall.NewLazyDLL("kernel32.dll").NewProc("GetTimeZoneInformationForYear")

var _ app.Clock = (*EnvironmentClock)(nil)
