package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/agent/completion"
	"github.com/dengweitian0-svg/TimeCue/internal/agent/tools"
	"github.com/dengweitian0-svg/TimeCue/internal/app"
	"github.com/dengweitian0-svg/TimeCue/internal/model"
	"github.com/dengweitian0-svg/TimeCue/internal/store"
)

// TestScheduleToolFeedsRealResultToModel 验证真实适配器和日程入口向模型回填当前规则及版本。
func TestScheduleToolFeedsRealResultToModel(t *testing.T) {
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request chat.ModelRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if requests.Add(1) == 1 {
			if len(request.Tools) != 3 {
				t.Errorf("query definitions: %s", mustJSON(request.Tools))
			}
			fmt.Fprint(w, "data: {\"id\":\"query\",\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"original-id\",\"type\":\"function\",\"function\":{\"name\":\"get_schedule\",\"arguments\":\"{\\\"id\\\":\"}}]}}]}\n\n")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"1}\"}}]},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"total_tokens\":12}}\n\ndata: [DONE]\n\n")
			return
		}
		messages := request.Messages
		if len(messages) < 3 || messages[len(messages)-1].Role != "tool" || messages[len(messages)-1].ToolCallID != "original-id" {
			t.Errorf("paired messages: %s", mustJSON(messages))
			return
		}
		var result map[string]any
		if err := json.Unmarshal([]byte(messages[len(messages)-1].Content), &result); err != nil || result["name"] != "真实会议" || result["revision"] != float64(1) {
			t.Errorf("real result: %v %v", result, err)
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"已查到真实会议\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer upstream.Close()
	service, _, _ := newToolFixture(t, upstream.URL)
	receipt, err := service.CreateRun(context.Background(), chat.CreateRunInput{
		SessionID:      "tools",
		Input:          "查询日程 1",
		Provider:       "test",
		Model:          "model",
		IdempotencyKey: "query",
	})
	if err != nil {
		t.Fatal(err)
	}
	run := awaitRun(t, service, receipt.RunID)
	if run.Status != chat.StatusSucceeded || run.Output == nil || *run.Output != "已查到真实会议" {
		t.Fatalf("run: %+v", run)
	}
	page, err := service.GetRunEvents(context.Background(), run.RunID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 5 || page.Events[1].Type != "tool_start" || page.Events[2].Type != "tool_result" {
		t.Fatalf("events: %s", mustJSON(page))
	}
	var result struct {
		ID     string         `json:"tool_call_id"`
		Output map[string]any `json:"output"`
		Error  *chat.RunError `json:"error"`
	}
	if err := json.Unmarshal(page.Events[2].Data, &result); err != nil || result.ID != "original-id" || result.Output["name"] != "真实会议" || result.Error != nil {
		t.Fatalf("tool result: %+v %v", result, err)
	}
}

// TestToolFailuresRemainPairedAndModelCanContinue 验证同批参数拒绝、业务错误、列表和日期结果均按原 ID 回填。
func TestToolFailuresRemainPairedAndModelCanContinue(t *testing.T) {
	requests := []chat.ToolRequest{
		toolRequest("syntax", "get_schedule", `{"id":`),
		toolRequest("array", "get_schedule", `[]`),
		toolRequest("null", "get_schedule", `null`),
		toolRequest("missing", "get_schedule", `{}`),
		toolRequest("type", "get_schedule", `{"id":"1"}`),
		toolRequest("extra", "get_schedule", `{"id":1,"context":"fake"}`),
		toolRequest("not-found", "get_schedule", `{"id":9999}`),
		toolRequest("list", "list_schedules", `{"name":"真实","limit":1,"offset":0}`),
		toolRequest("date", "query_occurrences", `{"date":"后天"}`),
	}
	expected := map[string]string{
		"syntax": "tool_argument_parse_error", "array": "tool_argument_validation_error", "null": "tool_argument_validation_error",
		"missing": "tool_argument_validation_error", "type": "tool_argument_validation_error", "extra": "tool_argument_validation_error", "not-found": "schedule_not_found",
	}
	var count atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if count.Add(1) == 1 {
			emitTools(w, requests)
			return
		}
		var input chat.ModelRequest
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			return
		}
		paired := map[string]bool{}
		for _, message := range input.Messages {
			if message.Role != "tool" {
				continue
			}
			paired[message.ToolCallID] = true
			if code := expected[message.ToolCallID]; code != "" {
				var failure chat.RunError
				if err := json.Unmarshal([]byte(message.Content), &failure); err != nil || failure.Code != code {
					t.Errorf("failure message: %+v %v", failure, err)
				}
			}
			if message.ToolCallID == "list" {
				var values []map[string]any
				if err := json.Unmarshal([]byte(message.Content), &values); err != nil || len(values) != 1 || values[0]["name"] != "真实会议" {
					t.Errorf("real list: %v %v", values, err)
				}
			}
			if message.ToolCallID == "date" {
				var page model.OccurrencePage
				if err := json.Unmarshal([]byte(message.Content), &page); err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].Name != "真实会议" {
					t.Errorf("real occurrences: %+v %v", page, err)
				}
			}
		}
		if len(paired) != len(requests) {
			t.Errorf("missing tool messages: %v", paired)
		}
		emitText(w, "已检查真实结果")
	}))
	defer upstream.Close()
	service, storage, _ := newToolFixture(t, upstream.URL)
	run := startToolRun(t, service, "batch")
	if run.Status != chat.StatusSucceeded || count.Load() != 2 {
		t.Fatalf("run: %+v requests=%d", run, count.Load())
	}
	page, err := service.GetRunEvents(context.Background(), run.RunID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	starts, results := map[string]int64{}, map[string]bool{}
	for _, event := range page.Events {
		if event.Type == chat.EventError {
			t.Fatalf("tool error duplicated as run error: %s", event.Data)
		}
		if event.Type == chat.EventToolStart {
			var data struct {
				Function []struct {
					ID       string `json:"id"`
					Argument any    `json:"argument"`
				} `json:"function"`
			}
			if err := json.Unmarshal(event.Data, &data); err != nil {
				t.Fatal(err)
			}
			item := data.Function[0]
			starts[item.ID] = event.ID
			if (item.ID == "syntax" || item.ID == "array" || item.ID == "null") && item.Argument != nil {
				t.Errorf("invalid argument projection: %v", item)
			}
			if item.ID == "extra" && item.Argument == nil {
				t.Error("schema failure lost parsed object")
			}
		}
		if event.Type == chat.EventToolResult {
			var data struct {
				ID     string         `json:"tool_call_id"`
				Output any            `json:"output"`
				Error  *chat.RunError `json:"error"`
			}
			if err := json.Unmarshal(event.Data, &data); err != nil {
				t.Fatal(err)
			}
			if results[data.ID] || starts[data.ID] == 0 || starts[data.ID] >= event.ID {
				t.Fatalf("invalid pairing: %+v", data)
			}
			results[data.ID] = true
			if code := expected[data.ID]; code != "" && (data.Error == nil || data.Error.Code != code) {
				t.Errorf("error: %+v", data)
			}
			if data.ID == "list" {
				if _, ok := data.Output.([]any); !ok {
					t.Errorf("array converted: %T", data.Output)
				}
			}
		}
	}
	if len(results) != len(requests) {
		t.Fatalf("results: %v", results)
	}
	call, err := storage.Execution.ReadCall(context.Background(), run.RunID)
	if err != nil || call == nil {
		t.Fatalf("call: %v %v", call, err)
	}
	if len(call.Input.Messages) != 11 {
		t.Fatalf("next input lost original calls or results: %s", mustJSON(call.Input.Messages))
	}
}

