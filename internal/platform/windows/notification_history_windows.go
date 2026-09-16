//go:build windows

package windows

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"github.com/go-ole/go-ole"
)

const toastNotificationManagerClass = "Windows.UI.Notifications.ToastNotificationManager"

const toastNotificationHistory2IID = "3bc3d253-2f31-4092-9129-8ad5abf067da"

// WinRTNotificationHistoryCleaner 按应用身份清除 Windows Toast 历史，不参与发送和激活。
type WinRTNotificationHistoryCleaner struct {
	applicationUserModelID string
}

// NewWinRTNotificationHistoryCleaner 创建绑定到单一应用身份的历史清理器。
func NewWinRTNotificationHistoryCleaner(applicationUserModelID string) (*WinRTNotificationHistoryCleaner, error) {
	if applicationUserModelID == "" {
		return nil, errors.New("notification application user model ID must not be empty")
	}
	return &WinRTNotificationHistoryCleaner{applicationUserModelID: applicationUserModelID}, nil
}

// Clear 在当前调用线程初始化 WinRT，并只清理当前应用身份的通知历史。
func (c *WinRTNotificationHistoryCleaner) Clear(ctx context.Context) error {
	if c == nil {
		return errors.New("clear notification history: cleaner is nil")
	}
	if ctx == nil {
		return errors.New("clear notification history: context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// WinRT 初始化与接口调用必须发生在同一个 OS 线程，不能允许 Go 调度器迁移 goroutine。
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := ole.RoInitialize(1); err != nil && !isWinRTAlreadyInitialized(err) {
		return fmt.Errorf("initialize WinRT: %w", err)
	}
	defer ole.CoUninitialize()
	factory, err := ole.RoGetActivationFactory(toastNotificationManagerClass, ole.NewGUID("d6f5f569-d40d-407c-8989-88cab42cfd14"))
	if err != nil {
		return fmt.Errorf("get toast notification manager: %w", err)
	}
	defer factory.Release()
	manager := (*toastNotificationManagerStatics)(unsafe.Pointer(factory))
	userManager, err := manager.getDefault()
	if err != nil {
		return fmt.Errorf("get current notification user: %w", err)
	}
	defer userManager.Release()
	history, err := userManager.getHistory()
	if err != nil {
		return fmt.Errorf("get notification history: %w", err)
	}
	defer history.Release()
	if err := history.clearWithID(c.applicationUserModelID); err != nil {
		return fmt.Errorf("clear notification history for %q: %w", c.applicationUserModelID, err)
	}
	return nil
}

// toastNotificationManagerStatics 是 WinRT 静态工厂的最小 ABI 视图。
type toastNotificationManagerStatics struct {
	ole.IInspectable
}

// toastNotificationManagerStaticsVtbl 声明 GetDefault 的稳定 vtable 槽位。
type toastNotificationManagerStaticsVtbl struct {
	ole.IInspectableVtbl
	GetDefault uintptr
}

// VTable 返回静态工厂的 ABI 表。
func (v *toastNotificationManagerStatics) VTable() *toastNotificationManagerStaticsVtbl {
	return (*toastNotificationManagerStaticsVtbl)(unsafe.Pointer(v.RawVTable))
}

// getDefault 获取当前登录用户的通知管理器。
func (v *toastNotificationManagerStatics) getDefault() (*toastNotificationManagerForUser, error) {
	var result *toastNotificationManagerForUser
	hr, _, _ := syscall.SyscallN(v.VTable().GetDefault, 0, uintptr(unsafe.Pointer(&result)))
	if hr != 0 {
		return nil, ole.NewError(hr)
	}
	return result, nil
}

// toastNotificationManagerForUser 是用户通知管理器的最小 ABI 视图。
type toastNotificationManagerForUser struct {
	ole.IInspectable
}

// toastNotificationManagerForUserVtbl 声明 GetHistory 的稳定 vtable 槽位。
type toastNotificationManagerForUserVtbl struct {
	ole.IInspectableVtbl
	CreateToastNotifier       uintptr
	CreateToastNotifierWithID uintptr
	GetHistory                uintptr
	GetUser                   uintptr
}

// VTable 返回用户管理器的 ABI 表。
func (v *toastNotificationManagerForUser) VTable() *toastNotificationManagerForUserVtbl {
	return (*toastNotificationManagerForUserVtbl)(unsafe.Pointer(v.RawVTable))
}

// getHistory 获取该用户的通知历史对象。
func (v *toastNotificationManagerForUser) getHistory() (*toastNotificationHistory, error) {
	var result *toastNotificationHistory
	hr, _, _ := syscall.SyscallN(v.VTable().GetHistory, uintptr(unsafe.Pointer(v)), uintptr(unsafe.Pointer(&result)))
	if hr != 0 {
		return nil, ole.NewError(hr)
	}
	return result, nil
}

// toastNotificationHistory 是 ToastNotificationHistory 的最小 ABI 视图。
type toastNotificationHistory struct {
	ole.IInspectable
}

// toastNotificationHistoryVtbl 声明 Clear 的稳定 vtable 槽位。
type toastNotificationHistoryVtbl struct {
	ole.IInspectableVtbl
	Clear       uintptr
	RemoveGroup uintptr
	Remove      uintptr
	GetHistory  uintptr
}

// VTable 返回通知历史对象的 ABI 表。
func (v *toastNotificationHistory) VTable() *toastNotificationHistoryVtbl {
	return (*toastNotificationHistoryVtbl)(unsafe.Pointer(v.RawVTable))
}

// clearWithID 通过 IToastNotificationHistory2 只清除指定桌面应用身份的历史。
func (v *toastNotificationHistory) clearWithID(applicationID string) error {
	var history2 *toastNotificationHistory2
	if err := v.PutQueryInterface(ole.NewGUID(toastNotificationHistory2IID), &history2); err != nil {
		return fmt.Errorf("query app-scoped notification history: %w", err)
	}
	defer history2.Release()
	hstring, err := ole.NewHString(applicationID)
	if err != nil {
		return fmt.Errorf("encode application identity: %w", err)
	}
	defer ole.DeleteHString(hstring)
	hr, _, _ := syscall.SyscallN(history2.VTable().ClearWithID, uintptr(unsafe.Pointer(history2)), uintptr(hstring))
	if hr != 0 {
		return ole.NewError(hr)
	}
	return nil
}

// toastNotificationHistory2 是支持显式 AUMID 清理的 WinRT 历史接口。
type toastNotificationHistory2 struct {
	ole.IInspectable
}

// toastNotificationHistory2Vtbl 声明应用身份版本的历史操作槽位。
type toastNotificationHistory2Vtbl struct {
	ole.IInspectableVtbl
	ClearWithID       uintptr
	RemoveGroupWithID uintptr
	RemoveWithID      uintptr
	GetHistoryWithID  uintptr
}

// VTable 返回 app-scoped 历史接口的 ABI 表。
func (v *toastNotificationHistory2) VTable() *toastNotificationHistory2Vtbl {
	return (*toastNotificationHistory2Vtbl)(unsafe.Pointer(v.RawVTable))
}

// isWinRTAlreadyInitialized 接受 RoInitialize 的 S_FALSE 成功状态。
func isWinRTAlreadyInitialized(err error) bool {
	var oleErr *ole.OleError
	return errors.As(err, &oleErr) && oleErr.Code() == 1
}
