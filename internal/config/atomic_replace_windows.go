//go:build windows

package config

import "golang.org/x/sys/windows"

// replaceFile 使用 Windows 原生替换语义，避免 os.Rename 在目标存在时失败。
func replaceFile(source, target string) error {
	return windows.MoveFileEx(
		windows.StringToUTF16Ptr(source),
		windows.StringToUTF16Ptr(target),
		windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH,
	)
}
