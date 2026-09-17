package bootstrap

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/dengweitian0-svg/TimeCue/internal/config"
	"github.com/dengweitian0-svg/TimeCue/internal/utils"
)

type applicationFunc func(context.Context, config.Config, *slog.Logger, Store) error

func (f applicationFunc) Run(ctx context.Context, cfg config.Config, logger *slog.Logger, store Store) error {
	return f(ctx, cfg, logger, store)
}

func testConfig(path string) config.Config {
	baseDir, _ := os.Getwd()
	logPath := path
	if !filepath.IsAbs(logPath) {
		logPath = filepath.Join(baseDir, logPath)
	}
	return config.Config{
		App:    config.AppConfig{Name: "TimeCue", Version: "0.0.1"},
		Log:    config.LogConfig{Level: "info", Path: path},
		Backup: config.BackupConfig{Dir: filepath.Join(baseDir, "backups")},
		ResolvedPaths: config.ResolvedPaths{
			DatabasePath: filepath.Join(baseDir, "data", "timecue.db"),
			LogPath:      logPath,
			BackupDir:    filepath.Join(baseDir, "backups"),
		},
	}
}

// TestRunTreatsContextCancellationAsNormalExit 验证纯主动取消返回成功且只记录启动和停止事件。
func TestRunTreatsContextCancellationAsNormalExit(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})

	result := make(chan error, 1)
	go func() {
		result <- Run(ctx, testConfig("log/timecue.log"), applicationFunc(func(ctx context.Context, _ config.Config, _ *slog.Logger, _ Store) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		}), AlwaysQualified{})
	}()

	<-started
	cancel()
	if err := <-result; err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}

	assertLogMessages(t, filepath.Join(dir, "log", "timecue.log"), []string{
		"sqlite store ready",
		"application starting",
		"application stopping",
		"sqlite store closed",
	})
	assertLogLevels(t, filepath.Join(dir, "log", "timecue.log"), []string{"INFO", "INFO", "INFO", "INFO"})
}

// TestRunReturnsApplicationErrorAndLogsIt 验证运行错误被包装、记录为 ERROR 并映射为失败退出。
func TestRunReturnsApplicationErrorAndLogsIt(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	wantErr := errors.New("application failed")

	err := Run(context.Background(), testConfig("timecue.log"), applicationFunc(func(context.Context, config.Config, *slog.Logger, Store) error {
		return wantErr
	}), AlwaysQualified{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Run() error = %v, want wrapped application error", err)
	}
	if utils.ExitCodeFor(err) != utils.ExitFailed {
		t.Fatalf("ExitCodeFor() = %d, want %d", utils.ExitCodeFor(err), utils.ExitFailed)
	}
	assertLogMessages(t, filepath.Join(dir, "timecue.log"), []string{
		"sqlite store ready",
		"application starting",
		"application failed",
		"application stopping",
		"sqlite store closed",
	})
	assertLogLevels(t, filepath.Join(dir, "timecue.log"), []string{"INFO", "INFO", "ERROR", "INFO", "INFO"})
}

// TestRunTreatsCombinedCancellationAsFailure 验证取消与真实错误组合时不会被误判为正常退出。
func TestRunTreatsCombinedCancellationAsFailure(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	wantErr := errors.New("runtime failure")
	combinedErr := errors.Join(context.Canceled, wantErr)

	err := Run(context.Background(), testConfig("timecue.log"), applicationFunc(func(context.Context, config.Config, *slog.Logger, Store) error {
		return combinedErr
	}), AlwaysQualified{})
	if !errors.Is(err, context.Canceled) || !errors.Is(err, wantErr) {
		t.Fatalf("Run() error = %v, want both cancellation and runtime causes", err)
	}
	if utils.ExitCodeFor(err) != utils.ExitFailed {
		t.Fatalf("ExitCodeFor() = %d, want %d", utils.ExitCodeFor(err), utils.ExitFailed)
	}
	assertLogMessages(t, filepath.Join(dir, "timecue.log"), []string{
		"sqlite store ready",
		"application starting",
		"application failed",
		"application stopping",
		"sqlite store closed",
	})
	assertLogLevels(t, filepath.Join(dir, "timecue.log"), []string{"INFO", "INFO", "ERROR", "INFO", "INFO"})
}

func TestRunRejectsNilApplication(t *testing.T) {
	err := Run(context.Background(), testConfig("timecue.log"), nil, AlwaysQualified{})
	if err == nil {
		t.Fatal("Run() error = nil, want error")
	}
}

func TestRunRejectsInvalidConfigurationBeforeCreatingLog(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	cfg := testConfig("timecue.txt")

	err := Run(context.Background(), cfg, applicationFunc(func(context.Context, config.Config, *slog.Logger, Store) error {
		t.Fatal("application was called for invalid configuration")
		return nil
	}), AlwaysQualified{})
	if err == nil {
		t.Fatal("Run() error = nil, want error")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "timecue.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("unexpected log file state: %v", statErr)
	}
}

// TestRunTreatsAlreadyRunningAsNormalExit 验证已成功转发的第二实例不初始化 Store 或记录运行错误。
func TestRunTreatsAlreadyRunningAsNormalExit(t *testing.T) {
	cfg := testConfig(filepath.Join(t.TempDir(), "timecue.log"))
	qualification := recordingQualification{err: ErrAlreadyRunning, events: new([]string)}
	err := Run(context.Background(), cfg, applicationFunc(func(context.Context, config.Config, *slog.Logger, Store) error {
		t.Fatal("application was called for already-running instance")
		return nil
	}), qualification)
	if err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}
	if _, statErr := os.Stat(cfg.ResolvedPaths.DatabasePath); !os.IsNotExist(statErr) {
		t.Fatalf("database state = %v, want not created", statErr)
	}
}

// logRecord 保存测试所需的结构化日志字段。
type logRecord struct {
	Level   string `json:"level"`
	Message string `json:"msg"`
}

// readLogRecords 读取 JSONL 日志并解析消息与级别。
func readLogRecords(t *testing.T, path string) []logRecord {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open log file: %v", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	var records []logRecord
	for scanner.Scan() {
		var record logRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("invalid log JSON: %v", err)
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan log file: %v", err)
	}
	return records
}

// assertLogMessages 验证日志消息按预期顺序写入。
func assertLogMessages(t *testing.T, path string, want []string) {
	t.Helper()
	records := readLogRecords(t, path)
	if len(records) != len(want) {
		t.Fatalf("log records = %d, want %d", len(records), len(want))
	}
	for i := range want {
		if records[i].Message != want[i] {
			t.Fatalf("messages[%d] = %q, want %q", i, records[i].Message, want[i])
		}
	}
}

// assertLogLevels 验证日志级别与生命周期事件严重程度匹配。
func assertLogLevels(t *testing.T, path string, want []string) {
	t.Helper()
	records := readLogRecords(t, path)
	if len(records) != len(want) {
		t.Fatalf("log records = %d, want %d", len(records), len(want))
	}
	for i := range want {
		if records[i].Level != want[i] {
			t.Fatalf("levels[%d] = %q, want %q", i, records[i].Level, want[i])
		}
	}
}
