package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	if cfg.Window.CloseBehavior != CloseBehaviorTray {
		t.Fatalf("Window.CloseBehavior = %q, want tray", cfg.Window.CloseBehavior)
	}
	if cfg.ResolvedPaths.DatabasePath != filepath.Join(os.Getenv("LOCALAPPDATA"), "TimeCue", "timecue.db") {
		t.Fatalf("DatabasePath = %q, want fixed LOCALAPPDATA path", cfg.ResolvedPaths.DatabasePath)
	}
	if cfg.ResolvedPaths.ConfigPath != filepath.Join(dir, "config.yaml") {
		t.Fatalf("ConfigPath = %q, want executable config path", cfg.ResolvedPaths.ConfigPath)
	}
	if cfg.ResolvedPaths.PreferencesPath != filepath.Join(os.Getenv("LOCALAPPDATA"), "TimeCue", "preferences.yaml") {
		t.Fatalf("PreferencesPath = %q, want fixed LOCALAPPDATA path", cfg.ResolvedPaths.PreferencesPath)
	}
	if cfg.ResolvedPaths.LogPath != filepath.Join(dir, "logs", "timecue.log") {
		t.Fatalf("LogPath = %q, want executable-relative path", cfg.ResolvedPaths.LogPath)
	}
	if cfg.ResolvedPaths.BackupDir != filepath.Join(dir, "backups") {
		t.Fatalf("BackupDir = %q, want executable-relative path", cfg.ResolvedPaths.BackupDir)
	}
}

// TestSaveStorageDirectoryPreservesFilenameAndEffectivePath 验证保存目录不替换当前进程路径且保留日志文件名。
func TestSaveStorageDirectoryPreservesFilenameAndEffectivePath(t *testing.T) {
	dir := t.TempDir()
	localAppData := filepath.Join(t.TempDir(), "LocalAppData")
	t.Setenv("LOCALAPPDATA", localAppData)
	oldLogs := filepath.Join(dir, "旧 日志")
	oldBackups := filepath.Join(dir, "旧备份")
	newLogs := filepath.Join(dir, "新的 中文 日志")
	newBackups := filepath.Join(dir, "新的 中文 备份")
	for _, path := range []string{oldLogs, oldBackups} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("create old directory %q: %v", path, err)
		}
	}
	configPath := filepath.Join(dir, "config.yaml")
	original := "app:\n  name: Custom\nlog:\n  level: debug\n  path: ./旧 日志/custom-name.log\nbackup:\n  dir: ./旧备份\nfuture:\n  enabled: true\n"
	if err := os.WriteFile(configPath, []byte(original), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := loadFromExecutable(filepath.Join(dir, "timecue.exe"))
	if err != nil {
		t.Fatalf("load configuration: %v", err)
	}
	effectiveLog := cfg.ResolvedPaths.LogPath
	effectiveBackup := cfg.ResolvedPaths.BackupDir
	if err := cfg.SaveLogDirectory(newLogs); err != nil {
		t.Fatalf("SaveLogDirectory() error = %v", err)
	}
	if cfg.ResolvedPaths.LogPath != effectiveLog || cfg.ResolvedPaths.BackupDir != effectiveBackup {
		t.Fatalf("effective paths changed after save: log=%q backup=%q", cfg.ResolvedPaths.LogPath, cfg.ResolvedPaths.BackupDir)
	}
	if filepath.Base(cfg.Log.Path) != "custom-name.log" || filepath.Dir(cfg.Log.Path) != filepath.Clean(newLogs) {
		t.Fatalf("saved log path = %q, want new directory and original filename", cfg.Log.Path)
	}
	if err := cfg.SaveBackupDirectory(newBackups); err != nil {
		t.Fatalf("SaveBackupDirectory() error = %v", err)
	}
	contents, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	text := string(contents)
	for _, expected := range []string{"future:", "enabled: true", "custom-name.log"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("saved config = %q, missing %q", text, expected)
		}
	}
	reloaded, err := loadFromExecutable(filepath.Join(dir, "timecue.exe"))
	if err != nil {
		t.Fatalf("reload configuration: %v", err)
	}
	if reloaded.ResolvedPaths.LogPath != filepath.Join(newLogs, "custom-name.log") || reloaded.ResolvedPaths.BackupDir != filepath.Clean(newBackups) {
		t.Fatalf("reloaded effective paths = (%q, %q), want new directories", reloaded.ResolvedPaths.LogPath, reloaded.ResolvedPaths.BackupDir)
	}
}

