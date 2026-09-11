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

//go:embed config.yaml
var embeddedConfig []byte

type Config struct {
	App AppConfig `mapstructure:"app"`
	Log LogConfig `mapstructure:"log"`
}

type AppConfig struct {
	Name    string `mapstructure:"name"`
	Version string `mapstructure:"version"`
}

type LogConfig struct {
	Level string `mapstructure:"level"`
	Path  string `mapstructure:"path"`
}

func Load() (Config, error) {
	workingDir, err := os.Getwd()
	if err != nil {
		return Config{}, fmt.Errorf("get working directory: %w", err)
	}

	v := viper.New()
	setDefaults(v)

	configPath := filepath.Join(workingDir, "config.yaml")
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

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("app.name", "TimeCue")
	v.SetDefault("app.version", "0.0.1")
	v.SetDefault("log.level", "info")
	v.SetDefault("log.path", "./log/timecue.log")
}

func (c Config) Validate() error {
	level := strings.ToLower(strings.TrimSpace(c.Log.Level))
	switch level {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("invalid log level %q: want debug, info, warn, or error", c.Log.Level)
	}

	path := strings.TrimSpace(c.Log.Path)
	if path == "" {
		return errors.New("log path must not be empty")
	}
	if !strings.EqualFold(filepath.Ext(path), ".log") {
		return fmt.Errorf("log path %q must use the .log extension", c.Log.Path)
	}
	return nil
}

func (c Config) LogFilePath() (string, error) {
	if err := c.Validate(); err != nil {
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