// TestDuplicateToolIDsRejectEntireReply 验证同一回复内重复 ID 不会执行任何工具或污染下一轮上下文。
func TestDuplicateToolIDsRejectEntireReply(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		emitTools(w, []chat.ToolRequest{toolRequest("same", "get_schedule", `{"id":1}`), toolRequest("same", "list_schedules", `{}`)})
	}))
	defer upstream.Close()
	service, storage, _ := newToolFixture(t, upstream.URL)
	run := startToolRun(t, service, "duplicates")
	if run.Status != chat.StatusFailed || run.Error == nil || run.Error.Code != "model_invalid_format" {
		t.Fatalf("run: %+v", run)
	}
	page, _ := service.GetRunEvents(context.Background(), run.RunID, 0, 100)
	for _, event := range page.Events {
		if event.Type == chat.EventToolStart || event.Type == chat.EventToolResult {
			t.Fatalf("duplicate reply executed tools: %s", mustJSON(page))
		}
	}
	history, err := storage.Execution.History(context.Background(), run.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range history {
		if len(message.ToolCalls) > 0 || message.Role == "tool" {
			t.Fatalf("unpairable history: %s", mustJSON(history))
		}
	}
}

// TestRecoveredAcceptedToolReplyDoesNotCallModelAgain 验证已接纳回复与已保存结果跨重启复用，尚未开始的查询继续执行。
func TestRecoveredAcceptedToolReplyDoesNotCallModelAgain(t *testing.T) {
	var count atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		var input chat.ModelRequest
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			return
		}
		if len(input.Messages) != 4 || input.Messages[2].Content != `{"name":"已保存结果"}` || input.Messages[3].ToolCallID != "remaining" {
			t.Errorf("restored input: %s", mustJSON(input))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		emitText(w, "恢复查询完成")
	}))
	defer upstream.Close()
	service, storage, application := newToolFixture(t, upstream.URL)
	service.Stop()
	ctx := context.Background()
	record := chat.Record{
		Run: chat.Run{
			RunID:     "recovered-tools",
			SessionID: "recovery",
			Input:     "查询",
			Provider:  "test",
			Model:     "model",
			CreatedAt: time.Now(),
		},
		State:          chat.StateRunning,
		IdempotencyKey: "recover",
	}
	if err := storage.Runs.Create(ctx, record); err != nil {
		t.Fatal(err)
	}
	requests := []chat.ToolRequest{toolRequest("saved", "get_schedule", `{"id":1}`), toolRequest("remaining", "get_schedule", `{"id":1}`)}
	encoded, _ := json.Marshal(requests)
	call := chat.ModelCall{
		RunID:     record.RunID,
		SessionID: record.SessionID,
		Input: chat.ModelRequest{
			Model: "model",
			Messages: []chat.Message{
				{
					Role:    "user",
					Content: "查询",
				},
			},
			Stream:    true,
			MaxTokens: 8192,
			Tools:     queryRegistry(t, application).Definitions(),
		},
		State: chat.StateRunning,
	}
	var err error
	call.ID, err = storage.Execution.BeginCall(ctx, call)
	if err != nil {
		t.Fatal(err)
	}
	call.State = chat.StateSucceeded
	call.Output = &chat.ModelOutput{
		Message: chat.Message{
			Role:      "assistant",
			ToolCalls: encoded,
		},
		FinishReason: "tool_calls",
	}
	if err := storage.Tools.AcceptResponse(ctx, call); err != nil {
		t.Fatal(err)
	}
	call.CheckpointStage = "tool_start"
	call.CheckpointContext = append(call.Input.Messages, call.Output.Message)
	call.CompletedTools = []string{}
	call.LastTool = "tool_use: get_schedule-saved"
	tool := chat.ToolCall{
		ModelCallID: call.ID,
		Request:     requests[0],
		State:       "running",
		MaxRetry:    3,
	}
	tool.ID, err = storage.Tools.BeginTool(ctx, call, tool)
	if err != nil {
		t.Fatal(err)
	}
	tool.State, tool.Output = "succeeded", json.RawMessage(`{"name":"已保存结果"}`)
	call.CompletedTools = []string{"saved"}
	call.LastTool = "tool_result: get_schedule-saved"
	call.CheckpointContext = append(call.CheckpointContext, chat.Message{
		Role:       "tool",
		ToolCallID: "saved",
		Content:    string(tool.Output),
	})
	if err := storage.Tools.FinishTool(ctx, call, tool); err != nil {
		t.Fatal(err)
	}
	restarted := chat.NewService(storage, completion.NewClient(nil), toolSettings(upstream.URL), queryRegistry(t, application))
	if err := restarted.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer restarted.Stop()
	run := awaitRun(t, restarted, record.RunID)
	if run.Status != chat.StatusSucceeded || count.Load() != 1 {
		t.Fatalf("run: %+v requests=%d", run, count.Load())
	}
	page, _ := restarted.GetRunEvents(ctx, run.RunID, 0, 100)
	starts, results := 0, 0
	for _, event := range page.Events {
		if event.Type == chat.EventToolStart {
			starts++
		}
		if event.Type == chat.EventToolResult {
			results++
		}
	}
	if starts != 2 || results != 2 {
		t.Fatalf("duplicate events after restart: %s", mustJSON(page))
	}
}

