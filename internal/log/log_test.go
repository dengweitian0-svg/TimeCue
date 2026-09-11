package log

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/dengweitian0-svg/TimeCue/internal/config"
)

func TestNewWritesJSONLinesToConfiguredFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	cfg := config.LogConfig{Level: "info", Path: "logs/timecue.log"}

	logger, file, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	logger.Info("started", "component", "test")
	if err := file.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	path := filepath.Join(dir, "logs", "timecue.log")
	readFile, err := os.Open(path)
	if err != nil {
		t.Fatalf("open log file: %v", err)
	}
	defer readFile.Close()

	scanner := bufio.NewScanner(readFile)
	if !scanner.Scan() {
		t.Fatal("log file has no records")
	}
	var record map[string]any
	if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
		t.Fatalf("log record is not valid JSON: %v", err)
	}
	if record["msg"] != "started" {
		t.Fatalf("msg = %v, want started", record["msg"])
	}
	if record["component"] != "test" {
		t.Fatalf("component = %v, want test", record["component"])
	}
	if scanner.Scan() {
		t.Fatal("expected one JSONL record")
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan log file: %v", err)
	}
}

func TestNewAppendsToExistingLogFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	path := filepath.Join(dir, "timecue.log")
	if err := os.WriteFile(path, []byte("{\"msg\":\"previous\"}\n"), 0o644); err != nil {
		t.Fatalf("write existing log: %v", err)
	}

	logger, file, err := New(config.LogConfig{Level: "info", Path: path})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	logger.Info("current")
	if err := file.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	scanner := bufio.NewScanner(bytes.NewReader(contents))
	var records []map[string]any
	for scanner.Scan() {
		var record map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("invalid JSONL record: %v", err)
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan log file: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("record count = %d, want 2", len(records))
	}
	if records[1]["msg"] != "current" {
		t.Fatalf("last msg = %v, want current", records[1]["msg"])
	}
}

func TestNewFiltersRecordsBelowConfiguredLevel(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	logger, file, err := New(config.LogConfig{Level: "warn", Path: "timecue.log"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	logger.Info("hidden")
	logger.Warn("visible")
	if err := file.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	contents, err := os.ReadFile(filepath.Join(dir, "timecue.log"))
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	if string(contents) == "" {
		t.Fatal("log file is empty")
	}
	if bytes.Contains(contents, []byte("hidden")) {
		t.Fatal("info record was not filtered")
	}
	if !bytes.Contains(contents, []byte("visible")) {
		t.Fatal("warn record is missing")
	}
}

func TestNewRejectsInvalidPathBeforeCreatingFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if _, _, err := New(config.LogConfig{Level: "info", Path: "timecue.json"}); err == nil {
		t.Fatal("New() error = nil, want error")
	}
	if _, err := os.Stat(filepath.Join(dir, "timecue.json")); !os.IsNotExist(err) {
		t.Fatalf("unexpected invalid log file state: %v", err)
	}
}
