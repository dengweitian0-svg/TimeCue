package config

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/spf13/viper"
	"go.yaml.in/yaml/v3"
)

//go:embed defaults.yaml
var embeddedConfig []byte

type Config struct {
	App           AppConfig     `mapstructure:"app"`
	Log           LogConfig     `mapstructure:"log"`
	Backup        BackupConfig  `mapstructure:"backup"`
	Window        WindowConfig  `mapstructure:"window"`
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

// StorageDirectoryKind 标识可以由用户配置的持久化目录类型。
type StorageDirectoryKind string

const (
	// StorageDirectoryLogs 表示日志文件所在目录。
	StorageDirectoryLogs StorageDirectoryKind = "logs"
	// StorageDirectoryBackups 表示 SQLite 迁移备份所在目录。
	StorageDirectoryBackups StorageDirectoryKind = "backups"
)

// DirectoryKind 是目录设置 API 使用的兼容别名。
type DirectoryKind = StorageDirectoryKind

const (
	// DirectoryKindLogs 是日志目录设置的兼容常量。
	DirectoryKindLogs = StorageDirectoryLogs
	// DirectoryKindBackups 是备份目录设置的兼容常量。
	DirectoryKindBackups = StorageDirectoryBackups
)

// CloseBehavior 表示用户关闭窗口时应用应隐藏到托盘还是直接退出。
type CloseBehavior string

const (
	// CloseBehaviorTray 表示关闭窗口后继续运行并隐藏到托盘。
	CloseBehaviorTray CloseBehavior = "tray"
	// CloseBehaviorExit 表示关闭窗口后执行正式退出流程。
	CloseBehaviorExit CloseBehavior = "exit"
)

// WindowConfig 描述窗口相关的用户配置。
type WindowConfig struct {
	CloseBehavior CloseBehavior `mapstructure:"close_behavior"`
}

// ResolvedPaths 保存启动时解析出的实际文件系统路径。
type ResolvedPaths struct {
	ConfigPath      string
	DatabasePath    string
	LogPath         string
	BackupDir       string
	PreferencesPath string
}

// configSaveMu 串行化所有配置文件保存，避免日志和备份设置并发读旧文件后互相覆盖。
var configSaveMu sync.Mutex

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
	if closeBehavior, found, err := loadCloseBehavior(paths.PreferencesPath); err != nil {
		return Config{}, err
	} else if found {
		// 偏好文件只拥有关闭行为这一项，其他配置仍由 config.yaml 决定。
		cfg.Window.CloseBehavior = closeBehavior
	}
	cfg.ResolvedPaths = paths
	return cfg, nil
}

// setDefaults 为配置文件未覆盖的字段设置稳定默认值。
func setDefaults(v *viper.Viper) {
	v.SetDefault("app.name", "TimeCue")
	v.SetDefault("app.version", "0.0.1")
	v.SetDefault("log.level", "info")
	// 使用 YAML/Unix 分隔符保存跨平台配置，解析后由 filepath 转换为宿主平台路径。
	v.SetDefault("log.path", "./logs/timecue.log")
	v.SetDefault("backup.dir", "./backups")
	v.SetDefault("window.close_behavior", string(CloseBehaviorTray))
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
	if err := c.Window.Validate(); err != nil {
		return err
	}
	return nil
}

// Validate 校验窗口关闭行为；空值仅作为手工构造 Config 的零值默认值。
func (c WindowConfig) Validate() error {
	if c.CloseBehavior == "" {
		return nil
	}
	return validateCloseBehavior(c.CloseBehavior)
}

// validateCloseBehavior 严格校验已经出现在用户输入中的关闭行为。
func validateCloseBehavior(behavior CloseBehavior) error {
	switch behavior {
	case CloseBehaviorTray, CloseBehaviorExit:
		return nil
	default:
		return fmt.Errorf("invalid window close behavior %q: want tray or exit", behavior)
	}
}

// validateExplicitCloseBehavior 拒绝偏好文件或保存请求中的空关闭行为。
func validateExplicitCloseBehavior(behavior CloseBehavior) error {
	return validateCloseBehavior(behavior)
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
	logPath := resolvePath(baseDir, c.Log.Path)
	logDirectory, err := normalizeConfiguredDirectory(filepath.Dir(logPath), "log")
	if err != nil {
		return ResolvedPaths{}, err
	}
	backupDirectory, err := normalizeConfiguredDirectory(resolvePath(baseDir, c.Backup.Dir), "backup")
	if err != nil {
		return ResolvedPaths{}, err
	}
	return ResolvedPaths{
		ConfigPath:      filepath.Clean(filepath.Join(baseDir, "config.yaml")),
		DatabasePath:    filepath.Clean(filepath.Join(localAppData, "TimeCue", "timecue.db")),
		LogPath:         filepath.Join(logDirectory, filepath.Base(logPath)),
		BackupDir:       backupDirectory,
		PreferencesPath: filepath.Clean(filepath.Join(localAppData, "TimeCue", "preferences.yaml")),
	}, nil
}

