package domain

import (
	"errors"
	"fmt"
	"strings"
)

// Code 表示业务错误分类。
type Code string

const (
	Validation             Code = "VALIDATION"
	TimeRangeExceeded      Code = "TIME_RANGE_EXCEEDED"
	NotFound               Code = "NOT_FOUND"
	StaleRevision          Code = "STALE_REVISION"
	StaleCursor            Code = "STALE_CURSOR"
	ReminderNotProcessable Code = "REMINDER_NOT_PROCESSABLE"
	RuntimeStopping        Code = "RUNTIME_STOPPING"
)

// Error 表示带有机器可读字段和原始原因的业务错误。
type Error struct {
	Code    Code
	Message string
	Fields  []string
	Bounds  *TimeBounds
	Cause   error
}

// NewValidation 创建输入校验错误。
func NewValidation(message string, fields ...string) error {
	return &Error{Code: Validation, Message: message, Fields: append([]string(nil), fields...)}
}

// NewTimeRangeExceeded 创建带时间边界的越界错误。
func NewTimeRangeExceeded(fields []string, bounds TimeBounds) error {
	return &Error{
		Code:    TimeRangeExceeded,
		Message: "schedule time is outside the allowed range",
		Fields:  append([]string(nil), fields...),
		Bounds:  &bounds,
	}
}

// NewNotFound 创建资源不存在错误。
func NewNotFound(message string) error {
	return &Error{Code: NotFound, Message: message}
}

// NewStaleCursor 创建要求从第一页刷新的查询游标错误。
func NewStaleCursor(message string, fields ...string) error {
	return &Error{Code: StaleCursor, Message: message, Fields: append([]string(nil), fields...)}
}

// NewReminderNotProcessable 创建发送状态不允许确认的业务错误。
func NewReminderNotProcessable(message string, fields ...string) error {
	return &Error{Code: ReminderNotProcessable, Message: message, Fields: append([]string(nil), fields...)}
}

// NewStaleRevision 创建并发更新使用的版本冲突错误。
func NewStaleRevision(message string, fields ...string) error {
	return &Error{Code: StaleRevision, Message: message, Fields: append([]string(nil), fields...)}
}

// NewRuntimeStopping 创建运行时进入停止阶段后拒绝业务写入的错误。
func NewRuntimeStopping() error {
	return &Error{Code: RuntimeStopping, Message: "runtime is stopping"}
}

// Error 返回包含错误分类、信息和字段的错误文本。
func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}

	message := e.Message
	if message == "" {
		message = string(e.Code)
	}
	if len(e.Fields) == 0 {
		return fmt.Sprintf("%s: %s", e.Code, message)
	}
	return fmt.Sprintf("%s: %s (%s)", e.Code, message, strings.Join(e.Fields, ", "))
}

// Unwrap 返回原始错误原因，以便边界层保留错误链。
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// withCause 将底层原因附加到业务错误，同时保持对外错误分类不变。
func withCause(err error, cause error) error {
	if err == nil {
		return nil
	}
	if cause == nil {
		return err
	}
	var businessErr *Error
	if errors.As(err, &businessErr) {
		businessErr.Cause = cause
	}
	return err
}
