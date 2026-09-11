package utils

import (
	"context"
	"errors"
	"testing"
)

func TestWrapPreservesKindAndCause(t *testing.T) {
	wantCause := errors.New("disk full")
	err := Wrap(CleanupError, "close log file", wantCause)

	if !errors.Is(err, wantCause) {
		t.Fatal("wrapped error does not preserve cause")
	}
	var typedErr *Error
	if !errors.As(err, &typedErr) {
		t.Fatal("wrapped error does not support errors.As")
	}
	if typedErr.Kind != CleanupError {
		t.Fatalf("Kind = %q, want %q", typedErr.Kind, CleanupError)
	}
	if KindOf(err) != CleanupError {
		t.Fatalf("KindOf() = %q, want %q", KindOf(err), CleanupError)
	}
}

func TestExitCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "nil", want: 0},
		{name: "canceled", err: context.Canceled, want: 0},
		{name: "config", err: Wrap(ConfigError, "load configuration", errors.New("invalid")), want: 2},
		{name: "startup", err: Wrap(StartupError, "initialize", errors.New("failed")), want: 1},
		{name: "runtime", err: Wrap(RuntimeError, "run", errors.New("failed")), want: 1},
		{name: "unknown", err: errors.New("failed"), want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExitCode(tt.err); got != tt.want {
				t.Fatalf("ExitCode() = %d, want %d", got, tt.want)
			}
		})
	}
}
