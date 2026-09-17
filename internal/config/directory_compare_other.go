//go:build !windows

package config

import "path/filepath"

// equalDirectoryPath 按非 Windows 文件系统的大小写敏感语义比较目录。
func equalDirectoryPath(left, right string) bool {
	return filepath.Clean(left) == filepath.Clean(right)
}
