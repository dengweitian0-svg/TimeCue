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
		want ExitCode
	}{
		{name: "nil", want: ExitOK},
		{name: "canceled", err: context.Canceled, want: ExitOK},
		{name: "config", err: Wrap(ConfigError, "load configuration", errors.New("invalid")), want: ExitInvalidConfiguration},
		{name: "startup", err: Wrap(StartupError, "initialize", errors.New("failed")), want: ExitFailed},
		{name: "runtime", err: Wrap(RuntimeError, "run", errors.New("failed")), want: ExitFailed},
		{name: "unknown", err: errors.New("failed"), want: ExitFailed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExitCodeFor(tt.err); got != tt.want {
				t.Fatalf("ExitCodeFor() = %d, want %d", got, tt.want)
			}
		})
	}
}
