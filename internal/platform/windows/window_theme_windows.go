//go:build windows

package windows

import (
	"context"
	"errors"
	"fmt"
	"unsafe"

	"github.com/dengweitian0-svg/TimeCue/internal/platform"
	win "golang.org/x/sys/windows"
)

const (
	// MainWindowClassName 是 Wails 主窗口使用的稳定类名，供 HWND 查找使用。
	MainWindowClassName = "TimeCueMainWindow"

	dwmwaCaptionColor = 35
	dwmwaTextColor    = 36
)

var titlebarUser32 = win.NewLazySystemDLL("user32.dll")

var procFindWindowW = titlebarUser32.NewProc("FindWindowW")

type windowThemeAPI struct {
	findWindow   func(string) (win.HWND, error)
	setAttribute func(win.HWND, uint32, int32) error
}

// WindowsWindowThemeController 将主题颜色应用到 Wails 原生窗口标题栏。
type WindowsWindowThemeController struct {
	className string
	api       windowThemeAPI
}

// NewWindowsWindowThemeController 创建通过窗口类名定位 Wails 主窗口的标题栏适配器。
func NewWindowsWindowThemeController(className string) (platform.WindowThemeController, error) {
	if className == "" {
		return nil, errors.New("create window theme controller: window class name is empty")
	}
	return &WindowsWindowThemeController{
		className: className,
		api: windowThemeAPI{
			findWindow:   findWindowByClass,
			setAttribute: setDWMWindowAttribute,
		},
	}, nil
}

// newWindowsWindowThemeControllerWithAPI 创建可注入 Win32 调用的测试适配器。
func newWindowsWindowThemeControllerWithAPI(className string, api windowThemeAPI) *WindowsWindowThemeController {
	return &WindowsWindowThemeController{className: className, api: api}
}

// Apply 查找 Wails 主窗口并更新标题栏背景和文字颜色。
func (c *WindowsWindowThemeController) Apply(ctx context.Context, colors platform.WindowThemeColors) error {
	if c == nil {
		return errors.New("apply window theme: controller is nil")
	}
	if ctx == nil {
		return errors.New("apply window theme: context is nil")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("apply window theme: %w", err)
	}
	if c.api.findWindow == nil || c.api.setAttribute == nil {
		return errors.New("apply window theme: native API is not configured")
	}
	hwnd, err := c.api.findWindow(c.className)
	if err != nil {
		return fmt.Errorf("apply window theme: find main window: %w", err)
	}
	if err := c.api.setAttribute(hwnd, dwmwaCaptionColor, rgbToWindowsColor(colors.TitleBar)); err != nil {
		return fmt.Errorf("apply window theme: set caption color: %w", err)
	}
	// 标题栏背景成功后再设置文字；文字失败时保留错误，让上层记录并提示可重试。
	if err := c.api.setAttribute(hwnd, dwmwaTextColor, rgbToWindowsColor(colors.TitleText)); err != nil {
		return fmt.Errorf("apply window theme: set text color: %w", err)
	}
	return nil
}

// findWindowByClass 通过稳定窗口类名获取 Wails 主窗口句柄。
func findWindowByClass(className string) (win.HWND, error) {
	classNamePointer, err := win.UTF16PtrFromString(className)
	if err != nil {
		return 0, fmt.Errorf("encode window class name: %w", err)
	}
	hwnd, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(classNamePointer)), 0)
	if hwnd == 0 {
		return 0, fmt.Errorf("window class %q was not found", className)
	}
	return win.HWND(hwnd), nil
}

// setDWMWindowAttribute 设置 Windows DWM 的标题栏颜色属性。
func setDWMWindowAttribute(hwnd win.HWND, attribute uint32, color int32) error {
	if err := win.DwmSetWindowAttribute(hwnd, attribute, unsafe.Pointer(&color), uint32(unsafe.Sizeof(color))); err != nil {
		return err
	}
	return nil
}

// rgbToWindowsColor 将 RGB 通道转换为 Windows 使用的 0x00BBGGRR 整数。
func rgbToWindowsColor(color platform.RGBColor) int32 {
	return int32(uint32(color.B)<<16 | uint32(color.G)<<8 | uint32(color.R))
}

var _ platform.WindowThemeController = (*WindowsWindowThemeController)(nil)
