package chat_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/agent/completion"
)

// TestNextRunUsesActualMessageSnapshot 验证多次逻辑调用后沿实际快照接续，不重复真实输入或丢失后端请求。
func TestNextRunUsesActualMessageSnapshot(t *testing.T) {
	_, ports, _ := admissionDatabase(t)
	ctx := context.Background()
	r := chat.Record{
		Run: chat.Run{
			RunID:     "previous",
			SessionID: "history",
			Provider:  "test",
			Model:     "model",
			Input:     " 原始输入\n",
			CreatedAt: time.Now(),
		},
		State:          chat.StateRunning,
		IdempotencyKey: "previous",
	}
	if err := ports.Runs.Create(ctx, r); err != nil {
		t.Fatal(err)
	}
	first := chat.ModelCall{
		RunID:     r.RunID,
		SessionID: r.SessionID,
		Input: chat.ModelRequest{Messages: []chat.Message{{
			Role:    "user",
			Content: r.Input,
		}}},
	}
	var err error
	first.ID, err = ports.Execution.BeginCall(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	// 后续切片的普通调用、续写和摘要均已把完整有效消息保存在实际请求中。
	messages := []chat.Message{
		{
			Role:    "system",
			Content: "固定提示",
		},
		{
			Role:    "user",
			Content: "已有摘要",
		},
		{
			Role:    "user",
			Content: r.Input,
		},
		{
			Role:      "assistant",
			ToolCalls: json.RawMessage(`[{"id":"original-tool","type":"function","function":{"name":"query_schedule","arguments":"{}"}}]`),
		},
		{
			Role:       "tool",
			Content:    `{"output":{"title":"原始结果"},"error":null}`,
			ToolCallID: "original-tool",
		},
		{
			Role:             "assistant",
			Content:          "第一段",
			ReasoningContent: "思考原文",
		},
		{
			Role:    "user",
			Content: "继续未完成的输出",
		},
	}
	last := chat.ModelCall{
		RunID:     r.RunID,
		SessionID: r.SessionID,
		Input:     chat.ModelRequest{Messages: messages},
		Output: &chat.ModelOutput{Message: chat.Message{
			Role:    "assistant",
			Content: "第二段",
		}},
		State: chat.StateFailed,
	}
	last.ID, err = ports.Execution.BeginCall(ctx, last)
	if err != nil {
		t.Fatal(err)
	}
	r.State = chat.StateFailed
	r.Error = &chat.RunError{
		Code:    "model_network_error",
		Message: "连接中断",
	}
	if err = ports.Execution.Finish(ctx, last, r); err != nil {
		t.Fatal(err)
	}
	requests := make(chan chat.ModelRequest, 2)
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var input chat.ModelRequest
		if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
			t.Error(err)
			return
		}
		requests <- input
		if calls.Add(1) == 1 {
			<-req.Context().Done()
			return
		}
		completedUpstreamForRetry(w)
	}))
	defer upstream.Close()
	service := startAdmissionService(t, ports, completion.NewClient(nil), retrySettings(upstream.URL, 0))
	input := admissionInput(r.SessionID, "next")
	input.Input = " 下一轮\n"
	receipt, err := service.CreateRun(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	actual := awaitHistoryRequest(t, requests)
	want := append(messages, last.Output.Message, chat.Message{
		Role:    "user",
		Content: "上一轮回复未完成；其操作表述不代表业务已经生效。",
	}, chat.Message{
		Role:    "user",
		Content: input.Input,
	})
	if !reflect.DeepEqual(actual.Messages, want) {
		t.Fatalf("实际模型历史不符:\ngot  %+v\nwant %+v", actual.Messages, want)
	}
	call, err := ports.Execution.ReadCall(ctx, receipt.RunID)
	if err != nil || call == nil || !reflect.DeepEqual(call.Input.Messages, want) {
		t.Fatalf("实际 input 未持久化: %+v %v", call, err)
	}
	if !reflect.DeepEqual(call.CheckpointContext, want) {
		t.Fatalf("请求前 checkpoint 未保存实际上下文: %+v", call.CheckpointContext)
	}
	service.Stop()
	resumed := startAdmissionService(t, ports, completion.NewClient(nil), retrySettings(upstream.URL, 0))
	awaitRun(t, resumed, receipt.RunID)
	replayed := awaitHistoryRequest(t, requests)
	if !reflect.DeepEqual(replayed, actual) {
		t.Fatalf("恢复重复追加或改写中断说明: %+v", replayed)
	}
	old, err := service.GetRun(ctx, r.RunID)
	if err != nil || old.Input != " 原始输入\n" {
		t.Fatalf("原始 run.input 被改写: %+v %v", old, err)
	}
}

