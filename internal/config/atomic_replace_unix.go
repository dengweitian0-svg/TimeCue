//go:build !windows

package config

import "os"

// replaceFile 在非 Windows 系统上使用同文件系统 rename 完成原子替换。
func replaceFile(source, target string) error {
	return os.Rename(source, target)
}
