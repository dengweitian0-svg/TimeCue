package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/dengweitian0-svg/TimeCue/internal/config"
	appLog "github.com/dengweitian0-svg/TimeCue/internal/log"
	"github.com/dengweitian0-svg/TimeCue/internal/storage/sqlite"
	"github.com/dengweitian0-svg/TimeCue/internal/utils"
)

// Store 表示由 bootstrap 持有并在运行单元退出后关闭的持久化资源。
type Store interface {
	Close() error
}

// Application 表示由 bootstrap 驱动、接收共享 Store 的应用运行单元。
type Application interface {
	Run(context.Context, config.Config, *slog.Logger, Store) error
}

// ExclusiveStartup 表示宿主已经取得独占启动资格的窄契约。
type ExclusiveStartup interface {
	Acquire(context.Context) (func() error, error)
}

// ErrAlreadyRunning 表示第二实例已成功把激活请求转发给主实例。
var ErrAlreadyRunning = errors.New("another instance is already running")

// AlwaysQualified 是当前尚未接入 Windows 单实例锁时使用的默认资格实现。
type AlwaysQualified struct{}

// Acquire 为不需要外部锁的宿主授予资格，并返回无操作释放函数。
func (AlwaysQualified) Acquire(context.Context) (func() error, error) {
	return func() error { return nil }, nil
}

// Resources 保存初始化完成后由 bootstrap 拥有的资源及其关闭顺序。
type Resources struct {
	Logger      *slog.Logger
	Store       Store
	logFile     *os.File
	releaseLock func() error
}

// storeOpener 保留 SQLite 创建边界，使启动顺序和部分失败清理可替换测试。
var storeOpener = func(ctx context.Context, paths config.ResolvedPaths, logger *slog.Logger) (Store, error) {
	return sqlite.OpenStore(ctx, paths.DatabasePath, paths.BackupDir, logger)
}

// Start 加载配置、取得独占资格并启动应用，是生产入口使用的启动函数。
func Start(ctx context.Context, app Application, qualification ExclusiveStartup) error {
	cfg, err := config.Load()
	if err != nil {
		return utils.Wrap(utils.ConfigError, "load configuration", err)
	}
	return Run(ctx, cfg, app, qualification)
}

// Initialize 按固定顺序准备配置、独占资格、目录、日志和 SQLite Store。
func Initialize(ctx context.Context, cfg config.Config, qualification ExclusiveStartup) (*Resources, error) {
	if err := cfg.Validate(); err != nil {
		return nil, utils.Wrap(utils.ConfigError, "validate configuration", err)
	}
	if qualification == nil {
		return nil, utils.New(utils.StartupError, "confirm exclusive startup", "startup qualification must not be nil")
	}
	if err := ensureResolvedPaths(&cfg); err != nil {
		return nil, utils.Wrap(utils.ConfigError, "resolve data paths", err)
	}

	releaseLock, err := qualification.Acquire(ctx)
	if err != nil {
		return nil, utils.Wrap(utils.StartupError, "confirm exclusive startup", err)
	}
	if releaseLock == nil {
		releaseLock = func() error { return nil }
	}

	if err := prepareDirectories(cfg.ResolvedPaths); err != nil {
		return nil, cleanupInitializationFailure(utils.Wrap(utils.StartupError, "prepare data directories", err), nil, nil, releaseLock)
	}
	logger, logFile, err := appLog.NewAt(cfg.ResolvedPaths.LogPath, cfg.Log.Level)
	if err != nil {
		return nil, cleanupInitializationFailure(utils.Wrap(utils.StartupError, "initialize logging", err), nil, nil, releaseLock)
	}
	store, err := storeOpener(ctx, cfg.ResolvedPaths, logger)
	if err != nil {
		return nil, cleanupInitializationFailure(utils.Wrap(utils.StartupError, "initialize sqlite store", err), store, logFile, releaseLock)
	}
	if store == nil {
		return nil, cleanupInitializationFailure(utils.New(utils.StartupError, "initialize sqlite store", "store opener returned nil store"), nil, logFile, releaseLock)
	}
	return &Resources{Logger: logger, Store: store, logFile: logFile, releaseLock: releaseLock}, nil
}