func awaitHistoryRequest(t *testing.T, requests <-chan chat.ModelRequest) chat.ModelRequest {
	t.Helper()
	select {
	case request := <-requests:
		return request
	case <-time.After(3 * time.Second):
		t.Fatal("没有收到实际模型请求")
		return chat.ModelRequest{}
	}
}

// TestInterruptedRepliesRemainExactInNextRun 验证失败、取消、仅思考及无输出均如实进入下一轮，不伪造空 assistant。
func TestInterruptedRepliesRemainExactInNextRun(t *testing.T) {
	for _, mode := range []string{"failed_text_thinking", "failed_thinking", "failed_empty", "cancelled_text_thinking", "cancelled_thinking"} {
		t.Run(mode, func(t *testing.T) {
			_, ports, _ := admissionDatabase(t)
			requests := make(chan chat.ModelRequest, 3)
			var calls atomic.Int32
			thinking, text := "想想想想想", "原文原文原文原文原文"
			if !strings.Contains(mode, "text") {
				text = ""
			}
			if mode == "failed_empty" {
				thinking = ""
			}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				var input chat.ModelRequest
				if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
					t.Error(err)
					return
				}
				requests <- input
				if calls.Add(1) > 1 {
					completedUpstreamForRetry(w)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				for i := 0; i < 5; i++ {
					delta := map[string]string{}
					if text != "" {
						delta["content"] = "原文"
					}
					if thinking != "" {
						delta["reasoning_content"] = "想"
					}
					encoded, _ := json.Marshal(delta)
					fmt.Fprintf(w, "data: {\"id\":\"partial\",\"choices\":[{\"delta\":%s}]}\n\n", encoded)
				}
				w.(http.Flusher).Flush()
				if strings.HasPrefix(mode, "cancelled") {
					<-req.Context().Done()
				}
				// 无正常结束信号的流在模型故障边界注入断流。
			}))
			defer upstream.Close()
			service := startAdmissionService(t, ports, completion.NewClient(nil), retrySettings(upstream.URL, 0))
			ctx := context.Background()
			input := admissionInput("partial", "one")
			input.Input = " 原始用户输入\n"
			first, err := service.CreateRun(ctx, input)
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(mode, "cancelled") {
				deadline := time.Now().Add(3 * time.Second)
				for {
					page, err := service.GetRunEvents(ctx, first.RunID, 0, 100)
					if err != nil {
						t.Fatal(err)
					}
					if len(page.Events) >= 6 {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("部分输出未保存")
					}
					time.Sleep(time.Millisecond)
				}
				if _, err = service.CancelRun(ctx, first.RunID); err != nil {
					t.Fatal(err)
				}
			}
			run := awaitRun(t, service, first.RunID)
			wantStatus := chat.StatusFailed
			if strings.HasPrefix(mode, "cancelled") {
				wantStatus = chat.StatusCancelled
			}
			if run.Status != wantStatus || run.Input != input.Input || text != "" && (run.Output == nil || *run.Output != text) || text == "" && run.Output != nil {
				t.Fatalf("中断查询未保留原文: %+v", run)
			}
			if wantStatus == chat.StatusFailed && (run.Error == nil || run.Error.Code != "model_network_error") {
				t.Fatalf("断流错误丢失: %+v", run.Error)
			}
			oldCall, err := ports.Execution.ReadCall(ctx, first.RunID)
			if err != nil || oldCall == nil || oldCall.Output == nil {
				t.Fatalf("部分回复记录缺失: %+v %v", oldCall, err)
			}
			input.Input, input.IdempotencyKey = "继续", "two"
			second, err := service.CreateRun(ctx, input)
			if err != nil {
				t.Fatal(err)
			}
			awaitRun(t, service, second.RunID)
			<-requests
			actual := <-requests
			want := []chat.Message{{
				Role:    "user",
				Content: " 原始用户输入\n",
			}}
			if thinking != "" || text != "" {
				want = append(want, chat.Message{
					Role:             "assistant",
					Content:          text,
					ReasoningContent: thinking,
				})
			}
			want = append(want, chat.Message{
				Role:    "user",
				Content: "上一轮回复未完成；其操作表述不代表业务已经生效。",
			}, chat.Message{
				Role:    "user",
				Content: "继续",
			})
			if !reflect.DeepEqual(actual.Messages, want) {
				t.Fatalf("中断历史不符:\ngot=%+v\nwant=%+v", actual.Messages, want)
			}
			after, err := ports.Execution.ReadCall(ctx, first.RunID)
			if err != nil || !reflect.DeepEqual(after, oldCall) {
				t.Fatalf("下一轮改写了原始部分调用: %+v %v", after, err)
			}
		})
	}
}