func toolRequest(id, name, arguments string) chat.ToolRequest {
	return chat.ToolRequest{
		ID:   id,
		Type: "function",
		Function: chat.ToolFunction{
			Name:      name,
			Arguments: arguments,
		},
	}
}

func emitTools(w http.ResponseWriter, calls []chat.ToolRequest) {
	emitToolFinish(w, calls, "tool_calls")
}

func emitToolFinish(w http.ResponseWriter, calls []chat.ToolRequest, reason string) {
	deltas := make([]map[string]any, 0, len(calls))
	for index, call := range calls {
		deltas = append(deltas, map[string]any{"index": index, "id": call.ID, "type": call.Type, "function": call.Function})
	}
	chunk := map[string]any{
		"choices": []any{
			map[string]any{
				"delta":         map[string]any{"tool_calls": deltas},
				"finish_reason": reason,
			},
		},
	}
	fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", mustJSON(chunk))
}

func emitText(w http.ResponseWriter, text string) {
	fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", mustJSON(map[string]any{
		"choices": []any{
			map[string]any{
				"delta":         map[string]any{"content": text},
				"finish_reason": "stop",
			},
		},
	}))
}

func startToolRun(t *testing.T, service *chat.Runtime, session string) chat.Run {
	t.Helper()
	receipt, err := service.CreateRun(context.Background(), chat.CreateRunInput{
		SessionID:      session,
		Input:          "查询日程",
		Provider:       "test",
		Model:          "model",
		IdempotencyKey: "query",
	})
	if err != nil {
		t.Fatal(err)
	}
	return awaitRun(t, service, receipt.RunID)
}

