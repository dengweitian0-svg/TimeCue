package runtime

import (
	"context"
	"log/slog"

	"github.com/dengweitian0-svg/TimeCue/internal/bootstrap"
	"github.com/dengweitian0-svg/TimeCue/internal/config"
)

// IdleApplication 是尚无业务组件时使用的最小应用实现。
type IdleApplication struct{}

// Run 持续等待取消信号，并把 context.Canceled 交给 bootstrap 识别为正常退出。
func (IdleApplication) Run(ctx context.Context, _ config.Config, _ *slog.Logger, _ bootstrap.Store) error {
	<-ctx.Done()
	return ctx.Err()
}