// TestRecoveredOrdinaryRoundBudgetUsesLogicalCalls 验证普通调用跨重启计数，已开始的最后一轮可恢复，重试、续写和失败摘要不占新轮数。
func TestRecoveredOrdinaryRoundBudgetUsesLogicalCalls(t *testing.T) {
	for _, mode := range []string{"needs_next", "last_running", "excluded_calls", "custom_limit"} {
		t.Run(mode, func(t *testing.T) {
			_, ports, path := admissionDatabase(t)
			ctx := context.Background()
			r := chat.Record{
				Run: chat.Run{
					RunID:     "resume",
					SessionID: "rounds",
					Provider:  "test",
					Model:     "model",
					Input:     "最初的输入",
					CreatedAt: time.Now(),
				},
				State:          chat.StateRunning,
				IdempotencyKey: "rounds-key",
			}
			if err := ports.Runs.Create(ctx, r); err != nil {
				t.Fatal(err)
			}
			fixture, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer fixture.Close()
			var last chat.ModelCall
			logicalCalls := 20
			if mode == "custom_limit" {
				logicalCalls = 3
			}
			for i := 0; i < logicalCalls; i++ {
				last = chat.ModelCall{
					RunID:     r.RunID,
					SessionID: r.SessionID,
					Input: chat.ModelRequest{
						Model: "model",
						Messages: []chat.Message{{
							Role:    "user",
							Content: "实际请求快照",
						}},
						Stream:        true,
						MaxTokens:     8192,
						StreamOptions: map[string]bool{"include_usage": true},
					},
					MaxRetry: 3,
				}
				last.ID, err = ports.Execution.BeginCall(ctx, last)
				if err != nil {
					t.Fatal(err)
				}
				status := "succeeded"
				if mode == "last_running" && i == 19 {
					status = "running"
				}
				if _, err = fixture.Exec(`UPDATE ai_model_calls SET status=?,retry_count=3 WHERE id=?`, status, last.ID); err != nil {
					t.Fatal(err)
				}
				if mode == "excluded_calls" {
					if i < 10 {
						if _, err = fixture.Exec(`UPDATE ai_model_calls SET continuation=1 WHERE id=?`, last.ID); err != nil {
							t.Fatal(err)
						}
					} else if i < 19 {
						// compact 是否成功不影响摘要调用身份，不能计入普通轮数。
						if _, err = fixture.Exec(`INSERT INTO ai_events(run_id,session_id,model_call_id,type,data,seq,compact_succeeded,created_at) VALUES(?,?,?,'compact','{}',?,0,?)`, r.RunID, r.SessionID, last.ID, i+2, time.Now().UnixNano()); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			if mode != "last_running" {
				if _, err = fixture.Exec(`UPDATE ai_checkpoints SET status='tool_end' WHERE run_id=?`, r.RunID); err != nil {
					t.Fatal(err)
				}
			}
			requests := make(chan chat.ModelRequest, 2)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				var input chat.ModelRequest
				if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
					t.Error(err)
					return
				}
				requests <- input
				completedUpstreamForRetry(w)
			}))
			defer upstream.Close()
			settings := retrySettings(upstream.URL, 3)
			if mode == "custom_limit" {
				settings = func() ([]chat.ModelConfig, error) {
					configs, err := retrySettings(upstream.URL, 3)()
					configs[0].MaxRounds = 3
					return configs, err
				}
			}
			service := startAdmissionService(t, ports, completion.NewClient(nil), settings)
			run := awaitRun(t, service, r.RunID)
			if mode == "needs_next" || mode == "custom_limit" {
				if run.Status != chat.StatusFailed || run.Error == nil || run.Error.Code != "model_round_limit_exceeded" || len(requests) != 0 {
					t.Fatalf("普通轮数耗尽未停止: %+v requests=%d", run, len(requests))
				}
				var status string
				if err = fixture.QueryRow(`SELECT status FROM ai_model_calls WHERE id=?`, last.ID).Scan(&status); err != nil || status != "succeeded" {
					t.Fatalf("上限错误改写了成功模型调用: %s %v", status, err)
				}
			} else {
				if run.Status != chat.StatusSucceeded || len(requests) != 1 {
					t.Fatalf("已开始调用恢复或计数排除错误: %+v requests=%d", run, len(requests))
				}
				var count int
				if err = fixture.QueryRow(`SELECT COUNT(*) FROM ai_model_calls WHERE run_id=?`, r.RunID).Scan(&count); err != nil {
					t.Fatal(err)
				}
				want := 20
				if mode == "excluded_calls" {
					want = 21
				}
				if count != want {
					t.Fatalf("逻辑调用数量=%d want=%d", count, want)
				}
			}
		})
	}
}

