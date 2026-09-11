package runtime

import (
	"context"
	"log/slog"

	"github.com/dengweitian0-svg/TimeCue/internal/config"
)

type IdleApplication struct{}

func (IdleApplication) Run(ctx context.Context, _ config.Config, _ *slog.Logger) error {
	<-ctx.Done()
	return ctx.Err()
}
