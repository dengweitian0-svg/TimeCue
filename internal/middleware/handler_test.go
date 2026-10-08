package middleware_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
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

type brokenJSON struct{}

func (brokenJSON) MarshalJSON() ([]byte, error) {
	return nil, errors.New("private serialization failure")
}

type panickingJSON struct{}

func (panickingJSON) MarshalJSON() ([]byte, error) {
	panic("serialization panic")
}

// TestHTTPResultPreservesMutationFacts 验证提交素材先于序列化、字段提取和响应校验收集。
func TestHTTPResultPreservesMutationFacts(t *testing.T) {
	readLog := captureHTTPLogs(t)
	for _, tc := range []struct {
		name    string
		body    any
		status  int
		fields  middleware.HTTPResultFields
		message string
	}{
		{"serialization error", brokenJSON{}, 201, nil, "HTTP request failed"},
		{"serialization panic", panickingJSON{}, 201, nil, "HTTP panic"},
		{"invalid status", nil, 0, nil, "HTTP request failed"},
		{"fields panic", nil, 201, func(*gin.Context, middleware.HTTPResult) []slog.Attr { panic("fields panic") }, "HTTP panic"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := middlewareTestRouter()
			router.POST("/committed", middleware.WrapHTTPHandler(func(*gin.Context) middleware.HTTPResult {
				return middleware.HTTPResult{
					Status: tc.status, Body: tc.body, SuccessMessage: "schedule write replayed",
					LogFields: []slog.Attr{slog.Int("schedule_id", 5), slog.Any("mutation", map[string]any{"outcome": "applied"})},
				}
			}, tc.fields))
			before := len(readLog())
			response, elapsed := logRequest(router, "POST", "/committed", "127.0.0.1:10086", "", "")
			if response.Code != 500 {
				t.Fatalf("unexpected status: %d", response.Code)
			}
			record := requireHTTPRecord(t, readLog()[before:], "POST", "/committed", "ERROR", tc.message, 500, elapsed)
			if record["schedule_id"] != float64(5) || record["mutation"].(map[string]any)["outcome"] != "applied" || record["error"] == nil {
				t.Fatalf("业务事实或响应错误丢失: %#v", record)
			}
		})
	}
}

func TestHTTPHandlerGuards(t *testing.T) {
	readLog := captureHTTPLogs(t)
	for _, tc := range []struct {
		name, cause, message, body string
		status                     int
		handler                    middleware.HTTPHandler
		fields                     middleware.HTTPResultFields
	}{
		{
			name: "missing status", cause: "invalid HTTP status: 0", message: "HTTP request failed", status: 500,
			handler: func(*gin.Context) middleware.HTTPResult { return middleware.HTTPResult{} },
		},
		{
			name: "informational status", cause: "invalid HTTP status: 199", message: "HTTP request failed", status: 500,
			handler: func(*gin.Context) middleware.HTTPResult { return middleware.JSONResult(199, "private-response") },
		},
		{
			name: "negative status", cause: "invalid HTTP status: -1", message: "HTTP request failed", status: 500,
			handler: func(*gin.Context) middleware.HTTPResult { return middleware.JSONResult(-1, "private-response") },
		},
		{
			name: "out of range status", cause: "invalid HTTP status: 600", message: "HTTP request failed", status: 500,
			handler: func(*gin.Context) middleware.HTTPResult {
				result := middleware.JSONResult(600, "private-response")
				result.Headers = http.Header{"Location": {"/private-resource"}}
				return result
			},
		},
		{
			name: "serialization failure", cause: "private serialization failure", message: "HTTP request failed", status: 500,
			handler: func(*gin.Context) middleware.HTTPResult {
				result := middleware.JSONResult(201, brokenJSON{})
				result.Headers = http.Header{"Location": {"/private-resource"}}
				return result
			},
		},
		{
			name: "serialization panic", cause: "serialization panic", message: "HTTP panic", status: 500,
			handler: func(*gin.Context) middleware.HTTPResult { return middleware.JSONResult(200, panickingJSON{}) },
		},
		{
			name: "handler panic", cause: "handler panic", message: "HTTP panic", status: 500,
			handler: func(*gin.Context) middleware.HTTPResult { panic("handler panic") },
		},
		{
			name: "fields panic", cause: "fields panic", message: "HTTP panic", status: 500,
			handler: func(*gin.Context) middleware.HTTPResult { return middleware.JSONResult(201, "private-response") },
			fields:  func(*gin.Context, middleware.HTTPResult) []slog.Attr { panic("fields panic") },
		},
		{
			name: "handler wrote response", cause: "handler wrote response", message: "HTTP request failed", status: 202, body: "already committed",
			handler: func(c *gin.Context) middleware.HTTPResult {
				c.String(202, "already committed")
				return middleware.JSONResult(201, "private-response")
			},
		},
		{
			name: "fields wrote response", cause: "HTTP result fields wrote response", message: "HTTP request failed", status: 202, body: "already committed",
			handler: func(*gin.Context) middleware.HTTPResult { return middleware.JSONResult(201, "private-response") },
			fields: func(c *gin.Context, _ middleware.HTTPResult) []slog.Attr {
				c.String(202, "already committed")
				return nil
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := middlewareTestRouter()
			fields := tc.fields
			if fields == nil {
				fields = func(*gin.Context, middleware.HTTPResult) []slog.Attr {
					t.Error("field extractor called for failed handler result")
					return nil
				}
			}
			downstream := false
			router.GET("/guard", middleware.WrapHTTPHandler(tc.handler, fields), func(c *gin.Context) {
				downstream = true
				c.String(200, "unwanted extra response")
			})
			before := len(readLog())
			response, elapsed := logRequest(router, "GET", "/guard", "127.0.0.1:10086", "", "")
			wantBody := tc.body
			if tc.status == 500 {
				wantBody = `{"error":{"code":"internal_error","message":"internal server error"}}`
			}
			if response.Code != tc.status || response.Body.String() != wantBody || response.Header().Get("Location") != "" || downstream {
				t.Fatalf("guard response: %d %s %v (downstream=%v)", response.Code, response.Body.String(), response.Header(), downstream)
			}
			record := requireHTTPRecord(t, readLog()[before:], "GET", "/guard", "ERROR", tc.message, tc.status, elapsed)
			cause, ok := record["error"].(string)
			if !ok || !strings.Contains(cause, tc.cause) {
				t.Fatalf("cause lost: %#v", record)
			}
			if tc.status == 500 && record["code"] != "internal_error" {
				t.Fatalf("fallback code lost: %#v", record)
			}
		})
	}
}

