package runtime

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dengweitian0-svg/TimeCue/internal/config"
	"github.com/dengweitian0-svg/TimeCue/internal/platform"
	"github.com/dengweitian0-svg/TimeCue/internal/schedule/app"
	"github.com/dengweitian0-svg/TimeCue/internal/schedule/domain"
)

// GetDesktopStatus 返回桌面生命周期、清理错误和激活握手状态。
func (a *DesktopApplication) GetDesktopStatus() DesktopStatusDTO {
	if a == nil {
		return DesktopStatusDTO{}
	}
	a.mu.Lock()
	state := a.state
	activationSequence := a.activationSequence
	frontendReady := a.frontendReady
	scheduler := a.scheduler
	cleanupError := a.cleanupError
	lastError := a.lastError
	a.mu.Unlock()
	recoveryStatus := app.RecoveryStatusIdle
	if scheduler != nil {
		recoveryStatus = scheduler.GetRuntimeStatus().RecoveryStatus
	}
	status := DesktopStatusDTO{State: state, ActivationSequence: activationSequence, FrontendReady: frontendReady, RecoveryStatus: recoveryStatus}
	if cleanupError != nil {
		status.CleanupError = cleanupError.Error()
	}
	if lastError != nil {
		status.LastError = lastError.Error()
	}
	return status
}

// GetStorageDirectories 返回日志和备份目录的 effective/saved 对照，不暴露固定数据库路径。
func (a *DesktopApplication) GetStorageDirectories() (StorageDirectoriesDTO, error) {
	if a == nil {
		return StorageDirectoriesDTO{}, errors.New("desktop application is nil")
	}
	a.mu.Lock()
	if a.config == nil || a.service == nil {
		a.mu.Unlock()
		return StorageDirectoriesDTO{}, errors.New("desktop application is not assembled")
	}
	cfg := *a.config
	a.mu.Unlock()
	return storageDirectoriesDTO(cfg)
}

