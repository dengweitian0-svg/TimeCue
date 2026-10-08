package api

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
	requireStatus(t, call(http.MethodPut, path, `{"name":"改类型","type":"single","expectedRevision":2,"single":{"notifyTime":"2099-01-01T09:30:00+08:00"}}`, "type"), 400)
	requireStatus(t, call(http.MethodDelete, path, "", ""), 204)
	requireStatus(t, call(http.MethodPut, path, strings.Replace(edit, `"expectedRevision":1`, `"expectedRevision":3`, 1), "deleted"), 404)
}
