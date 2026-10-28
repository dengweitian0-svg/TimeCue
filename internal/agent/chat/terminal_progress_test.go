package chat_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/agent/completion"
	"github.com/dengweitian0-svg/TimeCue/internal/app"
	"github.com/dengweitian0-svg/TimeCue/internal/model"
	"github.com/dengweitian0-svg/TimeCue/internal/store"
)

// TestCancellationRecoveryClosesAcceptedLegacyBatch 验证旧格式取消恢复补齐原 ID，保留成功调用和已提交真实结果。
func TestCancellationRecoveryClosesAcceptedLegacyBatch(t *testing.T) {
	for _, stage := range []string{"model_call_end", "tool_start", "tool_end"} {
		t.Run(stage, func(t *testing.T) {
			ctx := context.Background()
			_, ports, path := admissionDatabase(t)
			record := chat.Record{
				Run: chat.Run{
					RunID:     "cancel-legacy",
					SessionID: "cancel-legacy-session",
					Provider:  "test",
					Model:     "model",
					Input:     "旧输入",
					CreatedAt: time.Now(),
				},
				State:          chat.StateWaitForCancel,
				IdempotencyKey: "old",
			}
			if err := ports.Runs.Create(ctx, record); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			// 独立版本 3 样本不调用新进度构造器，成功调用的时间及原始 JSON 必须原样保留。
			output := `{"id":"accepted","message":{"role":"assistant","content":"准备查询","reasoning_content":"原思考","tool_calls":[{"id":"first","type":"function","function":{"name":"get_schedule","arguments":" {\"id\":1} "}},{"id":"second","type":"function","function":{"name":"get_schedule","arguments":"{\"id\":2}"}}]},"finish_reason":"tool_calls","usage":{"total_tokens":7}}`
			result, err := db.Exec(`INSERT INTO ai_model_calls(run_id,session_id,input,output,usage,status,max_retry,created_at,finished_at) VALUES(?,?,?, ?,?, 'succeeded',3,1,2)`, record.RunID, record.SessionID, `{"model":"model","messages":[{"role":"user","content":"旧输入"}]}`, output, `{"total_tokens":7}`)
			if err != nil {
				t.Fatal(err)
			}
			callID, _ := result.LastInsertId()
			state := `{"context":[{"role":"user","content":"旧输入"},` + `{"role":"assistant","content":"准备查询","reasoning_content":"原思考","tool_calls":[{"id":"first","type":"function","function":{"name":"get_schedule","arguments":" {\"id\":1} "}},{"id":"second","type":"function","function":{"name":"get_schedule","arguments":"{\"id\":2}"}}]}`
			completed := `[]`
			if stage != "model_call_end" {
				state += `,{"role":"tool","content":"[1,true,null]","tool_call_id":"first"}`
				completed = `["first"]`
				if _, err := db.Exec(`INSERT INTO ai_tool_calls(run_id,session_id,model_call_id,tool_call_id,name,input,output,status,max_retry,created_at,finished_at) VALUES(?,?,?,'first','get_schedule',?,?,'succeeded',3,1,2)`, record.RunID, record.SessionID, callID, `" {\"id\":1} "`, `[1,true,null]`); err != nil {
					t.Fatal(err)
				}
			}
			if stage == "tool_end" {
				state += `,{"role":"tool","content":"false","tool_call_id":"second"}`
				completed = `["first","second"]`
				if _, err := db.Exec(`INSERT INTO ai_tool_calls(run_id,session_id,model_call_id,tool_call_id,name,input,output,status,max_retry,created_at,finished_at) VALUES(?,?,?,'second','get_schedule',?,?,'succeeded',3,1,2)`, record.RunID, record.SessionID, callID, `"{\"id\":2}"`, `false`); err != nil {
					t.Fatal(err)
				}
			}
			if stage == "tool_start" {
				if _, err := db.Exec(`INSERT INTO ai_tool_calls(run_id,session_id,model_call_id,tool_call_id,name,input,status,max_retry,retry_count,created_at) VALUES(?,?,?,'second','get_schedule',?,'running',3,2,1)`, record.RunID, record.SessionID, callID, `"{\"id\":2}"`); err != nil {
					t.Fatal(err)
				}
			}
			state += `],"completed_tool":` + completed + `,"last_tool":""}`
			if _, err := db.Exec(`INSERT INTO ai_checkpoints(run_id,model_call_id,status,state) VALUES(?,?,?,?)`, record.RunID, callID, stage, state); err != nil {
				t.Fatal(err)
			}
			var attempts atomic.Int32
			inputs := make(chan chat.ModelRequest, 1)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				attempts.Add(1)
				var input chat.ModelRequest
				if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
					t.Error(err)
					return
				}
				inputs <- input
				emitText(w, "接续完成")
			}))
			defer upstream.Close()
			service := startAdmissionService(t, ports, completion.NewClient(nil), toolSettings(upstream.URL))
			run := awaitFinishedRun(t, service, record.RunID)
			if run.Status != chat.StatusCancelled || attempts.Load() != 0 {
				t.Fatalf("取消恢复执行了模型: %+v", run)
			}
			call, err := ports.Execution.ReadCall(ctx, record.RunID)
			if err != nil || call == nil || call.CheckpointStage != "tool_end" || len(call.CompletedTools) != 2 {
				t.Fatalf("取消恢复未配对: %+v %v", call, err)
			}
			var savedOutput, usage string
			var finished int64
			if err := db.QueryRow(`SELECT output,usage,finished_at FROM ai_model_calls WHERE id=?`, callID).Scan(&savedOutput, &usage, &finished); err != nil || savedOutput != output || usage != `{"total_tokens":7}` || finished != 2 {
				t.Fatalf("成功调用被改写: %s %s %d %v", savedOutput, usage, finished, err)
			}
			if stage != "model_call_end" && call.CheckpointContext[2].Content != `[1,true,null]` {
				t.Fatalf("真实结果被覆盖: %s", mustJSON(call.CheckpointContext))
			}
			tools, err := ports.Tools.ReadTools(ctx, callID)
			if err != nil || len(tools) != 2 {
				t.Fatalf("工具事实不完整: %+v %v", tools, err)
			}
			for _, tool := range tools {
				if stage == "model_call_end" || stage == "tool_start" && tool.Request.ID == "second" {
					expectedState, expectedCode := "skipped", "tool_skipped"
					if stage == "tool_start" {
						expectedState, expectedCode = "failed", "tool_result_unconfirmed"
					}
					if tool.State != expectedState || tool.Error == nil || tool.Error.Code != expectedCode {
						t.Fatalf("取消工具事实不符: %+v", tool)
					}
				}
				if stage == "tool_start" && tool.Request.ID == "second" && tool.RetryCount != 2 {
					t.Fatalf("取消恢复重执了在途工具: %+v", tool)
				}
			}
			next, err := service.CreateRun(ctx, admissionInput(record.SessionID, "next"))
			if err != nil {
				t.Fatal(err)
			}
			awaitRun(t, service, next.RunID)
			actual := awaitHistoryRequest(t, inputs)
			if len(actual.Messages) != 6 || !reflect.DeepEqual(actual.Messages[:4], call.CheckpointContext) || actual.Messages[4].Content != "上一轮回复未完成；其操作表述不代表业务已经生效。" {
				t.Fatalf("接续丢失配对或中断说明: %s", mustJSON(actual.Messages))
			}
		})
	}
}

