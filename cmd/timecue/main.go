package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/dengweitian0-svg/TimeCue/internal/bootstrap"
	appRuntime "github.com/dengweitian0-svg/TimeCue/internal/runtime"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err := bootstrap.Start(ctx, appRuntime.IdleApplication{})
	if err != nil {
		bootstrap.ReportError(err)
		os.Exit(bootstrap.ExitCode(err))
	}
}