// loadCloseBehavior 从用户偏好文件读取关闭行为；文件不存在时不覆盖配置默认值。
func loadCloseBehavior(path string) (CloseBehavior, bool, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read preferences file %q: %w", path, err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(contents, &document); err != nil {
		return "", false, fmt.Errorf("decode preferences file %q: %w", path, err)
	}
	windowValue, ok := document["window"]
	if !ok {
		return "", false, nil
	}
	window, ok := windowValue.(map[string]any)
	if !ok {
		return "", false, fmt.Errorf("invalid preferences file %q: window must be a mapping", path)
	}
	closeBehaviorValue, ok := window["close_behavior"]
	if !ok {
		return "", false, nil
	}
	closeBehavior, ok := closeBehaviorValue.(string)
	if !ok {
		return "", false, fmt.Errorf("invalid preferences file %q: window.close_behavior must be a string", path)
	}
	behavior := CloseBehavior(closeBehavior)
	if err := validateExplicitCloseBehavior(behavior); err != nil {
		return "", false, fmt.Errorf("invalid preferences file %q: %w", path, err)
	}
	return behavior, true, nil
}

// SaveCloseBehavior 原子保存关闭行为，并在保存成功后更新内存配置。
func (c *Config) SaveCloseBehavior(behavior CloseBehavior) error {
	if c == nil {
		return errors.New("save close behavior: config must not be nil")
	}
	if err := validateExplicitCloseBehavior(behavior); err != nil {
		return fmt.Errorf("save close behavior: %w", err)
	}
	path := filepath.Clean(strings.TrimSpace(c.ResolvedPaths.PreferencesPath))
	if path == "." || path == "" {
		return errors.New("save close behavior: preferences path must not be empty")
	}
	configSaveMu.Lock()
	defer configSaveMu.Unlock()
	document, err := readPreferencesDocument(path)
	if err != nil {
		return err
	}
	window, ok := document["window"].(map[string]any)
	if !ok {
		window = make(map[string]any)
		document["window"] = window
	}
	window["close_behavior"] = string(behavior)
	contents, err := yaml.Marshal(document)
	if err != nil {
		return fmt.Errorf("encode preferences file %q: %w", path, err)
	}
	if err := writePreferencesAtomically(path, contents); err != nil {
		return err
	}
	c.Window.CloseBehavior = behavior
	return nil
}

// SaveStorageDirectory 原子保存日志或备份目录，并保留当前进程仍在使用的 ResolvedPaths。
// 配置写入成功前不改变内存中的保存值；保存成功后也不替换运行中的日志器或 SQLite Store，
// 因此新目录统一在下一次启动时生效。
func (c *Config) SaveStorageDirectory(kind StorageDirectoryKind, directory string) error {
	if c == nil {
		return errors.New("save storage directory: config must not be nil")
	}
	if err := validateStorageDirectoryKind(kind); err != nil {
		return err
	}
	configPath := filepath.Clean(strings.TrimSpace(c.ResolvedPaths.ConfigPath))
	if configPath == "." || configPath == "" {
		return errors.New("save storage directory: config path must not be empty")
	}
	normalizedDirectory, err := ValidateDirectoryPath(context.Background(), directory)
	if err != nil {
		return fmt.Errorf("validate %s directory: %w", kind, err)
	}

	configSaveMu.Lock()
	defer configSaveMu.Unlock()
	document, err := readConfigDocument(configPath)
	if err != nil {
		return err
	}

	var savedValue string
	switch kind {
	case StorageDirectoryLogs:
		if err := c.Log.Validate(); err != nil {
			return fmt.Errorf("save log directory: %w", err)
		}
		filename := filepath.Base(strings.TrimSpace(c.Log.Path))
		savedValue = filepath.Join(normalizedDirectory, filename)
		if err := setConfigDocumentValue(document, "log", "path", savedValue, configPath); err != nil {
			return err
		}
	case StorageDirectoryBackups:
		savedValue = normalizedDirectory
		if err := setConfigDocumentValue(document, "backup", "dir", savedValue, configPath); err != nil {
			return err
		}
	}

	contents, err := yaml.Marshal(document)
	if err != nil {
		return fmt.Errorf("encode config file %q: %w", configPath, err)
	}
	if err := writeConfigAtomically(configPath, contents); err != nil {
		return err
	}

	// 只有原子替换成功后才改变配置保存值；ResolvedPaths 仍代表本进程的实际生效目录。
	switch kind {
	case StorageDirectoryLogs:
		c.Log.Path = savedValue
	case StorageDirectoryBackups:
		c.Backup.Dir = savedValue
	}
	return nil
}