type committedResultFailure struct {
	chat.ToolRepository
	call     chat.ModelCall
	snapshot string
}

// FinishTool 模拟提交成功但回执丢失，保留协调器所见的旧快照。
func (f *committedResultFailure) FinishTool(ctx context.Context, call chat.ModelCall, tool chat.ToolCall) (chat.ModelCall, error) {
	f.call, f.snapshot = call, mustJSON(call)
	if _, err := f.ToolRepository.FinishTool(ctx, call, tool); err != nil {
		return call, err
	}
	return call, errors.New("injected lost result commit receipt")
}

type rollbackTerminalGate struct {
	chat.ExecutionRepository
	failed   chan error
	release  chan struct{}
	once     sync.Once
	attempts atomic.Int32
}

// Finish 在真实终态回滚后暂停；成功后的重复提交必须复用原终态。
func (g *rollbackTerminalGate) Finish(ctx context.Context, call chat.ModelCall, record chat.Record) error {
	err := g.ExecutionRepository.Finish(ctx, call, record)
	if g.attempts.Add(1) == 1 {
		g.failed <- err
		<-g.release
	}
	if err == nil {
		return g.ExecutionRepository.Finish(ctx, call, record)
	}
	return err
}

// TestTerminalRetryUsesCommittedFactsWithoutPartialProgress 验证丢失提交回执和终态回滚后仍保留最新真实结果、连续事件及旧快照隔离。
func TestTerminalRetryUsesCommittedFactsWithoutPartialProgress(t *testing.T) {
	ctx := context.Background()
	database, ports, path := admissionDatabase(t)
	schedules, err := store.NewScheduleStore(database)
	if err != nil {
		t.Fatal(err)
	}
	application := &app.App{Schedule: schedules}
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		emitTools(w, []chat.ToolRequest{toolRequest("committed", "list_schedules", `{}`)})
	}))
	defer upstream.Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TRIGGER reject_terminal BEFORE INSERT ON ai_events WHEN NEW.type='end'
		BEGIN SELECT RAISE(ABORT,'injected terminal failure'); END`); err != nil {
		t.Fatal(err)
	}
	fault := &committedResultFailure{ToolRepository: ports.Tools}
	gate := &rollbackTerminalGate{
		ExecutionRepository: ports.Execution,
		failed:              make(chan error, 1),
		release:             make(chan struct{}),
	}
	ports.Tools, ports.Execution = fault, gate
	service := chat.NewService(ports, completion.NewClient(nil), toolSettings(upstream.URL), queryRegistry(t, application))
	if err := service.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer service.Stop()
	defer gate.once.Do(func() { close(gate.release) })
	receipt, err := service.CreateRun(ctx, admissionInput("uncertain-commit", "key"))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-gate.failed:
		if err == nil {
			t.Fatal("真实终态事务未失败")
		}
	case <-time.After(4 * time.Second):
		t.Fatal("未到达终态故障边界")
	}
	run, err := service.GetRun(ctx, receipt.RunID)
	if err != nil || run.Status != chat.StatusRunning || run.FinishedAt != nil {
		t.Fatalf("失败终态可见: %+v %v", run, err)
	}
	call, err := ports.Execution.ReadCall(ctx, receipt.RunID)
	if err != nil || call == nil || call.CheckpointStage != "tool_end" || len(call.CompletedTools) != 1 || call.CheckpointContext[2].Content != `[]` {
		t.Fatalf("丢失回执覆盖已提交实际结果: %+v %v", call, err)
	}
	page, err := service.GetRunEvents(ctx, receipt.RunID, 0, 100)
	if err != nil || len(page.Events) != 3 || page.Events[2].Type != chat.EventToolResult {
		t.Fatalf("失败终态暴露半份进度: %s %v", mustJSON(page), err)
	}
	if mustJSON(fault.call) != fault.snapshot || len(fault.call.CompletedTools) != 0 || len(fault.call.CheckpointContext) != 2 {
		t.Fatal("待提交进度污染旧快照")
	}
	if _, err := service.CreateRun(ctx, admissionInput("uncertain-commit", "new")); !errors.Is(err, chat.ErrSessionBusy) {
		t.Fatalf("终态失败提前释放占用: %v", err)
	}
	if _, err := db.Exec(`DROP TRIGGER reject_terminal`); err != nil {
		t.Fatal(err)
	}
	gate.once.Do(func() { close(gate.release) })
	// 既有收尾策略间隔五秒重试；期间不能重新请求模型或工具。
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		run, err = service.GetRun(ctx, receipt.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if run.FinishedAt != nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if run.FinishedAt == nil || run.Status != chat.StatusFailed || requests.Load() != 1 || gate.attempts.Load() != 2 {
		t.Fatalf("终态重试未按原事实完成: %+v attempts=%d requests=%d", run, gate.attempts.Load(), requests.Load())
	}
	page, err = service.GetRunEvents(ctx, receipt.RunID, 0, 100)
	if err != nil || len(page.Events) != 5 || page.Events[3].Type != chat.EventError || page.Events[4].Type != chat.EventEnd {
		t.Fatalf("重试或重复提交产生重复结果/终态: %s %v", mustJSON(page), err)
	}
	for index, event := range page.Events {
		if event.ID != int64(index+1) {
			t.Fatalf("重试造成序号缺口: %s", mustJSON(page))
		}
	}
	replay, err := service.CreateRun(ctx, admissionInput("uncertain-commit", "key"))
	if err != nil || replay.RunID != receipt.RunID || replay.Status != chat.StatusFailed {
		t.Fatalf("故障终态幂等失效: %+v %v", replay, err)
	}
}

func awaitFinishedRun(t *testing.T, service chat.RunService, id string) chat.Run {
	t.Helper()
	return awaitFinishedRunWithin(t, service, id, 4*time.Second)
}

func awaitFinishedRunWithin(t *testing.T, service chat.RunService, id string, timeout time.Duration) chat.Run {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		run, err := service.GetRun(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if run.FinishedAt != nil {
			return run
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("运行未完成终态提交")
	return chat.Run{}
}

type rollbackToolResult struct {
	chat.ToolRepository
	db            *sql.DB
	failed        atomic.Bool
	before, after string
}

// FinishTool 在实际结果与事件写入后令 checkpoint 更新失败，验证真实事务回滚。
func (f *rollbackToolResult) FinishTool(ctx context.Context, call chat.ModelCall, tool chat.ToolCall) (chat.ModelCall, error) {
	if f.failed.Swap(true) {
		return f.ToolRepository.FinishTool(ctx, call, tool)
	}
	if err := f.db.QueryRow(`SELECT state FROM ai_checkpoints WHERE run_id=?`, call.RunID).Scan(&f.before); err != nil {
		return call, err
	}
	progress, failure := f.ToolRepository.FinishTool(ctx, call, tool)
	if failure == nil {
		return call, errors.New("预期真实结果事务失败")
	}
	if err := f.db.QueryRow(`SELECT state FROM ai_checkpoints WHERE run_id=?`, call.RunID).Scan(&f.after); err != nil {
		return call, err
	}
	return progress, failure
}

// TestFailedResultTransactionRetainsAvailableBatchResults 验证结果回滚后停止新启动，保留已取得结果，未确认项不冒充成功。
func TestFailedResultTransactionRetainsAvailableBatchResults(t *testing.T) {
	ctx := context.Background()
	database, ports, path := admissionDatabase(t)
	schedules, err := store.NewScheduleStore(database)
	if err != nil {
		t.Fatal(err)
	}
	application := &app.App{Schedule: schedules}
	if _, _, err := application.CreateSchedule(ctx, model.Schedule{
		Name:   "真实故障查询",
		Type:   model.SingleSchedule,
		Single: &model.Single{NotifyTime: time.Now().Add(48 * time.Hour).Truncate(time.Minute)},
	}); err != nil {
		t.Fatal(err)
	}
	var attempts atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		attempts.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		emitTools(w, []chat.ToolRequest{
			toolRequest("one", "get_schedule", `{"id":1}`),
			toolRequest("two", "get_schedule", `{"id":1}`),
			toolRequest("three", "get_schedule", `{"id":1}`),
			toolRequest("not-started", "get_schedule", `{"id":1}`),
		})
	}))
	defer upstream.Close()
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TRIGGER reject_tool_result BEFORE UPDATE ON ai_checkpoints
  WHEN NEW.status='tool_start' AND EXISTS(SELECT 1 FROM ai_tool_calls WHERE model_call_id=NEW.model_call_id AND status='succeeded')
  BEGIN SELECT RAISE(ABORT,'injected result checkpoint failure'); END`); err != nil {
		t.Fatal(err)
	}
	fault := &rollbackToolResult{
		ToolRepository: ports.Tools,
		db:             db,
	}
	ports.Tools = fault
	service := chat.NewService(ports, completion.NewClient(nil), toolSettings(upstream.URL), queryRegistry(t, application))
	if err := service.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer service.Stop()
	run := startToolRun(t, service, "batch-result-fault")
	if run.Status != chat.StatusFailed || run.Error == nil || run.Error.Code != "persistence_failed" || attempts.Load() != 1 {
		t.Fatalf("故障后继续模型: %+v requests=%d", run, attempts.Load())
	}
	if fault.before == "" || fault.before != fault.after {
		t.Fatalf("失败事务污染快照: before=%s after=%s", fault.before, fault.after)
	}
	call, err := ports.Execution.ReadCall(ctx, run.RunID)
	if err != nil || call == nil || call.CheckpointStage != "tool_end" || len(call.CompletedTools) != 4 {
		t.Fatalf("批次未完整配对: %+v %v", call, err)
	}
	tools, err := ports.Tools.ReadTools(ctx, call.ID)
	if err != nil || len(tools) != 4 {
		t.Fatalf("工具事实不完整: %+v %v", tools, err)
	}
	succeeded := 0
	for _, tool := range tools {
		if tool.Request.ID == "not-started" || tool.State != "succeeded" {
			if tool.State != "failed" || tool.Error == nil || tool.Error.Code != "tool_result_unconfirmed" {
				t.Fatalf("未启动项事实不符: %+v", tool)
			}
		} else {
			succeeded++
			var value map[string]any
			if tool.State != "succeeded" || json.Unmarshal(tool.Output, &value) != nil || value["name"] != "真实故障查询" || tool.RetryCount != 0 {
				t.Fatalf("已启动查询的实际结果丢失或被重执: %+v", tool)
			}
		}
	}
	if succeeded == 0 {
		t.Fatal("导致结果事务失败的真实结果未被保留")
	}
	page, err := service.GetRunEvents(ctx, run.RunID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	results, ends := map[string]int{}, 0
	for index, event := range page.Events {
		if event.ID != int64(index+1) {
			t.Fatalf("事件序号不连续: %s", mustJSON(page))
		}
		if event.Type == chat.EventToolResult {
			var result chat.ToolResultData
			if err := json.Unmarshal(event.Data, &result); err != nil {
				t.Fatal(err)
			}
			results[result.ToolCallID]++
		}
		if event.Type == chat.EventEnd {
			ends++
		}
	}
	if len(results) != 4 || ends != 1 {
		t.Fatalf("可见结果或终态不完整: %s", mustJSON(page))
	}
	for id, count := range results {
		if count != 1 {
			t.Fatalf("%s 重复结果: %d", id, count)
		}
	}
}

