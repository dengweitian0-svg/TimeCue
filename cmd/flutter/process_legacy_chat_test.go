package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/agent/loop"
	"github.com/dengweitian0-svg/TimeCue/internal/store"
)

// managedLegacyChatRecovery 通过生产入口加载重构前 a92241e 生成的版本 3 数据库。
func managedLegacyChatRecovery(t *testing.T, binary, root string) {
	t.Helper()
	const (
		ordinaryID = "03d79dff-4402-40b2-90fa-e19c17a5c4ef"
		compactID  = "1e09aac8-f833-418d-9377-0d50de863d6c"
		pendingID  = "a4312cd6-e2ed-48cd-acb6-e36195aa551c"
		toolID     = "05f7a93b-9a5d-41e6-8057-e24d1cbffa75"
	)
	ctx := context.Background()
	directory := processFixture(t, root, "unmigrated")
	path := filepath.Join(directory, "fixture.db")
	compressed, err := os.ReadFile("testdata/agent-v3.sqlite.gz")
	if err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	fixture, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, fixture, 0o600); err != nil {
		t.Fatal(err)
	}

	db := store.NewSqlite()
	if err := db.Connect(ctx, path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	storage := store.NewChatStorage(db)
	saved, err := storage.Execution.ReadCall(ctx, pendingID)
	if err != nil || saved == nil || saved.State != chat.StateRetryWait || saved.CheckpointStage != "model_call_start" ||
		saved.RetryCount != 1 || saved.MaxRetry != 1 || len(saved.RetryHistory) != 1 || saved.NextRetryAt == nil ||
		saved.RetryHistory[0].Result != "retry_wait" || len(saved.CheckpointContext) == 0 {
		t.Fatalf("旧库缺少待恢复的调用事实: %+v, %v", saved, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		body, err := io.ReadAll(r.Body)
		if err != nil || string(body) != saved.InputBody {
			t.Errorf("重放改变了旧 SQLite 请求原文: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		writeProcessDelta(w, "旧库重试完成", "stop")
	}))
	defer upstream.Close()
	config := "db:\n  db_path: ./fixture.db\nlog:\n  log_path: ./logs\nchat:\n  max_retry: 0\nchat_model:\n  - provider: fixture\n    base_url: " + upstream.URL + "\n    models:\n      - model: model\n        context_window: 65536\n        max_output_tokens: 2048\n"
	if err := os.WriteFile(filepath.Join(directory, "config.yaml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, ".env"), []byte("FIXTURE_MODEL=fixture-only\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	p := launchProcess(t, binary, "--env", "test", "--data-dir", directory, "--instance-id", "legacy-v3")
	p.ready(t)
	for _, tc := range []struct{ id, output string }{
		{
			id:     ordinaryID,
			output: "甲乙",
		},
		{
			id:     compactID,
			output: "旧库普通答复",
		},
		{
			id:     pendingID,
			output: "旧库重试完成",
		},
	} {
		run := awaitProcessChatRun(t, tc.id)
		if run.Status != chat.StatusSucceeded || run.Output == nil || *run.Output != tc.output {
			t.Fatalf("旧运行行为改变: %+v", run)
		}
		var page chat.EventPage
		processChatHTTP(t, http.MethodGet, "/v1/run/"+tc.id+"/events?limit=100", "", http.StatusOK, &page)
		if len(page.Events) < 3 || page.Events[0].Type != chat.EventStart || page.Events[len(page.Events)-1].Type != chat.EventEnd {
			t.Fatalf("旧运行事件边界改变: %+v", page)
		}
	}
	toolRun := awaitProcessChatRun(t, toolID)
	if toolRun.Status != chat.StatusFailed || toolRun.Error == nil || toolRun.Error.Code != "model_round_limit_exceeded" {
		t.Fatalf("旧工具运行终态改变: %+v", toolRun)
	}
	stopProcessChat(t, p)
	if requests.Load() != 1 {
		t.Fatalf("恢复请求数: %d", requests.Load())
	}

	if err := db.Connect(ctx, path); err != nil {
		t.Fatal(err)
	}
	storage = store.NewChatStorage(db)
	finished, err := storage.Execution.ReadCall(ctx, pendingID)
	if err != nil || finished == nil || finished.ID != saved.ID || finished.State != chat.StateSucceeded ||
		finished.InputBody != saved.InputBody || finished.RetryCount != saved.RetryCount || finished.MaxRetry != saved.MaxRetry ||
		finished.CheckpointStage != "model_call_end" || finished.NextRetryAt != nil || len(finished.RetryHistory) != 1 ||
		finished.RetryHistory[0].Result != "succeeded" || !finished.RetryHistory[0].At.Equal(saved.RetryHistory[0].At) {
		t.Fatalf("重启没有沿原调用与重试历史结算: %+v, %v", finished, err)
	}
	toolCall, err := storage.Execution.ReadCall(ctx, toolID)
	if err != nil || toolCall == nil || toolCall.State != chat.StateSucceeded ||
		toolCall.CheckpointStage != "tool_end" || !toolCall.ToolCompleted("legacy-list") ||
		toolCall.LastTool != "tool_result: list_schedules-legacy-list" {
		t.Fatalf("旧工具配对 checkpoint 不可读: %+v, %v", toolCall, err)
	}
	tools, err := storage.Tools.ReadTools(ctx, toolCall.ID)
	if err != nil || len(tools) != 1 || tools[0].State != "succeeded" ||
		tools[0].Request.ID != "legacy-list" || string(tools[0].Output) != "[]" {
		t.Fatalf("旧工具结果或状态改变: %+v, %v", tools, err)
	}
	verifyLegacyUsage(t, loop.NewWithPorts(storage, nil, nil, nil), ordinaryID, compactID)
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var version, marked int
	if err := raw.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 3 {
		t.Fatalf("旧库版本改变: %d, %v", version, err)
	}
	if err := raw.QueryRow(`SELECT COUNT(*) FROM ai_sessions s JOIN ai_events e ON e.id=s.compact_event_id
		WHERE e.run_id=? AND e.type='compact' AND e.compact_succeeded=1`, compactID).Scan(&marked); err != nil || marked != 1 {
		t.Fatalf("有效摘要的旧标记或会话引用丢失: %d, %v", marked, err)
	}
}

func verifyLegacyUsage(t *testing.T, service *loop.Run, ordinaryID, compactID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	t.Cleanup(service.Stop)
	ordinary, err := service.GetRunUsage(ctx, ordinaryID)
	if err != nil || len(ordinary.Calls) != 2 || ordinary.Tokens.TotalTokens == nil || *ordinary.Tokens.TotalTokens != 3 ||
		string(ordinary.Calls[0].Usage) != `{"prompt_tokens": 2, "completion_tokens": 1, "total_tokens": 3, "provider_detail": {"kind": "ordinary"}}` ||
		string(ordinary.Calls[1].Usage) != `{"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0, "provider_detail": {"kind": "continuation"}}` {
		t.Fatalf("旧普通或续写用量被改写: %+v, %v", ordinary, err)
	}
	summary, err := service.GetRunUsage(ctx, compactID)
	if err != nil || len(summary.Calls) != 2 || summary.Tokens.TotalTokens == nil || *summary.Tokens.TotalTokens != 2 ||
		summary.Tokens.CompletionTokens != nil ||
		string(summary.Calls[0].Usage) != `{"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0, "provider_detail": {"kind": "summary"}}` {
		t.Fatalf("旧摘要用量或未知字段被改写: %+v, %v", summary, err)
	}
}
