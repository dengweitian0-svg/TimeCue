package main

import (
	"bytes"
	"context"
	"errors"
	"testing"

	platformwindows "github.com/dengweitian0-svg/TimeCue/internal/platform/windows"
	"github.com/dengweitian0-svg/TimeCue/internal/utils"
)

// TestReportErrorPreservesOutputAndExitCode 验证入口边界覆盖所有错误形态且保持原始输出。
func TestReportErrorPreservesOutputAndExitCode(t *testing.T) {
	runtimeCause := errors.New("runtime failure")
	configErr := utils.Wrap(utils.ConfigError, "load configuration", errors.New("invalid configuration"))
	joinedErr := errors.Join(context.Canceled, runtimeCause)
	tests := []struct {
		name       string
		err        error
		wantOutput string
		wantCode   utils.ExitCode
	}{
		{name: "nil", wantCode: utils.ExitOK},
		{name: "configuration", err: configErr, wantOutput: "load configuration: invalid configuration\n", wantCode: utils.ExitInvalidConfiguration},
		{name: "runtime", err: runtimeCause, wantOutput: "runtime failure\n", wantCode: utils.ExitFailed},
		{name: "canceled", err: context.Canceled, wantOutput: "context canceled\n", wantCode: utils.ExitOK},
		{name: "canceled with real error", err: joinedErr, wantOutput: "context canceled\nruntime failure\n", wantCode: utils.ExitFailed},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			var output bytes.Buffer
			if got := reportError(&output, testCase.err); got != testCase.wantCode {
				t.Fatalf("reportError() code = %d, want %d", got, testCase.wantCode)
			}
			if output.String() != testCase.wantOutput {
				t.Fatalf("reportError() output = %q, want %q", output.String(), testCase.wantOutput)
			}
		})
	}
}

// TestNormalizeDesktopStartErrorSilencesStaleToastColdActivation 验证无主实例的过期 Toast 不显示致命错误框。
func TestNormalizeDesktopStartErrorSilencesStaleToastColdActivation(t *testing.T) {
	wantErr := errors.New("other startup failure")
	if err := normalizeDesktopStartError(utils.Wrap(utils.StartupError, "confirm exclusive startup", platformwindows.ErrActivationWithoutInstance)); err != nil {
		t.Fatalf("normalizeDesktopStartError() = %v, want nil", err)
	}
	if err := normalizeDesktopStartError(wantErr); !errors.Is(err, wantErr) {
		t.Fatalf("normalizeDesktopStartError() = %v, want %v", err, wantErr)
	}
}
