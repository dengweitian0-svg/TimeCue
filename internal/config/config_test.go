package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadUsesEmbeddedDefaults(t *testing.T) {
	t.Setenv("LOCALAPPDATA", filepath.Join(t.TempDir(), "LocalAppData"))
	dir := t.TempDir()

	cfg, err := loadFromExecutable(filepath.Join(dir, "timecue.exe"))
	if err != nil {
		t.Fatalf("loadFromExecutable() error = %v", err)
	}
	if cfg.Log.Level != "info" {
		t.Fatalf("Log.Level = %q, want info", cfg.Log.Level)
	}
	if cfg.Log.Path != "./logs/timecue.log" {
		t.Fatalf("Log.Path = %q, want ./logs/timecue.log", cfg.Log.Path)
	}
	if cfg.Backup.Dir != "./backups" {
		t.Fatalf("Backup.Dir = %q, want ./backups", cfg.Backup.Dir)
	}
	if cfg.ResolvedPaths.DatabasePath != filepath.Join(os.Getenv("LOCALAPPDATA"), "TimeCue", "timecue.db") {
		t.Fatalf("DatabasePath = %q, want fixed LOCALAPPDATA path", cfg.ResolvedPaths.DatabasePath)
	}
	if cfg.ResolvedPaths.LogPath != filepath.Join(dir, "logs", "timecue.log") {
		t.Fatalf("LogPath = %q, want executable-relative path", cfg.ResolvedPaths.LogPath)
	}
	if cfg.ResolvedPaths.BackupDir != filepath.Join(dir, "backups") {
		t.Fatalf("BackupDir = %q, want executable-relative path", cfg.ResolvedPaths.BackupDir)
	}
}

func TestLoadUsesExternalConfigFromExecutableDirectory(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "config.yaml")
	t.Setenv("LOCALAPPDATA", filepath.Join(t.TempDir(), "LocalAppData"))
	config := []byte("app:\n  name: CustomTimeCue\nlog:\n  level: debug\n  path: ./custom/timecue.log\nbackup:\n  dir: ./custom/backups\n")
	if err := os.WriteFile(configFile, config, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := loadFromExecutable(filepath.Join(dir, "timecue.exe"))
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

func TestLoadIgnoresWorkingDirectoryWhenResolvingConfigAndPaths(t *testing.T) {
	executableDir := t.TempDir()
	workingDir := t.TempDir()
	t.Setenv("LOCALAPPDATA", filepath.Join(t.TempDir(), "LocalAppData"))
	if err := os.WriteFile(filepath.Join(executableDir, "config.yaml"), []byte("log:\n  path: ./exe.log\nbackup:\n  dir: ./exe-backups\n"), 0o600); err != nil {
		t.Fatalf("write executable config: %v", err)
	}
	t.Chdir(workingDir)

	cfg, err := loadFromExecutable(filepath.Join(executableDir, "timecue.exe"))
	if err != nil {
		t.Fatalf("loadFromExecutable() error = %v", err)
	}
	if cfg.ResolvedPaths.LogPath != filepath.Join(executableDir, "exe.log") {
		t.Fatalf("LogPath = %q, want executable-relative path", cfg.ResolvedPaths.LogPath)
	}
	if cfg.ResolvedPaths.BackupDir != filepath.Join(executableDir, "exe-backups") {
		t.Fatalf("BackupDir = %q, want executable-relative path", cfg.ResolvedPaths.BackupDir)
	}
}

func TestLoadRejectsLegacyDatabasePathSettings(t *testing.T) {
	for _, key := range []string{"data_dir", "database_path", "database.path"} {
		t.Run(key, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("LOCALAPPDATA", filepath.Join(t.TempDir(), "LocalAppData"))
			contents := []byte(key + ": ./old-data\n")
			if strings.Contains(key, ".") {
				contents = []byte("database:\n  path: ./old-data/timecue.db\n")
			}
			if err := os.WriteFile(filepath.Join(dir, "config.yaml"), contents, 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}
			if _, err := loadFromExecutable(filepath.Join(dir, "timecue.exe")); err == nil {
				t.Fatal("loadFromExecutable() error = nil, want legacy path rejection")
			}
		})
	}
}

func TestConfigRejectsInvalidLogSettings(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
	}{
		{
			name: "invalid level",
			cfg:  Config{Log: LogConfig{Level: "trace", Path: "timecue.log"}, Backup: BackupConfig{Dir: "backups"}},
		},
		{
			name: "missing path",
			cfg:  Config{Log: LogConfig{Level: "info"}, Backup: BackupConfig{Dir: "backups"}},
		},
		{
			name: "wrong extension",
			cfg:  Config{Log: LogConfig{Level: "info", Path: "timecue.json"}, Backup: BackupConfig{Dir: "backups"}},
		},
		{
			name: "missing backup directory",
			cfg:  Config{Log: LogConfig{Level: "info", Path: "timecue.log"}},
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
	cfg := Config{Log: LogConfig{Level: "info", Path: "./log/timecue.log"}, Backup: BackupConfig{Dir: "backups"}}

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
	cfg := Config{Log: LogConfig{Level: "info", Path: absolute}, Backup: BackupConfig{Dir: "backups"}}

	path, err := cfg.LogFilePath()
	if err != nil {
		t.Fatalf("LogFilePath() error = %v", err)
	}
	if path != filepath.Clean(absolute) {
		t.Fatalf("LogFilePath() = %q, want %q", path, filepath.Clean(absolute))
	}
}