// SaveLogDirectory 保存日志目录并保留 Log.Path 中原有的日志文件名。
func (c *Config) SaveLogDirectory(directory string) error {
	return c.SaveStorageDirectory(StorageDirectoryLogs, directory)
}

// SaveBackupDirectory 保存 SQLite 迁移备份目录。
func (c *Config) SaveBackupDirectory(directory string) error {
	return c.SaveStorageDirectory(StorageDirectoryBackups, directory)
}

// SavedStorageDirectory 返回配置中下次启动将使用的规范目录，而不是当前进程的生效目录。
func (c Config) SavedStorageDirectory(kind StorageDirectoryKind) (string, error) {
	if err := validateStorageDirectoryKind(kind); err != nil {
		return "", err
	}
	baseDir := filepath.Dir(filepath.Clean(strings.TrimSpace(c.ResolvedPaths.ConfigPath)))
	if baseDir == "." || baseDir == "" {
		// 手工构造的测试配置可能没有 ConfigPath；用当前解析路径提供兼容的受控视图。
		switch kind {
		case StorageDirectoryLogs:
			baseDir = filepath.Dir(c.ResolvedPaths.LogPath)
		case StorageDirectoryBackups:
			baseDir = filepath.Dir(c.ResolvedPaths.BackupDir)
		}
	}
	var value string
	switch kind {
	case StorageDirectoryLogs:
		value = filepath.Dir(resolvePath(baseDir, c.Log.Path))
	case StorageDirectoryBackups:
		value = resolvePath(baseDir, c.Backup.Dir)
	}
	return normalizeConfiguredDirectory(value, string(kind))
}

// StorageDirectory 返回保存配置中的目录，是 SavedStorageDirectory 的简短别名。
func (c Config) StorageDirectory(kind StorageDirectoryKind) (string, error) {
	return c.SavedStorageDirectory(kind)
}

// validateStorageDirectoryKind 限定设置 API 只能修改日志和备份两类目录。
func validateStorageDirectoryKind(kind StorageDirectoryKind) error {
	switch kind {
	case StorageDirectoryLogs, StorageDirectoryBackups:
		return nil
	default:
		return fmt.Errorf("invalid storage directory kind %q: want logs or backups", kind)
	}
}

// readConfigDocument 读取现有配置并保留未由本用例拥有的 YAML 字段。
func readConfigDocument(path string) (map[string]any, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			contents = embeddedConfig
		} else {
			return nil, fmt.Errorf("read config file %q: %w", path, err)
		}
	}
	var document map[string]any
	if err := yaml.Unmarshal(contents, &document); err != nil {
		return nil, fmt.Errorf("decode config file %q: %w", path, err)
	}
	if document == nil {
		document = make(map[string]any)
	}
	return document, nil
}

// setConfigDocumentValue 只修改目标配置节中的一个字段，不覆盖未知字段。
func setConfigDocumentValue(document map[string]any, section, key, value, path string) error {
	fields, ok := document[section]
	if !ok {
		fields = make(map[string]any)
		document[section] = fields
	}
	mapping, ok := fields.(map[string]any)
	if !ok {
		return fmt.Errorf("invalid config file %q: %s must be a mapping", path, section)
	}
	mapping[key] = value
	return nil
}

// writeConfigContents、syncConfigFile 和 replaceConfigFile 是配置原子保存的可控边界。
var (
	writeConfigContents = func(file *os.File, contents []byte) (int, error) { return file.Write(contents) }
	syncConfigFile      = func(file *os.File) error { return file.Sync() }
	replaceConfigFile   = replaceFile
)