// ensureResolvedPaths 为手工构造的测试配置补齐路径；生产 Load 已经完成解析。
func ensureResolvedPaths(cfg *config.Config) error {
	if cfg.ResolvedPaths.DatabasePath != "" && cfg.ResolvedPaths.LogPath != "" && cfg.ResolvedPaths.BackupDir != "" {
		return nil
	}
	workingDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	paths, err := cfg.ResolvePaths(workingDir)
	if err != nil {
		return err
	}
	cfg.ResolvedPaths = paths
	return nil
}

// prepareDirectories 在 SQLite 打开前准备所有实际路径对应的目录。
func prepareDirectories(paths config.ResolvedPaths) error {
	for _, item := range []struct {
		name string
		path string
	}{
		{name: "log", path: filepath.Dir(paths.LogPath)},
		{name: "database", path: filepath.Dir(paths.DatabasePath)},
		{name: "backup", path: paths.BackupDir},
	} {
		if err := os.MkdirAll(item.path, 0o755); err != nil {
			return fmt.Errorf("create %s directory %s: %w", item.name, item.path, err)
		}
	}
	return nil
}

// cleanupInitializationFailure 关闭初始化阶段已经取得的资源并保留主错误 cause。
func cleanupInitializationFailure(primary error, store Store, logFile *os.File, releaseLock func() error) error {
	cleanupErr := errors.Join(closeStore(store), closeLogFile(logFile), closeLock(releaseLock))
	if cleanupErr == nil {
		return primary
	}
	return errors.Join(primary, utils.Wrap(utils.CleanupError, "cleanup failed initialization", cleanupErr))
}

// Close 按 Store、日志文件、独占资格的逆序释放初始化资源。
func (r *Resources) Close() error {
	if r == nil {
		return nil
	}
	return errors.Join(closeStore(r.Store), closeLogFile(r.logFile), closeLock(r.releaseLock))
}

// closeStore 关闭 Store 并保留实际资源的错误上下文。
func closeStore(store Store) error {
	if store == nil {
		return nil
	}
	if err := store.Close(); err != nil {
		return utils.Wrap(utils.CleanupError, "close sqlite store", err)
	}
	return nil
}

// closeLogFile 关闭由日志器持有的文件句柄。
func closeLogFile(file *os.File) error {
	if file == nil {
		return nil
	}
	if err := file.Close(); err != nil {
		return utils.Wrap(utils.CleanupError, "close log file", err)
	}
	return nil
}

// closeLock 释放宿主的独占启动资格。
func closeLock(release func() error) error {
	if release == nil {
		return nil
	}
	if err := release(); err != nil {
		return utils.Wrap(utils.CleanupError, "release exclusive startup", err)
	}
	return nil
}

// Run 按固定顺序完成初始化、运行单元注入和资源清理。
func Run(ctx context.Context, cfg config.Config, app Application, qualification ExclusiveStartup) (runErr error) {
	if app == nil {
		return utils.New(utils.StartupError, "run application", "application must not be nil")
	}
	resources, err := Initialize(ctx, cfg, qualification)
	if err != nil {
		if errors.Is(err, ErrAlreadyRunning) {
			return nil
		}
		return err
	}
	defer func() {
		if closeErr := resources.Close(); closeErr != nil {
			if runErr == nil {
				runErr = closeErr
			} else {
				runErr = errors.Join(runErr, closeErr)
			}
		}
	}()

	resources.Logger.Info("application starting", "name", cfg.App.Name, "version", cfg.App.Version)
	runErr = app.Run(ctx, cfg, resources.Logger, resources.Store)
	if utils.IsPureContextCancellation(runErr) {
		runErr = nil
	} else if runErr != nil {
		runErr = utils.Wrap(utils.RuntimeError, "run application", runErr)
		resources.Logger.Error("application failed", "error", runErr)
	}
	resources.Logger.Info("application stopping")
	return runErr
}
