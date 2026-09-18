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

const notificationHistoryNotFoundHRESULT = uintptr(0x80070490)

// WinRTNotificationHistoryCleaner 清除当前桌面应用的 Windows Toast 历史，不参与发送和激活。
type WinRTNotificationHistoryCleaner struct{}

// NewWinRTNotificationHistoryCleaner 创建当前桌面应用的历史清理器。
func NewWinRTNotificationHistoryCleaner() *WinRTNotificationHistoryCleaner {
	return &WinRTNotificationHistoryCleaner{}
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
	if err := history.clear(); err != nil && !isNotificationHistoryNotFound(err) {
		return fmt.Errorf("clear current notification history: %w", err)
	}
	return nil
}

// isNotificationHistoryNotFound 判断清理目标不存在是否为可接受的幂等结果。
func isNotificationHistoryNotFound(err error) bool {
	var oleErr *ole.OleError
	return errors.As(err, &oleErr) && oleErr.Code() == notificationHistoryNotFoundHRESULT
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

// clear 清除当前应用发送的全部通知；带应用 ID 的重载只适用于同一应用包内的其他应用。
func (v *toastNotificationHistory) clear() error {
	hr, _, _ := syscall.SyscallN(v.VTable().Clear, uintptr(unsafe.Pointer(v)))
	if hr != 0 {
		return ole.NewError(hr)
	}
	return nil
}

// isWinRTAlreadyInitialized 接受 RoInitialize 的 S_FALSE 成功状态。
func isWinRTAlreadyInitialized(err error) bool {
	var oleErr *ole.OleError
	return errors.As(err, &oleErr) && oleErr.Code() == 1
}
