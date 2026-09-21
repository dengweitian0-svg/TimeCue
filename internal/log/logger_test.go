package log

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestNewCreatesInfoLevelBootstrapLogger(t *testing.T) {
	logger := New()

	if logger.currentLogger().Enabled(context.Background(), slog.LevelDebug) {
		t.Fatal("bootstrap logger enables debug records")
	}
	if !logger.currentLogger().Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("bootstrap logger does not enable info records")
	}
}

func TestUpgradeAtomicallySwitchesToJSONLLogger(t *testing.T) {
	logger := New()
	path := filepath.Join(t.TempDir(), "timecue.log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open log file: %v", err)
	}

	logger.SetLevel(slog.LevelDebug)
	logger.Upgrade(file)
	logger.Debug("debug record", "component", "test")
	logger.Info("info record", "component", "test")
	if err := file.Close(); err != nil {
		t.Fatalf("close log file: %v", err)
	}

	readFile, err := os.Open(path)
	if err != nil {
		t.Fatalf("open written log file: %v", err)
	}
	defer readFile.Close()

	scanner := bufio.NewScanner(readFile)
	var records []map[string]any
	for scanner.Scan() {
		var record map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("log record is not JSON: %v", err)
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan log file: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("record count = %d, want 2", len(records))
	}
	if records[0]["msg"] != "debug record" || records[1]["msg"] != "info record" {
		t.Fatalf("unexpected records: %#v", records)
	}
}

func TestSetLevelChangesThresholdWithoutReplacingLogger(t *testing.T) {
	logger := New()
	path := filepath.Join(t.TempDir(), "timecue.log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open log file: %v", err)
	}

	logger.Upgrade(file)
	current := logger.current.Load()
	logger.SetLevel(slog.LevelDebug)
	if logger.current.Load() != current {
		t.Fatal("SetLevel replaced the logger")
	}
	logger.Debug("debug after level update")
	if err := file.Close(); err != nil {
		t.Fatalf("close log file: %v", err)
	}

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	if !bytes.Contains(contents, []byte("debug after level update")) {
		t.Fatalf("debug record is missing after level update: %s", contents)
	}
}
