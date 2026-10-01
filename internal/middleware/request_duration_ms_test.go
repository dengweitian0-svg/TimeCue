package middleware_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/api"
	logger "github.com/dengweitian0-svg/TimeCue/internal/log"
	"github.com/gin-gonic/gin"
)

func TestRequestDuration(t *testing.T) {
	// 日志器为全局实例，本测试及其子测试不能并行运行。
	path := filepath.Join(t.TempDir(), "requests.jsonl")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	previousDefault := slog.Default()
	logger.Upgrade(file)
	logger.SetLevel(slog.LevelInfo)
	t.Cleanup(func() {
		logger.Upgrade(os.Stderr)
		logger.SetLevel(slog.LevelInfo)
		slog.SetDefault(previousDefault)
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	})

	router := api.NewRouter(nil, func(context.Context) error { return nil })
	const delay = 5 * time.Millisecond
	router.POST("/timed", func(c *gin.Context) {
		time.Sleep(delay)
		c.String(http.StatusCreated, "private-response")
	})
	router.GET("/panic", func(c *gin.Context) { panic("test panic") })

	readLog := func(t *testing.T) []byte {
		t.Helper()
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return contents
	}
	durationRecords := func(t *testing.T, contents []byte) []map[string]any {
		t.Helper()
		var records []map[string]any
		for _, line := range bytes.Split(bytes.TrimSpace(contents), []byte("\n")) {
			if len(line) == 0 {
				continue
			}
			var record map[string]any
			if err := json.Unmarshal(line, &record); err != nil {
				t.Fatalf("invalid JSON log: %v", err)
			}
			if record["msg"] == "HTTP request completed" {
				records = append(records, record)
			}
		}
		return records
	}

	for _, tc := range []struct {
		name   string
		method string
		target string
		host   string
		status int
	}{
		{"health", http.MethodGet, "/health", "127.0.0.1:10086", http.StatusOK},
		{"timed", http.MethodPost, "/timed?token=private-query", "127.0.0.1:10086", http.StatusCreated},
		{"forbidden host", http.MethodGet, "/health", "example.com", http.StatusForbidden},
		{"not found", http.MethodGet, "/absent", "127.0.0.1:10086", http.StatusNotFound},
		{"method not allowed", http.MethodPut, "/health", "127.0.0.1:10086", http.StatusMethodNotAllowed},
		{"recovered panic", http.MethodGet, "/panic", "127.0.0.1:10086", http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := len(readLog(t))
			req := httptest.NewRequest(tc.method, tc.target, strings.NewReader("private-request"))
			req.Host = tc.host
			response := httptest.NewRecorder()
			started := time.Now()
			router.ServeHTTP(response, req)
			elapsed := time.Since(started)
			if response.Code != tc.status {
				t.Fatalf("status = %d, want %d", response.Code, tc.status)
			}
			contents := readLog(t)[before:]
			records := durationRecords(t, contents)
			if len(records) != 1 {
				t.Fatalf("duration log count = %d, want 1: %s", len(records), contents)
			}
			record := records[0]
			if record["level"] != "INFO" || record["method"] != tc.method || record["path"] != req.URL.Path || record["status"] != float64(tc.status) {
				t.Fatalf("unexpected duration log: %#v", record)
			}
			ms, ok := record["duration_ms"].(float64)
			if !ok || ms < 0 || ms > float64(elapsed)/float64(time.Millisecond) {
				t.Fatalf("invalid duration_ms: %#v (request elapsed %s)", record["duration_ms"], elapsed)
			}
			if tc.name == "timed" && ms < float64(delay)/float64(time.Millisecond) {
				t.Fatalf("duration_ms = %v does not include handler delay %s", ms, delay)
			}
			for _, private := range []string{"private-query", "private-request", "private-response"} {
				if bytes.Contains(contents, []byte(private)) {
					t.Fatalf("log contains %q: %s", private, contents)
				}
			}
		})
	}

	t.Run("log level changes", func(t *testing.T) {
		for _, level := range []slog.Level{slog.LevelInfo, slog.LevelWarn, slog.LevelDebug, slog.LevelInfo} {
			logger.SetLevel(level)
			before := len(readLog(t))
			req := httptest.NewRequest(http.MethodGet, "/health", nil)
			req.Host = "127.0.0.1:10086"
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", response.Code)
			}
			records := durationRecords(t, readLog(t)[before:])
			want := 1
			if level == slog.LevelWarn {
				want = 0
			}
			if len(records) != want {
				t.Fatalf("level %s: duration log count = %d, want %d", level, len(records), want)
			}
		}
	})
}