// TestToolStartRollbackKeepsOriginalPairing 验证开始事实及事件已写入后回滚，不推进业务并按原 ID 保存未确认配对。
func TestToolStartRollbackKeepsOriginalPairing(t *testing.T) {
	ctx := context.Background()
	database, ports, path := admissionDatabase(t)
	schedules, err := store.NewScheduleStore(database)
	if err != nil {
		t.Fatal(err)
	}
	application := &app.App{Schedule: schedules}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		emitTools(w, []chat.ToolRequest{toolRequest("start-rollback", "get_schedule", ` {"id":1} `)})
	}))
	defer upstream.Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TRIGGER reject_tool_start BEFORE UPDATE ON ai_checkpoints
		WHEN json_extract(NEW.state,'$.last_tool') LIKE 'tool_use:%'
		BEGIN SELECT RAISE(ABORT,'injected start checkpoint failure'); END`); err != nil {
		t.Fatal(err)
	}
	service := chat.NewService(ports, completion.NewClient(nil), toolSettings(upstream.URL), queryRegistry(t, application))
	if err := service.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer service.Stop()
	run := startToolRun(t, service, "start-rollback")
	if run.Status != chat.StatusFailed || run.Error == nil || run.Error.Code != "persistence_failed" {
		t.Fatalf("开始事务未阻止推进: %+v", run)
	}
	call, err := ports.Execution.ReadCall(ctx, run.RunID)
	if err != nil || call == nil || call.CheckpointStage != "tool_end" || len(call.CompletedTools) != 1 {
		t.Fatalf("原调用未完整配对: %+v %v", call, err)
	}
	stored, err := ports.Tools.ReadTools(ctx, call.ID)
	if err != nil || len(stored) != 1 || stored[0].State != "failed" || stored[0].Error == nil || stored[0].Error.Code != "tool_result_unconfirmed" || stored[0].Request.Function.Arguments != ` {"id":1} ` {
		t.Fatalf("回滚产生伪造业务事实: %+v %v", stored, err)
	}
	page, err := service.GetRunEvents(ctx, run.RunID, 0, 100)
	if err != nil || len(page.Events) != 5 || page.Events[1].Type != chat.EventToolStart || page.Events[2].Type != chat.EventToolResult {
		t.Fatalf("开始回滚暴露事件或丢失原 ID 配对: %s %v", mustJSON(page), err)
	}
	for index, event := range page.Events {
		if event.ID != int64(index+1) {
			t.Fatalf("开始回滚产生序号缺口: %s", mustJSON(page))
		}
	}
}

// TestNormalStopKeepsAcceptedBatchUnfinished 验证接纳后正常退出只保留进度，重启沿原回复处理工具。
func TestNormalStopKeepsAcceptedBatchUnfinished(t *testing.T) {
	ctx := context.Background()
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if requests.Add(1) == 1 {
			emitTools(w, []chat.ToolRequest{toolRequest("resume", "list_schedules", `{}`)})
		} else {
			emitText(w, "恢复完成")
		}
	}))
	defer upstream.Close()
	service, ports, application := newToolFixture(t, upstream.URL)
	service.Stop()
	gate := &acceptedToolGate{
		ToolRepository: ports.Tools,
		accepted:       make(chan struct{}),
		release:        make(chan struct{}),
	}
	gated := ports
	gated.Tools = gate
	service = chat.NewService(gated, completion.NewClient(nil), toolSettings(upstream.URL), queryRegistry(t, application))
	if err := service.Start(ctx); err != nil {
		t.Fatal(err)
	}
	var release sync.Once
	defer service.Stop()
	defer release.Do(func() { close(gate.release) })
	input := admissionInput("stop-accepted", "query")
	receipt, err := service.CreateRun(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-gate.accepted:
	case <-time.After(4 * time.Second):
		t.Fatal("回复未接纳")
	}
	before, err := ports.Execution.ReadCall(ctx, receipt.RunID)
	if err != nil {
		t.Fatal(err)
	}
	stopped := make(chan struct{})
	go func() { service.Stop(); close(stopped) }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		_, err := service.CreateRun(ctx, input)
		if errors.Is(err, chat.ErrStopping) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("服务未进入退出")
		}
		time.Sleep(time.Millisecond)
	}
	release.Do(func() { close(gate.release) })
	select {
	case <-stopped:
	case <-time.After(4 * time.Second):
		t.Fatal("退出未完成")
	}
	run, err := service.GetRun(ctx, receipt.RunID)
	if err != nil || run.Status != chat.StatusRunning || run.FinishedAt != nil {
		t.Fatalf("正常退出伪造终态: %+v %v", run, err)
	}
	after, err := ports.Execution.ReadCall(ctx, receipt.RunID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("正常退出改写已接纳进度: before=%+v after=%+v %v", before, after, err)
	}
	page, err := service.GetRunEvents(ctx, receipt.RunID, 0, 100)
	if err != nil || len(page.Events) != 1 {
		t.Fatalf("正常退出伪造工具或终态事件: %s %v", mustJSON(page), err)
	}
	restarted := chat.NewService(ports, completion.NewClient(nil), toolSettings(upstream.URL), queryRegistry(t, application))
	if err := restarted.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer restarted.Stop()
	run = awaitRun(t, restarted, receipt.RunID)
	if run.Status != chat.StatusSucceeded || requests.Load() != 2 {
		t.Fatalf("恢复重发已接纳回复: %+v requests=%d", run, requests.Load())
	}
}
