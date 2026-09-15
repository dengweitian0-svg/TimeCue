package runtime

import (
	"context"
	"testing"

	"github.com/dengweitian0-svg/TimeCue/internal/config"
)

// TestIdleApplicationRunReturnsContextCancellation 验证最小运行单元在取消后原样返回 context.Canceled。
func TestIdleApplicationRunReturnsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- (IdleApplication{}).Run(ctx, config.Config{}, nil, nil)
	}()

	cancel()
	if err := <-result; err != context.Canceled {
		t.Fatalf("IdleApplication.Run() error = %v, want %v", err, context.Canceled)
	}
}
