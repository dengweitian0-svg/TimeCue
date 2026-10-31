package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/agent/completion"
	"github.com/dengweitian0-svg/TimeCue/internal/agent/tools"
)

// TestCompactBeforeCreationAndReuse 验证创建前摘要覆盖全部旧消息，真实输入保留且后续不重复上传原文。
func TestCompactBeforeCreationAndReuse(t *testing.T) {
	requests := make(chan chat.ModelRequest, 8)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request chat.ModelRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		requests <- request
		if request.Messages[0].Role == "system" {
			emitText(w, "目标：保留日程42；创建成功，修改失败；还有待澄清事项。")
		} else if len(request.Messages) == 1 {
			emitText(w, strings.Repeat("old-history-", 1600))
		} else {
			emitText(w, "普通答复")
		}
	}))
	defer upstream.Close()
	_, ports, _ := admissionDatabase(t)
	settings := func() ([]chat.ModelConfig, error) {
		return []chat.ModelConfig{{
			Provider:        "test",
			Model:           "model",
			BaseURL:         upstream.URL,
			Key:             "secret",
			ContextWindow:   6500,
			MaxOutputTokens: 1000,
		}}, nil
	}
	service := startAdmissionService(t, ports, completion.NewClient(nil), settings)
	create := func(key string) chat.Run {
		t.Helper()
		input := admissionInput("compact", key)
		input.Input = " 本轮真实输入 " + key
		receipt, err := service.CreateRun(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		run := awaitFinishedRun(t, service, receipt.RunID)
		if run.Status != chat.StatusSucceeded {
			t.Fatalf("run=%+v", run)
		}
		return run
	}
	create("first")
	<-requests
	run := create("second")
	if run.Output == nil || *run.Output != "普通答复" {
		t.Fatalf("摘要泄漏到正文：%+v", run)
	}
	summaryRequest := <-requests
	if len(summaryRequest.Messages) != 2 || summaryRequest.Messages[0].Role != "system" || summaryRequest.Messages[1].Role != "user" || len(summaryRequest.Tools) != 0 || !strings.Contains(summaryRequest.Messages[1].Content, "old-history-") || strings.Contains(summaryRequest.Messages[1].Content, "second") {
		t.Fatalf("未按完整历史摘要：%s", mustJSON(summaryRequest))
	}
	ordinary := <-requests
	if len(ordinary.Messages) != 2 || ordinary.Messages[0].Role != "user" || !strings.Contains(ordinary.Messages[0].Content, "日程42") || ordinary.Messages[1].Content != run.Input || strings.Contains(mustJSON(ordinary), "old-history-") {
		t.Fatalf("生效投影错误：%s", mustJSON(ordinary))
	}
	page, err := service.GetRunEvents(context.Background(), run.RunID, 0, 100)
	if err != nil || len(page.Events) != 4 || string(page.Events[1].Type) != "compact" || string(page.Events[1].Data) != "{}" {
		t.Fatalf("摘要事件错误：%s %v", mustJSON(page), err)
	}
	create("third")
	next := <-requests
	if len(next.Messages) != 4 || next.Messages[0].Content != ordinary.Messages[0].Content || strings.Contains(mustJSON(next), "old-history-") {
		t.Fatalf("后续没有复用摘要：%s", mustJSON(next))
	}
}