func toolSettings(base string) func() ([]chat.ModelConfig, error) {
	return func() ([]chat.ModelConfig, error) {
		return []chat.ModelConfig{
			{
				Provider:        "test",
				Model:           "model",
				Key:             "secret",
				BaseURL:         base,
				ContextWindow:   32768,
				MaxOutputTokens: 8192,
			},
		}, nil
	}
}

type failToolResultOnce struct {
	chat.ToolRepository
	failed atomic.Bool
}

// FinishTool 在事务边界注入一次结果提交失败，不替代真实查询或持久化。
func (f *failToolResultOnce) FinishTool(ctx context.Context, call chat.ModelCall, tool chat.ToolCall) error {
	if !f.failed.Swap(true) {
		return errors.New("injected result transaction failure")
	}
	return f.ToolRepository.FinishTool(ctx, call, tool)
}

// TestToolResultPersistenceFailureStopsModelAndRetainsActualResult 验证故障只重试保存实际结果，不重发模型或丢失工具事实。
func TestToolResultPersistenceFailureStopsModelAndRetainsActualResult(t *testing.T) {
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		emitTools(w, []chat.ToolRequest{toolRequest("lookup", "get_schedule", ` {"id": 1} `)})
	}))
	defer upstream.Close()
	service, storage, application := newToolFixture(t, upstream.URL)
	service.Stop()
	storage.Tools = &failToolResultOnce{ToolRepository: storage.Tools}
	service = chat.NewService(storage, completion.NewClient(nil), toolSettings(upstream.URL), queryRegistry(t, application))
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer service.Stop()
	run := startToolRun(t, service, "save-failure")
	if run.Status != chat.StatusFailed || run.Error == nil || run.Error.Code != "persistence_failed" || requests.Load() != 1 {
		t.Fatalf("run: %+v requests=%d", run, requests.Load())
	}
	call, err := storage.Execution.ReadCall(context.Background(), run.RunID)
	if err != nil || call == nil || call.State != chat.StateSucceeded || call.CheckpointStage != "tool_end" {
		t.Fatalf("accepted call: %+v %v", call, err)
	}
	stored, err := storage.Tools.ReadTools(context.Background(), call.ID)
	if err != nil || len(stored) != 1 || stored[0].State != "succeeded" || stored[0].Request.Function.Arguments != ` {"id": 1} ` {
		t.Fatalf("saved actual result: %+v %v", stored, err)
	}
	page, _ := service.GetRunEvents(context.Background(), run.RunID, 0, 100)
	if len(page.Events) != 5 || page.Events[2].Type != chat.EventToolResult || page.Events[3].Type != chat.EventError || page.Events[4].Type != chat.EventEnd {
		t.Fatalf("atomic result/failure: %s", mustJSON(page))
	}
}

