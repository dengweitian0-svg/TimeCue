//go:build windows

package config

import (
	"path/filepath"
	"strings"
)

// equalDirectoryPath 按 Windows 不区分大小写的文件系统语义比较目录。
func equalDirectoryPath(left, right string) bool {
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}
