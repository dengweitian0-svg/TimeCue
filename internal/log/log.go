package log

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/dengweitian0-svg/TimeCue/internal/config"
)

// New 创建写入指定 .log 文件的 JSONL logger，并返回由调用方负责关闭的文件。
func New(cfg config.LogConfig) (*slog.Logger, *os.File, error) {
	fullPath, err := (config.Config{Log: cfg}).LogFilePath()
	if err != nil {
		return nil, nil, fmt.Errorf("resolve log path: %w", err)
	}

	level, err := parseLevel(cfg.Level)
	if err != nil {
		return nil, nil, err
	}

	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return nil, nil, fmt.Errorf("create log directory: %w", err)
	}

	file, err := os.OpenFile(fullPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, nil, fmt.Errorf("open log file %q: %w", fullPath, err)
	}

	handler := slog.NewJSONHandler(file, &slog.HandlerOptions{Level: level})
	return slog.New(handler), file, nil
}

// parseLevel 将配置中的日志级别转换为 slog 级别。
func parseLevel(value string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("invalid log level %q: want debug, info, warn, or error", value)
	}
}
