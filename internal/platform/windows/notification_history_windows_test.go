//go:build windows

package windows

import (
	"fmt"
	"syscall"
	"testing"
	"unsafe"

	"github.com/go-ole/go-ole"
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

// TestIsNotificationHistoryNotFound 仅将不存在的当前应用通知历史视为幂等成功。
func TestIsNotificationHistoryNotFound(t *testing.T) {
	if !isNotificationHistoryNotFound(fmt.Errorf("clear: %w", ole.NewError(notificationHistoryNotFoundHRESULT))) {
		t.Fatal("not-found HRESULT was not recognized")
	}
	if isNotificationHistoryNotFound(ole.NewError(0x80004005)) {
		t.Fatal("unrelated HRESULT was incorrectly recognized")
	}
}
