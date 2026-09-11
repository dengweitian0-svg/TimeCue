package utils

import (
	"context"
	"errors"
	"fmt"
)

// Kind 表示系统错误所属的处理阶段。
type Kind string

const (
	ConfigError   Kind = "config"
	StartupError  Kind = "startup"
	RuntimeError  Kind = "runtime"
	CleanupError  Kind = "cleanup"
	InternalError Kind = "internal"
)

// ExitCode 表示进程最终退出状态。
type ExitCode uint8

const (
	// ExitOK 表示正常完成或主动取消。
	ExitOK ExitCode = iota
	// ExitFailed 表示启动、运行或清理失败。
	ExitFailed
	// ExitInvalidConfiguration 表示配置加载或校验失败。
	ExitInvalidConfiguration
)

// Error 为错误附加阶段和操作上下文，同时保留原始 cause。
type Error struct {
	Kind Kind
	Op   string
	Err  error
}

// Error 返回包含操作上下文的错误文本。
func (e *Error) Error() string {
	if e.Op == "" {
		return e.Err.Error()
	}
	return fmt.Sprintf("%s: %s", e.Op, e.Err)
}

// Unwrap 返回原始 cause，以支持 errors.Is/errors.As。
func (e *Error) Unwrap() error {
	return e.Err
}

// New 创建带有分类和操作上下文的系统错误。
func New(kind Kind, op, message string) error {
	return &Error{Kind: kind, Op: op, Err: errors.New(message)}
}

// Wrap 包装已有错误并附加分类和操作上下文。
func Wrap(kind Kind, op string, err error) error {
	if err == nil {
		return nil
	}
	return &Error{Kind: kind, Op: op, Err: err}
}

// KindOf 从错误链中提取系统错误分类，无法识别时返回 InternalError。
func KindOf(err error) Kind {
	if err == nil {
		return ""
	}
	var typedErr *Error
	if errors.As(err, &typedErr) {
		return typedErr.Kind
	}
	return InternalError
}

// ExitCodeFor 将系统错误映射为进程退出状态。
func ExitCodeFor(err error) ExitCode {
	if err == nil || errors.Is(err, context.Canceled) {
		return ExitOK
	}
	if KindOf(err) == ConfigError {
		return ExitInvalidConfiguration
	}
	return ExitFailed
}
