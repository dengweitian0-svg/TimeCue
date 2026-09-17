//go:build !windows

package config

import "path/filepath"

// canonicalDirectoryPath 在非 Windows 平台解析符号链接，保持配置包可跨平台编译测试。
func canonicalDirectoryPath(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	return filepath.Abs(filepath.Clean(resolved))
}
