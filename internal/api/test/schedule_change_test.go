package api_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestScheduleChangeHTTP 验证原子暂停创建、分钟精度、版本冲突和操作回放。
func TestScheduleChangeHTTP(t *testing.T) {
	router, _ := testRouter(t)
	call := func(method, path, body, key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Host = "127.0.0.1:10086"
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		return out
	}
	body := `{"name":"暂停创建","type":"period","period":{"open":false,"cron":["30 9 * * 1,3,5","0 18 * * 1,3,5"]}}`
	first := call(http.MethodPost, "/api/v1/scheduler/schedules", body, "create")
	requireStatus(t, first, 201)
	replay := call(http.MethodPost, "/api/v1/scheduler/schedules", body, "create")
	requireStatus(t, replay, 201)
	if replay.Body.String() != first.Body.String() {
		t.Fatal("创建重放未返回原结果")
	}
	value := decodeObject(t, first.Body.Bytes())["data"].(map[string]any)
	if value["period"].(map[string]any)["open"] != false || value["revision"] != float64(1) {
		t.Fatal("暂停创建失败")
	}
	id := int(value["id"].(float64))
	path := fmt.Sprintf("/api/v1/scheduler/schedules/%d", id)
	edit := `{"name":"恢复","type":"period","expectedRevision":1,"period":{"open":true,"cron":["0 17 * * *"]}}`
	changed := call(http.MethodPut, path, edit, "update")
	requireStatus(t, changed, 200)
	requireStatus(t, call(http.MethodPut, path, edit, "stale"), 409)
	requireStatus(t, call(http.MethodPut, path, edit, "update"), 200)
	requireStatus(t, call(http.MethodPut, path, strings.Replace(edit, "恢复", "不同内容", 1), "update"), 409)
	requireStatus(t, call(http.MethodPost, "/api/v1/scheduler/schedules", `{"name":"秒","type":"single","single":{"notifyTime":"2099-01-01T09:30:01+08:00"}}`, "seconds"), 400)
	requireStatus(t, call(http.MethodPost, "/api/v1/scheduler/schedules", `{"name":"亚秒","type":"single","single":{"notifyTime":"2099-01-01T09:30:00.001+08:00"}}`, "fraction"), 400)
	requireStatus(t, call(http.MethodPost, "/api/v1/scheduler/schedules", `{"name":"范围外","type":"single","single":{"notifyTime":"9999-01-01T09:30:00+08:00"}}`, "overflow-year"), 400)
	requireStatus(t, call(http.MethodPost, "/api/v1/scheduler/schedules", `{"name":"纳秒上界外","type":"single","single":{"notifyTime":"2262-04-12T07:48:00+08:00"}}`, "overflow-upper-bound"), 400)
	requireStatus(t, call(http.MethodPut, path, `{"name":"改类型","type":"single","expectedRevision":2,"single":{"notifyTime":"2099-01-01T09:30:00+08:00"}}`, "type"), 400)
	requireStatus(t, call(http.MethodDelete, path, "", ""), 204)
	requireStatus(t, call(http.MethodPut, path, strings.Replace(edit, `"expectedRevision":1`, `"expectedRevision":3`, 1), "deleted"), 404)
	remaining := request(router, http.MethodGet, "/api/v1/scheduler/schedules?limit=50", "", "")
	requireStatus(t, remaining, 200)
	if rows := decodeObject(t, remaining.Body.Bytes())["data"].([]any); len(rows) != 0 {
		t.Fatalf("非法单次不应写入日程: %#v", rows)
	}
}

// TestDateOpenScheduleCountHTTP 保证日期响应携带按日程去重的启用总数。
func TestDateOpenScheduleCountHTTP(t *testing.T) {
	router, _ := testRouter(t)
	base := "/api/v1/scheduler/schedules"
	check := func(want float64) {
		t.Helper()
		out := request(router, http.MethodGet, base+"?view=occurrences&date=2099-01-01", "", "")
		requireStatus(t, out, 200)
		result := decodeObject(t, out.Body.Bytes())["data"].(map[string]any)
		if result["openScheduleCount"] != want {
			t.Fatalf("启用日程总数=%v，期望=%v", result["openScheduleCount"], want)
		}
	}
	check(0)
	active := request(router, http.MethodPost, base, `{"name":"同日两次","type":"period","period":{"cron":["0 9 * * *","0 17 * * *"]}}`, "application/json")
	requireStatus(t, active, 201)
	check(1)
	paused := request(router, http.MethodPost, base, `{"name":"暂停","type":"period","period":{"open":false,"cron":["0 9 * * *"]}}`, "application/json")
	requireStatus(t, paused, 201)
	check(1)
	single := request(router, http.MethodPost, base, `{"name":"单次","type":"single","single":{"notifyTime":"2099-01-01T11:00:00+08:00"}}`, "application/json")
	requireStatus(t, single, 201)
	check(2)
}