// TestCompactUsesFixedPromptAndPairedBusinessFacts 验证固定提示原文、正式日程结果及失败/计划事实成组纳入摘要。
func TestCompactUsesFixedPromptAndPairedBusinessFacts(t *testing.T) {
	var calls atomic.Int32
	requests := make(chan chat.ModelRequest, 8)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request chat.ModelRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		requests <- request
		switch calls.Add(1) {
		case 1:
			emitTools(w, []chat.ToolRequest{toolRequest("created", "create_schedule", `{"name":"真实会议","type":"single","notifyTime":"2099-01-02 09:00"}`), toolRequest("failed", "update_schedule", `{"id":9999,"expectedRevision":1,"name":"失败修改"}`)})
		case 2:
			emitText(w, strings.Repeat("old-history-", 1600)+"仅计划删除；需要澄清日期；未确认的操作不能称为已生效。")
		case 3:
			emitText(w, "真实会议已创建；修改失败；删除仅计划；日期待澄清。")
		default:
			emitText(w, "普通答复")
		}
	}))
	defer upstream.Close()
	service, application, storage := newWriteFixture(t, upstream.URL, nil)
	first := startToolRun(t, service, "compact")
	if first.Status != chat.StatusSucceeded {
		t.Fatalf("run=%+v", first)
	}
	firstRequest := <-requests
	<-requests
	service.Stop()
	registry, err := tools.NewRegistry(context.Background(), append(tools.ScheduleTools(application), tools.ScheduleWriteTools(application, nil)...)...)
	if err != nil {
		t.Fatal(err)
	}
	service = chat.NewService(storage, completion.NewClient(nil), compactSettings(upstream.URL, nil), registry)
	if err = service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Stop)
	run := awaitFinishedRun(t, service, createCompactRun(t, service, "second").RunID)
	if run.Status != chat.StatusSucceeded {
		t.Fatalf("run=%+v", run)
	}
	request := <-requests
	if len(request.Messages) != 2 || len(request.Tools) != 0 || request.Model != "model" || request.MaxTokens != 1000 {
		t.Fatalf("summary request=%s", mustJSON(request))
	}
	document, err := os.ReadFile("../../../docs/AI-BACKEND.md")
	if err != nil {
		t.Fatal(err)
	}
	prompt := strings.Split(strings.Split(strings.ReplaceAll(string(document), "\r\n", "\n"), "```text\n你正在执行一次上下文检查点压缩。\n")[1], "\n```")[0]
	if request.Messages[0].Content != "你正在执行一次上下文检查点压缩。\n"+prompt {
		t.Fatal("专用提示未按确认原文发送")
	}
	var history []chat.Message
	if err = json.Unmarshal([]byte(request.Messages[1].Content), &history); err != nil {
		t.Fatal(err)
	}
	if len(history) != 5 || history[1].Role != "assistant" || history[2].Role != "tool" || history[3].Role != "tool" {
		t.Fatalf("工具没有成组纳入：%s", mustJSON(history))
	}
	results := map[string]string{}
	for _, message := range history {
		if message.Role == "tool" {
			results[message.ToolCallID] = message.Content
		}
	}
	if !strings.Contains(results["created"], "真实会议") || !strings.Contains(results["created"], `"revision":1`) || !strings.Contains(results["failed"], "schedule_not_found") || !strings.Contains(history[4].Content, "仅计划删除") || !strings.Contains(history[4].Content, "未确认") {
		t.Fatal("业务实际结果或不确定性丢失")
	}
	ordinary := <-requests
	if len(ordinary.Messages) != 3 || ordinary.Messages[0].Content != firstRequest.Messages[0].Content || ordinary.Messages[1].Role != "user" || ordinary.Messages[1].Content != "真实会议已创建；修改失败；删除仅计划；日期待澄清。" || ordinary.Messages[2].Content != run.Input || len(ordinary.Tools) == 0 {
		t.Fatalf("ordinary=%s", mustJSON(ordinary))
	}
	schedules, err := application.ListSchedules(context.Background())
	if err != nil || len(schedules) != 1 || schedules[0].Name != "真实会议" {
		t.Fatalf("摘要改变业务事实：%+v %v", schedules, err)
	}
}

// TestCompactFailureRetainsPreviousSummary 验证已有摘要的后续压缩失败仍沿原有效摘要与未覆盖消息接续。
func TestCompactFailureRetainsPreviousSummary(t *testing.T) {
	var expanded atomic.Bool
	var summaries atomic.Int32
	requests := make(chan chat.ModelRequest, 8)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request chat.ModelRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		requests <- request
		if request.Messages[0].Role == "system" {
			if summaries.Add(1) == 1 {
				emitText(w, "原有效摘要")
			} else {
				w.WriteHeader(401)
			}
		} else if len(request.Messages) <= 2 {
			emitText(w, strings.Repeat("old-history-", 1600))
		} else {
			emitText(w, "后续成功")
		}
	}))
	defer upstream.Close()
	_, ports, _ := admissionDatabase(t)
	service := startAdmissionService(t, ports, completion.NewClient(nil), compactSettings(upstream.URL, &expanded))
	awaitFinishedRun(t, service, createCompactRun(t, service, "first").RunID)
	<-requests
	awaitFinishedRun(t, service, createCompactRun(t, service, "second").RunID)
	<-requests
	<-requests
	run := awaitFinishedRun(t, service, createCompactRun(t, service, "third").RunID)
	if run.Status != chat.StatusFailed {
		t.Fatalf("run=%+v", run)
	}
	recompact := <-requests
	if !strings.Contains(recompact.Messages[1].Content, "原有效摘要") || strings.Contains(recompact.Messages[1].Content, "first") {
		t.Fatal("再次摘要没有沿有效历史")
	}
	expanded.Store(true)
	awaitFinishedRun(t, service, createCompactRun(t, service, "fourth").RunID)
	next := <-requests
	if next.Messages[0].Content != "原有效摘要" || !strings.Contains(mustJSON(next), "old-history-") || !strings.Contains(mustJSON(next), "third") {
		t.Fatal("失败替换了旧有效分界")
	}
}

