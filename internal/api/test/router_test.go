package api_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	_ "modernc.org/sqlite"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	api "github.com/dengweitian0-svg/TimeCue/internal/api"
	app "github.com/dengweitian0-svg/TimeCue/internal/app"
	"github.com/dengweitian0-svg/TimeCue/internal/config"
	"github.com/dengweitian0-svg/TimeCue/internal/model"
	"github.com/dengweitian0-svg/TimeCue/internal/store"
	"github.com/gin-gonic/gin"
)

func testRouter(t *testing.T) (*gin.Engine, *store.Sqlite) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "api.db")
	db := store.NewSqlite()
	if err := db.Connect(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	script, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "001_initial.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(string(script)); err != nil {
		t.Fatal(err)
	}
	script2, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "002_schedule_changes.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(string(script2)); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if err := db.CheckHealth(context.Background()); err != nil {
		t.Fatal(err)
	}
	schedules, err := store.NewScheduleStore(db)
	if err != nil {
		t.Fatal(err)
	}
	notifications, err := store.NewNotificationStore(db)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := config.NewManager()
	if err != nil {
		t.Fatal(err)
	}
	app, err := app.NewApp(manager, schedules, notifications)
	if err != nil {
		t.Fatal(err)
	}
	return api.NewRouter(app, db.CheckHealth), db
}

func request(router http.Handler, method, path, body, mediaType string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = "127.0.0.1:10086"
	if mediaType != "" {
		req.Header.Set("Content-Type", mediaType)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	return response
}

func TestRouterWithPortChecksConfiguredHost(t *testing.T) {
	router := api.NewRouterWithPort(nil, nil, 1145)

	allowed := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:1145/unknown", nil)
	allowed.Host = "127.0.0.1:1145"
	allowedResponse := httptest.NewRecorder()
	router.ServeHTTP(allowedResponse, allowed)
	if allowedResponse.Code != http.StatusNotFound {
		t.Fatalf("configured host status %d, want %d", allowedResponse.Code, http.StatusNotFound)
	}

	for _, host := range []string{"127.0.0.1:10086", "localhost:1145", "127.0.0.2:1145"} {
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:1145/health", nil)
		req.Host = host
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "Host must be 127.0.0.1:1145") {
			t.Fatalf("host %q was not rejected with configured port: %d %s", host, response.Code, response.Body.String())
		}
	}
}

func requireStatus(t *testing.T, response *httptest.ResponseRecorder, want int) {
	t.Helper()
	if response.Code != want {
		t.Fatalf("status %d, want %d: %s", response.Code, want, response.Body.String())
	}
}

func decodeObject(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func requireScheduleShape(t *testing.T, value any, kind string) int {
	t.Helper()
	row, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("schedule is not object: %#v", value)
	}
	for _, key := range []string{"id", "name", "type", "description", "single", "period", "cancelled", "createdAt", "updatedAt"} {
		if _, ok := row[key]; !ok {
			t.Fatalf("missing %s: %#v", key, row)
		}
	}
	id, ok := row["id"].(float64)
	if !ok || id <= 0 {
		t.Fatalf("invalid id: %#v", row["id"])
	}
	if _, ok := row["name"].(string); !ok {
		t.Fatalf("invalid name: %#v", row)
	}
	if row["type"] != kind {
		t.Fatalf("wrong type: %#v", row)
	}
	if _, ok := row["cancelled"].(bool); !ok {
		t.Fatalf("invalid cancelled: %#v", row)
	}
	for _, key := range []string{"createdAt", "updatedAt"} {
		stamp, ok := row[key].(string)
		if !ok || !strings.HasSuffix(stamp, "+08:00") {
			t.Fatalf("invalid %s: %#v", key, row[key])
		}
		if _, err := time.Parse(time.RFC3339, stamp); err != nil {
			t.Fatal(err)
		}
	}
	if kind == "single" {
		if row["period"] != nil {
			t.Fatalf("period must be null: %#v", row)
		}
		single, ok := row["single"].(map[string]any)
		if !ok {
			t.Fatalf("single must be object: %#v", row)
		}
		stamp, ok := single["notifyTime"].(string)
		if !ok || !strings.HasSuffix(stamp, "+08:00") {
			t.Fatalf("invalid notifyTime: %#v", single)
		}
	} else {
		if row["single"] != nil {
			t.Fatalf("single must be null: %#v", row)
		}
		period, ok := row["period"].(map[string]any)
		if !ok || period["open"] != true {
			t.Fatalf("invalid period: %#v", row)
		}
		cron, ok := period["cron"].([]any)
		if !ok || len(cron) == 0 {
			t.Fatalf("invalid cron: %#v", period)
		}
		for _, item := range cron {
			if _, ok := item.(string); !ok {
				t.Fatalf("cron item is not string: %#v", item)
			}
		}
	}
	return int(id)
}

