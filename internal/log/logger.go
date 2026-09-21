// Package log 提供项目统一使用的日志器。
package log

import (
	"io"
	"log/slog"
	"os"
	"sync/atomic"
)

// Logger 保存当前生效的 slog.Logger。
// 通过原子替换，可以在配置热更新后切换日志级别和输出文件，
// 而不需要修改所有调用方持有的 Logger 指针。
type Logger struct {
	current atomic.Pointer[slog.Logger]
	level   *slog.LevelVar
}

// New 创建启动阶段使用的 Logger。
// 配置尚未加载时，日志记录会被丢弃，但仍保留 Info 级别阈值。
func New() *Logger {
	level := new(slog.LevelVar)
	level.Set(slog.LevelInfo)
	logger := &Logger{level: level}
	logger.current.Store(slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{
		Level: level,
	})))
	return logger
}

// Upgrade 根据已经加载的配置安装正式 JSONL Logger。
// file 的创建、路径解析和生命周期由调用方负责；运行期间只通过 SetLevel
// 热更新日志级别，不重新安装日志文件。
func (l *Logger) Upgrade(file *os.File) {
	if l == nil || l.level == nil || file == nil {
		return
	}

	full := slog.New(slog.NewJSONHandler(file, &slog.HandlerOptions{
		Level: l.level,
	}))
	l.current.Store(full)
}

// SetLevel 原子修改当前 Logger 的最低日志级别，不替换 Logger 或输出文件。
func (l *Logger) SetLevel(level slog.Level) {
	if l == nil || l.level == nil {
		return
	}
	l.level.Set(level)
}

// currentLogger 返回当前生效的 slog.Logger。
func (l *Logger) currentLogger() *slog.Logger {
	if l == nil {
		return slog.Default()
	}
	if logger := l.current.Load(); logger != nil {
		return logger
	}
	return slog.Default()
}

// Debug 记录 Debug 级别日志。
func (l *Logger) Debug(msg string, args ...any) {
	l.currentLogger().Debug(msg, args...)
}

// Info 记录 Info 级别日志。
func (l *Logger) Info(msg string, args ...any) {
	l.currentLogger().Info(msg, args...)
}

// Warn 记录 Warn 级别日志。
func (l *Logger) Warn(msg string, args ...any) {
	l.currentLogger().Warn(msg, args...)
}

// Error 记录 Error 级别日志。
func (l *Logger) Error(msg string, args ...any) {
	l.currentLogger().Error(msg, args...)
}
