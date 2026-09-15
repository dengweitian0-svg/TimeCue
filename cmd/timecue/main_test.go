package main

import (
	"bytes"
	"context"
	"errors"
	"testing"

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