func mustJSON(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func newToolFixture(t *testing.T, base string) (*chat.Runtime, chat.Storage, *app.App) {
	t.Helper()
	ctx := context.Background()
	db := store.NewSqlite()
	if err := db.Connect(ctx, filepath.Join(t.TempDir(), "tools.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PrepareSchema(ctx); err != nil {
		t.Fatal(err)
	}
	schedules, err := store.NewScheduleStore(db)
	if err != nil {
		t.Fatal(err)
	}
	application := &app.App{Schedule: schedules}
	_, _, err = application.CreateSchedule(ctx, model.Schedule{
		Name:   "真实会议",
		Type:   model.SingleSchedule,
		Single: &model.Single{NotifyTime: time.Now().Add(48 * time.Hour).Truncate(time.Minute)},
	})
	if err != nil {
		t.Fatal(err)
	}
	storage := store.NewChatStorage(db)
	service := chat.NewService(storage, completion.NewClient(nil), func() ([]chat.ModelConfig, error) {
		return []chat.ModelConfig{
			{
				Provider:        "test",
				Model:           "model",
				Key:             "secret",
				BaseURL:         base,
				ContextWindow:   32768,
				MaxOutputTokens: 8192,
			},
		}, nil
	}, queryRegistry(t, application))
	if err := service.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Stop)
	return service, storage, application
}

func queryRegistry(t *testing.T, port tools.ScheduleQueries) *tools.Registry {
	t.Helper()
	registry, err := tools.NewRegistry(context.Background(), tools.ScheduleTools(port)...)
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

// TestLengthTruncatedToolDoesNotDiscardCompleteSibling 验证正常长度截断的坏参数按原 ID 拒绝，同批完整查询仍回填真实结果。
func TestLengthTruncatedToolDoesNotDiscardCompleteSibling(t *testing.T) {
	var count atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if count.Add(1) == 1 {
			emitToolFinish(w, []chat.ToolRequest{
				toolRequest("complete", "get_schedule", `{"id":1}`),
				toolRequest("truncated", "get_schedule", `{"id":`),
			}, "length")
			return
		}
		var input chat.ModelRequest
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			return
		}
		paired := map[string]string{}
		for _, message := range input.Messages {
			if message.Role == "tool" {
				paired[message.ToolCallID] = message.Content
			}
		}
		var failure chat.RunError
		if err := json.Unmarshal([]byte(paired["truncated"]), &failure); err != nil || failure.Code != "tool_argument_parse_error" {
			t.Errorf("truncated feedback: %+v %v", failure, err)
		}
		var value map[string]any
		if err := json.Unmarshal([]byte(paired["complete"]), &value); err != nil || value["name"] != "真实会议" {
			t.Errorf("complete query: %v %v", value, err)
		}
		emitText(w, "已处理同批工具")
	}))
	defer upstream.Close()
	service, _, _ := newToolFixture(t, upstream.URL)
	run := startToolRun(t, service, "length-tools")
	if run.Status != chat.StatusSucceeded || count.Load() != 2 {
		t.Fatalf("run: %+v requests=%d", run, count.Load())
	}
}

type acceptedToolGate struct {
	chat.ToolRepository
	accepted chan struct{}
	release  chan struct{}
}

// AcceptResponse 在完整回复提交后的取消边界暂停，不改变真实提交内容。
func (g *acceptedToolGate) AcceptResponse(ctx context.Context, call chat.ModelCall) error {
	if err := g.ToolRepository.AcceptResponse(ctx, call); err != nil {
		return err
	}
	close(g.accepted)
	<-g.release
	return nil
}