// TestCompactSharesContinuationBudget 验证摘要已用续写计入本 run 后续普通回复的统一预算。
func TestCompactSharesContinuationBudget(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request chat.ModelRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		if request.Messages[0].Role == "system" {
			if len(request.Messages) == 2 {
				emitLengthDelta(w, "content", "摘要前段", "length")
			} else {
				emitText(w, "摘要后段")
			}
		} else if len(request.Messages) == 1 {
			emitText(w, strings.Repeat("old-history-", 1600))
		} else {
			emitLengthDelta(w, "content", "普通正文", "length")
		}
	}))
	defer upstream.Close()
	_, ports, _ := admissionDatabase(t)
	service := startAdmissionService(t, ports, completion.NewClient(nil), compactSettings(upstream.URL, nil))
	awaitFinishedRun(t, service, createCompactRun(t, service, "first").RunID)
	run := awaitFinishedRun(t, service, createCompactRun(t, service, "second").RunID)
	if run.Status != chat.StatusFailed || run.Error == nil || run.Error.Code != "model_continue_limit_exceeded" || run.Output == nil || *run.Output != "普通正文" {
		t.Fatalf("run=%+v", run)
	}
	count, err := ports.Execution.ContinuationCallCount(context.Background(), run.RunID)
	if err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
}

// TestCompactLargeValidSummaryHasNoHardTokenGate 验证约 10k 是目标而非精确门禁，成功后不因仍达阈值递归压缩。
func TestCompactLargeValidSummaryHasNoHardTokenGate(t *testing.T) {
	var calls atomic.Int32
	summary := strings.Repeat("s", 42500)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request chat.ModelRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		switch calls.Add(1) {
		case 1:
			emitText(w, strings.Repeat("h", 42000))
		case 2:
			emitText(w, summary)
		default:
			if len(request.Messages) != 2 || request.Messages[0].Content != summary {
				t.Error("大摘要投影错误或发生递归摘要")
			}
			emitText(w, "普通答复")
		}
	}))
	defer upstream.Close()
	_, ports, _ := admissionDatabase(t)
	service := startAdmissionService(t, ports, completion.NewClient(nil), func() ([]chat.ModelConfig, error) {
		configs, err := compactSettings(upstream.URL, nil)()
		configs[0].ContextWindow = 30000
		configs[0].MaxOutputTokens = 17000
		return configs, err
	})
	awaitFinishedRun(t, service, createCompactRun(t, service, "first").RunID)
	run := awaitFinishedRun(t, service, createCompactRun(t, service, "second").RunID)
	if run.Status != chat.StatusSucceeded || calls.Load() != 3 {
		t.Fatalf("run=%+v calls=%d", run, calls.Load())
	}
}

