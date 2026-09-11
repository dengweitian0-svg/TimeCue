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

type ExitCode uint8

const (
	ExitOK ExitCode = iota
	ExitFailed
	ExitInvalidConfiguration
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

func ExitCodeFor(err error) ExitCode {
	if err == nil || errors.Is(err, context.Canceled) {
		return ExitOK
	}
	if KindOf(err) == ConfigError {
		return ExitInvalidConfiguration
	}
	return ExitFailed
}
