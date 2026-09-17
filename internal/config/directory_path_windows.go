//go:build windows

package config

import (
	"fmt"
	"strings"

	"golang.org/x/sys/windows"
)

// canonicalDirectoryPath 使用目录句柄取得 Windows 最终路径，覆盖大小写、junction、符号链接和 UNC 别名。
func canonicalDirectoryPath(path string) (string, error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	handle, err := windows.CreateFile(
		pathPtr,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(handle)

	buffer := make([]uint16, 256)
	for {
		length, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), 0)
		if err != nil {
			return "", err
		}
		if int(length) < len(buffer)-1 {
			return normalizeWindowsFinalPath(windows.UTF16ToString(buffer[:length])), nil
		}
		if len(buffer) >= 32768 {
			return "", fmt.Errorf("final directory path is too long")
		}
		buffer = make([]uint16, len(buffer)*2)
	}
}

// normalizeWindowsFinalPath 将 Windows 扩展路径恢复为稳定的 DOS/UNC 表达。
func normalizeWindowsFinalPath(path string) string {
	if strings.HasPrefix(path, `\\?\UNC\`) {
		return `\\` + strings.TrimPrefix(path, `\\?\UNC\`)
	}
	if strings.HasPrefix(path, `\\?\`) {
		return strings.TrimPrefix(path, `\\?\`)
	}
	return path
}