type failingHTTPWriter struct {
	*httptest.ResponseRecorder
	cause  error
	short  bool
	writes int
}

func (w *failingHTTPWriter) Write(body []byte) (int, error) {
	w.writes++
	if w.short {
		return w.ResponseRecorder.Write(body[:len(body)/2])
	}
	return 0, w.cause
}

func TestHTTPHandlerWriteFailures(t *testing.T) {
	readLog := captureHTTPLogs(t)
	for _, short := range []bool{false, true} {
		router := middlewareTestRouter()
		downstream := false
		router.GET("/write", middleware.WrapHTTPHandler(func(*gin.Context) middleware.HTTPResult {
			return middleware.DataResult(201, "private-response")
		}, nil), func(*gin.Context) { downstream = true })
		writer := &failingHTTPWriter{
			ResponseRecorder: httptest.NewRecorder(),
			cause:            errors.New("connection write failed"),
			short:            short,
		}
		request := httptest.NewRequest("GET", "/write", nil)
		request.Host = "127.0.0.1:10086"
		before := len(readLog())
		started := time.Now()
		router.ServeHTTP(writer, request)
		elapsed := time.Since(started)
		if writer.Code != 201 || writer.writes != 1 || downstream {
			t.Fatalf("write failure retried or changed status: %d, %d writes, downstream=%v", writer.Code, writer.writes, downstream)
		}
		record := requireHTTPRecord(t, readLog()[before:], "GET", "/write", "ERROR", "HTTP request failed", 201, elapsed)
		want := writer.cause.Error()
		if short {
			want = io.ErrShortWrite.Error()
		}
		if record["error"] != want {
			t.Fatalf("write cause lost: %#v", record)
		}
	}
}

func TestHTTPHandlerPreservesCauseOnWriteFailure(t *testing.T) {
	readLog := captureHTTPLogs(t)
	original := errors.New("original service failure")
	writeErr := errors.New("response write failure")
	router := middlewareTestRouter()
	router.GET("/two-failures", middleware.WrapHTTPHandler(func(*gin.Context) middleware.HTTPResult {
		return middleware.ErrorResult(503, middleware.APIError{
			Code:    "unavailable",
			Message: "try later",
		}, original)
	}, nil))
	writer := &failingHTTPWriter{
		ResponseRecorder: httptest.NewRecorder(),
		cause:            writeErr,
	}
	request := httptest.NewRequest("GET", "/two-failures", nil)
	request.Host = "127.0.0.1:10086"
	started := time.Now()
	router.ServeHTTP(writer, request)
	elapsed := time.Since(started)
	if writer.Code != 503 || writer.writes != 1 {
		t.Fatalf("unexpected failure response: %d, %d writes", writer.Code, writer.writes)
	}
	record := requireHTTPRecord(t, readLog(), "GET", "/two-failures", "ERROR", "HTTP request failed", 503, elapsed)
	cause, ok := record["error"].(string)
	if !ok || !strings.Contains(cause, original.Error()) || !strings.Contains(cause, writeErr.Error()) {
		t.Fatalf("original or write error lost: %#v", record)
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
		if c.Param("kind") == "mutation" {
			result := middleware.DataResult(200, "private-response")
			result.LogFields = []slog.Attr{slog.Int("schedule_id", 5), slog.Any("mutation", map[string]any{"outcome": "replayed"})}
			result.SuccessMessage = "schedule write replayed"
			return result
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
	for _, path := range []string{"/isolated/failure", "/isolated/success", "/isolated-panic", "/isolated/success", "/isolated/mutation", "/isolated/success"} {
		before := len(readLog())
		response, elapsed := logRequest(router, "GET", path, "127.0.0.1:10086", "", "")
		level, message, status := "INFO", "HTTP request completed", 200
		if path == "/isolated/failure" {
			level, message, status = "ERROR", "HTTP request failed", 500
		} else if path == "/isolated-panic" {
			level, message, status = "ERROR", "HTTP panic", 500
		} else if path == "/isolated/mutation" {
			message = "schedule write replayed"
		}
		if response.Code != status {
			t.Fatalf("status = %d, want %d", response.Code, status)
		}
		record := requireHTTPRecord(t, readLog()[before:], "GET", path, level, message, status, elapsed)
		if path == "/isolated/success" {
			for _, key := range []string{"code", "message", "error", "schedule_id", "mutation"} {
				if _, exists := record[key]; exists {
					t.Fatalf("previous request's %s leaked: %#v", key, record)
				}
			}
		}
	}
}
