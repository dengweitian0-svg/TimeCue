package chat_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
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

type toolCommitGate struct {
	chat.ToolRepository
	afterResults int
	results      int
	committed    chan struct{}
	release      chan struct{}
	once         sync.Once
}

func (g *toolCommitGate) wait() {
	close(g.committed)
	<-g.release
}

// AcceptResponse 在真实回复接纳事务提交后暂停，供测试保存崩溃时的数据库快照。
func (g *toolCommitGate) AcceptResponse(ctx context.Context, call chat.ModelCall) error {
	if err := g.ToolRepository.AcceptResponse(ctx, call); err != nil {
		return err
	}
	if g.afterResults == 0 {
		g.wait()
	}
	return nil
}

// FinishTool 在指定的真实逐项结果事务提交后暂停，不替换查询或结果。
func (g *toolCommitGate) FinishTool(ctx context.Context, call chat.ModelCall, tool chat.ToolCall) (chat.ModelCall, error) {
	progress, err := g.ToolRepository.FinishTool(ctx, call, tool)
	if err != nil {
		return call, err
	}
	g.results++
	if g.results == g.afterResults {
		g.wait()
	}
	return progress, nil
}

// TestToolBatchResumesAtCommittedBoundaries 验证真实执行在回复接纳、部分结果和批次结束三个提交点恢复时保留原工具事实。
func TestToolBatchResumesAtCommittedBoundaries(t *testing.T) {
	for name, saved := range map[string]int{"accepted": 0, "partial": 1, "complete": 5} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			database, ports, path := admissionDatabase(t)
			schedules, err := store.NewScheduleStore(database)
			if err != nil {
				t.Fatal(err)
			}
			application := &app.App{Schedule: schedules}
			date := time.Now().Add(48 * time.Hour).Truncate(time.Minute)
			if _, _, err := application.CreateSchedule(ctx, model.Schedule{
				Name:   "真实会议",
				Type:   model.SingleSchedule,
				Single: &model.Single{NotifyTime: date},
			}); err != nil {
				t.Fatal(err)
			}
			calls := []chat.ToolRequest{
				toolRequest("object", "get_schedule", " {\"id\": 1} "),
				toolRequest("list", "list_schedules", `{}`),
				toolRequest("truncated", "get_schedule", `{"id":`),
				toolRequest("missing", "get_schedule", `{"id":9999}`),
				toolRequest("date", "query_occurrences", `{"date":"`+date.Format("2006-01-02")+`"}`),
			}
			var requests atomic.Int32
			inputs := make(chan chat.ModelRequest, 3)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				var input chat.ModelRequest
				if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
					t.Error(err)
					return
				}
				inputs <- input
				w.Header().Set("Content-Type", "text/event-stream")
				if requests.Add(1) == 1 {
					emitToolFinish(w, calls, "length")
				} else {
					emitText(w, "已按真实结果处理")
				}
			}))
			t.Cleanup(upstream.Close)
			gate := &toolCommitGate{
				ToolRepository: ports.Tools,
				afterResults:   saved,
				committed:      make(chan struct{}),
				release:        make(chan struct{}),
			}
			gated := ports
			gated.Tools = gate
			service := chat.NewService(gated, completion.NewClient(nil), toolSettings(upstream.URL), queryRegistry(t, application))
			if err := service.Start(ctx); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(service.Stop)
			t.Cleanup(func() { gate.once.Do(func() { close(gate.release) }) })
			receipt, err := service.CreateRun(ctx, admissionInput("snapshot", "query"))
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-gate.committed:
			case <-time.After(5 * time.Second):
				t.Fatal("未到达工具提交边界")
			}
			before, err := ports.Execution.ReadCall(ctx, receipt.RunID)
			if err != nil || before == nil || len(before.CompletedTools) != saved {
				t.Fatalf("提交边界进度不符: %+v %v", before, err)
			}
			wantStage := map[int]string{0: "model_call_end", 1: "tool_start", 5: "tool_end"}[saved]
			if before.CheckpointStage != wantStage {
				t.Fatalf("批次提前或延后结束: %s", before.CheckpointStage)
			}
			original, err := ports.Tools.ReadTools(ctx, before.ID)
			if err != nil {
				t.Fatal(err)
			}
			// 在暂停的事务边界取得一致快照，之后原实例继续完成；恢复实例只看到此刻已提交的事实。
			backup := filepath.Join(t.TempDir(), "crash.db")
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
				t.Fatalf("原执行失败: %+v", run)
			}
			service.Stop()
			awaitHistoryRequest(t, inputs)
			baseline := awaitHistoryRequest(t, inputs)
			assertToolResultOrder(t, service, receipt.RunID, baseline, calls)

			restoredDB := store.NewSqlite()
			if err := restoredDB.Connect(ctx, backup); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = restoredDB.Close() })
			restored := store.NewChatStorage(restoredDB)
			restoredSchedules, err := store.NewScheduleStore(restoredDB)
			if err != nil {
				t.Fatal(err)
			}
			restarted := chat.NewService(restored, completion.NewClient(nil), toolSettings(upstream.URL), queryRegistry(t, &app.App{Schedule: restoredSchedules}))
			if err := restarted.Start(ctx); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(restarted.Stop)
			if run := awaitRun(t, restarted, receipt.RunID); run.Status != chat.StatusSucceeded || requests.Load() != 3 {
				t.Fatalf("恢复重发成功模型调用或未完成: %+v requests=%d", run, requests.Load())
			}
			replay := awaitHistoryRequest(t, inputs)
			assertToolResultOrder(t, restarted, receipt.RunID, replay, calls)
			if !reflect.DeepEqual(replay.Messages[:len(before.CheckpointContext)], before.CheckpointContext) {
				t.Fatalf("恢复覆盖了已提交上下文: %s", mustJSON(replay))
			}
			after, err := restored.Tools.ReadTools(ctx, before.ID)
			if err != nil || len(after) != len(calls) {
				t.Fatalf("恢复工具事实不完整: %+v %v", after, err)
			}
			for _, old := range original {
				for _, current := range after {
					if current.Request.ID != old.Request.ID {
						continue
					}
					if current.ID != old.ID || current.Request != old.Request || current.MaxRetry != old.MaxRetry {
						t.Fatalf("恢复改变了原身份或参数: %+v -> %+v", old, current)
					}
					if old.State == "running" {
						if current.RetryCount != old.RetryCount+1 {
							t.Fatalf("未确认查询恢复预算不符: %+v", current)
						}
					} else if !reflect.DeepEqual(current, old) {
						t.Fatalf("已保存结果被重执或改写: %+v -> %+v", old, current)
					}
				}
			}
		})
	}
}

