package api_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	logger "github.com/dengweitian0-svg/TimeCue/internal/log"
	"github.com/dengweitian0-svg/TimeCue/internal/model"
	"github.com/dengweitian0-svg/TimeCue/internal/store"
	"github.com/gin-gonic/gin"
)

// scheduleLoggingRouter 复用 API 的真实数据库装配，并提供状态校验入口。
func scheduleLoggingRouter(t *testing.T) (*gin.Engine, *store.ScheduleStore) {
	t.Helper()
	router, db := testRouter(t)
	schedules, err := store.NewScheduleStore(db)
	if err != nil {
		t.Fatal(err)
	}
	return router, schedules
}

// captureHTTPLogs 捕获全局日志；调用它的测试不能并行执行。
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

// requireHTTPRecord 校验唯一请求日志、北京时间和内容暴露边界。
func requireHTTPRecord(t *testing.T, contents []byte, method, path, level, message string, status int, elapsed time.Duration) map[string]any {
	t.Helper()
	lines := bytes.Split(bytes.TrimSpace(contents), []byte("\n"))
	if len(lines) != 1 || len(lines[0]) == 0 {
		t.Fatalf("请求日志必须只有一条: %s", contents)
	}
	var record map[string]any
	if err := json.Unmarshal(lines[0], &record); err != nil {
		t.Fatalf("日志 JSON 无效: %v", err)
	}
	if record["level"] != level || record["msg"] != "[http] "+message || record["method"] != method || record["path"] != path || record["status"] != float64(status) {
		t.Fatalf("HTTP 日志不符: %#v", record)
	}
	if _, ok := record["route"].(string); !ok {
		t.Fatalf("缺少路由: %#v", record)
	}
	ms, ok := record["duration_ms"].(float64)
	if !ok || ms < 0 || ms > float64(elapsed)/float64(time.Millisecond) {
		t.Fatalf("请求耗时无效: %#v", record)
	}
	stamp, ok := record["time"].(string)
	if !ok {
		t.Fatalf("时间戳无效: %#v", record)
	}
	when, err := time.ParseInLocation("2006-01-02-15:04:05", stamp, model.BeijingLocation())
	if err != nil || time.Since(when) < 0 || time.Since(when) > elapsed+2*time.Second {
		t.Fatalf("日志不是当前北京时间: %q", stamp)
	}
	for _, private := range []string{"private-query", "private-request", "private-response", "private-description"} {
		if bytes.Contains(contents, []byte(private)) {
			t.Fatalf("日志暴露 %q: %s", private, contents)
		}
	}
	return record
}

// shortHTTPWriter 模拟连接只接受部分响应，验证提交事实仍可观察。
type shortHTTPWriter struct {
	*httptest.ResponseRecorder
}

func (w *shortHTTPWriter) Write(body []byte) (int, error) {
	return w.ResponseRecorder.Write(body[:len(body)/2])
}