func TestScheduleRoutes(t *testing.T) {
	router, _ := testRouter(t)
	base := "/api/v1/scheduler/schedules"
	response := request(router, http.MethodGet, "/health", "", "")
	requireStatus(t, response, 200)
	if decodeObject(t, response.Body.Bytes())["data"].(map[string]any)["status"] != "ok" {
		t.Fatal(response.Body.String())
	}

	future := time.Now().In(time.FixedZone("UTC+8", 8*3600)).Add(24 * time.Hour).Truncate(time.Minute).Format(time.RFC3339)
	singleBody := fmt.Sprintf(`{"name":"  体检  ","type":"single","description":"带报告","single":{"notifyTime":%q}}`, future)
	response = request(router, http.MethodPost, base, singleBody, "application/json")
	requireStatus(t, response, 201)
	singleID := requireScheduleShape(t, decodeObject(t, response.Body.Bytes())["data"], "single")
	if response.Header().Get("Location") != fmt.Sprintf("%s/%d", base, singleID) {
		t.Fatal(response.Header())
	}
	if decodeObject(t, response.Body.Bytes())["data"].(map[string]any)["name"] != "体检" {
		t.Fatal(response.Body.String())
	}

	response = request(router, http.MethodPost, base, `{"name":"晨会","type":"period","period":{"cron":["0 9 * * 1,2,3,4,5"]}}`, "application/json")
	requireStatus(t, response, 201)
	periodID := requireScheduleShape(t, decodeObject(t, response.Body.Bytes())["data"], "period")
	if decodeObject(t, response.Body.Bytes())["data"].(map[string]any)["description"] != nil {
		t.Fatal(response.Body.String())
	}

	response = request(router, http.MethodGet, base, "", "")
	requireStatus(t, response, 200)
	rows := decodeObject(t, response.Body.Bytes())["data"].([]any)
	if len(rows) != 2 || requireScheduleShape(t, rows[0], "period") != periodID || requireScheduleShape(t, rows[1], "single") != singleID {
		t.Fatalf("wrong order: %#v", rows)
	}
	response = request(router, http.MethodGet, base+"?type=single&name=%E4%BD%93&limit=1&offset=0", "", "")
	requireStatus(t, response, 200)
	rows = decodeObject(t, response.Body.Bytes())["data"].([]any)
	if len(rows) != 1 || requireScheduleShape(t, rows[0], "single") != singleID {
		t.Fatal(response.Body.String())
	}
	response = request(router, http.MethodGet, base+"?offset=2", "", "")
	requireStatus(t, response, 200)
	if len(decodeObject(t, response.Body.Bytes())["data"].([]any)) != 0 {
		t.Fatal(response.Body.String())
	}

	response = request(router, http.MethodGet, fmt.Sprintf("%s/%d", base, periodID), "", "")
	requireStatus(t, response, 200)
	requireScheduleShape(t, decodeObject(t, response.Body.Bytes())["data"], "period")
	response = request(router, http.MethodDelete, fmt.Sprintf("%s/%d", base, singleID), "", "")
	requireStatus(t, response, 204)
	if response.Body.Len() != 0 {
		t.Fatal(response.Body.String())
	}
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		response = request(router, method, fmt.Sprintf("%s/%d", base, singleID), "", "")
		requireStatus(t, response, 404)
		if decodeObject(t, response.Body.Bytes())["error"].(map[string]any)["code"] != "schedule_not_found" {
			t.Fatal(response.Body.String())
		}
	}
	response = request(router, http.MethodGet, base, "", "")
	requireStatus(t, response, 200)
	if len(decodeObject(t, response.Body.Bytes())["data"].([]any)) != 1 {
		t.Fatal(response.Body.String())
	}
}

