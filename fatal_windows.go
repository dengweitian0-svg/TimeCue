//go:build windows && !bindings

package main

import (
	"syscall"
	"unsafe"
)

// showFatalError 在日志边界不可用时使用原生 Windows 对话框展示最终错误。
func showFatalError(title string, err error) {
	if err == nil {
		return
	}
	titleUTF16, titleErr := syscall.UTF16PtrFromString(title)
	messageUTF16, messageErr := syscall.UTF16PtrFromString(err.Error())
	if titleErr != nil || messageErr != nil {
		return
	}
	_, _, _ = procMessageBox.Call(0, uintptr(unsafe.Pointer(messageUTF16)), uintptr(unsafe.Pointer(titleUTF16)), 0x00000010)
}

var procMessageBox = syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW")