// TestRequestBudgetMatchesPersistedAndWireBytes 验证预算使用实际上游请求的 UTF-8 序列化字节，包含转义和结构开销。
func TestRequestBudgetMatchesPersistedAndWireBytes(t *testing.T) {
	_, ports, _ := admissionDatabase(t)
	wire := make(chan []byte, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		raw, err := io.ReadAll(req.Body)
		if err != nil {
			t.Error(err)
			return
		}
		wire <- raw
		completedUpstreamForRetry(w)
	}))
	defer upstream.Close()
	service := startAdmissionService(t, ports, completion.NewClient(nil), retrySettings(upstream.URL, 0))
	input := admissionInput("wire", "one")
	input.Input = " 中文🙂 <>&\n\"\\ "
	receipt, err := service.CreateRun(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	awaitRun(t, service, receipt.RunID)
	call, err := ports.Execution.ReadCall(context.Background(), receipt.RunID)
	if err != nil || call == nil {
		t.Fatalf("读取调用失败: %v", err)
	}
	persisted, err := json.Marshal(call.Input)
	if err != nil {
		t.Fatal(err)
	}
	if actual := <-wire; !bytes.Equal(actual, persisted) {
		t.Fatalf("实际序列化请求与预算/持久化不一致:\nwire=%s\nsaved=%s", actual, persisted)
	}
}

// TestBudgetFailureIsAtomicAndReplayable 验证创建即返回失败回执，且没有模型调用或 checkpoint。
func TestBudgetFailureIsAtomicAndReplayable(t *testing.T) {
	_, ports, path := admissionDatabase(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		t.Error("预算拒绝不应请求上游")
	}))
	defer upstream.Close()
	service := startAdmissionService(t, ports, completion.NewClient(nil), retrySettings(upstream.URL, 0))
	input := admissionInput("budget", "one")
	input.Input = " " + strings.Repeat("中🙂\"\\", 15000) + "\n"
	receipt, err := service.CreateRun(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Status != chat.StatusFailed || receipt.Error == nil || receipt.Error.Code != "context_budget_exceeded" {
		t.Fatalf("创建未原子返回预算失败: %+v", receipt)
	}
	run, err := service.GetRun(context.Background(), receipt.RunID)
	if err != nil || run.Input != input.Input || run.FinishedAt == nil {
		t.Fatalf("失败原文或终态丢失: %+v %v", run, err)
	}
	call, err := ports.Execution.ReadCall(context.Background(), receipt.RunID)
	if err != nil || call != nil {
		t.Fatalf("预算拒绝不应创建调用/恢复入口: %+v %v", call, err)
	}
	inspection, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer inspection.Close()
	var calls, checkpoints, compacts int
	if err = inspection.QueryRow(`SELECT (SELECT COUNT(*) FROM ai_model_calls),(SELECT COUNT(*) FROM ai_checkpoints),(SELECT COUNT(*) FROM ai_events WHERE type='compact')`).Scan(&calls, &checkpoints, &compacts); err != nil || calls != 0 || checkpoints != 0 || compacts != 0 {
		t.Fatalf("预算拒绝遗留执行记录: calls=%d checkpoints=%d compacts=%d error=%v", calls, checkpoints, compacts, err)
	}
	page, err := service.GetRunEvents(context.Background(), receipt.RunID, 0, 100)
	if err != nil || len(page.Events) != 3 || page.Events[0].Type != chat.EventStart || page.Events[1].Type != chat.EventError || page.Events[2].Type != chat.EventEnd {
		t.Fatalf("预算拒绝事件不完整: %+v %v", page, err)
	}
	replay, err := service.CreateRun(context.Background(), input)
	if err != nil || !reflect.DeepEqual(replay, receipt) {
		t.Fatalf("预算失败未沿原键重放: %+v %v", replay, err)
	}
}

