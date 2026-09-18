//go:build windows

package windows

import (
	"context"
	"errors"
	"testing"

	"github.com/dengweitian0-svg/TimeCue/internal/platform"
	win "golang.org/x/sys/windows"
)

// TestRGBToWindowsColor 验证 RGB 到 Windows BGR 整数的转换顺序。
func TestRGBToWindowsColor(t *testing.T) {
	if got := rgbToWindowsColor(platform.RGBColor{R: 0x12, G: 0x34, B: 0x56}); got != 0x563412 {
		t.Fatalf("rgbToWindowsColor() = %#x, want %#x", got, int32(0x563412))
	}
}

// TestWindowsWindowThemeControllerAppliesCaptionAndText 验证标题栏背景和文字属性按顺序应用。
func TestWindowsWindowThemeControllerAppliesCaptionAndText(t *testing.T) {
	type call struct {
		attribute uint32
		color     int32
	}
	var calls []call
	controller := newWindowsWindowThemeControllerWithAPI("TimeCueTestWindow", windowThemeAPI{
		findWindow: func(className string) (win.HWND, error) {
			if className != "TimeCueTestWindow" {
				t.Fatalf("findWindow() className = %q", className)
			}
			return win.HWND(42), nil
		},
		setAttribute: func(_ win.HWND, attribute uint32, color int32) error {
			calls = append(calls, call{attribute: attribute, color: color})
			return nil
		},
	})
	colors := platform.WindowThemeColors{TitleBar: platform.RGBColor{R: 1, G: 2, B: 3}, TitleText: platform.RGBColor{R: 4, G: 5, B: 6}}

	if err := controller.Apply(context.Background(), colors); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("setAttribute() calls = %d, want 2", len(calls))
	}
	if calls[0] != (call{attribute: dwmwaCaptionColor, color: 0x030201}) {
		t.Fatalf("caption call = %+v, want caption color", calls[0])
	}
	if calls[1] != (call{attribute: dwmwaTextColor, color: 0x060504}) {
		t.Fatalf("text call = %+v, want text color", calls[1])
	}
}

// TestWindowsWindowThemeControllerPropagatesNativeFailures 验证窗口查找和 DWM 失败不会被吞掉。
func TestWindowsWindowThemeControllerPropagatesNativeFailures(t *testing.T) {
	wantErr := errors.New("native failure")
	findFailure := newWindowsWindowThemeControllerWithAPI("TimeCueTestWindow", windowThemeAPI{
		findWindow: func(string) (win.HWND, error) { return 0, wantErr },
		setAttribute: func(win.HWND, uint32, int32) error {
			t.Fatal("setAttribute() called after find failure")
			return nil
		},
	})
	if err := findFailure.Apply(context.Background(), platform.WindowThemeColors{}); !errors.Is(err, wantErr) {
		t.Fatalf("Apply() find error = %v, want %v", err, wantErr)
	}

	setFailure := newWindowsWindowThemeControllerWithAPI("TimeCueTestWindow", windowThemeAPI{
		findWindow:   func(string) (win.HWND, error) { return win.HWND(42), nil },
		setAttribute: func(win.HWND, uint32, int32) error { return wantErr },
	})
	if err := setFailure.Apply(context.Background(), platform.WindowThemeColors{}); !errors.Is(err, wantErr) {
		t.Fatalf("Apply() DWM error = %v, want %v", err, wantErr)
	}
}

// TestWindowsWindowThemeControllerHonorsCancellation 验证取消的上下文不会调用 Win32 API。
func TestWindowsWindowThemeControllerHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	controller := newWindowsWindowThemeControllerWithAPI("TimeCueTestWindow", windowThemeAPI{
		findWindow: func(string) (win.HWND, error) {
			t.Fatal("findWindow() called after cancellation")
			return 0, nil
		},
		setAttribute: func(win.HWND, uint32, int32) error {
			t.Fatal("setAttribute() called after cancellation")
			return nil
		},
	})
	if err := controller.Apply(ctx, platform.WindowThemeColors{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Apply() error = %v, want context.Canceled", err)
	}
}