// TestCompactRetryContinuationAndRestart 验证摘要重试与续写沿原身份恢复，专用提示及正文顺序保持不变。
func TestCompactRetryContinuationAndRestart(t *testing.T) {
	ctx := context.Background()
	var summaries, continuations atomic.Int32
	requests := make(chan chat.ModelRequest, 10)
	blocked := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request chat.ModelRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		requests <- request
		if request.Messages[0].Role != "system" {
			if len(request.Messages) == 1 {
				emitText(w, strings.Repeat("old-history-", 1600))
			} else {
				emitText(w, "普通答复")
			}
			return
		}
		if len(request.Messages) == 2 {
			if summaries.Add(1) == 1 {
				w.WriteHeader(503)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"摘要思考\"}}]}\n\n")
			emitLengthDelta(w, "content", "目标与实际结果；", "length")
			return
		}
		if continuations.Add(1) == 1 {
			close(blocked)
			<-r.Context().Done()
			return
		}
		emitText(w, "尚未完成与待澄清事项。")
	}))
	defer upstream.Close()
	_, ports, _ := admissionDatabase(t)
	baseSettings := compactSettings(upstream.URL, nil)
	settings := func() ([]chat.ModelConfig, error) {
		cfg, err := baseSettings()
		cfg[0].MaxRetry = 1
		cfg[0].MaxRounds = 1
		return cfg, err
	}
	service := startAdmissionService(t, ports, completion.NewClient(nil), settings)
	awaitFinishedRun(t, service, createCompactRun(t, service, "first").RunID)
	<-requests
	receipt := createCompactRun(t, service, "second")
	planned := awaitRetryWait(t, ports, receipt.RunID, 1)
	if planned.CheckpointStage != "compact" {
		t.Fatalf("stage=%s", planned.CheckpointStage)
	}
	service.Stop()
	service = startAdmissionService(t, ports, completion.NewClient(nil), settings)
	select {
	case <-blocked:
	case <-time.After(10 * time.Second):
		t.Fatal("摘要续写没有开始")
	}
	service.Stop()
	continuation, err := ports.Execution.ReadCall(ctx, receipt.RunID)
	if err != nil || continuation.CheckpointStage != "compact" || !continuation.Continuation {
		t.Fatalf("call=%+v err=%v", continuation, err)
	}
	service = startAdmissionService(t, ports, completion.NewClient(nil), settings)
	run := awaitFinishedRun(t, service, receipt.RunID)
	if run.Status != chat.StatusSucceeded || run.Output == nil || *run.Output != "普通答复" {
		t.Fatalf("run=%+v", run)
	}
	first, retry, original, replayed, ordinary := <-requests, <-requests, <-requests, <-requests, <-requests
	if mustJSON(first) != mustJSON(retry) || mustJSON(original) != mustJSON(replayed) {
		t.Fatal("恢复改变了实际请求")
	}
	if len(original.Messages) != 4 || original.Messages[2].Role != "assistant" || original.Messages[2].Content != "目标与实际结果；" || original.Messages[2].ReasoningContent != "摘要思考" || original.Messages[3].Content != "继续未完成的输出" {
		t.Fatalf("续写边界=%s", mustJSON(original.Messages))
	}
	if ordinary.Messages[0].Content != "目标与实际结果；尚未完成与待澄清事项。" {
		t.Fatalf("摘要投影=%s", mustJSON(ordinary.Messages))
	}
	page := eventPage(t, service, run.RunID, 0, 100)
	count := 0
	for i, e := range page.Events {
		if e.ID != int64(i+1) {
			t.Fatal("事件序号不连续")
		}
		if e.Type == chat.EventCompact {
			count++
		}
	}
	used, err := ports.Execution.ContinuationCallCount(ctx, run.RunID)
	if count != 1 || used != 1 || err != nil {
		t.Fatalf("compact=%d continuations=%d err=%v", count, used, err)
	}
}

type compactSwitchFault struct {
	chat.ExecutionRepository
	mode    string
	entered chan struct{}
	release chan struct{}
}

// CompleteCompact 在真实原子切换事务内注入外键失败，或模拟提交成功但回执丢失。
func (f *compactSwitchFault) CompleteCompact(ctx context.Context, call chat.ModelCall, next *chat.ModelCall) (chat.ModelCall, error) {
	if f.mode == "cancel" {
		close(f.entered)
		<-f.release
		return f.ExecutionRepository.CompleteCompact(context.Background(), call, next)
	}
	if f.mode == "rollback" {
		invalid := *next
		invalid.SessionID = "missing-session"
		return f.ExecutionRepository.CompleteCompact(ctx, call, &invalid)
	}
	result, err := f.ExecutionRepository.CompleteCompact(ctx, call, next)
	if err == nil {
		err = errors.New("丢失提交回执")
	}
	return result, err
}

