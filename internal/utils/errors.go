package utils

import (
	"context"
	"errors"
	"fmt"
)

type Kind string

const (
	ConfigError   Kind = "config"
	StartupError  Kind = "startup"
	RuntimeError  Kind = "runtime"
	CleanupError  Kind = "cleanup"
	InternalError Kind = "internal"
)

type Error struct {
	Kind Kind
	Op   string
	Err  error
}

func (e *Error) Error() string {
	if e.Op == "" {
		return e.Err.Error()
	}
	return fmt.Sprintf("%s: %s", e.Op, e.Err)
}

func (e *Error) Unwrap() error {
	return e.Err
}

func New(kind Kind, op, message string) error {
	return &Error{Kind: kind, Op: op, Err: errors.New(message)}
}

func Wrap(kind Kind, op string, err error) error {
	if err == nil {
		return nil
	}
	return &Error{Kind: kind, Op: op, Err: err}
}

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

func ExitCode(err error) int {
	if err == nil || errors.Is(err, context.Canceled) {
		return 0
	}
	if KindOf(err) == ConfigError {
		return 2
	}
	return 1
}