// TestSaveStorageDirectoryCreatesConfigFromEmbeddedDefaults 验证无 config.yaml 时以嵌入默认配置为基底创建文件。
func TestSaveStorageDirectoryCreatesConfigFromEmbeddedDefaults(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", filepath.Join(t.TempDir(), "LocalAppData"))
	cfg, err := loadFromExecutable(filepath.Join(dir, "timecue.exe"))
	if err != nil {
		t.Fatalf("load configuration: %v", err)
	}
	newLogs := filepath.Join(dir, "logs with 空格")
	if err := cfg.SaveLogDirectory(newLogs); err != nil {
		t.Fatalf("SaveLogDirectory() error = %v", err)
	}
	contents, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatalf("read created config: %v", err)
	}
	if !strings.Contains(string(contents), "path:") || !strings.Contains(string(contents), "timecue.log") || !strings.Contains(string(contents), "backup:") {
		t.Fatalf("created config = %q, want embedded defaults plus saved log path", contents)
	}
}

// TestSaveStorageDirectoriesConcurrentPreservesBothFields 验证并发保存两类目录不会互相覆盖 YAML 字段。
func TestSaveStorageDirectoriesConcurrentPreservesBothFields(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", filepath.Join(t.TempDir(), "LocalAppData"))
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("log:\n  level: info\n  path: ./logs/timecue.log\nbackup:\n  dir: ./backups\nfuture: keep\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := loadFromExecutable(filepath.Join(dir, "timecue.exe"))
	if err != nil {
		t.Fatalf("load configuration: %v", err)
	}
	logs := filepath.Join(dir, "并发日志")
	backups := filepath.Join(dir, "并发备份")
	var group sync.WaitGroup
	group.Add(2)
	var logErr, backupErr error
	go func() { defer group.Done(); logErr = cfg.SaveLogDirectory(logs) }()
	go func() { defer group.Done(); backupErr = cfg.SaveBackupDirectory(backups) }()
	group.Wait()
	if logErr != nil || backupErr != nil {
		t.Fatalf("concurrent save errors = (%v, %v)", logErr, backupErr)
	}
	reloaded, err := loadFromExecutable(filepath.Join(dir, "timecue.exe"))
	if err != nil {
		t.Fatalf("reload configuration: %v", err)
	}
	if filepath.Dir(reloaded.Log.Path) != filepath.Clean(logs) || reloaded.Backup.Dir != filepath.Clean(backups) {
		t.Fatalf("saved fields = (%q, %q), want both concurrent updates", reloaded.Log.Path, reloaded.Backup.Dir)
	}
}

// TestSaveStorageDirectoryFailurePreservesFileAndMemory 验证写入、同步或替换失败不会更新文件和内存保存值。
func TestSaveStorageDirectoryFailurePreservesFileAndMemory(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", filepath.Join(t.TempDir(), "LocalAppData"))
	configPath := filepath.Join(dir, "config.yaml")
	original := []byte("log:\n  level: info\n  path: ./logs/timecue.log\nbackup:\n  dir: ./backups\n")
	if err := os.WriteFile(configPath, original, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := loadFromExecutable(filepath.Join(dir, "timecue.exe"))
	if err != nil {
		t.Fatalf("load configuration: %v", err)
	}
	oldPath, oldBackup := cfg.Log.Path, cfg.Backup.Dir
	failures := []struct {
		name string
		set  func() func()
	}{
		{name: "write", set: func() func() {
			old := writeConfigContents
			writeConfigContents = func(*os.File, []byte) (int, error) { return 0, errors.New("write failed") }
			return func() { writeConfigContents = old }
		}},
		{name: "sync", set: func() func() {
			old := syncConfigFile
			syncConfigFile = func(*os.File) error { return errors.New("sync failed") }
			return func() { syncConfigFile = old }
		}},
		{name: "replace", set: func() func() {
			old := replaceConfigFile
			replaceConfigFile = func(string, string) error { return errors.New("replace failed") }
			return func() { replaceConfigFile = old }
		}},
	}
	for _, failure := range failures {
		t.Run(failure.name, func(t *testing.T) {
			restore := failure.set()
			defer restore()
			if err := cfg.SaveLogDirectory(filepath.Join(dir, "new", failure.name)); err == nil {
				t.Fatal("SaveLogDirectory() error = nil, want injected failure")
			}
			contents, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatalf("read original config: %v", err)
			}
			if string(contents) != string(original) || cfg.Log.Path != oldPath || cfg.Backup.Dir != oldBackup {
				t.Fatalf("failure changed config or memory: file=%q log=%q backup=%q", contents, cfg.Log.Path, cfg.Backup.Dir)
			}
		})
	}
}

// TestSamePhysicalDirectoryResolvesSymlink 验证目录别名不会被判断为需要重启的不同物理目录。
func TestSamePhysicalDirectoryResolvesSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "real")
	link := filepath.Join(root, "alias")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("create target directory: %v", err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink is not available in this environment: %v", err)
	}
	if !SamePhysicalDirectory(target, link) {
		t.Fatalf("SamePhysicalDirectory(%q, %q) = false, want true", target, link)
	}
}

