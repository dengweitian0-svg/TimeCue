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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM) // 暂时使用 Ctrl+C 中断程序, 后续增加 UI 后再另外做退出
	defer stop()

	err := bootstrap.Start(ctx, appRuntime.IdleApplication{}, bootstrap.AlwaysQualified{})
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(int(utils.ExitCodeFor(err)))
	}
}