// TestCancelAfterToolReplyAcceptedPairsSkippedResults 验证接纳后立即取消仍按全部原 ID 保存跳过结果。
func TestCancelAfterToolReplyAcceptedPairsSkippedResults(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		emitTools(w, []chat.ToolRequest{toolRequest("cancelled-query", "get_schedule", `{"id":1}`)})
	}))
	defer upstream.Close()
	service, storage, application := newToolFixture(t, upstream.URL)
	service.Stop()
	gate := &acceptedToolGate{
		ToolRepository: storage.Tools,
		accepted:       make(chan struct{}),
		release:        make(chan struct{}),
	}
	storage.Tools = gate
	service = chat.NewService(storage, completion.NewClient(nil), toolSettings(upstream.URL), queryRegistry(t, application))
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer service.Stop()
	receipt, err := service.CreateRun(context.Background(), chat.CreateRunInput{
		SessionID:      "cancel-accepted",
		Input:          "查询",
		Provider:       "test",
		Model:          "model",
		IdempotencyKey: "query",
	})
	if err != nil {
		t.Fatal(err)
	}
	<-gate.accepted
	cancelCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, _ = service.CancelRun(cancelCtx, receipt.RunID)
	close(gate.release)
	run := awaitRun(t, service, receipt.RunID)
	if run.Status != chat.StatusCancelled {
		t.Fatalf("run: %+v", run)
	}
	page, _ := service.GetRunEvents(context.Background(), run.RunID, 0, 100)
	if len(page.Events) != 4 || page.Events[1].Type != chat.EventToolStart || page.Events[2].Type != chat.EventToolResult {
		t.Fatalf("unpaired cancelled tools: %s", mustJSON(page))
	}
	var result chat.ToolResultData
	if err := json.Unmarshal(page.Events[2].Data, &result); err != nil || result.Error == nil || result.Error.Code != "tool_skipped" {
		t.Fatalf("skip: %+v %v", result, err)
	}
}

type failToolStart struct{ chat.ToolRepository }

// BeginTool 在真实开始事务边界注入失败，工具不能执行。
func (f failToolStart) BeginTool(context.Context, chat.ModelCall, chat.ToolCall) (int64, error) {
	return 0, errors.New("injected tool start failure")
}

// TestToolStartPersistenceFailureKeepsNextHistoryPaired 验证开始事务失败仍让终态历史中的原调用取得明确错误配对。
func TestToolStartPersistenceFailureKeepsNextHistoryPaired(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		emitTools(w, []chat.ToolRequest{toolRequest("not-started", "get_schedule", `{"id":1}`)})
	}))
	defer upstream.Close()
	service, storage, application := newToolFixture(t, upstream.URL)
	service.Stop()
	storage.Tools = failToolStart{ToolRepository: storage.Tools}
	service = chat.NewService(storage, completion.NewClient(nil), toolSettings(upstream.URL), queryRegistry(t, application))
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer service.Stop()
	run := startToolRun(t, service, "start-failure")
	if run.Status != chat.StatusFailed || run.Error == nil || run.Error.Code != "persistence_failed" {
		t.Fatalf("run: %+v", run)
	}
	history, err := storage.Execution.History(context.Background(), run.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	paired := false
	for _, message := range history {
		if message.Role == "tool" && message.ToolCallID == "not-started" {
			paired = true
		}
	}
	if !paired {
		t.Fatalf("unpaired terminal history: %s", mustJSON(history))
	}
}

// TestNextModelCallReadsCurrentRetryBudget 验证工具后的新逻辑调用读取最新重试配置。
func TestNextModelCallReadsCurrentRetryBudget(t *testing.T) {
	var budget atomic.Int32
	budget.Store(1)
	var count atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if count.Add(1) == 1 {
			budget.Store(0)
			w.Header().Set("Content-Type", "text/event-stream")
			emitTools(w, []chat.ToolRequest{toolRequest("lookup", "get_schedule", `{"id":1}`)})
			return
		}
		http.Error(w, "temporary", http.StatusServiceUnavailable)
	}))
	defer upstream.Close()
	service, storage, application := newToolFixture(t, upstream.URL)
	service.Stop()
	settings := func() ([]chat.ModelConfig, error) {
		configs, err := toolSettings(upstream.URL)()
		configs[0].MaxRetry = int(budget.Load())
		return configs, err
	}
	service = chat.NewService(storage, completion.NewClient(nil), settings, queryRegistry(t, application))
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer service.Stop()
	run := startToolRun(t, service, "hot-next")
	if run.Status != chat.StatusFailed || run.Error == nil || run.Error.Code != "model_server_busy" || count.Load() != 2 {
		t.Fatalf("run: %+v requests=%d", run, count.Load())
	}
	call, err := storage.Execution.ReadCall(context.Background(), run.RunID)
	if err != nil || call.MaxRetry != 0 {
		t.Fatalf("retry budget: %+v %v", call, err)
	}
}
