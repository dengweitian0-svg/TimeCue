package chat_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/agent/completion"
	"github.com/dengweitian0-svg/TimeCue/internal/app"
	"github.com/dengweitian0-svg/TimeCue/internal/store"
)

// TestLegacyCheckpointContinuesExactContext 验证独立旧格式样本保留工具配对与仅思考回复，过滤全空回复并只追加一次中断说明。
func TestLegacyCheckpointContinuesExactContext(t *testing.T) {
	_, ports, path := admissionDatabase(t)
	ctx := context.Background()
	record := chat.Record{
		Run: chat.Run{
			RunID:     "legacy",
			SessionID: "legacy-session",
			Input:     "旧输入",
			Provider:  "test",
			Model:     "model",
			CreatedAt: time.Now().Add(-time.Minute),
		},
		State:          chat.StateRunning,
		IdempotencyKey: "legacy",
	}
	if err := ports.Runs.Create(ctx, record); err != nil {
		t.Fatal(err)
	}
	// 样本直接采用版本 3 的磁盘格式，不经待验证的进度构造规则生成。
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`INSERT INTO ai_model_calls(run_id,session_id,input,status,max_retry,created_at) VALUES('legacy','legacy-session','{"model":"model","messages":[]}','failed',0,1)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO ai_checkpoints(run_id,model_call_id,status,state) VALUES('legacy',last_insert_rowid(),'model_call_end',
		'{"context":[{"role":"user","content":"旧输入"},{"role":"assistant","content":"","tool_calls":[{"id":"old-id","type":"function","function":{"name":"get_schedule","arguments":" {\"id\":1} "}}]},{"role":"tool","content":"{\"name\":\"旧结果\"}","tool_call_id":"old-id"},{"role":"assistant","content":""},{"role":"assistant","content":"","tool_calls":[]},{"role":"assistant","content":"","tool_calls":null},{"role":"assistant","content":"","reasoning_content":"只留下思考"}],"completed_tool":[],"last_tool":""}')`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE ai_runs SET status='failed',finished_at=2 WHERE run_id='legacy'`); err != nil {
		t.Fatal(err)
	}
	requests := make(chan chat.ModelRequest, 2)
	var attempts atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var input chat.ModelRequest
		if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
			t.Error(err)
			return
		}
		requests <- input
		if attempts.Add(1) == 1 {
			<-req.Context().Done()
			return
		}
		emitText(w, "已接续")
	}))
	t.Cleanup(upstream.Close)
	service := startAdmissionService(t, ports, completion.NewClient(nil), retrySettings(upstream.URL, 0))
	input := admissionInput(record.SessionID, "next")
	input.Input = "新输入"
	receipt, err := service.CreateRun(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	actual := awaitHistoryRequest(t, requests)
	want := []chat.Message{
		{
			Role:    "user",
			Content: "旧输入",
		},
		{
			Role:      "assistant",
			ToolCalls: json.RawMessage(`[{"id":"old-id","type":"function","function":{"name":"get_schedule","arguments":" {\"id\":1} "}}]`),
		},
		{
			Role:       "tool",
			Content:    `{"name":"旧结果"}`,
			ToolCallID: "old-id",
		},
		{
			Role:             "assistant",
			ReasoningContent: "只留下思考",
		},
		{
			Role:    "user",
			Content: "上一轮回复未完成；其操作表述不代表业务已经生效。",
		},
		{
			Role:    "user",
			Content: "新输入",
		},
	}
	if !reflect.DeepEqual(actual.Messages, want) {
		t.Fatalf("旧格式上下文被改变: %s", mustJSON(actual.Messages))
	}
	service.Stop()
	resumed := startAdmissionService(t, ports, completion.NewClient(nil), retrySettings(upstream.URL, 0))
	if run := awaitRun(t, resumed, receipt.RunID); run.Status != chat.StatusSucceeded {
		t.Fatalf("恢复失败: %+v", run)
	}
	if replay := awaitHistoryRequest(t, requests); !reflect.DeepEqual(replay, actual) {
		t.Fatalf("恢复重复追加说明: %s", mustJSON(replay))
	}
	session, err := resumed.GetSession(ctx, record.SessionID)
	if err != nil || !reflect.DeepEqual(session.RunIDs, []string{record.RunID, receipt.RunID}) {
		t.Fatalf("历史会话被改变: %+v %v", session, err)
	}
}

// TestRejectedResponseRetainsRawOutputWithoutToolContext 验证接纳失败不执行工具，原始回复及 usage 保留而后续上下文只接续正文与思考。
func TestRejectedResponseRetainsRawOutputWithoutToolContext(t *testing.T) {
	requests := make(chan chat.ModelRequest, 2)
	var attempts atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var input chat.ModelRequest
		if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
			t.Error(err)
			return
		}
		requests <- input
		if attempts.Add(1) == 1 {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Write([]byte("data: {\"id\":\"raw\",\"choices\":[{\"delta\":{\"content\":\"已有正文\",\"reasoning_content\":\"已有思考\",\"tool_calls\":[{\"index\":0,\"id\":\"unaccepted\",\"type\":\"function\",\"function\":{\"name\":\"get_schedule\",\"arguments\":\"{\\\"id\\\":1}\"}}]},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"total_tokens\":7}}\n\ndata: [DONE]\n\n"))
			return
		}
		emitText(w, "下一轮")
	}))
	t.Cleanup(upstream.Close)
	database, ports, path := admissionDatabase(t)
	schedules, err := store.NewScheduleStore(database)
	if err != nil {
		t.Fatal(err)
	}
	application := &app.App{Schedule: schedules}
	faultDB, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = faultDB.Close() })
	// 接纳事务已写入 succeeded/output 和尾批后才失败；失败收尾仍可保存真实部分回复。
	if _, err := faultDB.Exec(`CREATE TRIGGER reject_response BEFORE UPDATE ON ai_checkpoints
		WHEN NEW.status='model_call_end' AND EXISTS(SELECT 1 FROM ai_model_calls WHERE id=NEW.model_call_id AND status='succeeded' AND json_array_length(output,'$.message.tool_calls')>0)
		BEGIN SELECT RAISE(ABORT,'injected checkpoint failure'); END`); err != nil {
		t.Fatal(err)
	}
	service := chat.NewService(ports, completion.NewClient(nil), toolSettings(upstream.URL), queryRegistry(t, application))
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Stop)
	run := startToolRun(t, service, "rejected")
	if run.Status != chat.StatusFailed || run.Error == nil || run.Error.Code != "persistence_failed" || run.Output == nil || *run.Output != "已有正文" {
		t.Fatalf("故障事实不符: %+v", run)
	}
	call, err := ports.Execution.ReadCall(context.Background(), run.RunID)
	if err != nil || call == nil || call.Output == nil || len(call.Output.Message.ToolCalls) == 0 || string(call.Output.Usage) != `{"total_tokens":7}` {
		t.Fatalf("原始回复丢失: %+v %v", call, err)
	}
	tools, err := ports.Tools.ReadTools(context.Background(), call.ID)
	if err != nil || len(tools) != 0 {
		t.Fatalf("未接纳回复执行了工具: %+v %v", tools, err)
	}
	page, err := service.GetRunEvents(context.Background(), run.RunID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	for i, event := range page.Events {
		if event.ID != int64(i+1) || event.Type == chat.EventToolStart || event.Type == chat.EventToolResult {
			t.Fatalf("故障暴露了工具进度或序号缺口: %s", mustJSON(page))
		}
	}
	service.Stop()
	resumed := chat.NewService(ports, completion.NewClient(nil), toolSettings(upstream.URL), queryRegistry(t, application))
	if err := resumed.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(resumed.Stop)
	next, err := resumed.CreateRun(context.Background(), admissionInput(run.SessionID, "next"))
	if err != nil {
		t.Fatal(err)
	}
	awaitRun(t, resumed, next.RunID)
	awaitHistoryRequest(t, requests)
	actual := awaitHistoryRequest(t, requests)
	if len(actual.Messages) != 4 || actual.Messages[1].Content != "已有正文" || actual.Messages[1].ReasoningContent != "已有思考" || len(actual.Messages[1].ToolCalls) != 0 || actual.Messages[2].Content != "上一轮回复未完成；其操作表述不代表业务已经生效。" {
		t.Fatalf("未提交工具进入有效上下文: %s", mustJSON(actual.Messages))
	}
}

// TestNewCallsResetToolFactsButKeepPairedHistory 验证新调用重置完成集合，跨回复同值工具 ID 仍分别处理并保留原配对。
func TestNewCallsResetToolFactsButKeepPairedHistory(t *testing.T) {
	var ports chat.Storage
	var attempts atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var input chat.ModelRequest
		if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
			t.Error(err)
			return
		}
		number := attempts.Add(1)
		session, err := ports.Sessions.Session(req.Context(), "repeat-id")
		if err != nil || len(session.RunIDs) != 1 {
			t.Errorf("会话事实不符: %+v %v", session, err)
			return
		}
		call, err := ports.Execution.ReadCall(req.Context(), session.RunIDs[0])
		if err != nil || call == nil || call.CheckpointStage != "model_call_start" || len(call.CompletedTools) != 0 || call.LastTool != "" || !reflect.DeepEqual(call.Input, input) || !reflect.DeepEqual(call.CheckpointContext, input.Messages) {
			t.Errorf("新调用沿用了旧工具事实或改变实际请求: %+v %v", call, err)
			return
		}
		switch number {
		case 1:
			emitTools(w, []chat.ToolRequest{toolRequest("same", "get_schedule", `{"id":1}`)})
		case 2:
			emitTools(w, []chat.ToolRequest{toolRequest("same", "get_schedule", `{"id":9999}`)})
		case 3:
			if len(input.Messages) != 5 || input.Messages[2].ToolCallID != "same" || input.Messages[4].ToolCallID != "same" {
				t.Errorf("工具配对被覆盖: %s", mustJSON(input.Messages))
				return
			}
			var first map[string]any
			var second chat.RunError
			if err := json.Unmarshal([]byte(input.Messages[2].Content), &first); err != nil || first["name"] != "真实会议" {
				t.Errorf("第一批实际结果丢失: %v %v", first, err)
			}
			if err := json.Unmarshal([]byte(input.Messages[4].Content), &second); err != nil || second.Code != "schedule_not_found" {
				t.Errorf("同值 ID 错误复用第一批结果: %+v %v", second, err)
			}
			emitText(w, "分别完成查询")
		default:
			t.Errorf("多余模型请求: %d", number)
		}
	}))
	t.Cleanup(upstream.Close)
	service, storage, _ := newToolFixture(t, upstream.URL)
	ports = storage
	run := startToolRun(t, service, "repeat-id")
	if run.Status != chat.StatusSucceeded || run.Output == nil || *run.Output != "分别完成查询" || attempts.Load() != 3 {
		t.Fatalf("重复工具 ID 跨调用执行失败: %+v", run)
	}
}
