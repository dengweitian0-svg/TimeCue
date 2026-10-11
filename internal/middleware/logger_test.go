package middleware_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/api"
	logger "github.com/dengweitian0-svg/TimeCue/internal/log"
	"github.com/dengweitian0-svg/TimeCue/internal/middleware"
	"github.com/dengweitian0-svg/TimeCue/internal/model"
	"github.com/dengweitian0-svg/TimeCue/internal/service"
	"github.com/dengweitian0-svg/TimeCue/internal/store"
	"github.com/gin-gonic/gin"
)

func TestHTTPLogger(t *testing.T) {
	readLog := captureHTTPLogs(t)
	router := api.NewRouter(nil, func(context.Context) error { return nil })
	const delay = 5 * time.Millisecond
	router.POST("/timed", middleware.WrapHTTPHandler(func(c *gin.Context) middleware.HTTPResult {
		time.Sleep(delay)
		return middleware.JSONResult(http.StatusCreated, "private-response")
	}, nil))
	router.GET("/panic", func(c *gin.Context) { panic("test panic") })
	router.GET("/written-panic", func(c *gin.Context) {
		middleware.WriteHTTPResult(c, middleware.HTTPResult{
			Status: http.StatusAccepted,
			Body:   "private-response",
			Cause:  errors.New("earlier failure"),
		}, nil)
		panic(errors.New("test panic"))
	})
	router.GET("/failed", middleware.WrapHTTPHandler(func(*gin.Context) middleware.HTTPResult {
		return middleware.JSONResult(http.StatusBadGateway, nil)
	}, nil))
	router.GET("/error-result", middleware.WrapHTTPHandler(func(*gin.Context) middleware.HTTPResult {
		return middleware.HTTPResult{
			Status: http.StatusBadRequest,
			Cause:  errors.New("original failure"),
		}
	}, nil))

	for _, tc := range []struct {
		name, method, target, host, level, message, code, cause string
		status                                                  int
	}{
		{"health", "GET", "/health", "127.0.0.1:10086", "DEBUG", "HTTP health check passed", "", "", 200},
		{"timed", "POST", "/timed?token=private-query", "127.0.0.1:10086", "INFO", "HTTP request completed", "", "", 201},
		{"forbidden host", "GET", "/health", "example.com", "WARN", "HTTP request rejected", "forbidden_host", "", 403},
		{"not found", "GET", "/absent", "127.0.0.1:10086", "WARN", "HTTP request rejected", "route_not_found", "", 404},
		{"method not allowed", "PUT", "/health", "127.0.0.1:10086", "WARN", "HTTP request rejected", "method_not_allowed", "", 405},
		{"recovered panic", "GET", "/panic", "127.0.0.1:10086", "ERROR", "HTTP panic", "internal_error", "test panic", 500},
		{"written panic", "GET", "/written-panic", "127.0.0.1:10086", "ERROR", "HTTP panic", "", "test panic", 202},
		{"generic failure", "GET", "/failed", "127.0.0.1:10086", "ERROR", "HTTP request failed", "", "", 502},
		{"error precedes status", "GET", "/error-result", "127.0.0.1:10086", "ERROR", "HTTP request failed", "", "original failure", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := len(readLog())
			response, elapsed := logRequest(router, tc.method, tc.target, tc.host, "private-request", "")
			if response.Code != tc.status {
				t.Fatalf("status = %d, want %d", response.Code, tc.status)
			}
			path := strings.SplitN(tc.target, "?", 2)[0]
			record := requireHTTPRecord(t, readLog()[before:], tc.method, path, tc.level, tc.message, tc.status, elapsed)
			if tc.code != "" && (record["code"] != tc.code || record["message"] == nil) {
				t.Fatalf("missing error details: %#v", record)
			}
			if tc.cause != "" && record["error"] != tc.cause {
				t.Fatalf("original cause lost: %#v", record)
			}
			if tc.name == "timed" && record["duration_ms"].(float64) < float64(delay)/float64(time.Millisecond) {
				t.Fatalf("duration excludes handler delay: %#v", record)
			}
		})
	}
}

func scheduleLogRouter(t *testing.T) *gin.Engine {
	t.Helper()
	router, _ := resourceLogRouter(t)
	return router
}

