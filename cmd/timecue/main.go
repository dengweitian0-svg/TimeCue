package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/dengweitian0-svg/TimeCue/internal/bootstrap"
	appRuntime "github.com/dengweitian0-svg/TimeCue/internal/runtime"
	"github.com/dengweitian0-svg/TimeCue/internal/utils"
)

// main 创建进程级取消上下文并把启动结果交给统一的错误输出边界。
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM) // 暂时使用 Ctrl+C 中断程序, 后续增加 UI 后再另外做退出
	defer stop()

	err := bootstrap.Start(ctx, appRuntime.IdleApplication{}, bootstrap.AlwaysQualified{})
	if exitCode := reportError(os.Stderr, err); exitCode != utils.ExitOK {
		os.Exit(int(exitCode))
	}
}

// reportError 保持入口的错误文本输出和退出码映射行为，并允许不启动进程进行测试。
func reportError(writer io.Writer, err error) utils.ExitCode {
	if err == nil {
		return utils.ExitOK
	}
	_, _ = fmt.Fprintln(writer, err)
	return utils.ExitCodeFor(err)
}