func TestInvalidRequestsAndHealth(t *testing.T) {
	router, db := testRouter(t)
	base := "/api/v1/scheduler/schedules"
	future := time.Now().In(time.FixedZone("UTC+8", 8*3600)).Add(time.Hour).Truncate(time.Minute).Format(time.RFC3339)
	invalid := []string{
		`{"name":" ","type":"period","period":{"cron":["0 9 * * *"]}}`,
		`{"name":"x","type":"period","period":{"cron":[]}}`,
		`{"name":"x","type":"period","period":{"cron":["0 9 1 * 1"]}}`,
		`{"name":"x","type":"period","period":{"cron":["0 9 * * *"]},"open":false}`,
		`{"Name":"x","type":"period","period":{"cron":["0 9 * * *"]}}`,
		`{"name":"x","type":"period","period":{"Cron":["0 9 * * *"]}}`,
		`{"name":"x","type":"period","period":{"cron":["0 9 * * *"]},"single":null}`,
		`{"name":"x","type":"single","single":{"notifyTime":"2020-01-01T00:00:00+08:00"}}`,
		`{"name":"x","type":"single","single":{"notifyTime":"2099-01-01T00:00:00Z"}}`,
		fmt.Sprintf(`{"name":"x","type":"single","single":{"notifyTime":%q,"extra":1}}`, future),
		`{"name":"x","type":"other"}`, `null`, `{`,
	}
	for _, body := range invalid {
		response := request(router, http.MethodPost, base, body, "application/json")
		requireStatus(t, response, 400)
		if decodeObject(t, response.Body.Bytes())["error"].(map[string]any)["code"] != "invalid_request" {
			t.Fatal(response.Body.String())
		}
	}
	for _, query := range []string{"?limit=0", "?limit=51", "?limit=-1", "?limit=x", "?offset=-1", "?offset=1.5", "?type=other"} {
		requireStatus(t, request(router, http.MethodGet, base+query, "", ""), 400)
	}
	requireStatus(t, request(router, http.MethodGet, base+"/0", "", ""), 400)
	requireStatus(t, request(router, http.MethodDelete, base+"/abc", "", ""), 400)
	requireStatus(t, request(router, http.MethodGet, base+"/9999", "", ""), 404)
	requireStatus(t, request(router, http.MethodPost, base, `{}`, "text/plain"), 415)
	requireStatus(t, request(router, http.MethodPost, base, strings.Repeat(" ", 1<<20+1), "application/json"), 413)
	requireStatus(t, request(router, http.MethodPost, base, "{"+strings.Repeat("x", 1<<20), "application/json"), 413)
	requireStatus(t, request(router, http.MethodGet, "/absent", "", ""), 404)
	requireStatus(t, request(router, http.MethodPut, base, "", ""), 405)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Host = "example.com"
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	requireStatus(t, response, 403)
	if decodeObject(t, response.Body.Bytes())["error"].(map[string]any)["code"] != "forbidden_host" {
		t.Fatal(response.Body.String())
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	response = request(router, http.MethodGet, "/health", "", "")
	requireStatus(t, response, 503)
	data := decodeObject(t, response.Body.Bytes())["data"].(map[string]any)
	if data["status"] != "error" || data["error"].(map[string]any)["code"] != "database_unavailable" {
		t.Fatal(response.Body.String())
	}
}

func TestHealthRejectsUnmigratedDatabase(t *testing.T) {
	db := store.NewSqlite()
	if err := db.Connect(context.Background(), filepath.Join(t.TempDir(), "empty.db")); err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.CheckHealth(context.Background()); err == nil {
		t.Fatal("unmigrated database passed health check")
	}
	response := request(api.NewRouter(nil, db.CheckHealth), http.MethodGet, "/health", "", "")
	requireStatus(t, response, 503)
	data := decodeObject(t, response.Body.Bytes())["data"].(map[string]any)
	if data["status"] != "error" || data["error"].(map[string]any)["code"] != "schema_invalid" {
		t.Fatal(response.Body.String())
	}
}

func TestScheduleViewsAndAcknowledgements(t *testing.T) {
	router, db := testRouter(t)
	schedules, _ := store.NewScheduleStore(db)
	notifications, _ := store.NewNotificationStore(db)
	ctx := context.Background()
	now := time.Now().In(model.BeijingLocation())
	schedule, err := schedules.CreateSchedule(ctx, model.Schedule{Name: "历史详情", Type: model.PeriodSchedule, Period: &model.Period{Open: true, Cron: []string{"0 9 * * *"}}})
	if err != nil {
		t.Fatal(err)
	}
	due, err := notifications.CreateNotification(ctx, schedule.ID, now.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	failed, err := notifications.CreateNotification(ctx, schedule.ID, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	future, err := notifications.CreateNotification(ctx, schedule.ID, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err = notifications.SetDeliveryResult(ctx, failed.ID, model.NotifyFailed); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/scheduler/schedules?view=occurrences&include=unacknowledged", "/api/v1/scheduler/schedules?view=occurrences&date=" + now.Format("2006-01-02"), "/api/v1/scheduler/schedules?view=calendar&month=" + now.Format("2006-01"), "/api/v1/notifications?state=unacknowledged", fmt.Sprintf("/api/v1/notifications/%d", due.ID)} {
		requireStatus(t, request(router, "GET", path, "", ""), http.StatusOK)
	}
	for _, path := range []string{"/api/v1/scheduler/schedules?view=occurrences&date=2026-02-30", "/api/v1/scheduler/schedules?view=calendar&month=bad", "/api/v1/notifications?state=sent", "/api/v1/notifications?state=unacknowledged&cursor=garbage"} {
		requireStatus(t, request(router, "GET", path, "", ""), http.StatusBadRequest)
	}
	for _, query := range []string{
		"view=unknown", "view=", "view=calendar", "view=calendar&month=2026-10&date=2026-10-08",
		"view=occurrences&include=unknown", "view=occurrences&include=unacknowledged&cursor=",
		"view=occurrences&name=ignored", "view=occurrences&date=2026-10-08&date=2026-10-09",
	} {
		requireStatus(t, request(router, "GET", "/api/v1/scheduler/schedules?"+query, "", ""), http.StatusBadRequest)
	}
	// 规则列表保持原有数组协议；独立的页面路由不再注册。
	responseList := request(router, "GET", "/api/v1/scheduler/schedules?limit=1", "", "")
	requireStatus(t, responseList, http.StatusOK)
	if _, ok := decodeObject(t, responseList.Body.Bytes())["data"].([]any); !ok {
		t.Fatal("默认列表协议不应改变")
	}
	for _, path := range []string{"notebook", "occurrences", "calendar"} {
		requireStatus(t, request(router, "GET", "/api/v1/scheduler/"+path, "", ""), http.StatusNotFound)
	}
	requireStatus(t, request(router, "POST", fmt.Sprintf("/api/v1/notifications/%d/acknowledgement", due.ID), "", ""), http.StatusNotFound)
	batchPath := "/api/v1/notifications/acknowledgements"
	requireStatus(t, request(router, "POST", batchPath, fmt.Sprintf(`{"notificationIds":[%d,%d]}`, due.ID, future.ID), "application/json"), http.StatusConflict)
	stored, _ := notifications.GetNotificationByID(ctx, due.ID)
	if stored.AcknowledgedAt != nil {
		t.Fatal("非法批次不能部分写入")
	}
	requireStatus(t, request(router, "POST", batchPath, `{"notificationIds":[1],"all":true}`, "application/json"), http.StatusBadRequest)
	requireStatus(t, request(router, "POST", batchPath, `{"notificationIds":[]}`, "application/json"), http.StatusConflict)
	requireStatus(t, request(router, "POST", batchPath, `null`, "application/json"), http.StatusBadRequest)
	body := fmt.Sprintf(`{"notificationIds":[%d,%d,%d]}`, due.ID, failed.ID, due.ID)
	response := request(router, "POST", batchPath, body, "application/json")
	requireStatus(t, response, http.StatusOK)
	value := decodeObject(t, response.Body.Bytes())["data"].([]any)
	if len(value) != 2 {
		t.Fatal("重复 ID 应去重")
	}
	first := value[0].(map[string]any)["acknowledgedAt"]
	response = request(router, "POST", batchPath, fmt.Sprintf(`{"notificationIds":[%d]}`, due.ID), "application/json")
	requireStatus(t, response, http.StatusOK)
	if decodeObject(t, response.Body.Bytes())["data"].([]any)[0].(map[string]any)["acknowledgedAt"] != first {
		t.Fatal("首次确认时间不应改变")
	}
	stored, _ = notifications.GetNotificationByID(ctx, failed.ID)
	if stored.Status != model.NotifyFailed || stored.AcknowledgedAt == nil {
		t.Fatal("投递结果与完成事实必须独立")
	}
	if _, err = schedules.DeleteScheduleByID(ctx, schedule.ID); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, request(router, "GET", fmt.Sprintf("/api/v1/notifications/%d", due.ID), "", ""), http.StatusOK)
}
