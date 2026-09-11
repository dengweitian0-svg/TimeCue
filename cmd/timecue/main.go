package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/dengweitian0-svg/TimeCue/internal/bootstrap"
	appRuntime "github.com/dengweitian0-svg/TimeCue/internal/runtime"
	"github.com/dengweitian0-svg/TimeCue/internal/utils"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err := bootstrap.Start(ctx, appRuntime.IdleApplication{})
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(utils.ExitCode(err))
	}
}
