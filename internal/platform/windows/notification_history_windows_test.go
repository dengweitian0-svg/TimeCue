//go:build windows

package windows

import (
	"syscall"
	"testing"
	"unsafe"
)

// TestToastNotificationHistoryClearUsesCurrentAppMethod 验证历史清理调用当前应用的无参数 Clear。
func TestToastNotificationHistoryClearUsesCurrentAppMethod(t *testing.T) {
	var calledWith uintptr
	clear := syscall.NewCallback(func(history uintptr) uintptr {
		calledWith = history
		return 0
	})
	vtable := toastNotificationHistoryVtbl{Clear: clear}
	history := &toastNotificationHistory{}
	history.RawVTable = (*interface{})(unsafe.Pointer(&vtable))

	if err := history.clear(); err != nil {
		t.Fatalf("clear() error = %v", err)
	}
	if calledWith != uintptr(unsafe.Pointer(history)) {
		t.Fatalf("clear() receiver = %#x, want %#x", calledWith, uintptr(unsafe.Pointer(history)))
	}
}
