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
	t.Cleanup(manager.Close)
	if err := manager.LoadConfig(); err != nil {
		t.Fatalf("load config: %v", err)
	}

	current, err := manager.CurrentConfig()
	if err != nil {
		t.Fatalf("get current config: %v", err)
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
	t.Cleanup(manager.Close)
	if err := manager.LoadConfig(); err != nil {
		t.Fatalf("load defaults without config file: %v", err)
	}

	current, err := manager.CurrentConfig()
	if err != nil {
		t.Fatalf("get current config: %v", err)
	}
	if current.Log.Level != "info" {
		t.Fatalf("log.level = %q, want %q", current.Log.Level, "info")
	}

	writeTestConfig(t, "log:\n  level: debug\n")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		current, err := manager.CurrentConfig()
		if err == nil && current.Log.Level == "debug" {
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
	t.Cleanup(manager.Close)
	if err := manager.LoadConfig(); err != nil {
		t.Fatalf("load config: %v", err)
	}
	// 同步重载断言只观察本次调用，目录监听的中间读取由热更新测试覆盖。
	manager.Close()

	previousPointer := manager.currentCfg.Load()
	previous, err := manager.CurrentConfig()
	if err != nil {
		t.Fatalf("get current config: %v", err)
	}

	writeTestConfig(t, "log: [invalid\n")
	if err := manager.reloadConfig(); err == nil {
		t.Fatal("expected reload error")
	}

	afterFailure, err := manager.CurrentConfig()
	if err != nil {
		t.Fatalf("get current config after failed reload: %v", err)
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
	updated, err := manager.CurrentConfig()
	if err != nil {
		t.Fatalf("get updated config: %v", err)
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
	t.Cleanup(manager.Close)
	if err := manager.LoadConfig(); err != nil {
		t.Fatalf("load config: %v", err)
	}

	writeTestConfig(t, "log:\n  level: debug\n")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		current, err := manager.CurrentConfig()
		if err == nil && current.Log.Level == "debug" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("watcher did not reload config.yaml; current config = %#v", manager.currentCfg.Load())
}

// TestFixedDirectoryAndAtomicReplacement 从其他工作目录验证外部原子替换、清空及重建后持续监听。
func TestFixedDirectoryAndAtomicReplacement(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "中文 空格")
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, configFileName)
	if err := os.WriteFile(path, []byte("desktop:\n  start_on_login: true\nlog:\n  level: info\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	t.Cleanup(manager.Close)
	if err := manager.LoadConfig(); err != nil {
		t.Fatal(err)
	}
	for _, level := range []string{"warn", "debug"} {
		replaceTestConfig(t, path, "desktop:\n  start_on_login: true\nlog:\n  level: "+level+"\n")
		waitTestLogLevel(t, manager, level)
	}
	contents, err := os.ReadFile(path)
	if err != nil || !bytes.Contains(contents, []byte("start_on_login: true")) {
		t.Fatal("读取配置时改写了外部字段", string(contents), err)
	}
	replaceTestConfig(t, path, "")
	waitTestLogLevel(t, manager, "info")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	replaceTestConfig(t, path, "log:\n  level: debug\n")
	waitTestLogLevel(t, manager, "debug")
	if _, err := os.Stat(configFileName); !os.IsNotExist(err) {
		t.Fatal("写入了工作目录配置", err)
	}
	for _, item := range []struct{ input, want string }{
		{"./db/timecue.db", filepath.Join(directory, "db", "timecue.db")},
		{path, path}, {":memory:", ":memory:"}, {"file:test?mode=memory", "file:test?mode=memory"}, {"", ""},
	} {
		if got := manager.ResolveDatabasePath(item.input); got != item.want {
			t.Fatalf("%q: %q != %q", item.input, got, item.want)
		}
	}
	cfg, _ := manager.CurrentConfig()
	if cfg.DB.DBPath != "./db/timecue.db" {
		t.Fatal("配置路径文本被改写", cfg)
	}
}

// replaceTestConfig 模拟外部写入方保存并原子替换，不属于 Manager 的生产职责。
func replaceTestConfig(t *testing.T, path, contents string) {
	t.Helper()
	file, err := os.CreateTemp(filepath.Dir(path), ".config-test-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(file.Name()) })
	if _, err := file.WriteString(contents); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(file.Name(), path); err != nil {
		t.Fatal(err)
	}
}

func waitTestLogLevel(t *testing.T, manager *Manager, level string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		cfg, err := manager.CurrentConfig()
		if err == nil && cfg.Log.Level == level {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("未读到外部配置", cfg, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestReadConfigDoesNotCreateOrRewriteFile 验证缺少文件时只发布默认值，已有文件保持字节不变。
func TestReadConfigDoesNotCreateOrRewriteFile(t *testing.T) {
	directory := t.TempDir()
	manager, err := NewManager(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	if err := manager.LoadConfig(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, configFileName)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("Go 创建了配置文件", err)
	}
	original := "# 外部注释\nlog:\n  level: debug\ndesktop:\n  start_on_login: true\n"
	replaceTestConfig(t, path, original)
	waitTestLogLevel(t, manager, "debug")
	actual, err := os.ReadFile(path)
	if err != nil || string(actual) != original {
		t.Fatal("Go 改写了配置", string(actual), err)
	}
}