func resourceLogRouter(t *testing.T) (*gin.Engine, *service.App) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "api.db")
	db := store.NewSqlite()
	if err := db.Connect(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	script, err := os.ReadFile(filepath.Join("..", "..", "migrations", "001_initial.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Exec(string(script)); err != nil {
		t.Fatal(err)
	}
	script2, err := os.ReadFile(filepath.Join("..", "..", "migrations", "002_schedule_changes.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(string(script2)); err != nil {
		t.Fatal(err)
	}
	if err := db.CheckHealth(context.Background()); err != nil {
		t.Fatal(err)
	}
	repo, err := store.NewScheduleStore(db)
	if err != nil {
		t.Fatal(err)
	}
	notify, err := store.NewNotificationStore(db)
	if err != nil {
		t.Fatal(err)
	}
	app := &service.App{Schedule: repo, Notify: notify}
	return api.NewRouter(app, db.CheckHealth), app
}

func TestHTTPLoggerNotificationRoutes(t *testing.T) {
	router, app := resourceLogRouter(t)
	ctx := context.Background()
	now := time.Now().In(model.BeijingLocation())
	past := now.Add(-time.Hour)
	description := "private-description"
	schedule, first, err := app.Schedule.CreateScheduleWithNotification(ctx, model.Schedule{
		Name:        "private-response",
		Description: &description,
		Type:        model.PeriodSchedule,
		Period:      &model.Period{Open: true, Cron: []string{"0 9 * * *"}},
	}, &past)
	if err != nil {
		t.Fatal(err)
	}
	second, err := app.Notify.CreateNotification(ctx, schedule.ID, past.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	future, err := app.Notify.CreateNotification(ctx, schedule.ID, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	readLog := captureHTTPLogs(t)
	const base = "/api/v1/notifications"
	for _, tc := range []struct {
		method, target, body, level, message, route, code string
		status                                            int
		fields                                            map[string]any
	}{
		{"GET", base + "?state=unacknowledged&token=private-query", "", "DEBUG", "notifications listed", base, "", 200, map[string]any{"count": float64(2), "total": float64(2)}},
		{"GET", fmt.Sprintf("%s/%d", base, first.ID), "", "DEBUG", "notification retrieved", base + "/:id", "", 200, map[string]any{"notification_id": float64(first.ID), "schedule_id": float64(schedule.ID)}},
		{"POST", base + "/acknowledgements", fmt.Sprintf(`{"notificationIds":[%d,%d,%d]}`, first.ID, second.ID, first.ID), "INFO", "notifications acknowledged", base + "/acknowledgements", "", 200, map[string]any{"count": float64(2)}},
		{"POST", base + "/acknowledgements", fmt.Sprintf(`{"notificationIds":[%d]}`, first.ID), "INFO", "notifications acknowledged", base + "/acknowledgements", "", 200, map[string]any{"count": float64(1)}},
		{"GET", base + "?state=unacknowledged", "", "DEBUG", "notifications listed", base, "", 200, map[string]any{"count": float64(0), "total": float64(0)}},
		{"GET", base + "?state=invalid", "", "WARN", "HTTP request rejected", base, "invalid_request", 400, nil},
		{"GET", base + "/999999", "", "WARN", "HTTP request rejected", base + "/:id", "notification_not_found", 404, nil},
		{"POST", base + "/acknowledgements", fmt.Sprintf(`{"notificationIds":[%d]}`, future.ID), "WARN", "HTTP request rejected", base + "/acknowledgements", "invalid_acknowledgement", 409, nil},
	} {
		t.Run(tc.method+" "+tc.target+" "+tc.body, func(t *testing.T) {
			before := len(readLog())
			response, elapsed := logRequest(router, tc.method, tc.target, "127.0.0.1:10086", tc.body, "application/json")
			if response.Code != tc.status {
				t.Fatalf("status=%d, want=%d: %s", response.Code, tc.status, response.Body.String())
			}
			record := requireHTTPRecord(t, readLog()[before:], tc.method, strings.SplitN(tc.target, "?", 2)[0], tc.level, tc.message, tc.status, elapsed)
			if record["route"] != tc.route {
				t.Fatalf("route facts lost: %#v", record)
			}
			for key, want := range tc.fields {
				if record[key] != want {
					t.Fatalf("missing %s=%v: %#v", key, want, record)
				}
			}
			if tc.code != "" {
				if record["code"] != tc.code {
					t.Fatalf("error code lost: %#v", record)
				}
				for _, key := range []string{"count", "total", "notification_id", "schedule_id"} {
					if _, exists := record[key]; exists {
						t.Fatalf("failure must not include successful %s: %#v", key, record)
					}
				}
			}
		})
	}
}

func TestHTTPLoggerScheduleRoutes(t *testing.T) {
	router := scheduleLogRouter(t)
	readLog := captureHTTPLogs(t)
	const base = "/api/v1/scheduler/schedules"
	future := time.Now().In(model.BeijingLocation()).Add(time.Hour).Truncate(time.Minute).Format(time.RFC3339)
	body := fmt.Sprintf(`{"name":"private-request","description":"private-description","type":"single","single":{"notifyTime":%q}}`, future)
	before := len(readLog())
	response, elapsed := logRequest(router, "POST", base, "127.0.0.1:10086", body, "application/json")
	if response.Code != http.StatusCreated {
		t.Fatalf("create failed: %s", response.Body.String())
	}
	var result struct {
		Data struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	id := result.Data.ID
	record := requireHTTPRecord(t, readLog()[before:], "POST", base, "INFO", "schedule created", 201, elapsed)
	if id <= 0 || record["schedule_id"] != float64(id) || record["type"] != "single" || record["route"] != base {
		t.Fatalf("creation facts lost: %#v", record)
	}
	if mutation := record["mutation"].(map[string]any); mutation["state"].(map[string]any)["single.notify_time"] != future {
		t.Fatalf("单次创建时间丢失: %#v", mutation)
	}
	path := fmt.Sprintf("%s/%d", base, id)
	before = len(readLog())
	updateBody := strings.TrimSuffix(body, "}") + `,"expectedRevision":1}`
	response, elapsed = logRequest(router, "PUT", path, "127.0.0.1:10086", updateBody, "application/json")
	if response.Code != 200 {
		t.Fatalf("update failed: %s", response.Body.String())
	}
	record = requireHTTPRecord(t, readLog()[before:], "PUT", path, "INFO", "schedule updated", 200, elapsed)
	if record["schedule_id"] != float64(id) || record["route"] != base+"/:id" {
		t.Fatalf("update facts lost: %#v", record)
	}
	for _, tc := range []struct {
		method, target, level, message, code string
		status                               int
	}{
		{"GET", path, "DEBUG", "schedule retrieved", "", 200},
		{"GET", base + "?limit=1&offset=0&token=private-query", "DEBUG", "schedules listed", "", 200},
		{"DELETE", path, "INFO", "schedule deleted", "", 204},
		{"GET", path, "WARN", "HTTP request rejected", "schedule_not_found", 404},
		{"DELETE", path, "WARN", "HTTP request rejected", "schedule_not_found", 404},
		{"GET", base + "/abc", "WARN", "HTTP request rejected", "invalid_request", 400},
		{"GET", base + "?limit=51", "WARN", "HTTP request rejected", "invalid_request", 400},
	} {
		t.Run(tc.method+" "+tc.target, func(t *testing.T) {
			before := len(readLog())
			response, elapsed := logRequest(router, tc.method, tc.target, "127.0.0.1:10086", "", "")
			if response.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", response.Code, tc.status, response.Body.String())
			}
			record := requireHTTPRecord(t, readLog()[before:], tc.method, strings.SplitN(tc.target, "?", 2)[0], tc.level, tc.message, tc.status, elapsed)
			if tc.code != "" && (record["code"] != tc.code || record["message"] == nil) {
				t.Fatalf("rejection details lost: %#v", record)
			}
			if tc.message == "schedules listed" {
				if record["count"] != float64(1) || record["limit"] != float64(1) || record["offset"] != float64(0) || record["route"] != base {
					t.Fatalf("pagination facts lost: %#v", record)
				}
			} else if tc.code == "" && (record["schedule_id"] != float64(id) || record["route"] != base+"/:id") {
				t.Fatalf("schedule facts lost: %#v", record)
			}
		})
	}
	for _, tc := range []struct {
		body, contentType, code string
		status                  int
	}{
		{"{", "application/json", "invalid_request", 400},
		{"{}", "text/plain", "unsupported_media_type", 415},
		{strings.Repeat(" ", 1<<20+1), "application/json", "payload_too_large", 413},
	} {
		before := len(readLog())
		response, elapsed := logRequest(router, "POST", base, "127.0.0.1:10086", tc.body, tc.contentType)
		if response.Code != tc.status {
			t.Fatalf("status = %d, want %d", response.Code, tc.status)
		}
		record := requireHTTPRecord(t, readLog()[before:], "POST", base, "WARN", "HTTP request rejected", tc.status, elapsed)
		if record["code"] != tc.code || record["message"] == nil {
			t.Fatalf("validation details lost: %#v", record)
		}
	}
}

type failingScheduleRepo struct {
	service.ScheduleRepo
	cause error
}

func (r failingScheduleRepo) ApplyScheduleChange(context.Context, model.ScheduleChange, model.PrepareScheduleChange) (*model.ScheduleWrite, error) {
	return nil, r.cause
}

func (r failingScheduleRepo) CreateScheduleWithNotification(context.Context, model.Schedule, *time.Time) (*model.Schedule, *model.Notification, error) {
	return nil, nil, r.cause
}

func (r failingScheduleRepo) GetScheduleByID(context.Context, int, ...model.QueryOption) (*model.Schedule, error) {
	return nil, r.cause
}

func (r failingScheduleRepo) GetAllSchedule(context.Context, ...model.QueryOption) ([]*model.Schedule, error) {
	return nil, r.cause
}

func (r failingScheduleRepo) DeleteScheduleByID(context.Context, int) (*model.ScheduleWrite, error) {
	return nil, r.cause
}

func (r failingScheduleRepo) ReadUnacknowledged(context.Context, model.UnacknowledgedRange, model.NotificationPosition, int) ([]*model.Schedule, []*model.Notification, int, error) {
	return nil, nil, 0, r.cause
}

func (r failingScheduleRepo) ReadScheduleState(context.Context, time.Time, time.Time, time.Time, model.NotificationPosition, int) ([]*model.Schedule, []*model.Notification, []*model.Notification, int, error) {
	return nil, nil, nil, 0, r.cause
}

type failingNotifyRepo struct {
	service.NotifyRepo
	cause error
}

func (r failingNotifyRepo) GetNotificationByID(context.Context, int) (*model.Notification, error) {
	return nil, r.cause
}

func (r failingNotifyRepo) AcknowledgeNotifications(context.Context, []int, time.Time) ([]*model.Notification, error) {
	return nil, r.cause
}

func TestHTTPLoggerFailures(t *testing.T) {
	readLog := captureHTTPLogs(t)
	cause := errors.New("original database failure")
	router := api.NewRouter(&service.App{Schedule: failingScheduleRepo{cause: cause}, Notify: failingNotifyRepo{cause: cause}}, func(context.Context) error { return cause })
	const base = "/api/v1/scheduler/schedules"
	for _, tc := range []struct {
		method, target, message string
	}{
		{"POST", base, "failed to create schedule"},
		{"GET", base, "failed to list schedules"},
		{"GET", base + "/1", "failed to get schedule"},
		{"DELETE", base + "/1", "failed to delete schedule"},
		{"GET", "/health", "HTTP health check failed"},
		{"GET", "/api/v1/notifications?state=unacknowledged", "failed to list notifications"},
		{"GET", "/api/v1/notifications/1", "failed to get notification"},
		{"POST", "/api/v1/notifications/acknowledgements", "failed to acknowledge notifications"},
	} {
		t.Run(tc.message, func(t *testing.T) {
			before := len(readLog())
			body := `{"name":"private-request","type":"period","period":{"cron":["0 9 * * *"]}}`
			if tc.target == "/api/v1/notifications/acknowledgements" {
				body = `{"notificationIds":[1]}`
			}
			response, elapsed := logRequest(router, tc.method, tc.target, "127.0.0.1:10086", body, "application/json")
			status, code := 500, "internal_error"
			if tc.target == "/health" {
				status, code = 503, "database_unavailable"
			}
			if response.Code != status || strings.Contains(response.Body.String(), cause.Error()) {
				t.Fatalf("unexpected failure response: %d %s", response.Code, response.Body.String())
			}
			record := requireHTTPRecord(t, readLog()[before:], tc.method, strings.SplitN(tc.target, "?", 2)[0], "ERROR", tc.message, status, elapsed)
			if record["error"] != cause.Error() || record["code"] != code {
				t.Fatalf("original error or code lost: %#v", record)
			}
			if record["mutation"] != nil {
				t.Fatalf("失败操作携带已提交摘要: %#v", record)
			}
		})
	}
	before := len(readLog())
	schemaErr := fmt.Errorf("schema check failed: %w", store.ErrSchemaInvalid)
	router = api.NewRouter(nil, func(context.Context) error { return schemaErr })
	response, elapsed := logRequest(router, "GET", "/health", "127.0.0.1:10086", "", "")
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("schema failure status = %d", response.Code)
	}
	record := requireHTTPRecord(t, readLog()[before:], "GET", "/health", "ERROR", "HTTP health check failed", 503, elapsed)
	if record["error"] != schemaErr.Error() || record["code"] != "schema_invalid" {
		t.Fatalf("schema failure details lost: %#v", record)
	}
}

func TestHTTPLoggerLevelChanges(t *testing.T) {
	readLog := captureHTTPLogs(t)
	router := api.NewRouter(nil, func(context.Context) error { return nil })
	router.GET("/success", middleware.WrapHTTPHandler(func(*gin.Context) middleware.HTTPResult {
		return middleware.JSONResult(http.StatusOK, nil)
	}, nil))
	router.GET("/panic", func(c *gin.Context) { panic("test panic") })
	for _, threshold := range []slog.Level{slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError, slog.LevelDebug} {
		logger.SetLevel(threshold)
		for _, tc := range []struct {
			path, message string
			level         slog.Level
			status        int
		}{
			{"/health", "HTTP health check passed", slog.LevelDebug, 200},
			{"/success", "HTTP request completed", slog.LevelInfo, 200},
			{"/absent", "HTTP request rejected", slog.LevelWarn, 404},
			{"/panic", "HTTP panic", slog.LevelError, 500},
		} {
			before := len(readLog())
			response, elapsed := logRequest(router, "GET", tc.path, "127.0.0.1:10086", "", "")
			if response.Code != tc.status {
				t.Fatalf("status = %d, want %d", response.Code, tc.status)
			}
			contents := readLog()[before:]
			if tc.level < threshold {
				if len(contents) != 0 {
					t.Fatalf("threshold %s emitted %s log: %s", threshold, tc.level, contents)
				}
				continue
			}
			requireHTTPRecord(t, contents, "GET", tc.path, tc.level.String(), tc.message, tc.status, elapsed)
		}
	}
}

func TestHTTPHandlerPaginationMetadata(t *testing.T) {
	router := scheduleLogRouter(t)
	readLog := captureHTTPLogs(t)
	for _, tc := range []struct {
		query         string
		limit, offset int
	}{
		{"", 50, 0},
		{"?limit=2&offset=5", 2, 5},
	} {
		before := len(readLog())
		const path = "/api/v1/scheduler/schedules"
		response, elapsed := logRequest(router, "GET", path+tc.query, "127.0.0.1:10086", "", "")
		if response.Code != 200 || !bytes.Equal(response.Body.Bytes(), []byte(`{"data":[]}`)) {
			t.Fatalf("pagination metadata changed response: %d %s", response.Code, response.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || len(body) != 1 {
			t.Fatalf("unexpected response fields: %#v, %v", body, err)
		}
		record := requireHTTPRecord(t, readLog()[before:], "GET", path, "DEBUG", "schedules listed", 200, elapsed)
		if record["count"] != float64(0) || record["limit"] != float64(tc.limit) || record["offset"] != float64(tc.offset) {
			t.Fatalf("effective pagination facts lost: %#v", record)
		}
	}
}