// TestSavedToolJSONAndExhaustedRecoveryRemainPaired 验证独立版本 3 样本中的任意 JSON 类型、完成次序与耗尽预算保持兼容。
func TestSavedToolJSONAndExhaustedRecoveryRemainPaired(t *testing.T) {
	for name, order := range map[string][]int{
		"forward": {0, 1, 2, 3, 4, 5},
		"reverse": {5, 4, 3, 2, 1, 0},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			_, ports, path := admissionDatabase(t)
			record := chat.Record{
				Run: chat.Run{
					RunID:     "old-tools",
					SessionID: "old-session",
					Input:     "查询",
					Provider:  "test",
					Model:     "model",
					CreatedAt: time.Now(),
				},
				State:          chat.StateRunning,
				IdempotencyKey: "original",
			}
			if err := ports.Runs.Create(ctx, record); err != nil {
				t.Fatal(err)
			}
			values := []string{`null`, `true`, `7`, `"原始结果"`, `[1,"二"]`, `{"name":"旧事实"}`}
			calls := []chat.ToolRequest{}
			for index := range values {
				calls = append(calls, toolRequest(fmt.Sprintf("saved-%d", index), "get_schedule", ` {"id": 1} `))
			}
			calls = append(calls, toolRequest("unconfirmed", "get_schedule", `{"id":1}`))
			messages := []chat.Message{
				{
					Role:    "user",
					Content: record.Input,
				},
				{
					Role:      "assistant",
					ToolCalls: json.RawMessage(mustJSON(calls)),
				},
			}
			completed := []string{}
			for _, index := range order {
				messages = append(messages, chat.Message{
					Role:       "tool",
					ToolCallID: calls[index].ID,
					Content:    values[index],
				})
				completed = append(completed, calls[index].ID)
			}
			// 样本不经新进度操作生成，直接保存版本 3 的既有磁盘字段和原始 JSON。
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			request := chat.ModelRequest{
				Model:    "model",
				Messages: messages[:1],
			}
			output := chat.ModelOutput{
				Message:      messages[1],
				FinishReason: "tool_calls",
			}
			result, err := db.Exec(`INSERT INTO ai_model_calls(run_id,session_id,input,output,status,max_retry,created_at,finished_at) VALUES(?,?,?,?, 'succeeded',0,1,2)`, record.RunID, record.SessionID, mustJSON(request), mustJSON(output))
			if err != nil {
				t.Fatal(err)
			}
			id, err := result.LastInsertId()
			if err != nil {
				t.Fatal(err)
			}
			state := mustJSON(map[string]any{
				"context":        messages,
				"completed_tool": completed,
				"last_tool":      "tool_use: get_schedule-unconfirmed",
			})
			if _, err := db.Exec(`INSERT INTO ai_checkpoints(run_id,model_call_id,status,state) VALUES(?,?,'tool_start',?)`, record.RunID, id, state); err != nil {
				t.Fatal(err)
			}
			for index, call := range calls {
				status, retries := "succeeded", 0
				var value any
				if index < len(values) {
					value = values[index]
				} else {
					status, retries = "running", 3
				}
				if _, err := db.Exec(`INSERT INTO ai_tool_calls(run_id,session_id,model_call_id,tool_call_id,name,input,output,status,max_retry,retry_count,created_at) VALUES(?,?,?,?,?,?,?,?,3,?,1)`, record.RunID, record.SessionID, id, call.ID, call.Function.Name, mustJSON(call.Function.Arguments), value, status, retries); err != nil {
					t.Fatal(err)
				}
			}
			var requests atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				requests.Add(1)
				var input chat.ModelRequest
				if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
					t.Error(err)
					return
				}
				if len(input.Messages) != len(messages)+1 || !reflect.DeepEqual(input.Messages[:len(messages)], messages) {
					t.Errorf("恢复改变原 JSON 类型或完成次序: %s", mustJSON(input))
					return
				}
				last := input.Messages[len(messages)]
				var failure chat.RunError
				if last.ToolCallID != "unconfirmed" || json.Unmarshal([]byte(last.Content), &failure) != nil || failure.Code != "tool_result_unconfirmed" {
					t.Errorf("耗尽预算未按原 ID 回填: %+v", last)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				emitText(w, "复用已有事实")
			}))
			t.Cleanup(upstream.Close)
			// 恢复预算已耗尽；即使正式日程入口没有对应日程，也不得重新查询并改为 not_found。
			realDB := store.NewSqlite()
			if err := realDB.Connect(ctx, path); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = realDB.Close() })
			schedules, err := store.NewScheduleStore(realDB)
			if err != nil {
				t.Fatal(err)
			}
			service := chat.NewService(ports, completion.NewClient(nil), toolSettings(upstream.URL), queryRegistry(t, &app.App{Schedule: schedules}))
			if err := service.Start(ctx); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(service.Stop)
			if run := awaitRun(t, service, record.RunID); run.Status != chat.StatusSucceeded || requests.Load() != 1 {
				t.Fatalf("恢复未复用原成功模型调用: %+v requests=%d", run, requests.Load())
			}
			stored, err := ports.Tools.ReadTools(ctx, id)
			if err != nil || len(stored) != len(calls) {
				t.Fatalf("原工具事实丢失: %+v %v", stored, err)
			}
			for index, tool := range stored {
				if tool.Request != calls[index] {
					t.Fatalf("原参数或身份被改写: %+v", tool)
				}
				if index < len(values) && string(tool.Output) != values[index] {
					t.Fatalf("原始结果被改写: %s != %s", tool.Output, values[index])
				}
				if index == len(values) && (tool.RetryCount != 3 || tool.Error == nil || tool.Error.Code != "tool_result_unconfirmed") {
					t.Fatalf("恢复超出原预算: %+v", tool)
				}
			}
			page, err := service.GetRunEvents(ctx, record.RunID, 0, 100)
			if err != nil {
				t.Fatal(err)
			}
			results := 0
			for _, event := range page.Events {
				if event.Type == chat.EventToolStart {
					t.Fatal("恢复重复生成工具开始事件")
				}
				if event.Type == chat.EventToolResult {
					results++
				}
			}
			if results != 1 {
				t.Fatalf("恢复重复保存原结果: %s", mustJSON(page))
			}
		})
	}
}