func TestLoadUsesExternalConfigFromExecutableDirectory(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "config.yaml")
	t.Setenv("LOCALAPPDATA", filepath.Join(t.TempDir(), "LocalAppData"))
	config := []byte("app:\n  name: CustomTimeCue\nlog:\n  level: debug\n  path: ./custom/timecue.log\nbackup:\n  dir: ./custom/backups\nwindow:\n  close_behavior: exit\n")
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
	if cfg.Window.CloseBehavior != CloseBehaviorExit {
		t.Fatalf("Window.CloseBehavior = %q, want exit", cfg.Window.CloseBehavior)
	}
}

// TestLoadPreferenceOverridesExecutableConfig 验证偏好层只覆盖窗口关闭行为。
func TestLoadPreferenceOverridesExecutableConfig(t *testing.T) {
	dir := t.TempDir()
	localAppData := filepath.Join(t.TempDir(), "LocalAppData")
	preferencesDir := filepath.Join(localAppData, "TimeCue")
	t.Setenv("LOCALAPPDATA", localAppData)
	if err := os.MkdirAll(preferencesDir, 0o755); err != nil {
		t.Fatalf("create preferences directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("window:\n  close_behavior: exit\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(preferencesDir, "preferences.yaml"), []byte("window:\n  close_behavior: tray\n"), 0o600); err != nil {
		t.Fatalf("write preferences: %v", err)
	}

	cfg, err := loadFromExecutable(filepath.Join(dir, "timecue.exe"))
	if err != nil {
		t.Fatalf("loadFromExecutable() error = %v", err)
	}
	if cfg.Window.CloseBehavior != CloseBehaviorTray {
		t.Fatalf("Window.CloseBehavior = %q, want tray preference", cfg.Window.CloseBehavior)
	}
}

// TestLoadRejectsInvalidPreference 验证非法偏好在启动配置边界包含文件路径。
func TestLoadRejectsInvalidPreference(t *testing.T) {
	localAppData := filepath.Join(t.TempDir(), "LocalAppData")
	preferencesPath := filepath.Join(localAppData, "TimeCue", "preferences.yaml")
	t.Setenv("LOCALAPPDATA", localAppData)
	if err := os.MkdirAll(filepath.Dir(preferencesPath), 0o755); err != nil {
		t.Fatalf("create preferences directory: %v", err)
	}
	if err := os.WriteFile(preferencesPath, []byte("window:\n  close_behavior: minimize\n"), 0o600); err != nil {
		t.Fatalf("write preferences: %v", err)
	}

	_, err := loadFromExecutable(filepath.Join(t.TempDir(), "timecue.exe"))
	if err == nil {
		t.Fatal("loadFromExecutable() error = nil, want invalid preference error")
	}
	if !strings.Contains(err.Error(), filepath.Base(preferencesPath)) || !strings.Contains(err.Error(), "tray or exit") {
		t.Fatalf("error = %v, want preference path and close behavior context", err)
	}
}

// TestSaveCloseBehaviorPreservesUnknownPreferences 验证保存关闭行为时保留其他偏好字段。
func TestSaveCloseBehaviorPreservesUnknownPreferences(t *testing.T) {
	localAppData := t.TempDir()
	preferencesDir := filepath.Join(localAppData, "TimeCue")
	preferencesPath := filepath.Join(preferencesDir, "preferences.yaml")
	t.Setenv("LOCALAPPDATA", localAppData)
	if err := os.MkdirAll(preferencesDir, 0o755); err != nil {
		t.Fatalf("create preferences directory: %v", err)
	}
	original := []byte("theme: mint\nfuture:\n  enabled: true\nwindow:\n  close_behavior: tray\n")
	if err := os.WriteFile(preferencesPath, original, 0o600); err != nil {
		t.Fatalf("write preferences: %v", err)
	}
	cfg := Config{Window: WindowConfig{CloseBehavior: CloseBehaviorTray}, ResolvedPaths: ResolvedPaths{PreferencesPath: preferencesPath}}

	if err := cfg.SaveCloseBehavior(CloseBehaviorExit); err != nil {
		t.Fatalf("SaveCloseBehavior() error = %v", err)
	}
	contents, err := os.ReadFile(preferencesPath)
	if err != nil {
		t.Fatalf("read preferences: %v", err)
	}
	text := string(contents)
	for _, expected := range []string{"theme: mint", "future:", "enabled: true", "close_behavior: exit"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("saved preferences = %q, missing %q", text, expected)
		}
	}
	if cfg.Window.CloseBehavior != CloseBehaviorExit {
		t.Fatalf("Window.CloseBehavior = %q, want exit", cfg.Window.CloseBehavior)
	}
	reloaded, err := loadFromExecutable(filepath.Join(t.TempDir(), "timecue.exe"))
	if err != nil {
		t.Fatalf("reload configuration: %v", err)
	}
	if reloaded.Window.CloseBehavior != CloseBehaviorExit {
		t.Fatalf("reloaded Window.CloseBehavior = %q, want exit", reloaded.Window.CloseBehavior)
	}
}

