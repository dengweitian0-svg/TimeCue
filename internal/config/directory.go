package config

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ValidateDirectoryPath 创建缺失目录并通过临时文件写入、同步、删除验证目录可用性。
// 返回值是解析符号链接、junction 和平台路径别名后的规范绝对路径。
func ValidateDirectoryPath(ctx context.Context, value string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("directory path must not be empty")
	}
	absolute, err := filepath.Abs(filepath.Clean(value))
	if err != nil {
		return "", fmt.Errorf("resolve directory path %q: %w", value, err)
	}
	if err := os.MkdirAll(absolute, 0o755); err != nil {
		return "", fmt.Errorf("create directory %q: %w", absolute, err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", fmt.Errorf("inspect directory %q: %w", absolute, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("path %q is not a directory", absolute)
	}
	normalized, err := canonicalDirectoryPath(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve physical directory %q: %w", absolute, err)
	}
	if err := probeDirectory(ctx, normalized); err != nil {
		return "", err
	}
	return normalized, nil
}

// normalizeConfiguredDirectory 规范化启动配置中的已有目录，但不在配置加载阶段创建目录。
func normalizeConfiguredDirectory(value, label string) (string, error) {
	absolute, err := filepath.Abs(filepath.Clean(strings.TrimSpace(value)))
	if err != nil {
		return "", fmt.Errorf("resolve %s directory %q: %w", label, value, err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return filepath.Clean(absolute), nil
		}
		return "", fmt.Errorf("inspect %s directory %q: %w", label, absolute, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s path %q is not a directory", label, absolute)
	}
	normalized, err := canonicalDirectoryPath(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve physical %s directory %q: %w", label, absolute, err)
	}
	return normalized, nil
}

// SamePhysicalDirectory 比较两个目录的规范物理路径，供 effective/saved DTO 判断是否需要重启。
func SamePhysicalDirectory(left, right string) bool {
	left = normalizeForComparison(left)
	right = normalizeForComparison(right)
	return equalDirectoryPath(left, right)
}

// normalizeForComparison 尽可能解析已有目录；不可访问路径仍保留绝对路径用于稳定比较。
func normalizeForComparison(value string) string {
	absolute, err := filepath.Abs(filepath.Clean(strings.TrimSpace(value)))
	if err != nil {
		return filepath.Clean(value)
	}
	if normalized, err := normalizeConfiguredDirectory(absolute, "comparison"); err == nil {
		return normalized
	}
	return filepath.Clean(absolute)
}

// probeDirectory 验证当前进程拥有在目录中创建、写入、同步和删除临时文件的能力。
func probeDirectory(ctx context.Context, directory string) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".timecue-directory-check-*")
	if err != nil {
		return fmt.Errorf("create directory probe in %q: %w", directory, err)
	}
	temporaryPath := temporary.Name()
	temporaryClosed := false
	defer func() {
		if !temporaryClosed {
			if closeErr := temporary.Close(); closeErr != nil {
				err = errors.Join(err, fmt.Errorf("close directory probe %q: %w", temporaryPath, closeErr))
			}
		}
		if removeErr := os.Remove(temporaryPath); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
			err = errors.Join(err, fmt.Errorf("remove directory probe %q: %w", temporaryPath, removeErr))
		}
	}()
	if _, err := temporary.Write([]byte("timecue")); err != nil {
		return fmt.Errorf("write directory probe %q: %w", temporaryPath, err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync directory probe %q: %w", temporaryPath, err)
	}
	if err := temporary.Close(); err != nil {
		temporaryClosed = true
		return fmt.Errorf("close directory probe %q: %w", temporaryPath, err)
	}
	temporaryClosed = true
	return nil
}
