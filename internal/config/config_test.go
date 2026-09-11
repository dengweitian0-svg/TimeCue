package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadUsesEmbeddedDefaults(t *testing.T) {
	t.Chdir(t.TempDir())

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Log.Level != "info" {
		t.Fatalf("Log.Level = %q, want info", cfg.Log.Level)
	}
	if cfg.Log.Path != "./log/timecue.log" {
		t.Fatalf("Log.Path = %q, want ./log/timecue.log", cfg.Log.Path)
	}
}

func TestLoadUsesExternalConfigFromWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	configFile := filepath.Join(dir, "config.yaml")
	config := []byte("app:\n  name: CustomTimeCue\nlog:\n  level: debug\n  path: ./custom/timecue.log\n")
	if err := os.WriteFile(configFile, config, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.App.Name != "CustomTimeCue" {
		t.Fatalf("App.Name = %q, want CustomTimeCue", cfg.App.Name)
	}
	if cfg.Log.Level != "debug" {
		t.Fatalf("Log.Level = %q, want debug", cfg.Log.Level)
	}
	if cfg.Log.Path != "./custom/timecue.log" {
		t.Fatalf("Log.Path = %q, want ./custom/timecue.log", cfg.Log.Path)
	}
}

func TestConfigRejectsInvalidLogSettings(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
	}{
		{
			name: "invalid level",
			cfg:  Config{Log: LogConfig{Level: "trace", Path: "timecue.log"}},
		},
		{
			name: "missing path",
			cfg:  Config{Log: LogConfig{Level: "info"}},
		},
		{
			name: "wrong extension",
			cfg:  Config{Log: LogConfig{Level: "info", Path: "timecue.json"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.cfg.Validate(); err == nil {
				t.Fatal("Validate() error = nil, want error")
			}
		})
	}
}

func TestLogFilePathResolvesRelativeToWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	cfg := Config{Log: LogConfig{Level: "info", Path: "./log/timecue.log"}}

	path, err := cfg.LogFilePath()
	if err != nil {
		t.Fatalf("LogFilePath() error = %v", err)
	}
	want := filepath.Join(dir, "log", "timecue.log")
	if path != filepath.Clean(want) {
		t.Fatalf("LogFilePath() = %q, want %q", path, filepath.Clean(want))
	}
}

func TestLogFilePathPreservesAbsolutePath(t *testing.T) {
	absolute := filepath.Join(t.TempDir(), "timecue.log")
	cfg := Config{Log: LogConfig{Level: "info", Path: absolute}}

	path, err := cfg.LogFilePath()
	if err != nil {
		t.Fatalf("LogFilePath() error = %v", err)
	}
	if path != filepath.Clean(absolute) {
		t.Fatalf("LogFilePath() = %q, want %q", path, filepath.Clean(absolute))
	}
}
