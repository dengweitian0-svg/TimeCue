package config

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

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
	manager, err := NewManager()
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	if err := manager.LoadConfig(); err != nil {
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

	manager, err := NewManager()
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	if err := manager.LoadConfig(); err != nil {
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

	manager, err := NewManager()
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	if err := manager.LoadConfig(); err != nil {
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

	manager, err := NewManager()
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	if err := manager.LoadConfig(); err != nil {
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

func TestChangeConfigCreatesAndUpdatesUserConfigWithoutDefaults(t *testing.T) {
	t.Chdir(t.TempDir())

	manager, err := NewManager()
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	if err := manager.ChangeDB("db_path", "./new.db"); err != nil {
		t.Fatalf("create config through ChangeDB: %v", err)
	}

	contents, err := os.ReadFile(configFileName)
	if err != nil {
		t.Fatalf("read created config: %v", err)
	}
	if bytes.Contains(contents, []byte("app:")) {
		t.Fatalf("created config unexpectedly contains defaults: %s", contents)
	}
	if !bytes.Contains(contents, []byte("db_path: ./new.db")) {
		t.Fatalf("created config does not contain changed key: %s", contents)
	}

	if err := manager.ChangeDB("db_path", "./updated.db"); err != nil {
		t.Fatalf("update config through ChangeDB: %v", err)
	}
	updated, err := os.ReadFile(configFileName)
	if err != nil {
		t.Fatalf("read updated config: %v", err)
	}
	if !bytes.Contains(updated, []byte("db_path: ./updated.db")) {
		t.Fatalf("updated config does not contain new value: %s", updated)
	}
	if bytes.Contains(updated, []byte("./new.db")) {
		t.Fatalf("updated config still contains old value: %s", updated)
	}

	current, ok := manager.CurrentConfig()
	if !ok {
		t.Fatal("expected current config after change")
	}
	if current.DB.DBPath != "./updated.db" {
		t.Fatalf("db.db_path = %q, want %q", current.DB.DBPath, "./updated.db")
	}
}

func TestChangeConfigPreservesExistingKeysAndRejectsApp(t *testing.T) {
	t.Chdir(t.TempDir())
	writeTestConfig(t, "log:\n  level: info\n  custom: old\n")

	manager, err := NewManager()
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	if err := manager.ChangeLog("level", "debug"); err != nil {
		t.Fatalf("update existing key: %v", err)
	}
	if err := manager.ChangeLog("new_key", "new_value"); err != nil {
		t.Fatalf("add missing key: %v", err)
	}

	contents, err := os.ReadFile(configFileName)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !bytes.Contains(contents, []byte("level: debug")) ||
		!bytes.Contains(contents, []byte("custom: old")) ||
		!bytes.Contains(contents, []byte("new_key: new_value")) {
		t.Fatalf("config changes were not preserved: %s", contents)
	}

	if err := manager.ChangeConfig("app", "name", "changed"); err == nil {
		t.Fatal("expected app config change to fail")
	}
	afterReject, err := os.ReadFile(configFileName)
	if err != nil {
		t.Fatalf("read config after rejected change: %v", err)
	}
	if !bytes.Equal(afterReject, contents) {
		t.Fatal("app change modified config file")
	}
}

func TestRestoreDefaultClearsUserConfig(t *testing.T) {
	t.Chdir(t.TempDir())
	writeTestConfig(t, "log:\n  level: debug\n")

	manager, err := NewManager()
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	if err := manager.RestoreDefault(); err != nil {
		t.Fatalf("restore defaults: %v", err)
	}

	contents, err := os.ReadFile(configFileName)
	if err != nil {
		t.Fatalf("read cleared config: %v", err)
	}
	if len(contents) != 0 {
		t.Fatalf("config file is not empty: %q", contents)
	}

	current, ok := manager.CurrentConfig()
	if !ok {
		t.Fatal("expected current config after restoring defaults")
	}
	if current.Log.Level != "info" {
		t.Fatalf("log.level = %q, want default %q", current.Log.Level, "info")
	}
}
