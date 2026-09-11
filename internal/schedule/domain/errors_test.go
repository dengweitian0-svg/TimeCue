package domain

import (
	"errors"
	"testing"
)

// TestBusinessErrorPreservesCause 验证业务错误可以回流底层原因。
func TestBusinessErrorPreservesCause(t *testing.T) {
	cause := errors.New("decode failed")
	err := withCause(NewValidation("invalid JSON", "body"), cause)
	if !errors.Is(err, cause) {
		t.Fatalf("errors.Is() = false for cause")
	}
	var businessErr *Error
	if !errors.As(err, &businessErr) || businessErr.Code != Validation {
		t.Fatalf("error = %#v, want validation error", err)
	}
}

// TestNewTimeRangeExceededIncludesBounds 验证越界错误暴露机器可读的字段和边界。
func TestNewTimeRangeExceededIncludesBounds(t *testing.T) {
	bounds := TimeBounds{}
	err := NewTimeRangeExceeded([]string{"start_unix"}, bounds)
	var businessErr *Error
	if !errors.As(err, &businessErr) {
		t.Fatalf("error type = %T, want *Error", err)
	}
	if businessErr.Code != TimeRangeExceeded || len(businessErr.Fields) != 1 || businessErr.Bounds == nil {
		t.Fatalf("error = %#v", businessErr)
	}
}
