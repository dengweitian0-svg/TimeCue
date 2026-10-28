package chat_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/agent/completion"
	"github.com/dengweitian0-svg/TimeCue/internal/agent/tools"
	"github.com/dengweitian0-svg/TimeCue/internal/app"
	"github.com/dengweitian0-svg/TimeCue/internal/model"
	"github.com/dengweitian0-svg/TimeCue/internal/store"
	"github.com/google/uuid"
)

// TestAssistantDeletePersistsIdentityAndPairsActualResults 验证危险删除的真实结果、参数错误及后台身份。
func TestAssistantDeletePersistsIdentityAndPairsActualResults(t *testing.T) {
	ctx := context.Background()
	_, ports, application, _ := deleteFixture(t)
	calls := []chat.ToolRequest{
		toolRequest("deleted", "delete_schedule", `{"id":1,"expectedRevision":1}`),
		toolRequest("absent", "delete_schedule", `{"id":1,"expectedRevision":1}`),
		toolRequest("conflict", "delete_schedule", `{"id":2,"expectedRevision":2}`),
		toolRequest("invalid", "delete_schedule", `{"id":`),
		toolRequest("forged", "delete_schedule", `{"id":2,"expectedRevision":1,"key":"model-key"}`),
	}
	wantErrors := map[string]string{
		"absent":   "schedule_not_found",
		"conflict": "revision_conflict",
		"invalid":  "tool_argument_parse_error",
		"forged":   "tool_argument_validation_error",
	}
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if requests.Add(1) == 1 {
			emitTools(w, calls)
			return
		}
		results := requestToolResults(t, r)
		if len(results) != len(calls) {
			t.Errorf("原 ID 配对丢失: %v", results)
		}
		for id, code := range wantErrors {
			if !strings.Contains(string(results[id]), code) {
				t.Errorf("%s 未回填实际错误 %s: %s", id, code, results[id])
			}
		}
		if !strings.Contains(string(results["deleted"]), `"deleted":true`) || !strings.Contains(string(results["deleted"]), `"revision":2`) {
			t.Errorf("未回填实际删除: %s", results["deleted"])
		}
		emitText(w, "已按实际结果处理")
	}))
	t.Cleanup(upstream.Close)
	registry := deleteRegistry(t, application)
	gate := &toolCommitGate{
		ToolRepository: ports.Tools,
		afterResults:   len(calls),
		committed:      make(chan struct{}),
		release:        make(chan struct{}),
	}
	gated := ports
	gated.Tools = gate
	service := chat.NewService(gated, completion.NewClient(nil), toolSettings(upstream.URL), registry)
	if err := service.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Stop)
	t.Cleanup(func() { gate.once.Do(func() { close(gate.release) }) })
	receipt, err := service.CreateRun(ctx, admissionInput("delete", "delete"))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-gate.committed:
	case <-time.After(5 * time.Second):
		t.Fatal("未到达真实工具结果提交边界")
	}
	call, err := ports.Execution.ReadCall(ctx, receipt.RunID)
	if err != nil || call == nil {
		t.Fatalf("未保存模型调用: %+v %v", call, err)
	}
	stored, err := ports.Tools.ReadTools(ctx, call.ID)
	if err != nil || len(stored) != len(calls) {
		t.Fatalf("未保存全部工具: %+v %v", stored, err)
	}
	keys := map[string]bool{}
	for index, tool := range stored {
		assertDeleteKey(t, tool)
		if tool.Request != calls[index] || keys[tool.IdempotencyKey] || tool.RetryCount != 0 {
			t.Fatalf("身份/参数被改写、复用或业务错误被重试: %+v", tool)
		}
		keys[tool.IdempotencyKey] = true
	}
	if business, err := application.ReadScheduleDeleteReceipt(ctx, 1, 1, stored[0].IdempotencyKey); err != nil || business == nil || !business.Schedule.Deleted {
		t.Fatalf("成功删除没有同身份业务回执: %+v %v", business, err)
	}
	gate.once.Do(func() { close(gate.release) })
	run := awaitRun(t, service, receipt.RunID)
	if run.Status != chat.StatusSucceeded || requests.Load() != 2 {
		t.Fatalf("删除失败直接中断模型: %+v requests=%d", run, requests.Load())
	}
	page, err := service.GetRunEvents(ctx, run.RunID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	results := map[string]chat.ToolResultData{}
	for _, event := range page.Events {
		if event.Type == chat.EventError {
			t.Fatalf("工具错误生成 run error: %s", event.Data)
		}
		if event.Type == chat.EventToolResult {
			var data chat.ToolResultData
			if err := json.Unmarshal(event.Data, &data); err != nil {
				t.Fatal(err)
			}
			results[data.ToolCallID] = data
		}
	}
	if len(results) != len(calls) {
		t.Fatalf("事件未逐项配对: %+v", results)
	}
	for id, code := range wantErrors {
		if results[id].Error == nil || results[id].Error.Code != code {
			t.Fatalf("事件错误未保留: %+v", results[id])
		}
	}
	if _, err := application.GetSchedule(ctx, 1); !errors.Is(err, app.ErrScheduleNotFound) {
		t.Fatalf("真实日程未删除: %v", err)
	}
	if value, err := application.GetSchedule(ctx, 2); err != nil || value.Revision != 1 {
		t.Fatalf("失败调用修改了目标: %+v %v", value, err)
	}
}

