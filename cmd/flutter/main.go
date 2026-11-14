package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	logger "github.com/dengweitian0-svg/TimeCue/internal/log"
	"github.com/gin-gonic/gin"
)

func main() {
	log := logger.Default()
	if err := run(log); err != nil {
		log.Error("[main] server stopped", "error", err)
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run 从 Go 入口计时，并将系统信号和父端停止消息接入宿主。
func run(log *logger.Logger) error {
	started := time.Now()
	options, err := parseStartup(os.Args[1:])
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var control *parentControl
	if options.managed {
		gin.SetMode(gin.ReleaseMode)
		control = startParentControl(os.Stdin, cancel)
		defer control.closeAndWait()
	}
	host, err := newHost(options, log)
	if err == nil {
		err = host.run(ctx, started)
	}
	if control != nil {
		control.closeAndWait()
		if failure := control.err(); failure != nil {
			return failure
		}
	}
	if ctx.Err() != nil && errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