// TestSaveCloseBehaviorCreatesPreferenceFile 验证首次保存写入默认偏好文档。
func TestSaveCloseBehaviorCreatesPreferenceFile(t *testing.T) {
	preferencesDir := filepath.Join(t.TempDir(), "TimeCue")
	preferencesPath := filepath.Join(preferencesDir, "preferences.yaml")
	if err := os.MkdirAll(preferencesDir, 0o755); err != nil {
		t.Fatalf("create preferences directory: %v", err)
	}
	cfg := Config{ResolvedPaths: ResolvedPaths{PreferencesPath: preferencesPath}}
	if err := cfg.SaveCloseBehavior(CloseBehaviorExit); err != nil {
		t.Fatalf("SaveCloseBehavior() error = %v", err)
	}
	loaded, found, err := loadCloseBehavior(preferencesPath)
	if err != nil {
		t.Fatalf("loadCloseBehavior() error = %v", err)
	}
	if !found {
		t.Fatal("loadCloseBehavior() found = false, want true")
	}
	if loaded != CloseBehaviorExit {
		t.Fatalf("loaded close behavior = %q, want exit", loaded)
	}
}

// TestSaveCloseBehaviorFailurePreservesOldState 验证临时文件创建失败时旧文件和内存值不变。
func TestSaveCloseBehaviorFailurePreservesOldState(t *testing.T) {
	preferencesPath := filepath.Join(t.TempDir(), "missing", "preferences.yaml")
	cfg := Config{Window: WindowConfig{CloseBehavior: CloseBehaviorTray}, ResolvedPaths: ResolvedPaths{PreferencesPath: preferencesPath}}
	if err := cfg.SaveCloseBehavior(CloseBehaviorExit); err == nil {
		t.Fatal("SaveCloseBehavior() error = nil, want missing directory failure")
	}
	if cfg.Window.CloseBehavior != CloseBehaviorTray {
		t.Fatalf("Window.CloseBehavior = %q, want old tray value", cfg.Window.CloseBehavior)
	}
	if _, err := os.Stat(preferencesPath); !os.IsNotExist(err) {
		t.Fatalf("preference file state = %v, want not created", err)
	}
}

// TestWritePreferencesAtomicallyReplacementFailure 验证替换失败时目标和临时文件状态均安全。
func TestWritePreferencesAtomicallyReplacementFailure(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "preferences.yaml")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatalf("create target directory: %v", err)
	}
	if err := writePreferencesAtomically(target, []byte("window:\n  close_behavior: exit\n")); err == nil {
		t.Fatal("writePreferencesAtomically() error = nil, want replacement failure")
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat target after failed replacement: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("target was replaced despite replacement failure")
	}
	temporary, err := filepath.Glob(filepath.Join(dir, ".preferences.yaml.tmp-*"))
	if err != nil {
		t.Fatalf("glob temporary files: %v", err)
	}
	if len(temporary) != 0 {
		t.Fatalf("temporary files after failed replacement = %v, want none", temporary)
	}
}

