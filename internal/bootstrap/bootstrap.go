package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/dengweitian0-svg/TimeCue/internal/config"
	appLog "github.com/dengweitian0-svg/TimeCue/internal/log"
)

type Application interface {
	Run(context.Context, config.Config, *slog.Logger) error
}

func Start(ctx context.Context, app Application) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	return Run(ctx, cfg, app)
}

func Run(ctx context.Context, cfg config.Config, app Application) (runErr error) {
	if app == nil {
		return errors.New("application must not be nil")
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("validate configuration: %w", err)
	}

	logger, file, err := appLog.New(cfg.Log)
	if err != nil {
		return fmt.Errorf("initialize logging: %w", err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			closeErr = fmt.Errorf("close log file: %w", closeErr)
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
		logger.Error("application failed", "error", runErr)
	}
	logger.Info("application stopping")
	return runErr
}

func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	if errors.Is(err, context.Canceled) {
		return 0
	}
	return 1
}

func ReportError(err error) {
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
	}
}
