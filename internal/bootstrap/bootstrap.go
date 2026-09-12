package bootstrap

import (
	"context"
	"errors"
	"log/slog"

	"github.com/dengweitian0-svg/TimeCue/internal/config"
	appLog "github.com/dengweitian0-svg/TimeCue/internal/log"
	"github.com/dengweitian0-svg/TimeCue/internal/utils"
)

// Application 表示由 bootstrap 驱动的应用运行单元。
type Application interface {
	Run(context.Context, config.Config, *slog.Logger) error
}

// Start 加载配置并启动应用，是生产入口使用的启动函数。
func Start(ctx context.Context, app Application) error {
	cfg, err := config.Load()
	if err != nil {
		return utils.Wrap(utils.ConfigError, "load configuration", err)
	}
	return Run(ctx, cfg, app)
}

// Run 按固定顺序完成配置校验、日志初始化、应用运行和资源清理。
func Run(ctx context.Context, cfg config.Config, app Application) (runErr error) {
	if app == nil {
		return utils.New(utils.StartupError, "run application", "application must not be nil")
	}
	if err := cfg.Validate(); err != nil {
		return utils.Wrap(utils.ConfigError, "validate configuration", err)
	}

	logger, file, err := appLog.New(cfg.Log)
	if err != nil {
		return utils.Wrap(utils.StartupError, "initialize logging", err)
	}
	// 日志文件由本函数取得，因此也由本函数负责关闭。
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			closeErr = utils.Wrap(utils.CleanupError, "close log file", closeErr)
			if runErr == nil {
				runErr = closeErr
			} else {
				runErr = errors.Join(runErr, closeErr)
			}
		}
	}()

	logger.Info("application starting", "name", cfg.App.Name, "version", cfg.App.Version)

	runErr = app.Run(ctx, cfg, logger)
	if utils.IsPureContextCancellation(runErr) {
		runErr = nil
	} else if runErr != nil {
		runErr = utils.Wrap(utils.RuntimeError, "run application", runErr)
		logger.Error("application failed", "error", runErr)
	}
	logger.Info("application stopping")
	return runErr
}