func deleteFixture(t *testing.T) (*store.Sqlite, chat.Storage, *app.App, string) {
	t.Helper()
	db, ports, path := admissionDatabase(t)
	schedules, err := store.NewScheduleStore(db)
	if err != nil {
		t.Fatal(err)
	}
	application := &app.App{Schedule: schedules}
	for _, name := range []string{"删除目标", "保留目标"} {
		if _, _, err := application.CreateSchedule(context.Background(), model.Schedule{
			Name:   name,
			Type:   model.SingleSchedule,
			Single: &model.Single{NotifyTime: time.Now().Add(48 * time.Hour).Truncate(time.Minute)},
		}); err != nil {
			t.Fatal(err)
		}
	}
	return db, ports, application, path
}

func deleteRegistry(t *testing.T, application *app.App) *tools.Registry {
	t.Helper()
	definitions := append(tools.ScheduleTools(application), tools.ScheduleWriteTools(application, nil)...)
	definitions = append(definitions, tools.ScheduleDeleteTools(application)...)
	registry, err := tools.NewRegistry(context.Background(), definitions...)
	if err != nil {
		t.Fatal(err)
	}
	for _, definition := range registry.Definitions() {
		info, _ := registry.Lookup(definition.Function.Name)
		if info.Dangerous != (info.Name == "delete_schedule") || info.Name == "delete_schedule" && info.Parallel {
			t.Fatalf("危险或串行元数据错误: %+v", info)
		}
	}
	return registry
}

func assertDeleteKey(t *testing.T, tool chat.ToolCall) {
	t.Helper()
	prefix := "tool:" + tool.Request.ID + ":"
	if !strings.HasPrefix(tool.IdempotencyKey, prefix) {
		t.Fatalf("身份键未保留原 ID: %+v", tool)
	}
	if _, err := uuid.Parse(strings.TrimPrefix(tool.IdempotencyKey, prefix)); err != nil {
		t.Fatalf("身份键不是后台 UUID: %+v", tool)
	}
}

type deleteResultGate struct {
	chat.ToolRepository
	ready   chan struct{}
	release chan struct{}
	once    sync.Once
}

// FinishTool 在真实业务返回后、AI 结果保存之前暂停，模拟两个短事务之间的中断。
func (g *deleteResultGate) FinishTool(ctx context.Context, call chat.ModelCall, tool chat.ToolCall) (chat.ModelCall, error) {
	close(g.ready)
	<-g.release
	return g.ToolRepository.FinishTool(ctx, call, tool)
}

