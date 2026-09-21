package config

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func testLogger() slog.Logger {
	return *slog.New(slog.NewTextHandler(io.Discard, nil))
}

func writeTestConfig(t *testing.T, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(".", configFileName), []byte(contents), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func TestLoadConfigUsesImmutableDefaultsAndUserOverrides(t *testing.T) {
	t.Chdir(t.TempDir())
	writeTestConfig(t, "log:\n  level: debug\n")

	defaultBefore := append([]byte(nil), defaultConfig...)
	manager, err := NewManager(testLogger())
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	if err := manager.LoadConfig(testLogger()); err != nil {
		t.Fatalf("load config: %v", err)
	}

	current, ok := manager.GetCurrentConfig()
	if !ok {
		t.Fatal("expected current config")
	}
	if current.App.Name != "timecue" {
		t.Fatalf("app.name = %q, want %q", current.App.Name, "timecue")
	}
	if current.Log.Level != "debug" {
		t.Fatalf("log.level = %q, want %q", current.Log.Level, "debug")
	}
	if current.Log.LogPath != "./log" {
		t.Fatalf("log.log_path = %q, want %q", current.Log.LogPath, "./log")
	}
	if !bytes.Equal(defaultBefore, defaultConfig) {
		t.Fatal("embedded default config was modified")
	}
}

func TestLoadConfigUsesDefaultsWhenConfigFileIsMissing(t *testing.T) {
	t.Chdir(t.TempDir())

	manager, err := NewManager(testLogger())
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	if err := manager.LoadConfig(testLogger()); err != nil {
		t.Fatalf("load defaults without config file: %v", err)
	}

	current, ok := manager.GetCurrentConfig()
	if !ok {
		t.Fatal("expected current config")
	}
	if current.Log.Level != "info" {
		t.Fatalf("log.level = %q, want %q", current.Log.Level, "info")
	}

	writeTestConfig(t, "log:\n  level: debug\n")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		current, ok := manager.GetCurrentConfig()
		if ok && current.Log.Level == "debug" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("watcher did not load newly created config.yaml; current config = %#v", manager.currentCfg.Load())
}

func TestReloadConfigKeepsPreviousSnapshotOnFailure(t *testing.T) {
	t.Chdir(t.TempDir())
	writeTestConfig(t, "log:\n  level: info\n")

	manager, err := NewManager(testLogger())
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	if err := manager.LoadConfig(testLogger()); err != nil {
		t.Fatalf("load config: %v", err)
	}

	previousPointer := manager.currentCfg.Load()
	previous, ok := manager.GetCurrentConfig()
	if !ok {
		t.Fatal("expected current config")
	}

	writeTestConfig(t, "log: [invalid\n")
	if err := manager.reloadConfig(); err == nil {
		t.Fatal("expected reload error")
	}

	afterFailure, ok := manager.GetCurrentConfig()
	if !ok {
		t.Fatal("expected current config after failed reload")
	}
	if !reflect.DeepEqual(afterFailure, previous) {
		t.Fatalf("config changed after failed reload: got %#v, want %#v", afterFailure, previous)
	}
	if manager.currentCfg.Load() != previousPointer {
		t.Fatal("config pointer changed after failed reload")
	}

	writeTestConfig(t, "log:\n  level: warn\n")
	if err := manager.reloadConfig(); err != nil {
		t.Fatalf("reload valid config: %v", err)
	}
	updatedPointer := manager.currentCfg.Load()
	updated, ok := manager.GetCurrentConfig()
	if !ok {
		t.Fatal("expected updated config")
	}
	if updated.Log.Level != "warn" {
		t.Fatalf("log.level = %q, want %q", updated.Log.Level, "warn")
	}
	if updatedPointer == previousPointer {
		t.Fatal("valid reload reused previous config pointer")
	}
	if previous.Log.Level != "info" {
		t.Fatalf("previous snapshot was modified: log.level = %q", previous.Log.Level)
	}
}

func TestLoadConfigStartsConfigYamlWatcher(t *testing.T) {
	t.Chdir(t.TempDir())
	writeTestConfig(t, "log:\n  level: info\n")

	manager, err := NewManager(testLogger())
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	if err := manager.LoadConfig(testLogger()); err != nil {
		t.Fatalf("load config: %v", err)
	}

	writeTestConfig(t, "log:\n  level: debug\n")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		current, ok := manager.GetCurrentConfig()
		if ok && current.Log.Level == "debug" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("watcher did not reload config.yaml; current config = %#v", manager.currentCfg.Load())
}
