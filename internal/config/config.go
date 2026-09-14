package config

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

//go:embed defaults.yaml
var embeddedConfig []byte

type Config struct {
	App           AppConfig     `mapstructure:"app"`
	Log           LogConfig     `mapstructure:"log"`
	Backup        BackupConfig  `mapstructure:"backup"`
	ResolvedPaths ResolvedPaths `mapstructure:"-"`
}

type AppConfig struct {
	Name    string `mapstructure:"name"`
	Version string `mapstructure:"version"`
}

type LogConfig struct {
	Level string `mapstructure:"level"`
	Path  string `mapstructure:"path"`
}

// BackupConfig 描述迁移备份目录的用户可配置部分。
type BackupConfig struct {
	Dir string `mapstructure:"dir"`
}

// ResolvedPaths 保存启动时解析出的实际文件系统路径。
type ResolvedPaths struct {
	DatabasePath string
	LogPath      string
	BackupDir    string
}

// Load 加载可执行文件目录下的用户配置；文件不存在时回退到内嵌默认配置。
func Load() (Config, error) {
	executablePath, err := os.Executable()
	if err != nil {
		return Config{}, fmt.Errorf("get executable path: %w", err)
	}
	return loadFromExecutable(executablePath)
}

// loadFromExecutable 按指定可执行文件路径加载配置，便于测试固定的解析基准目录。
func loadFromExecutable(executablePath string) (Config, error) {
	baseDir := filepath.Dir(filepath.Clean(executablePath))
	return loadFromDirectory(baseDir)
}

// loadFromDirectory 从指定程序目录加载配置并生成解析后的固定路径。
func loadFromDirectory(baseDir string) (Config, error) {
	baseDir, err := filepath.Abs(baseDir)
	if err != nil {
		return Config{}, fmt.Errorf("resolve executable directory %q: %w", baseDir, err)
	}

	v := viper.New()
	setDefaults(v)

	configPath := filepath.Join(baseDir, "config.yaml")
	if _, statErr := os.Stat(configPath); statErr == nil {
		v.SetConfigFile(configPath)
		if err := v.ReadInConfig(); err != nil {
			return Config{}, fmt.Errorf("read config file %q: %w", configPath, err)
		}
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return Config{}, fmt.Errorf("inspect config file %q: %w", configPath, statErr)
	} else {
		v.SetConfigType("yaml")
		if err := v.ReadConfig(bytes.NewReader(embeddedConfig)); err != nil {
			return Config{}, fmt.Errorf("read embedded config: %w", err)
		}
	}
	if err := rejectLegacyPathSettings(v); err != nil {
		return Config{}, err
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	paths, err := cfg.ResolvePaths(baseDir)
	if err != nil {
		return Config{}, err
	}
	cfg.ResolvedPaths = paths
	return cfg, nil
}

// setDefaults 为配置文件未覆盖的字段设置稳定默认值。
func setDefaults(v *viper.Viper) {
	v.SetDefault("app.name", "TimeCue")
	v.SetDefault("app.version", "0.0.1")
	v.SetDefault("log.level", "info")
	v.SetDefault("log.path", ".\\logs\\timecue.log")
	v.SetDefault("backup.dir", ".\\backups")
}

// rejectLegacyPathSettings 拒绝会破坏固定数据库位置契约的旧配置键。
func rejectLegacyPathSettings(v *viper.Viper) error {
	legacyKeys := []string{
		"data_dir",
		"data-dir",
		"database",
		"database.path",
		"database_path",
		"db.path",
		"db_path",
		"paths.database",
		"paths.database_path",
	}
	for _, key := range legacyKeys {
		if v.IsSet(key) {
			return fmt.Errorf("unsupported configuration key %q: database path is fixed", key)
		}
	}
	return nil
}

// Validate 校验启动阶段必须满足的配置约束。
func (c Config) Validate() error {
	if err := c.Log.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(c.Backup.Dir) == "" {
		return errors.New("backup directory must not be empty")
	}
	return nil
}

// Validate 校验日志级别、路径和扩展名约束。
func (c LogConfig) Validate() error {
	level := strings.ToLower(strings.TrimSpace(c.Level))
	switch level {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("invalid log level %q: want debug, info, warn, or error", c.Level)
	}

	path := strings.TrimSpace(c.Path)
	if path == "" {
		return errors.New("log path must not be empty")
	}
	if !strings.EqualFold(filepath.Ext(path), ".log") {
		return fmt.Errorf("log path %q must use the .log extension", c.Path)
	}
	return nil
}

// ResolvePaths 解析日志和备份相对路径，并生成不可被配置覆盖的数据库路径。
func (c Config) ResolvePaths(baseDir string) (ResolvedPaths, error) {
	if err := c.Validate(); err != nil {
		return ResolvedPaths{}, err
	}
	baseDir, err := filepath.Abs(baseDir)
	if err != nil {
		return ResolvedPaths{}, fmt.Errorf("resolve executable directory %q: %w", baseDir, err)
	}
	localAppData, ok := os.LookupEnv("LOCALAPPDATA")
	if !ok || strings.TrimSpace(localAppData) == "" {
		return ResolvedPaths{}, errors.New("resolve database path: LOCALAPPDATA must not be empty")
	}
	return ResolvedPaths{
		DatabasePath: filepath.Clean(filepath.Join(localAppData, "TimeCue", "timecue.db")),
		LogPath:      resolvePath(baseDir, c.Log.Path),
		BackupDir:    resolvePath(baseDir, c.Backup.Dir),
	}, nil
}

// resolvePath 将相对路径固定解析到程序目录，避免工作目录变化改变数据位置。
func resolvePath(baseDir, path string) string {
	path = filepath.Clean(strings.TrimSpace(path))
	if !filepath.IsAbs(path) {
		path = filepath.Join(baseDir, path)
	}
	return filepath.Clean(path)
}

// LogFilePath 兼容调用方按当前目录解析日志路径的旧辅助方法。
func (c Config) LogFilePath() (string, error) {
	if err := c.Log.Validate(); err != nil {
		return "", err
	}
	workingDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}

	path := filepath.Clean(strings.TrimSpace(c.Log.Path))
	if !filepath.IsAbs(path) {
		path = filepath.Join(workingDir, path)
	}
	return filepath.Clean(path), nil
}