func assertToolResultOrder(t *testing.T, service chat.RunService, run string, input chat.ModelRequest, calls []chat.ToolRequest) {
	t.Helper()
	page, err := service.GetRunEvents(context.Background(), run, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	results := []chat.Message{}
	starts := map[string]int{}
	for index, event := range page.Events {
		if event.ID != int64(index+1) {
			t.Fatalf("可见事件序号不连续: %s", mustJSON(page))
		}
		if event.Type == chat.EventToolStart {
			var data chat.ToolStartData
			if err := json.Unmarshal(event.Data, &data); err != nil {
				t.Fatal(err)
			}
			starts[data.Function[0].ID]++
		}
		if event.Type == chat.EventToolResult {
			var data chat.ToolResultData
			if err := json.Unmarshal(event.Data, &data); err != nil {
				t.Fatal(err)
			}
			content := string(data.Output)
			if data.Error != nil {
				content = mustJSON(data.Error)
			}
			results = append(results, chat.Message{
				Role:       "tool",
				ToolCallID: data.ToolCallID,
				Content:    content,
			})
		}
	}
	if len(results) != len(calls) || len(input.Messages) != len(calls)+2 || !reflect.DeepEqual(input.Messages[2:], results) {
		t.Fatalf("模型未按真实接纳次序接收全批结果: input=%s events=%s", mustJSON(input), mustJSON(page))
	}
	var actual []chat.ToolRequest
	if err := json.Unmarshal(input.Messages[1].ToolCalls, &actual); err != nil || !reflect.DeepEqual(actual, calls) {
		t.Fatalf("原工具请求或参数被改写: %s %v", input.Messages[1].ToolCalls, err)
	}
	seen := map[string]bool{}
	for _, result := range results {
		if starts[result.ToolCallID] != 1 || seen[result.ToolCallID] {
			t.Fatalf("工具开始或结果重复: %s", mustJSON(page))
		}
		seen[result.ToolCallID] = true
	}
}