// TestCompactAtomicSwitchFailure 验证切换事务回滚保留旧历史，已提交却丢失回执时保留成功摘要且不重复请求。
func TestCompactAtomicSwitchFailure(t *testing.T) {
	for _, mode := range []string{"rollback", "lost-ack", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			var expanded atomic.Bool
			requests := make(chan chat.ModelRequest, 6)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request chat.ModelRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				requests <- request
				if request.Messages[0].Role == "system" {
					emitText(w, "有效摘要")
				} else if len(request.Messages) == 1 {
					emitText(w, strings.Repeat("old-history-", 1600))
				} else {
					emitText(w, "后续成功")
				}
			}))
			defer upstream.Close()
			_, ports, _ := admissionDatabase(t)
			fault := &compactSwitchFault{
				ExecutionRepository: ports.Execution,
				mode:                mode,
				entered:             make(chan struct{}),
				release:             make(chan struct{}),
			}
			ports.Execution = fault
			service := startAdmissionService(t, ports, completion.NewClient(nil), compactSettings(upstream.URL, &expanded))
			awaitFinishedRun(t, service, createCompactRun(t, service, "first").RunID)
			<-requests
			receipt := createCompactRun(t, service, "second")
			if mode == "cancel" {
				select {
				case <-fault.entered:
				case <-time.After(5 * time.Second):
					t.Fatal("摘要没有进入切换")
				}
				if _, err := service.CancelRun(context.Background(), receipt.RunID); err != nil {
					t.Fatal(err)
				}
				close(fault.release)
			}
			run := awaitFinishedRun(t, service, receipt.RunID)
			if mode == "cancel" && run.Status != chat.StatusCancelled || mode != "cancel" && (run.Status != chat.StatusFailed || run.Error == nil || run.Error.Code != "persistence_failed") {
				t.Fatalf("run=%+v", run)
			}
			<-requests
			expanded.Store(true)
			awaitFinishedRun(t, service, createCompactRun(t, service, "third").RunID)
			next := <-requests
			if mode != "lost-ack" && !strings.Contains(mustJSON(next), "old-history-") || mode == "lost-ack" && (next.Messages[0].Content != "有效摘要" || strings.Contains(mustJSON(next), "old-history-")) {
				t.Fatal("摘要原子切换被破坏")
			}
		})
	}
}

func compactSettings(base string, expanded *atomic.Bool) func() ([]chat.ModelConfig, error) {
	return func() ([]chat.ModelConfig, error) {
		window := 6500
		if expanded != nil && expanded.Load() {
			window = 100000
		}
		return []chat.ModelConfig{{
			Provider:        "test",
			Model:           "model",
			BaseURL:         base,
			Key:             "secret",
			ContextWindow:   window,
			MaxOutputTokens: 1000,
			MaxContinue:     1,
		}}, nil
	}
}