// TestRequestBudgetRoundsUTF8BytesUp 验证完整请求的 135/136/137 字节分别占用 34/34/35 个估算 token。
func TestRequestBudgetRoundsUTF8BytesUp(t *testing.T) {
	for _, text := range []string{"中🙂", "中🙂x", "中🙂xx"} {
		t.Run(text, func(t *testing.T) {
			_, ports, _ := admissionDatabase(t)
			var requests atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				requests.Add(1)
				completedUpstreamForRetry(w)
			}))
			defer upstream.Close()
			settings := func() ([]chat.ModelConfig, error) {
				models, err := retrySettings(upstream.URL, 0)()
				models[0].MaxOutputTokens = 8
				models[0].ContextWindow = 42
				return models, err
			}
			service := startAdmissionService(t, ports, completion.NewClient(nil), settings)
			input := admissionInput("boundary", "one")
			input.Input = text
			receipt, err := service.CreateRun(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			run := awaitRun(t, service, receipt.RunID)
			if text == "中🙂xx" {
				if run.Status != chat.StatusFailed || run.Error == nil || run.Error.Code != "context_budget_exceeded" || requests.Load() != 0 {
					t.Fatalf("137 字节必须向上取整为 35 token 并拒绝: %+v requests=%d", run, requests.Load())
				}
			} else if run.Status != chat.StatusSucceeded || requests.Load() != 1 {
				t.Fatalf("预算刚好等于 34 token 时应允许: %+v requests=%d", run, requests.Load())
			}
		})
	}
}

// TestHistorySelectsLatestSnapshotByCreationTime 验证插入 ID 与创建时间不一致时沿最新完整快照接续。
func TestHistorySelectsLatestSnapshotByCreationTime(t *testing.T) {
	_, ports, _ := admissionDatabase(t)
	ctx := context.Background()
	base := time.Now().Add(-time.Hour)
	want := []chat.Message{
		{
			Role:    "user",
			Content: "较早输入",
		},
		{
			Role:    "assistant",
			Content: "较早回复",
		},
		{
			Role:    "user",
			Content: "较晚输入",
		},
		{
			Role:    "assistant",
			Content: "较晚回复",
		},
		{
			Role:    "user",
			Content: "新输入",
		},
	}
	for _, later := range []bool{true, false} {
		id, index := "earlier", 0
		created := base
		if later {
			id, index, created = "later", 2, base.Add(time.Minute)
		}
		r := chat.Record{
			Run: chat.Run{
				RunID:     id,
				SessionID: "ordered",
				Input:     want[index].Content,
				Provider:  "test",
				Model:     "model",
				CreatedAt: created,
			},
			State:          chat.StateRunning,
			IdempotencyKey: id,
		}
		if err := ports.Runs.Create(ctx, r); err != nil {
			t.Fatal(err)
		}
		call := chat.ModelCall{
			RunID:     id,
			SessionID: r.SessionID,
			Input:     chat.ModelRequest{Messages: want[:index+1]},
			Output:    &chat.ModelOutput{Message: want[index+1]},
			State:     chat.StateSucceeded,
		}
		var err error
		call.ID, err = ports.Execution.BeginCall(ctx, call)
		if err != nil {
			t.Fatal(err)
		}
		r.State = chat.StateSucceeded
		if err = ports.Execution.Finish(ctx, call, r); err != nil {
			t.Fatal(err)
		}
	}
	requests := make(chan chat.ModelRequest, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var input chat.ModelRequest
		if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
			t.Error(err)
			return
		}
		requests <- input
		completedUpstreamForRetry(w)
	}))
	defer upstream.Close()
	service := startAdmissionService(t, ports, completion.NewClient(nil), retrySettings(upstream.URL, 0))
	input := admissionInput("ordered", "new")
	input.Input = "新输入"
	receipt, err := service.CreateRun(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	awaitRun(t, service, receipt.RunID)
	if actual := awaitHistoryRequest(t, requests); !reflect.DeepEqual(actual.Messages, want) {
		t.Fatalf("跨 run 的创建顺序或快照错误: %+v", actual.Messages)
	}
	session, err := service.GetSession(ctx, "ordered")
	if err != nil || !reflect.DeepEqual(session.RunIDs, []string{"earlier", "later", receipt.RunID}) {
		t.Fatalf("会话运行顺序错误: %+v %v", session, err)
	}
}
