//go:build !windows

package windows

import (
	"context"

	"github.com/dengweitian0-svg/TimeCue/internal/platform"
)

// MainWindowClassName 保持跨平台构造代码使用相同的窗口类名常量。
const MainWindowClassName = "TimeCueMainWindow"

// unsupportedWindowThemeController 是非 Windows 构建下的安全空实现。
type unsupportedWindowThemeController struct{}

// NewWindowsWindowThemeController 在非 Windows 平台返回不产生副作用的空实现。
func NewWindowsWindowThemeController(string) (platform.WindowThemeController, error) {
	return unsupportedWindowThemeController{}, nil
}

// Apply 在非 Windows 平台忽略原生标题栏更新，保持应用其余功能可测试。
func (unsupportedWindowThemeController) Apply(context.Context, platform.WindowThemeColors) error {
	return nil
}

var _ platform.WindowThemeController = unsupportedWindowThemeController{}
