package logger

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewCreatesInfoLevelBootstrapLogger(t *testing.T) {
	logger := newLogger()

	if logger.currentLogger().Enabled(context.Background(), slog.LevelDebug) {
		t.Fatal("bootstrap logger enables debug records")
	}
	if !logger.currentLogger().Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("bootstrap logger does not enable info records")
	}
}

func TestUpgradeFormatsTimestampInBeijingTime(t *testing.T) {
	for _, tc := range []struct {
		name string
		when time.Time
	}{
		{"UTC crosses midnight", time.Date(2026, 9, 30, 17, 2, 3, 123456789, time.UTC)},
		{"different source timezone", time.Date(2026, 9, 30, 10, 2, 3, 123456789, time.FixedZone("UTC-7", -7*60*60))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logger := newLogger()
			path := filepath.Join(t.TempDir(), "timecue.jsonl")
			file, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { file.Close() })
			logger.Upgrade(file)
			record := slog.NewRecord(tc.when, slog.LevelInfo, "timestamp test", 0)
			record.AddAttrs(slog.Group("event", slog.Time("time", tc.when)))
			if err := logger.currentLogger().Handler().Handle(context.Background(), record); err != nil {
				t.Fatal(err)
			}
			contents, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var output struct {
				Time  string `json:"time"`
				Event struct {
					Time time.Time `json:"time"`
				} `json:"event"`
			}
			if err := json.Unmarshal(contents, &output); err != nil {
				t.Fatal(err)
			}
			if output.Time != "2026-10-01-01:02:03" {
				t.Fatalf("time = %q, want 2026-10-01-01:02:03", output.Time)
			}
			if !output.Event.Time.Equal(tc.when) {
				t.Fatalf("nested event time changed: %v", output.Event.Time)
			}
		})
	}
}

func TestUpgradeDoesNotWriteBootstrapRecords(t *testing.T) {
	logger := newLogger()
	logger.Info("before upgrade")

	path := filepath.Join(t.TempDir(), "timecue.jsonl")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open log file: %v", err)
	}
	logger.Upgrade(file)
	logger.Info("after upgrade")
	if err := file.Close(); err != nil {
		t.Fatalf("close log file: %v", err)
	}

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	lines := bytes.Split(bytes.TrimSpace(contents), []byte("\n"))
	if len(lines) != 1 {
		t.Fatalf("record count = %d, want 1: %s", len(lines), contents)
	}
	var record map[string]any
	if err := json.Unmarshal(lines[0], &record); err != nil {
		t.Fatalf("record is not JSON: %v", err)
	}
	if record["msg"] != "after upgrade" {
		t.Fatalf("message = %v, want after upgrade", record["msg"])
	}
}

func TestUpgradeAtomicallySwitchesToJSONLLogger(t *testing.T) {
	logger := newLogger()
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
	logger := newLogger()
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
