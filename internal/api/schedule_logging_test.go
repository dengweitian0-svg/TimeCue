package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestScheduleMutationLogs 使用实际 SQLite 验证规则变更、隐私边界和幂等回执。
func TestScheduleMutationLogs(t *testing.T) {
	router, schedules := scheduleLoggingRouter(t)
	readLog := captureHTTPLogs(t)
	const base = "/api/v1/scheduler/schedules"
	request := func(method, path, body, key, message string, status int) (map[string]any, *httptest.ResponseRecorder) {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Host = "127.0.0.1:10086"
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		response := httptest.NewRecorder()
		before := len(readLog())
		started := time.Now()
		router.ServeHTTP(response, req)
		if response.Code != status {
			t.Fatalf("status=%d, want=%d: %s", response.Code, status, response.Body.String())
		}
		level := "INFO"
		if status >= 400 {
			level = "WARN"
		}
		record := requireHTTPRecord(t, readLog()[before:], method, path, level, message, status, time.Since(started))
		if strings.Contains(response.Body.String(), `"mutation"`) || strings.Contains(response.Body.String(), `"Before"`) {
			t.Fatalf("内部日志素材进入响应: %s", response.Body.String())
		}
		return record, response
	}
	body := func(revision int, cron string, open bool, text string) string {
		return fmt.Sprintf(`{"name":%q,"description":%q,"type":"period","period":{"cron":[%q],"open":%t},"expectedRevision":%d}`, text, text, cron, open, revision)
	}
	createBody := `{"name":"private-request","description":"private-description","type":"period","period":{"cron":["0 13 * * *"],"open":true}}`
	record, response := request("POST", base, createBody, "create-log", "schedule created", 201)
	var created struct {
		Data struct{ ID int }
	}
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("%s/%d", base, created.Data.ID)
	mutation := record["mutation"].(map[string]any)
	state := mutation["state"].(map[string]any)
	if mutation["outcome"] != "applied" || mutation["revision_before"] != nil || mutation["revision_after"] != float64(1) || state["period.open"] != true || !reflect.DeepEqual(state["period.cron"], []any{"0 13 * * *"}) || mutation["notification_id"] == nil || mutation["next_notify_at"] == nil {
		t.Fatalf("创建规则或提醒事实丢失: %#v", mutation)
	}
	record, _ = request("POST", base, createBody, "create-log", "schedule write replayed", 201)
	if replay := record["mutation"].(map[string]any); replay["outcome"] != "replayed" || replay["effective_at"] != mutation["effective_at"] || replay["state"] != nil || replay["changes"] != nil {
		t.Fatalf("创建重放被记录成新变更: %#v", replay)
	}
	updateBody := body(1, "0 17 * * *", true, "private-response")
	record, _ = request("PUT", path, updateBody, "update-log", "schedule updated", 200)
	updated := record["mutation"].(map[string]any)
	changes := updated["changes"].(map[string]any)
	cron := changes["period.cron"].(map[string]any)
	if !reflect.DeepEqual(updated["changed_fields"], []any{"name", "description", "period.cron"}) || updated["revision_before"] != float64(1) || updated["revision_after"] != float64(2) || !reflect.DeepEqual(cron["before"], []any{"0 13 * * *"}) || !reflect.DeepEqual(cron["after"], []any{"0 17 * * *"}) || len(changes) != 1 {
		t.Fatalf("差异或文本保护错误: %#v", updated)
	}
	record, _ = request("PUT", path, body(2, "0 17 * * *", true, "private-response"), "", "schedule updated", 200)
	unchanged := record["mutation"].(map[string]any)
	if unchanged["revision_after"] != float64(3) || !reflect.DeepEqual(unchanged["changed_fields"], []any{}) || unchanged["changes"] != nil {
		t.Fatalf("原样提交被误报为字段变化: %#v", unchanged)
	}
	for _, tc := range []struct {
		revision int
		open     bool
	}{{3, false}, {4, true}} {
		record, _ = request("PUT", path, body(tc.revision, "0 17 * * *", tc.open, "private-response"), "", "schedule updated", 200)
		change := record["mutation"].(map[string]any)
		open := change["changes"].(map[string]any)["period.open"].(map[string]any)
		if open["before"] != !tc.open || open["after"] != tc.open || (change["notification_id"] != nil) != tc.open || (change["next_notify_at"] != nil) != tc.open {
			t.Fatalf("启停事实错误: %#v", change)
		}
	}
	// 后续已经变更到版本 5，重放仍只能描述原版本 2 的回执。
	record, _ = request("PUT", path, updateBody, "update-log", "schedule write replayed", 200)
	replay := record["mutation"].(map[string]any)
	if replay["outcome"] != "replayed" || replay["revision_before"] != nil || replay["revision_after"] != float64(2) || replay["effective_at"] != updated["effective_at"] || replay["changes"] != nil {
		t.Fatalf("重放丢失原回执语义: %#v", replay)
	}
	for _, rejected := range []struct{ body, key string }{
		{updateBody, ""},
		{body(5, "0 18 * * *", true, "private-response"), "update-log"},
	} {
		record, _ = request("PUT", path, rejected.body, rejected.key, "HTTP request rejected", 409)
		if record["mutation"] != nil || record["schedule_id"] != nil {
			t.Fatalf("拒绝请求携带成功变更: %#v", record)
		}
	}
	record, _ = request("DELETE", path, "", "", "schedule deleted", 204)
	deleted := record["mutation"].(map[string]any)
	if deleted["revision_before"] != float64(5) || deleted["revision_after"] != float64(6) || !reflect.DeepEqual(deleted["changed_fields"], []any{"deleted"}) {
		t.Fatalf("删除事实错误: %#v", deleted)
	}
	removed := deleted["changes"].(map[string]any)["deleted"].(map[string]any)
	if removed["before"] != false || removed["after"] != true {
		t.Fatalf("删除差异错误: %#v", removed)
	}
	// 删除后的持久化状态与日志版本一致。
	if _, err := schedules.GetScheduleByID(context.Background(), created.Data.ID); err == nil {
		t.Fatal("删除日志与存储状态不一致")
	}
	// 原子创建暂停规则必须明确记录状态，并且没有首提醒。
	record, _ = request("POST", base, strings.Replace(createBody, `"open":true`, `"open":false`, 1), "", "schedule created", 201)
	paused := record["mutation"].(map[string]any)
	if paused["state"].(map[string]any)["period.open"] != false || paused["notification_id"] != nil || paused["next_notify_at"] != nil {
		t.Fatalf("暂停创建的规则或提醒错误: %#v", paused)
	}
}

// TestScheduleMutationOnResponseWriteFailure 验证响应失败不会掩盖实际提交。
func TestScheduleMutationOnResponseWriteFailure(t *testing.T) {
	router, schedules := scheduleLoggingRouter(t)
	readLog := captureHTTPLogs(t)
	const path = "/api/v1/scheduler/schedules"
	req := httptest.NewRequest("POST", path, strings.NewReader(`{"name":"private-request","type":"period","period":{"cron":["0 13 * * *"]}}`))
	req.Host = "127.0.0.1:10086"
	req.Header.Set("Content-Type", "application/json")
	writer := &shortHTTPWriter{ResponseRecorder: httptest.NewRecorder()}
	started := time.Now()
	router.ServeHTTP(writer, req)
	record := requireHTTPRecord(t, readLog(), "POST", path, "ERROR", "failed to create schedule", 201, time.Since(started))
	mutation := record["mutation"].(map[string]any)
	stored, err := schedules.GetScheduleByID(req.Context(), int(record["schedule_id"].(float64)))
	if err != nil || stored.Revision != int(mutation["revision_after"].(float64)) || mutation["outcome"] != "applied" || record["error"] == nil {
		t.Fatalf("提交或响应失败事实丢失: %#v, err=%v", record, err)
	}
}
