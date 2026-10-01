package config

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"sync"
	"sync/atomic"

	logger "github.com/dengweitian0-svg/TimeCue/internal/log"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
)

//go:embed default.yaml
var defaultConfig []byte

const configFileName = "config.yaml"

type Config struct {
	App appConfig `mapstructure:"app"`
	Log logConfig `mapstructure:"log"`
	DB  dbConfig  `mapstructure:"db"`
}

type appConfig struct {
	Name    string `mapstructure:"name"`
	Version string `mapstructure:"version"`
}

type logConfig struct {
	Level   string `mapstructure:"level"`
	LogPath string `mapstructure:"log_path"`
}

type dbConfig struct {
	DBPath string `mapstructure:"db_path"`
}

// Manager 配置管理器, 提供配置的加载和热更新功能
type Manager struct {
	v          *viper.Viper
	currentCfg atomic.Pointer[Config]
	watchOnce  sync.Once
}

// NewManager 创建一个配置管理器实例, 并设置用户配置文件路径
// 注意, 这里并不加载任何配置
func NewManager() (*Manager, error) {
	m := &Manager{
		v: newConfigViper(),
	}
	m.v.OnConfigChange(func(event fsnotify.Event) {
		if err := m.reloadConfig(); err != nil {
			logger.Error("[Manager] failed to reload config", "error", err, "file", event.Name)
			return
		}
		logger.Info("[Manager] config file changed", "file", event.Name)
	})
	return m, nil
}

// LoadConfig 加载配置文件，并在首次加载成功后启动热更新监听。
func (m *Manager) LoadConfig() error {
	if err := m.reloadConfig(); err != nil {
		logger.Error("[Manager] failed to load config", "error", err, "file", configFileName)
		return fmt.Errorf("failed to load config: %w", err)
	}
	m.watchOnce.Do(m.v.WatchConfig)
	return nil
}

// CurrentConfig 返回当前配置的值快照。配置尚未成功加载时返回 false。
func (m *Manager) CurrentConfig() (Config, error) {
	current := m.currentCfg.Load()
	if current == nil {
		return Config{}, fmt.Errorf("[config] no config")
	}
	return *current, nil
}

// newConfigViper 创建只负责读取 config.yaml 的 Viper 实例。
// 默认值由 loadDefaultConfig 单独加载，避免监听器状态和默认基线相互污染。
func newConfigViper() *viper.Viper {
	v := viper.New()
	v.SetConfigFile(configFileName)
	v.SetConfigType("yaml")
	return v
}

// reloadConfig 从不可变默认基线和用户配置构造新快照，并原子替换当前配置。
func (m *Manager) reloadConfig() error {
	v := newConfigViper()
	if err := loadDefaultConfig(v); err != nil {
		return fmt.Errorf("load default config: %w", err)
	}
	if err := v.ReadInConfig(); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read %s: %w", configFileName, err)
	}

	next := new(Config)
	if err := v.Unmarshal(next); err != nil {
		return fmt.Errorf("unmarshal %s: %w", configFileName, err)
	}

	// 先原子读取旧快照，再原子写入新快照；旧快照不会被原地修改。
	m.currentCfg.Store(next)
	logger.SetLevel(translateLogLevel(next.Log.Level))
	return nil
}

// loadDefaultConfig 将嵌入的默认配置加载为 Viper 默认值。
// defaultConfig 只通过 bytes.Reader 读取，绝不会被用户配置或热更新写回。
func loadDefaultConfig(v *viper.Viper) error {
	defaults := viper.New()
	defaults.SetConfigType("yaml")
	if err := defaults.ReadConfig(bytes.NewReader(defaultConfig)); err != nil {
		return err
	}
	for _, key := range defaults.AllKeys() {
		v.SetDefault(key, defaults.Get(key))
	}
	return nil
}

func translateLogLevel(level string) slog.Level {
	switch level {
	case "info":
		return slog.LevelInfo
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
