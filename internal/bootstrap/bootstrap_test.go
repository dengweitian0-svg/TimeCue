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

type applicationFunc func(context.Context, config.Config, *slog.Logger) error

func (f applicationFunc) Run(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	return f(ctx, cfg, logger)
}

func testConfig(path string) config.Config {
	return config.Config{
		App: config.AppConfig{Name: "TimeCue", Version: "0.0.1"},
		Log: config.LogConfig{Level: "info", Path: path},
	}
}

func TestRunTreatsContextCancellationAsNormalExit(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})

	result := make(chan error, 1)
	go func() {
		result <- Run(ctx, testConfig("log/timecue.log"), applicationFunc(func(ctx context.Context, _ config.Config, _ *slog.Logger) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		}))
	}()

	<-started
	cancel()
	if err := <-result; err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}

	assertLogMessages(t, filepath.Join(dir, "log", "timecue.log"), []string{
		"application starting",
		"application ready",
		"application stopping",
	})
}

func TestRunReturnsApplicationErrorAndLogsIt(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	wantErr := errors.New("application failed")

	err := Run(context.Background(), testConfig("timecue.log"), applicationFunc(func(context.Context, config.Config, *slog.Logger) error {
		return wantErr
	}))
	if !errors.Is(err, wantErr) {
		t.Fatalf("Run() error = %v, want wrapped application error", err)
	}
	if utils.ExitCode(err) != 1 {
		t.Fatalf("ExitCode() = %d, want 1", utils.ExitCode(err))
	}
	assertLogMessages(t, filepath.Join(dir, "timecue.log"), []string{
		"application starting",
		"application ready",
		"application failed",
		"application stopping",
	})
}

func TestRunRejectsNilApplication(t *testing.T) {
	err := Run(context.Background(), testConfig("timecue.log"), nil)
	if err == nil {
		t.Fatal("Run() error = nil, want error")
	}
}

func TestRunRejectsInvalidConfigurationBeforeCreatingLog(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	cfg := testConfig("timecue.txt")

	err := Run(context.Background(), cfg, applicationFunc(func(context.Context, config.Config, *slog.Logger) error {
		t.Fatal("application was called for invalid configuration")
		return nil
	}))
	if err == nil {
		t.Fatal("Run() error = nil, want error")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "timecue.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("unexpected log file state: %v", statErr)
	}
}

func assertLogMessages(t *testing.T, path string, want []string) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open log file: %v", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	var messages []string
	for scanner.Scan() {
		var record map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("invalid log JSON: %v", err)
		}
		message, ok := record["msg"].(string)
		if !ok {
			t.Fatalf("log record has no string msg: %v", record)
		}
		messages = append(messages, message)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan log file: %v", err)
	}
	if len(messages) != len(want) {
		t.Fatalf("messages = %v, want %v", messages, want)
	}
	for i := range want {
		if messages[i] != want[i] {
			t.Fatalf("messages[%d] = %q, want %q", i, messages[i], want[i])
		}
	}
}