// TestWritePreferencesAtomicallyWriteFailurePreservesOldFile 验证写入失败不会覆盖旧偏好。
func TestWritePreferencesAtomicallyWriteFailurePreservesOldFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "preferences.yaml")
	original := []byte("window:\n  close_behavior: tray\n")
	if err := os.WriteFile(target, original, 0o600); err != nil {
		t.Fatalf("write original preferences: %v", err)
	}
	wantErr := errors.New("injected write failure")
	originalWriter := writePreferencesContents
	t.Cleanup(func() { writePreferencesContents = originalWriter })
	writePreferencesContents = func(*os.File, []byte) (int, error) { return 0, wantErr }

	if err := writePreferencesAtomically(target, []byte("new")); !errors.Is(err, wantErr) {
		t.Fatalf("writePreferencesAtomically() error = %v, want injected write failure", err)
	}
	contents, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read original preferences: %v", err)
	}
	if string(contents) != string(original) {
		t.Fatalf("preferences after failed write = %q, want %q", contents, original)
	}
	temporary, err := filepath.Glob(filepath.Join(dir, ".preferences.yaml.tmp-*"))
	if err != nil {
		t.Fatalf("glob temporary files: %v", err)
	}
	if len(temporary) != 0 {
		t.Fatalf("temporary files after failed write = %v, want none", temporary)
	}
}

// TestWritePreferencesAtomicallyReplaceFailurePreservesOldFile 验证替换失败不会覆盖旧偏好。
func TestWritePreferencesAtomicallyReplaceFailurePreservesOldFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "preferences.yaml")
	original := []byte("window:\n  close_behavior: tray\n")
	if err := os.WriteFile(target, original, 0o600); err != nil {
		t.Fatalf("write original preferences: %v", err)
	}
	wantErr := errors.New("injected replace failure")
	originalReplacer := replacePreferencesFile
	t.Cleanup(func() { replacePreferencesFile = originalReplacer })
	replacePreferencesFile = func(string, string) error { return wantErr }

	if err := writePreferencesAtomically(target, []byte("new")); !errors.Is(err, wantErr) {
		t.Fatalf("writePreferencesAtomically() error = %v, want injected replace failure", err)
	}
	contents, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read original preferences: %v", err)
	}
	if string(contents) != string(original) {
		t.Fatalf("preferences after failed replacement = %q, want %q", contents, original)
	}
	temporary, err := filepath.Glob(filepath.Join(dir, ".preferences.yaml.tmp-*"))
	if err != nil {
		t.Fatalf("glob temporary files: %v", err)
	}
	if len(temporary) != 0 {
		t.Fatalf("temporary files after failed replacement = %v, want none", temporary)
	}
}

// TestSaveCloseBehaviorReplaceFailurePreservesMemory 验证保存替换失败时 Config 内存值仍保持旧值。
func TestSaveCloseBehaviorReplaceFailurePreservesMemory(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "preferences.yaml")
	original := []byte("window:\n  close_behavior: tray\n")
	if err := os.WriteFile(target, original, 0o600); err != nil {
		t.Fatalf("write original preferences: %v", err)
	}
	cfg := Config{Window: WindowConfig{CloseBehavior: CloseBehaviorTray}, ResolvedPaths: ResolvedPaths{PreferencesPath: target}}
	wantErr := errors.New("injected replace failure")
	originalReplacer := replacePreferencesFile
	t.Cleanup(func() { replacePreferencesFile = originalReplacer })
	replacePreferencesFile = func(string, string) error { return wantErr }

	if err := cfg.SaveCloseBehavior(CloseBehaviorExit); !errors.Is(err, wantErr) {
		t.Fatalf("SaveCloseBehavior() error = %v, want injected replace failure", err)
	}
	if cfg.Window.CloseBehavior != CloseBehaviorTray {
		t.Fatalf("Window.CloseBehavior = %q, want old tray value", cfg.Window.CloseBehavior)
	}
	contents, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read original preferences: %v", err)
	}
	if string(contents) != string(original) {
		t.Fatalf("preferences after failed replacement = %q, want %q", contents, original)
	}
}

// TestSaveCloseBehaviorRejectsInvalidValue 验证保存请求只接受两个关闭行为枚举值。
func TestSaveCloseBehaviorRejectsInvalidValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "preferences.yaml")
	cfg := Config{Window: WindowConfig{CloseBehavior: CloseBehaviorTray}, ResolvedPaths: ResolvedPaths{PreferencesPath: path}}
	if err := cfg.SaveCloseBehavior(CloseBehavior("minimize")); err == nil {
		t.Fatal("SaveCloseBehavior() error = nil, want invalid enum error")
	}
	if cfg.Window.CloseBehavior != CloseBehaviorTray {
		t.Fatalf("Window.CloseBehavior = %q, want tray", cfg.Window.CloseBehavior)
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