func createCompactRun(t *testing.T, service *chat.Runtime, key string) chat.Receipt {
	t.Helper()
	input := admissionInput("compact", key)
	input.Input = "本轮真实输入 " + key
	receipt, err := service.CreateRun(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

// TestCompactFailureKeepsEffectiveHistory 验证失败、部分输出、取消及续写耗尽不启用摘要或丢失旧历史。
func TestCompactFailureKeepsEffectiveHistory(t *testing.T) {
	for _, mode := range []string{"auth", "partial", "cancel", "limit", "tools", "empty"} {
		t.Run(mode, func(t *testing.T) {
			var expanded atomic.Bool
			var calls atomic.Int32
			requests := make(chan chat.ModelRequest, 8)
			started := make(chan struct{}, 2)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request chat.ModelRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				requests <- request
				calls.Add(1)
				if request.Messages[0].Role != "system" {
					if len(request.Messages) == 1 {
						emitText(w, strings.Repeat("old-history-", 1600))
					} else {
						emitText(w, "后续成功")
					}
					return
				}
				switch mode {
				case "auth":
					w.WriteHeader(401)
				case "partial":
					w.Header().Set("Content-Type", "text/event-stream")
					w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"不完整摘要\"}}]}\n\n"))
				case "cancel":
					started <- struct{}{}
					<-r.Context().Done()
				case "limit":
					emitLengthDelta(w, "content", "不完整摘要", "length")
				case "tools":
					emitTools(w, []chat.ToolRequest{toolRequest("forbidden", "get_schedule", `{"id":1}`)})
				case "empty":
					emitLengthDelta(w, "reasoning_content", "只有思考", "stop")
				}
			}))
			defer upstream.Close()
			_, ports, _ := admissionDatabase(t)
			service := startAdmissionService(t, ports, completion.NewClient(nil), compactSettings(upstream.URL, &expanded))
			awaitFinishedRun(t, service, createCompactRun(t, service, "first").RunID)
			<-requests
			receipt := createCompactRun(t, service, "second")
			if mode == "cancel" {
				select {
				case <-started:
				case <-time.After(5 * time.Second):
					t.Fatal("摘要没有开始")
				}
				if _, err := service.CancelRun(context.Background(), receipt.RunID); err != nil {
					t.Fatal(err)
				}
			}
			run := awaitFinishedRun(t, service, receipt.RunID)
			wantCode := map[string]string{"auth": "model_auth_failed", "partial": "model_network_error", "limit": "model_continue_limit_exceeded", "tools": "model_invalid_format", "empty": "model_invalid_format"}[mode]
			if mode == "cancel" {
				if run.Status != chat.StatusCancelled {
					t.Fatalf("run=%+v", run)
				}
			} else if run.Status != chat.StatusFailed || run.Error == nil || run.Error.Code != wantCode {
				t.Fatalf("run=%+v error=%+v", run, run.Error)
			}
			if run.Output != nil {
				t.Fatalf("摘要进入正文：%+v", run)
			}
			page := eventPage(t, service, run.RunID, 0, 100)
			compacts := 0
			for _, e := range page.Events {
				if e.Type == chat.EventCompact {
					compacts++
				}
			}
			if compacts != 1 {
				t.Fatalf("compact=%d", compacts)
			}
			<-requests
			if mode == "limit" {
				<-requests
			}
			expanded.Store(true)
			awaitFinishedRun(t, service, createCompactRun(t, service, "third").RunID)
			next := <-requests
			encoded := mustJSON(next.Messages)
			if !strings.Contains(encoded, "old-history-") || !strings.Contains(encoded, "second") || !strings.Contains(encoded, "上一轮回复未完成") || strings.Contains(encoded, "不完整摘要") || strings.Contains(encoded, "只有思考") || strings.Contains(encoded, "上下文检查点压缩") {
				t.Fatal("摘要失败破坏了有效历史")
			}
			wantCalls := int32(3)
			if mode == "limit" {
				wantCalls++
			}
			if calls.Load() != wantCalls {
				t.Fatalf("requests=%d", calls.Load())
			}
		})
	}
}

// TestCompactSuccessOverBudgetRemainsReusable 验证成功但超预算时保留摘要、不登记普通调用、不递归压缩。
func TestCompactSuccessOverBudgetRemainsReusable(t *testing.T) {
	var expanded atomic.Bool
	requests := make(chan chat.ModelRequest, 5)
	summary := strings.Repeat("valid-summary-", 1800)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request chat.ModelRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		requests <- request
		if request.Messages[0].Role == "system" {
			emitText(w, summary)
		} else if len(request.Messages) == 1 {
			emitText(w, strings.Repeat("old-history-", 1600))
		} else {
			emitText(w, "后续成功")
		}
	}))
	defer upstream.Close()
	_, ports, _ := admissionDatabase(t)
	service := startAdmissionService(t, ports, completion.NewClient(nil), compactSettings(upstream.URL, &expanded))
	awaitFinishedRun(t, service, createCompactRun(t, service, "first").RunID)
	<-requests
	run := awaitFinishedRun(t, service, createCompactRun(t, service, "second").RunID)
	if run.Status != chat.StatusFailed || run.Error == nil || run.Error.Code != "context_budget_exceeded" || run.Output != nil {
		t.Fatalf("run=%+v", run)
	}
	count, err := ports.Execution.OrdinaryCallCount(context.Background(), run.RunID)
	if err != nil || count != 0 {
		t.Fatalf("ordinary=%d err=%v", count, err)
	}
	<-requests
	expanded.Store(true)
	awaitFinishedRun(t, service, createCompactRun(t, service, "third").RunID)
	next := <-requests
	if next.Messages[0].Content != summary || strings.Contains(mustJSON(next), "old-history-") {
		t.Fatal("成功摘要没有复用")
	}
}
