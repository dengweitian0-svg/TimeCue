// Package logger 提供项目统一使用的全局日志器。
package logger

import (
	"io"
	"log/slog"
	"os"
	"sync/atomic"
	"time"
)

// Logger 保存当前生效的 slog.Logger 和可热更新的日志级别。
type Logger struct {
	current atomic.Pointer[slog.Logger]
	level   slog.LevelVar
}

var global = newLogger()

// 日志时间固定采用 UTC+8，不受主机时区或夏令时影响。
var logTimeZone = time.FixedZone("UTC+8", 8*60*60)

const logTimeLayout = "2006-01-02-15:04:05"

func newLogger() *Logger {
	logger := &Logger{}
	logger.level.Set(slog.LevelInfo)
	logger.current.Store(slog.New(slog.NewTextHandler(io.Discard, logger.handlerOptions())))
	return logger
}

// handlerOptions 为启动和正式日志器提供统一的级别与时间格式配置。
func (l *Logger) handlerOptions() *slog.HandlerOptions {
	return &slog.HandlerOptions{
		Level: &l.level,
		ReplaceAttr: func(groups []string, attr slog.Attr) slog.Attr {
			if len(groups) == 0 && attr.Key == slog.TimeKey && attr.Value.Kind() == slog.KindTime {
				return slog.String(slog.TimeKey, attr.Value.Time().In(logTimeZone).Format(logTimeLayout))
			}
			return attr
		},
	}
}

// Default 返回项目唯一的全局 Logger，是 New 的语义别名。
func Default() *Logger {
	return global
}

// Upgrade 根据已经加载的配置安装正式 JSONL Logger。
// file 的创建、路径解析和生命周期由调用方负责；运行期间只通过
// SetLevel 热更新日志级别，不重新安装日志文件。
func (l *Logger) Upgrade(file *os.File) {
	if l == nil || file == nil {
		return
	}
	full := slog.New(slog.NewJSONHandler(file, l.handlerOptions()))
	l.current.Store(full)
	slog.SetDefault(full)
}

// SetLevel 原子修改当前 Logger 的最低日志级别，不替换 Logger 或输出文件。
func (l *Logger) SetLevel(level slog.Level) {
	if l == nil {
		return
	}
	l.level.Set(level)
}

func (l *Logger) currentLogger() *slog.Logger {
	if l == nil {
		return slog.Default()
	}
	if logger := l.current.Load(); logger != nil {
		return logger
	}
	return slog.Default()
}

// SetLevel 原子修改全局 Logger 的最低日志级别。
func SetLevel(level slog.Level) {
	global.SetLevel(level)
}

// Upgrade 为全局 Logger 安装正式 JSONL Logger。
func Upgrade(file *os.File) {
	global.Upgrade(file)
}

// Debug 记录全局 Logger 的 Debug 级别日志。
func Debug(msg string, args ...any) {
	global.currentLogger().Debug(msg, args...)
}

// Info 记录全局 Logger 的 Info 级别日志。
func Info(msg string, args ...any) {
	global.currentLogger().Info(msg, args...)
}

// Warn 记录全局 Logger 的 Warn 级别日志。
func Warn(msg string, args ...any) {
	global.currentLogger().Warn(msg, args...)
}

// Error 记录全局 Logger 的 Error 级别日志。
func Error(msg string, args ...any) {
	global.currentLogger().Error(msg, args...)
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