// TestDeleteCommittedBeforeAIResultRetainsOriginalIdentity 验证业务提交窗口中的原身份、只读核对与回执复用。
func TestDeleteCommittedBeforeAIResultRetainsOriginalIdentity(t *testing.T) {
	ctx := context.Background()
	_, ports, application, path := deleteFixture(t)
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if requests.Add(1) == 1 {
			emitTools(w, []chat.ToolRequest{toolRequest("original", "delete_schedule", ` {"id":1,"expectedRevision":1} `)})
			return
		}
		results := requestToolResults(t, r)
		if !strings.Contains(string(results["original"]), `"deleted":true`) {
			t.Errorf("真实结果未按原 ID 回填: %v", results)
		}
		emitText(w, "已删除")
	}))
	t.Cleanup(upstream.Close)
	gate := &deleteResultGate{
		ToolRepository: ports.Tools,
		ready:          make(chan struct{}),
		release:        make(chan struct{}),
	}
	gated := ports
	gated.Tools = gate
	service := chat.NewService(gated, completion.NewClient(nil), toolSettings(upstream.URL), deleteRegistry(t, application))
	if err := service.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Stop)
	t.Cleanup(func() { gate.once.Do(func() { close(gate.release) }) })
	receipt, err := service.CreateRun(ctx, admissionInput("crash-delete", "crash-delete"))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-gate.ready:
	case <-time.After(5 * time.Second):
		t.Fatal("未到达业务已提交、AI 结果未保存的窗口")
	}
	call, err := ports.Execution.ReadCall(ctx, receipt.RunID)
	if err != nil || call == nil {
		t.Fatalf("调用不存在: %+v %v", call, err)
	}
	before, err := ports.Tools.ReadTools(ctx, call.ID)
	if err != nil || len(before) != 1 || before[0].State != "running" || len(before[0].Output) != 0 {
		t.Fatalf("中断窗口事实不符: %+v %v", before, err)
	}
	assertDeleteKey(t, before[0])
	if result, err := application.ReadScheduleDeleteReceipt(ctx, 1, 1, before[0].IdempotencyKey); err != nil || result == nil || result.Schedule.Revision != 2 {
		t.Fatalf("业务回执未先提交: %+v %v", result, err)
	}
	backup := filepath.Join(t.TempDir(), "delete-crash.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = raw.ExecContext(ctx, `VACUUM INTO '`+strings.ReplaceAll(backup, "'", "''")+`'`)
	raw.Close()
	if err != nil {
		t.Fatal(err)
	}
	gate.once.Do(func() { close(gate.release) })
	if run := awaitRun(t, service, receipt.RunID); run.Status != chat.StatusSucceeded {
		t.Fatalf("原运行失败: %+v", run)
	}
	service.Stop()
	restoredDB := store.NewSqlite()
	if err := restoredDB.Connect(ctx, backup); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restoredDB.Close() })
	restoredPorts := store.NewChatStorage(restoredDB)
	schedules, err := store.NewScheduleStore(restoredDB)
	if err != nil {
		t.Fatal(err)
	}
	restoredApp := &app.App{Schedule: schedules}
	stored, err := restoredPorts.Tools.ReadTools(ctx, call.ID)
	if err != nil || len(stored) != 1 {
		t.Fatalf("身份记录丢失: %+v %v", stored, err)
	}
	if stored[0].ID != before[0].ID || stored[0].IdempotencyKey != before[0].IdempotencyKey || stored[0].Request != before[0].Request {
		t.Fatalf("重开数据库改变原身份: %+v -> %+v", before[0], stored[0])
	}
	// 先只读核对，之后由现有工具循环携带同一键再次进入正式端口。
	if result, err := restoredApp.ReadScheduleDeleteReceipt(ctx, 1, 1, stored[0].IdempotencyKey); err != nil || result == nil || result.Schedule.Revision != 2 {
		t.Fatalf("重启只读核对失败: %+v %v", result, err)
	}
	restarted := chat.NewService(restoredPorts, completion.NewClient(nil), toolSettings(upstream.URL), deleteRegistry(t, restoredApp))
	if err := restarted.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.Stop)
	if run := awaitRun(t, restarted, receipt.RunID); run.Status != chat.StatusSucceeded || requests.Load() != 3 {
		t.Fatalf("恢复未复用已接纳模型回复: %+v requests=%d", run, requests.Load())
	}
	after, err := restoredPorts.Tools.ReadTools(ctx, call.ID)
	if err != nil || len(after) != 1 || after[0].ID != before[0].ID || after[0].IdempotencyKey != before[0].IdempotencyKey || after[0].Request != before[0].Request || after[0].State != "succeeded" || !strings.Contains(string(after[0].Output), `"outcome":"replayed"`) {
		t.Fatalf("恢复未沿原身份复用回执: %+v %v", after, err)
	}
	if business, err := restoredApp.ReadScheduleDeleteReceipt(ctx, 1, 1, after[0].IdempotencyKey); err != nil || business == nil || business.Schedule.Revision != 2 {
		t.Fatalf("重复删除改变业务版本: %+v %v", business, err)
	}
}
