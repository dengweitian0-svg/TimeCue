package bootstrap

import (
	"context"
	"errors"
	"log/slog"

	"github.com/dengweitian0-svg/TimeCue/internal/config"
	appLog "github.com/dengweitian0-svg/TimeCue/internal/log"
	"github.com/dengweitian0-svg/TimeCue/internal/utils"
)

type Application interface {
	Run(context.Context, config.Config, *slog.Logger) error
}

func Start(ctx context.Context, app Application) error {
	cfg, err := config.Load()
	if err != nil {
		return utils.Wrap(utils.ConfigError, "load configuration", err)
	}
	return Run(ctx, cfg, app)
}

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
	logger.Info("application ready")

	runErr = app.Run(ctx, cfg, logger)
	if errors.Is(runErr, context.Canceled) {
		runErr = nil
	} else if runErr != nil {
		runErr = utils.Wrap(utils.RuntimeError, "run application", runErr)
		logger.Error("application failed", "error", runErr)
	}
	logger.Info("application stopping")
	return runErr
}