// GetLogLevel 返回日志级别当前生效值与保存值，日志器不会在运行中切换级别。
func (a *DesktopApplication) GetLogLevel() (LogLevelDTO, error) {
	if a == nil {
		return LogLevelDTO{}, errors.New("desktop application is nil")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.config == nil || a.service == nil {
		return LogLevelDTO{}, errors.New("desktop application is not assembled")
	}
	return logLevelDTO(a.effectiveLogLevel, a.config.Log.Level)
}

// GetTheme 返回当前生效的应用主题。
func (a *DesktopApplication) GetTheme() (ThemeDTO, error) {
	if a == nil {
		return ThemeDTO{}, errors.New("desktop application is nil")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.config == nil || a.service == nil {
		return ThemeDTO{}, errors.New("desktop application is not assembled")
	}
	return ThemeDTO{Theme: string(a.config.Theme)}, nil
}

// ApplyWindowTheme 将前端主题的 background/text 颜色应用到原生窗口标题栏。
// 标题栏是非关键桌面副作用，平台失败会被记录并返回给前端，但不改变配置状态。
func (a *DesktopApplication) ApplyWindowTheme(ctx context.Context, request ApplyWindowThemeRequest) error {
	if a == nil {
		return errors.New("desktop application is nil")
	}
	if err := a.ensureWritable(); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	colors, err := windowThemeColorsFromRequest(request)
	if err != nil {
		return fmt.Errorf("apply window theme: %w", err)
	}
	a.mu.Lock()
	controller := a.deps.WindowTheme
	logger := a.logger
	a.mu.Unlock()
	if controller == nil {
		return nil
	}
	if err := controller.Apply(ctx, colors); err != nil {
		wrapped := fmt.Errorf("apply window theme: %w", err)
		if logger != nil {
			logger.Warn("native window theme synchronization failed", "operation", "apply_window_theme", "dependency", "windows_dwm", "error", wrapped)
		}
		return wrapped
	}
	return nil
}

// windowThemeColorsFromRequest 严格解析前端传入的两个不透明十六进制颜色。
func windowThemeColorsFromRequest(request ApplyWindowThemeRequest) (platform.WindowThemeColors, error) {
	titleBar, err := parseWindowThemeColor("background", request.Background)
	if err != nil {
		return platform.WindowThemeColors{}, err
	}
	titleText, err := parseWindowThemeColor("text", request.Text)
	if err != nil {
		return platform.WindowThemeColors{}, err
	}
	return platform.WindowThemeColors{TitleBar: titleBar, TitleText: titleText}, nil
}

// parseWindowThemeColor 将 #RRGGBB 解析为平台无关 RGB 通道，并拒绝透明或畸形值。
func parseWindowThemeColor(name, value string) (platform.RGBColor, error) {
	if len(value) != 7 || value[0] != '#' {
		return platform.RGBColor{}, fmt.Errorf("window theme %s must be #RRGGBB", name)
	}
	decoded, err := hex.DecodeString(value[1:])
	if err != nil {
		return platform.RGBColor{}, fmt.Errorf("window theme %s must be #RRGGBB: %w", name, err)
	}
	return platform.RGBColor{R: decoded[0], G: decoded[1], B: decoded[2]}, nil
}

// SaveLogLevel 保存日志级别；当前日志器保持原级别，重启后加载新值。
func (a *DesktopApplication) SaveLogLevel(ctx context.Context, request SaveLogLevelRequest) (LogLevelDTO, error) {
	if a == nil {
		return LogLevelDTO{}, errors.New("desktop application is nil")
	}
	if err := a.ensureWritable(); err != nil {
		return LogLevelDTO{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return LogLevelDTO{}, err
	}
	a.mu.Lock()
	if a.state != DesktopStateRunning {
		a.mu.Unlock()
		return LogLevelDTO{}, domain.NewRuntimeStopping()
	}
	if a.config == nil {
		a.mu.Unlock()
		return LogLevelDTO{}, errors.New("runtime config is not configured")
	}
	if err := a.config.SaveLogLevel(request.Level); err != nil {
		wrapped := a.newConfigPersistenceError("save log level", "log.level", err)
		a.lastError = wrapped
		logger := a.logger
		a.mu.Unlock()
		if logger != nil {
			logger.Error("log level configuration save failed", "operation", "save_log_level", "error", wrapped)
		}
		return LogLevelDTO{}, wrapped
	}
	result, err := logLevelDTO(a.effectiveLogLevel, a.config.Log.Level)
	logger := a.logger
	a.mu.Unlock()
	if err != nil {
		return LogLevelDTO{}, err
	}
	if logger != nil {
		logger.Info("log level configuration saved", "operation", "save_log_level", "restart_required", result.RestartRequired)
	}
	return result, nil
}

// SaveTheme 保存应用主题；只有配置原子替换成功后才更新运行时内存主题。
func (a *DesktopApplication) SaveTheme(ctx context.Context, request SaveThemeRequest) (ThemeDTO, error) {
	if a == nil {
		return ThemeDTO{}, errors.New("desktop application is nil")
	}
	if err := a.ensureWritable(); err != nil {
		return ThemeDTO{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return ThemeDTO{}, err
	}
	a.mu.Lock()
	if a.state != DesktopStateRunning {
		a.mu.Unlock()
		return ThemeDTO{}, domain.NewRuntimeStopping()
	}
	if a.config == nil {
		a.mu.Unlock()
		return ThemeDTO{}, errors.New("runtime config is not configured")
	}
	if err := a.config.SaveTheme(config.Theme(request.Theme)); err != nil {
		wrapped := a.newConfigPersistenceError("save theme", "theme", err)
		a.lastError = wrapped
		logger := a.logger
		a.mu.Unlock()
		if logger != nil {
			logger.Error("theme configuration save failed", "operation", "save_theme", "error", wrapped)
		}
		return ThemeDTO{}, wrapped
	}
	result := ThemeDTO{Theme: string(a.config.Theme)}
	logger := a.logger
	a.mu.Unlock()
	if logger != nil {
		logger.Info("theme configuration saved", "operation", "save_theme", "theme", result.Theme)
	}
	return result, nil
}

// RestoreConfigDefault 将一个公开配置属性的默认值写回 config.yaml。
func (a *DesktopApplication) RestoreConfigDefault(ctx context.Context, property string) error {
	if a == nil {
		return errors.New("desktop application is nil")
	}
	if err := a.ensureWritable(); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	a.mu.Lock()
	if a.state != DesktopStateRunning {
		a.mu.Unlock()
		return domain.NewRuntimeStopping()
	}
	if a.config == nil {
		a.mu.Unlock()
		return errors.New("runtime config is not configured")
	}
	if err := a.config.RestoreDefault(config.ConfigProperty(property)); err != nil {
		wrapped := a.newConfigPersistenceError("restore config default", property, err)
		a.lastError = wrapped
		logger := a.logger
		a.mu.Unlock()
		if logger != nil {
			logger.Error("config default restore failed", "operation", "restore_config_default", "property", property, "error", wrapped)
		}
		return wrapped
	}
	logger := a.logger
	a.mu.Unlock()
	if logger != nil {
		logger.Info("config default restored", "operation", "restore_config_default", "property", property)
	}
	return nil
}

// SaveStorageDirectory 验证并保存目录配置；当前生效路径保持不变，重启后才加载新路径。
func (a *DesktopApplication) SaveStorageDirectory(ctx context.Context, request SaveStorageDirectoryRequest) (StorageDirectoryDTO, error) {
	if a == nil {
		return StorageDirectoryDTO{}, errors.New("desktop application is nil")
	}
	if err := a.ensureWritable(); err != nil {
		return StorageDirectoryDTO{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return StorageDirectoryDTO{}, err
	}
	kind, err := parseStorageDirectoryKind(request.Kind)
	if err != nil {
		return StorageDirectoryDTO{}, a.configPersistenceError("validate storage directory kind", request.Kind, err)
	}
	a.mu.Lock()
	access := a.deps.DirectoryAccess
	configValue := a.config
	state := a.state
	a.mu.Unlock()
	if state != DesktopStateRunning {
		return StorageDirectoryDTO{}, domain.NewRuntimeStopping()
	}
	if configValue == nil {
		return StorageDirectoryDTO{}, errors.New("runtime config is not configured")
	}
	if access == nil {
		return StorageDirectoryDTO{}, a.configPersistenceError("validate storage directory", string(kind), errors.New("directory access is not configured"))
	}
	normalized, err := access.ValidateDirectory(ctx, request.Path)
	if err != nil {
		return StorageDirectoryDTO{}, a.configPersistenceError("validate storage directory", string(kind), err)
	}
	if err := ctx.Err(); err != nil {
		return StorageDirectoryDTO{}, err
	}

	a.mu.Lock()
	if a.state != DesktopStateRunning {
		a.mu.Unlock()
		return StorageDirectoryDTO{}, domain.NewRuntimeStopping()
	}
	if a.config == nil {
		a.mu.Unlock()
		return StorageDirectoryDTO{}, errors.New("runtime config is not configured")
	}
	if err := a.config.SaveStorageDirectory(kind, normalized); err != nil {
		wrapped := a.newConfigPersistenceError("save storage directory", string(kind), err)
		a.lastError = wrapped
		logger := a.logger
		a.mu.Unlock()
		if logger != nil {
			logger.Error("storage directory save failed", "operation", "save_storage_directory", "kind", string(kind), "error", wrapped)
		}
		return StorageDirectoryDTO{}, wrapped
	}
	directory, err := storageDirectoryDTO(*a.config, kind)
	if err != nil {
		a.mu.Unlock()
		return StorageDirectoryDTO{}, err
	}
	logger := a.logger
	a.mu.Unlock()
	if logger != nil {
		logger.Info("storage directory configuration saved", "operation", "save_storage_directory", "kind", string(kind), "restart_required", directory.RestartRequired)
	}
	return directory, nil
}

// ChooseStorageDirectory 打开目录选择器；取消返回空字符串且不改变任何配置。
func (a *DesktopApplication) ChooseStorageDirectory(ctx context.Context, kindValue string) (string, error) {
	if a == nil {
		return "", errors.New("desktop application is nil")
	}
	kind, err := parseStorageDirectoryKind(kindValue)
	if err != nil {
		return "", a.configPersistenceError("choose storage directory", kindValue, err)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	a.mu.Lock()
	chooser := a.deps.DirectoryChooser
	configValue := a.config
	state := a.state
	a.mu.Unlock()
	if state != DesktopStateRunning {
		return "", domain.NewRuntimeStopping()
	}
	if configValue == nil {
		return "", errors.New("runtime config is not configured")
	}
	if chooser == nil {
		return "", a.configPersistenceError("choose storage directory", string(kind), errors.New("directory chooser is not configured"))
	}
	directory, err := storageDirectoryDTO(*configValue, kind)
	if err != nil {
		return "", err
	}
	defaultDirectory := directory.SavedPath
	if _, statErr := os.Stat(defaultDirectory); statErr != nil {
		defaultDirectory = directory.EffectivePath
	}
	title := "选择日志目录"
	if kind == config.StorageDirectoryBackups {
		title = "选择备份目录"
	}
	selected, err := chooser.ChooseDirectory(ctx, title, defaultDirectory)
	if err != nil {
		return "", a.configPersistenceError("choose storage directory", string(kind), err)
	}
	if selected == "" {
		return "", nil
	}
	return selected, nil
}

// storageDirectoriesDTO 将配置保存值与进程启动时冻结的生效值投影到前端。
func storageDirectoriesDTO(cfg config.Config) (StorageDirectoriesDTO, error) {
	logs, err := storageDirectoryDTO(cfg, config.StorageDirectoryLogs)
	if err != nil {
		return StorageDirectoriesDTO{}, err
	}
	backups, err := storageDirectoryDTO(cfg, config.StorageDirectoryBackups)
	if err != nil {
		return StorageDirectoriesDTO{}, err
	}
	return StorageDirectoriesDTO{Logs: logs, Backups: backups}, nil
}

// logLevelDTO 将日志级别的运行时生效值与配置保存值投影到前端。
func logLevelDTO(effective, saved string) (LogLevelDTO, error) {
	effective = strings.ToLower(strings.TrimSpace(effective))
	saved = strings.ToLower(strings.TrimSpace(saved))
	if effective == "" || saved == "" {
		return LogLevelDTO{}, errors.New("log level must not be empty")
	}
	return LogLevelDTO{EffectiveLevel: effective, SavedLevel: saved, RestartRequired: effective != saved}, nil
}

// storageDirectoryDTO 计算单个目录的生效/保存差异，并按物理目录判断重启需求。
func storageDirectoryDTO(cfg config.Config, kind config.StorageDirectoryKind) (StorageDirectoryDTO, error) {
	saved, err := cfg.SavedStorageDirectory(kind)
	if err != nil {
		return StorageDirectoryDTO{}, err
	}
	var effective string
	switch kind {
	case config.StorageDirectoryLogs:
		effective = filepath.Dir(cfg.ResolvedPaths.LogPath)
	case config.StorageDirectoryBackups:
		effective = cfg.ResolvedPaths.BackupDir
	default:
		return StorageDirectoryDTO{}, fmt.Errorf("invalid storage directory kind %q", kind)
	}
	effective = filepath.Clean(effective)
	saved = filepath.Clean(saved)
	return StorageDirectoryDTO{Kind: string(kind), EffectivePath: effective, SavedPath: saved, RestartRequired: !config.SamePhysicalDirectory(effective, saved)}, nil
}

// parseStorageDirectoryKind 将 Wails 请求限制到两个产品目录类型。
func parseStorageDirectoryKind(value string) (config.StorageDirectoryKind, error) {
	kind := config.StorageDirectoryKind(value)
	if kind != config.StorageDirectoryLogs && kind != config.StorageDirectoryBackups {
		return "", fmt.Errorf("invalid storage directory kind %q: want logs or backups", value)
	}
	return kind, nil
}
