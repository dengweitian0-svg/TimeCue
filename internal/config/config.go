package config

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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
	ChatModel []ChatProvider `mapstructure:"chat_model"`
	Chat      ChatRuntime    `mapstructure:"chat"`
	App       appConfig      `mapstructure:"app"`
	Log       logConfig      `mapstructure:"log"`
	DB        dbConfig       `mapstructure:"db"`
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
	currentCfg atomic.Pointer[Config]
	reloadMu   sync.Mutex
	watchMu    sync.Mutex
	watcher    *fsnotify.Watcher
	watchDone  chan struct{}
	dataDir    string
	configPath string
}

// NewManager 创建一个配置管理器实例, 并设置用户配置文件路径
// 注意, 这里并不加载任何配置
func NewManager(directory ...string) (*Manager, error) {
	if len(directory) > 1 {
		return nil, fmt.Errorf("only one data directory is allowed")
	}
	base := "."
	if len(directory) == 1 {
		base = directory[0]
		if strings.TrimSpace(base) == "" {
			return nil, fmt.Errorf("data directory is empty")
		}
	}
	abs, err := filepath.Abs(base)
	if err != nil {
		return nil, fmt.Errorf("resolve data directory: %w", err)
	}
	path := filepath.Join(abs, configFileName)
	return &Manager{dataDir: abs, configPath: path}, nil
}

// LoadConfig 加载配置文件，并在首次加载成功后启动热更新监听。
func (m *Manager) LoadConfig() error {
	if err := m.reloadConfig(); err != nil {
		logger.Error("[Manager] failed to load config", "error", err, "file", m.configPath)
		return fmt.Errorf("failed to load config: %w", err)
	}
	m.watchMu.Lock()
	defer m.watchMu.Unlock()
	if m.watcher != nil {
		return nil
	}
	if err := os.MkdirAll(m.dataDir, 0o755); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create config watcher: %w", err)
	}
	if err := watcher.Add(m.dataDir); err != nil {
		_ = watcher.Close()
		return fmt.Errorf("watch config directory: %w", err)
	}
	m.watcher, m.watchDone = watcher, make(chan struct{})
	go m.watch(watcher, m.watchDone)
	// 补读监听建立前的文件变化，避免初始化窗口丢失更新。
	return m.reloadConfig()
}

// watch 监听固定目录；文件删除和原子替换不会结束监听，配置解析仍复用 Viper。
func (m *Manager) watch(watcher *fsnotify.Watcher, done chan struct{}) {
	defer close(done)
	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if filepath.Clean(event.Name) != m.configPath {
				continue
			}
			if err := m.reloadConfig(); err != nil {
				logger.Error("[Manager] failed to reload config", "error", err, "file", event.Name)
			} else {
				logger.Info("[Manager] config file changed", "file", event.Name)
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			logger.Error("[Manager] config watcher failed", "error", err)
		}
	}
}

// Close 关闭并等待配置监听，调用方在关闭日志之前释放监听资源。
func (m *Manager) Close() {
	m.watchMu.Lock()
	defer m.watchMu.Unlock()
	if m.watcher != nil {
		_ = m.watcher.Close()
		<-m.watchDone
		m.watcher = nil
	}
}

// CurrentConfig 返回当前配置的值快照。配置尚未成功加载时返回 false。
func (m *Manager) CurrentConfig() (Config, error) {
	current := m.currentCfg.Load()
	if current == nil {
		return Config{}, fmt.Errorf("[config] no config")
	}
	result := *current
	result.ChatModel = append([]ChatProvider(nil), current.ChatModel...)
	for i := range result.ChatModel {
		result.ChatModel[i].Models = append([]ChatModel(nil), current.ChatModel[i].Models...)
		for j := range result.ChatModel[i].Models {
			if value := result.ChatModel[i].Models[j].MaxOutputTokens; value != nil {
				copied := *value
				result.ChatModel[i].Models[j].MaxOutputTokens = &copied
			}
		}
	}
	if current.Chat.MaxRetry != nil {
		copied := *current.Chat.MaxRetry
		result.Chat.MaxRetry = &copied
	}
	return result, nil
}

// newConfigViper 创建只负责读取 config.yaml 的 Viper 实例。
// 默认值由 loadDefaultConfig 单独加载，避免监听器状态和默认基线相互污染。
func newConfigViper(path string) *viper.Viper {
	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")
	return v
}

// reloadConfig 从不可变默认基线和用户配置构造新快照，并原子替换当前配置。
func (m *Manager) reloadConfig() error {
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()
	v := newConfigViper(m.configPath)
	if err := loadDefaultConfig(v); err != nil {
		return fmt.Errorf("load default config: %w", err)
	}
	if err := v.ReadInConfig(); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read %s: %w", m.configPath, err)
	}

	next := new(Config)
	if err := v.Unmarshal(next); err != nil {
		return fmt.Errorf("unmarshal %s: %w", m.configPath, err)
	}

	// 先原子读取旧快照，再原子写入新快照；旧快照不会被原地修改。
	m.currentCfg.Store(next)
	logger.SetLevel(translateLogLevel(next.Log.Level))
	return nil
}

// ResolvePath 固定普通运行文件的基准，不修改配置快照中的原始文本。
func (m *Manager) ResolvePath(path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(m.dataDir, path)
}

// ResolveDatabasePath 保留 SQLite 特殊数据源的既有语义。
func (m *Manager) ResolveDatabasePath(path string) string {
	if path == ":memory:" || strings.HasPrefix(path, "file:") {
		return path
	}
	return m.ResolvePath(path)
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