// writeConfigAtomically 通过同目录临时文件、Sync、关闭和原子替换保存配置。
func writeConfigAtomically(path string, contents []byte) (err error) {
	dir := filepath.Dir(path)
	temporary, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary config file in %q: %w", dir, err)
	}
	temporaryPath := temporary.Name()
	temporaryClosed := false
	defer func() {
		if !temporaryClosed {
			if closeErr := temporary.Close(); closeErr != nil {
				closeErr = fmt.Errorf("close temporary config file %q: %w", temporaryPath, closeErr)
				if err == nil {
					err = closeErr
				} else {
					err = errors.Join(err, closeErr)
				}
			}
		}
		if cleanupErr := os.Remove(temporaryPath); cleanupErr != nil && !errors.Is(cleanupErr, fs.ErrNotExist) {
			cleanupErr = fmt.Errorf("remove temporary config file %q: %w", temporaryPath, cleanupErr)
			if err == nil {
				err = cleanupErr
			} else {
				err = errors.Join(err, cleanupErr)
			}
		}
	}()

	written, err := writeConfigContents(temporary, contents)
	if err != nil {
		return fmt.Errorf("write temporary config file %q: %w", temporaryPath, err)
	}
	if written != len(contents) {
		return fmt.Errorf("write temporary config file %q: %w", temporaryPath, io.ErrShortWrite)
	}
	if err := syncConfigFile(temporary); err != nil {
		return fmt.Errorf("sync temporary config file %q: %w", temporaryPath, err)
	}
	closeErr := temporary.Close()
	temporaryClosed = true
	if closeErr != nil {
		return fmt.Errorf("close temporary config file %q: %w", temporaryPath, closeErr)
	}
	if err := replaceConfigFile(temporaryPath, path); err != nil {
		return fmt.Errorf("replace config file %q: %w", path, err)
	}
	return nil
}

// readPreferencesDocument 读取并解析偏好文档，同时保留未被配置层拥有的字段。
func readPreferencesDocument(path string) (map[string]any, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return make(map[string]any), nil
		}
		return nil, fmt.Errorf("read preferences file %q: %w", path, err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(contents, &document); err != nil {
		return nil, fmt.Errorf("decode preferences file %q: %w", path, err)
	}
	if document == nil {
		document = make(map[string]any)
	}
	if window, exists := document["window"]; exists {
		if _, ok := window.(map[string]any); !ok {
			return nil, fmt.Errorf("invalid preferences file %q: window must be a mapping", path)
		}
	}
	return document, nil
}

// writePreferencesContents 保留临时文件写入边界，便于验证失败时的旧文件语义。
var writePreferencesContents = func(file *os.File, contents []byte) (int, error) {
	return file.Write(contents)
}

// syncPreferencesFile 保留临时文件同步边界，便于验证持久化失败路径。
var syncPreferencesFile = func(file *os.File) error {
	return file.Sync()
}

// replacePreferencesFile 保留目标替换边界，便于验证替换失败路径。
var replacePreferencesFile = replaceFile

// writePreferencesAtomically 通过同目录临时文件和替换保证失败时旧文件仍可读取。
func writePreferencesAtomically(path string, contents []byte) (err error) {
	dir := filepath.Dir(path)
	temporary, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary preferences file in %q: %w", dir, err)
	}
	temporaryPath := temporary.Name()
	temporaryClosed := false
	defer func() {
		if !temporaryClosed {
			if closeErr := temporary.Close(); closeErr != nil {
				closeErr = fmt.Errorf("close temporary preferences file %q: %w", temporaryPath, closeErr)
				if err == nil {
					err = closeErr
				} else {
					err = errors.Join(err, closeErr)
				}
			}
		}
		if cleanupErr := os.Remove(temporaryPath); cleanupErr != nil && !errors.Is(cleanupErr, fs.ErrNotExist) {
			cleanupErr = fmt.Errorf("remove temporary preferences file %q: %w", temporaryPath, cleanupErr)
			if err == nil {
				err = cleanupErr
			} else {
				err = errors.Join(err, cleanupErr)
			}
		}
	}()
	if _, err := writePreferencesContents(temporary, contents); err != nil {
		return fmt.Errorf("write temporary preferences file %q: %w", temporaryPath, err)
	}
	if err := syncPreferencesFile(temporary); err != nil {
		return fmt.Errorf("sync temporary preferences file %q: %w", temporaryPath, err)
	}
	closeErr := temporary.Close()
	temporaryClosed = true
	if closeErr != nil {
		return fmt.Errorf("close temporary preferences file %q: %w", temporaryPath, closeErr)
	}
	if err := replacePreferencesFile(temporaryPath, path); err != nil {
		return fmt.Errorf("replace preferences file %q: %w", path, err)
	}
	return nil
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
