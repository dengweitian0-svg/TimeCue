package middleware_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	logger "github.com/dengweitian0-svg/TimeCue/internal/log"
	"github.com/dengweitian0-svg/TimeCue/internal/middleware"
	"github.com/gin-gonic/gin"
)

// 全局日志器测试及其子测试均不能并行执行。
func captureHTTPLogs(t *testing.T) func() []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "requests.jsonl")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	previousDefault := slog.Default()
	logger.Upgrade(file)
	logger.SetLevel(slog.LevelDebug)
	t.Cleanup(func() {
		logger.Upgrade(os.Stderr)
		logger.SetLevel(slog.LevelInfo)
		slog.SetDefault(previousDefault)
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	})
	return func() []byte {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return contents
	}
}

func logRequest(router http.Handler, method, target, host, body, contentType string) (*httptest.ResponseRecorder, time.Duration) {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Host = host
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	response := httptest.NewRecorder()
	started := time.Now()
	router.ServeHTTP(response, req)
	return response, time.Since(started)
}

func requireHTTPRecord(t *testing.T, contents []byte, method, path, level, message string, status int, elapsed time.Duration) map[string]any {
	t.Helper()
	lines := bytes.Split(bytes.TrimSpace(contents), []byte("\n"))
	if len(lines) != 1 || len(lines[0]) == 0 {
		t.Fatalf("log count must be 1: %s", contents)
	}
	var record map[string]any
	if err := json.Unmarshal(lines[0], &record); err != nil {
		t.Fatalf("invalid JSON log: %v", err)
	}
	if record["level"] != level || record["msg"] != "[http] "+message || record["method"] != method || record["path"] != path || record["status"] != float64(status) {
		t.Fatalf("unexpected HTTP log: %#v", record)
	}
	if _, ok := record["route"].(string); !ok {
		t.Fatalf("missing route: %#v", record)
	}
	ms, ok := record["duration_ms"].(float64)
	if !ok || ms < 0 || ms > float64(elapsed)/float64(time.Millisecond) {
		t.Fatalf("invalid duration_ms: %#v (request elapsed %s)", record["duration_ms"], elapsed)
	}
	stamp, ok := record["time"].(string)
	if !ok {
		t.Fatalf("invalid timestamp: %#v", record["time"])
	}
	when, err := time.ParseInLocation("2006-01-02-15:04:05", stamp, time.FixedZone("UTC+8", 8*3600))
	if err != nil || time.Since(when) < 0 || time.Since(when) > elapsed+2*time.Second {
		t.Fatalf("timestamp is not current Beijing time: %q", stamp)
	}
	for _, private := range []string{"private-query", "private-request", "private-response", "private-description"} {
		if bytes.Contains(contents, []byte(private)) {
			t.Fatalf("log contains %q: %s", private, contents)
		}
	}
	return record
}

func middlewareTestRouter() *gin.Engine {
	router := gin.New()
	router.Use(middleware.HTTPLogger())
	router.Use(middleware.HostGuard("127.0.0.1:10086", middleware.ErrorResult(http.StatusForbidden, middleware.APIError{
		Code:    "forbidden_host",
		Message: "Host must be 127.0.0.1:10086",
	}, nil)))
	router.Use(middleware.Recovery())
	return router
}

func TestHTTPResultResponses(t *testing.T) {
	readLog := captureHTTPLogs(t)
	cause := errors.New("private internal failure")
	for _, tc := range []struct {
		name, body, contentType string
		result                  middleware.HTTPResult
	}{
		{"data", `{"data":{"value":7}}`, "application/json; charset=utf-8", middleware.DataResult(200, struct {
			Value int `json:"value"`
		}{Value: 7})},
		{"json", `{"data":[]}`, "application/json; charset=utf-8", middleware.JSONResult(200, map[string]any{"data": []int{}})},
		{"null", `null`, "application/json; charset=utf-8", middleware.JSONResult(200, nil)},
		{"error", `{"error":{"code":"unavailable","message":"try later"}}`, "application/json; charset=utf-8", middleware.ErrorResult(503, middleware.APIError{
			Code:    "unavailable",
			Message: "try later",
		}, cause)},
		{"no content", "", "", middleware.NoContentResult()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := middlewareTestRouter()
			result := tc.result
			result.Headers = http.Header{
				"Location": {"/resource/7"},
				"X-Values": {"first", "second"},
			}
			router.GET("/result", middleware.WrapHTTPHandler(func(*gin.Context) middleware.HTTPResult {
				return result
			}, nil))
			before := len(readLog())
			response, elapsed := logRequest(router, "GET", "/result", "127.0.0.1:10086", "", "")
			if response.Code != result.Status || response.Body.String() != tc.body || response.Header().Get("Content-Type") != tc.contentType {
				t.Fatalf("unexpected response: %d %s %v", response.Code, response.Body.String(), response.Header())
			}
			if response.Header().Get("Location") != "/resource/7" || strings.Join(response.Header().Values("X-Values"), ",") != "first,second" {
				t.Fatalf("response headers lost: %v", response.Header())
			}
			level, message := "INFO", "HTTP request completed"
			if result.Cause != nil {
				level, message = "ERROR", "HTTP request failed"
			}
			record := requireHTTPRecord(t, readLog()[before:], "GET", "/result", level, message, result.Status, elapsed)
			if result.Cause != nil && (record["error"] != cause.Error() || record["code"] != "unavailable" || record["message"] != "try later") {
				t.Fatalf("internal cause and public detail lost: %#v", record)
			}
		})
	}
}

func TestHTTPHandlerRequestIsolation(t *testing.T) {
	readLog := captureHTTPLogs(t)
	router := middlewareTestRouter()
	router.GET("/isolated/:kind", middleware.WrapHTTPHandler(func(c *gin.Context) middleware.HTTPResult {
		if c.Param("kind") == "failure" {
			return middleware.ErrorResult(500, middleware.APIError{
				Code:    "failed_operation",
				Message: "operation failed",
			}, errors.New("original request error"))
		}
		return middleware.DataResult(200, "private-response")
	}, func(c *gin.Context, _ middleware.HTTPResult) []slog.Attr {
		if c.Param("kind") == "failure" {
			return []slog.Attr{slog.Int("schedule_id", 42)}
		}
		return nil
	}))
	router.GET("/isolated-panic", middleware.WrapHTTPHandler(func(*gin.Context) middleware.HTTPResult {
		panic("isolated panic")
	}, nil))
	for _, path := range []string{"/isolated/failure", "/isolated/success", "/isolated-panic", "/isolated/success"} {
		before := len(readLog())
		response, elapsed := logRequest(router, "GET", path, "127.0.0.1:10086", "", "")
		level, message, status := "INFO", "HTTP request completed", 200
		if path == "/isolated/failure" {
			level, message, status = "ERROR", "HTTP request failed", 500
		} else if path == "/isolated-panic" {
			level, message, status = "ERROR", "HTTP panic", 500
		}
		if response.Code != status {
			t.Fatalf("status = %d, want %d", response.Code, status)
		}
		record := requireHTTPRecord(t, readLog()[before:], "GET", path, level, message, status, elapsed)
		if path == "/isolated/success" {
			for _, key := range []string{"code", "message", "error", "schedule_id"} {
				if _, exists := record[key]; exists {
					t.Fatalf("previous request's %s leaked: %#v", key, record)
				}
			}
		}
	}
}
