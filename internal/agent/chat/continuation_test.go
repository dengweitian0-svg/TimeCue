package chat_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/agent/completion"
)

// TestLengthContinuesTextAndThinking 验证真实长度结束保留消息边界、原调用和连续事件。
func TestLengthContinuesTextAndThinking(t *testing.T) {
	for _, thinking := range []bool{false, true} {
		t.Run(fmt.Sprint(thinking), func(t *testing.T) {
			var calls atomic.Int32
			requests := make(chan chat.ModelRequest, 3)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request chat.ModelRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				requests <- request
				if calls.Add(1) == 1 {
					field := "content"
					if thinking {
						field = "reasoning_content"
					}
					emitLengthDelta(w, field, "第一段", "length")
					return
				}
				if thinking {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"第二段思考\"}}]}\n\n")
				}
				emitLengthDelta(w, "content", "第二段", "stop")
			}))
			defer upstream.Close()
			_, ports, _ := admissionDatabase(t)
			settings := retrySettings(upstream.URL, 0)
			service := startAdmissionService(t, ports, completion.NewClient(nil), func() ([]chat.ModelConfig, error) {
				configs, err := settings()
				configs[0].MaxRounds = 1
				return configs, err
			})
			receipt, err := service.CreateRun(context.Background(), admissionInput("length", "first"))
			if err != nil {
				t.Fatal(err)
			}
			run := awaitFinishedRun(t, service, receipt.RunID)
			want := "第一段第二段"
			if thinking {
				want = "第二段"
			}
			if run.Status != chat.StatusSucceeded || run.Output == nil || *run.Output != want || calls.Load() != 2 {
				t.Fatalf("run=%+v calls=%d", run, calls.Load())
			}
			<-requests
			input := <-requests
			if len(input.Messages) != 3 || input.Messages[1].Role != "assistant" || input.Messages[2].Role != "user" || input.Messages[2].Content != "继续未完成的输出" {
				t.Fatalf("continuation messages: %s", mustJSON(input.Messages))
			}
			if thinking && input.Messages[1].ReasoningContent != "第一段" || !thinking && input.Messages[1].Content != "第一段" {
				t.Fatalf("lost original output: %s", mustJSON(input.Messages))
			}
			page, err := service.GetRunEvents(context.Background(), run.RunID, 0, 100)
			wantEvents := 4
			if thinking {
				wantEvents = 5
			}
			if err != nil || len(page.Events) != wantEvents {
				t.Fatalf("events=%s err=%v", mustJSON(page), err)
			}
			var thoughts string
			for i, event := range page.Events {
				if event.ID != int64(i+1) {
					t.Fatalf("event sequence: %s", mustJSON(page))
				}
				if event.Type == chat.EventThinking {
					var data chat.ThinkingData
					if err := json.Unmarshal(event.Data, &data); err != nil {
						t.Fatal(err)
					}
					thoughts += data.Content
				}
			}
			if thinking && thoughts != "第一段第二段思考" {
				t.Fatalf("thinking increments changed: %s", thoughts)
			}
			ordinary, err := ports.Execution.OrdinaryCallCount(context.Background(), run.RunID)
			if err != nil || ordinary != 1 {
				t.Fatalf("ordinary=%d err=%v", ordinary, err)
			}
		})
	}
}

// TestLengthToolFeedbackRepairsOnlyContext 验证混合批次不丢失成功事实，修复只进入原 ID 的模型反馈。
func TestLengthToolFeedbackRepairsOnlyContext(t *testing.T) {
	var calls atomic.Int32
	requests := make(chan chat.ModelRequest, 4)
	raw := `{"id":1,"unused":`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		var request chat.ModelRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		requests <- request
		switch calls.Add(1) {
		case 1:
			emitToolFinish(w, []chat.ToolRequest{
				toolRequest("complete", "get_schedule", `{"id":1}`),
				toolRequest("broken", "get_schedule", raw),
			}, "length")
		case 2:
			if len(request.Messages) != 4 || request.Messages[3].Role != "tool" {
				t.Errorf("batch not paired or extra user: %s", mustJSON(request.Messages))
			}
			for _, message := range request.Messages {
				if message.ToolCallID != "broken" {
					continue
				}
				var feedback struct {
					Code    string          `json:"code"`
					Context json.RawMessage `json:"context_arguments"`
					Notice  string          `json:"notice"`
				}
				if err := json.Unmarshal([]byte(message.Content), &feedback); err != nil || feedback.Code != "tool_argument_parse_error" || string(feedback.Context) != `{"id":1}` || !strings.Contains(feedback.Notice, "未执行业务") || !strings.Contains(feedback.Notice, "完整调用") {
					t.Errorf("unsafe feedback: %s err=%v", message.Content, err)
				}
			}
			emitTools(w, []chat.ToolRequest{toolRequest("new-actual-id", "get_schedule", `{"id":1}`)})
		default:
			emitText(w, "最终结果")
		}
	}))
	defer upstream.Close()
	service, storage, _ := newToolFixture(t, upstream.URL)
	run := startToolRun(t, service, "length-feedback")
	if run.Status != chat.StatusSucceeded || calls.Load() != 3 {
		t.Fatalf("run=%+v calls=%d", run, calls.Load())
	}
	count, err := storage.Execution.ContinuationCallCount(context.Background(), run.RunID)
	if err != nil || count != 1 {
		t.Fatalf("continuations=%d err=%v", count, err)
	}
	page, err := service.GetRunEvents(context.Background(), run.RunID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	starts := map[string]int{}
	for _, event := range page.Events {
		if event.Type == chat.EventToolStart {
			var data chat.ToolStartData
			if err := json.Unmarshal(event.Data, &data); err != nil {
				t.Fatal(err)
			}
			for _, function := range data.Function {
				starts[function.ID]++
				if function.ID == "broken" && string(function.Argument) != "null" {
					t.Fatalf("fragment exposed: %s", event.Data)
				}
			}
		}
		if event.Type == chat.EventToolResult {
			var data chat.ToolResultData
			if err := json.Unmarshal(event.Data, &data); err != nil {
				t.Fatal(err)
			}
			if data.ToolCallID == "broken" && (data.Error == nil || data.Error.Code != "tool_argument_parse_error" || string(data.Output) != "null" || strings.Contains(string(event.Data), "context_arguments")) {
				t.Fatalf("repair leaked to raw result: %s", event.Data)
			}
		}
	}
	if starts["complete"] != 1 || starts["broken"] != 1 || starts["new-actual-id"] != 1 {
		t.Fatalf("tool repeated or IDs changed: %v", starts)
	}
	<-requests
	input := <-requests
	var original []chat.ToolRequest
	if err := json.Unmarshal(input.Messages[1].ToolCalls, &original); err != nil || original[1].Function.Arguments != raw {
		t.Fatalf("original arguments modified: %s %v", mustJSON(input), err)
	}
}

func emitLengthDelta(w http.ResponseWriter, field, content, reason string) {
	w.Header().Set("Content-Type", "text/event-stream")
	payload := map[string]any{"choices": []any{map[string]any{
		"delta": map[string]string{field: content}, "finish_reason": reason,
	}}}
	fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", mustJSON(payload))
}

// TestLengthContinuationSurvivesRestart 验证续写重发沿原请求和预算，保留先前正文及事件。
func TestLengthContinuationSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	var calls atomic.Int32
	started := make(chan struct{})
	inputs := make(chan chat.ModelRequest, 3)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request chat.ModelRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		inputs <- request
		switch calls.Add(1) {
		case 1:
			emitLengthDelta(w, "content", "保留前段", "length")
		case 2:
			close(started)
			<-r.Context().Done()
		default:
			emitLengthDelta(w, "content", "接续后段", "stop")
		}
	}))
	defer upstream.Close()
	_, storage, _ := admissionDatabase(t)
	settings := retrySettings(upstream.URL, 0)
	service := startAdmissionService(t, storage, completion.NewClient(nil), settings)
	receipt, err := service.CreateRun(ctx, admissionInput("restart-continuation", "first"))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("continuation did not start")
	}
	service.Stop()
	before := eventPage(t, service, receipt.RunID, 0, 100)
	firstCall, err := storage.Execution.ReadCall(ctx, receipt.RunID)
	if err != nil {
		t.Fatal(err)
	}
	service = startAdmissionService(t, storage, completion.NewClient(nil), settings)
	run := awaitFinishedRun(t, service, receipt.RunID)
	if run.Status != chat.StatusSucceeded || run.Output == nil || *run.Output != "保留前段接续后段" {
		t.Fatalf("run=%+v output=%v", run, run.Output)
	}
	lastCall, err := storage.Execution.ReadCall(ctx, receipt.RunID)
	count, countErr := storage.Execution.ContinuationCallCount(ctx, receipt.RunID)
	if err != nil || countErr != nil || firstCall.ID != lastCall.ID || count != 1 {
		t.Fatalf("recovery spent new call: first=%+v last=%+v count=%d err=%v %v", firstCall, lastCall, count, err, countErr)
	}
	<-inputs
	original, replay := <-inputs, <-inputs
	if string(mustJSON(original)) != string(mustJSON(replay)) {
		t.Fatalf("request changed: %s / %s", mustJSON(original), mustJSON(replay))
	}
	after := eventPage(t, service, receipt.RunID, 0, 100)
	if len(after.Events) <= len(before.Events) || string(mustJSON(after.Events[:len(before.Events)])) != string(mustJSON(before.Events)) {
		t.Fatalf("old events rewritten: before=%s after=%s", mustJSON(before), mustJSON(after))
	}
}

// TestLengthContinuationBudgetFailsWithAcceptedOutput 验证默认五次和配置预算耗尽均保留正常调用及全部正文。
func TestLengthContinuationBudgetFailsWithAcceptedOutput(t *testing.T) {
	for _, limit := range []int{0, 2} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				emitLengthDelta(w, "content", "段", "length")
			}))
			defer upstream.Close()
			_, ports, _ := admissionDatabase(t)
			settings := retrySettings(upstream.URL, 3)
			service := startAdmissionService(t, ports, completion.NewClient(nil), func() ([]chat.ModelConfig, error) {
				configs, err := settings()
				configs[0].MaxContinue, configs[0].MaxRounds = limit, 1
				return configs, err
			})
			receipt, err := service.CreateRun(context.Background(), admissionInput("budget", "first"))
			if err != nil {
				t.Fatal(err)
			}
			run := awaitFinishedRun(t, service, receipt.RunID)
			want := limit
			if want == 0 {
				want = 5
			}
			if run.Status != chat.StatusFailed || run.Error == nil || run.Error.Code != "model_continue_limit_exceeded" || run.Output == nil || *run.Output != strings.Repeat("段", want+1) || calls.Load() != int32(want+1) {
				t.Fatalf("run=%+v error=%+v calls=%d", run, run.Error, calls.Load())
			}
			count, err := ports.Execution.ContinuationCallCount(context.Background(), run.RunID)
			if err != nil || count != want {
				t.Fatalf("count=%d err=%v", count, err)
			}
			last, err := ports.Execution.ReadCall(context.Background(), run.RunID)
			if err != nil || last.State != chat.StateSucceeded || last.Error != nil || last.RetryCount != 0 || last.Output.Message.Content != "段" {
				t.Fatalf("accepted call overwritten: %+v err=%v", last, err)
			}
		})
	}
}

// TestNetworkInterruptionNeverContinuesLength 验证部分正文的异常断流不按长度续写，也不网络重试。
func TestNetworkInterruptionNeverContinuesLength(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"异常部分\"}}]}\n\n")
	}))
	defer upstream.Close()
	_, ports, _ := admissionDatabase(t)
	service := startAdmissionService(t, ports, completion.NewClient(nil), retrySettings(upstream.URL, 3))
	receipt, err := service.CreateRun(context.Background(), admissionInput("network", "first"))
	if err != nil {
		t.Fatal(err)
	}
	run := awaitFinishedRun(t, service, receipt.RunID)
	count, countErr := ports.Execution.ContinuationCallCount(context.Background(), run.RunID)
	if run.Status != chat.StatusFailed || run.Error == nil || run.Error.Code != "model_network_error" || run.Output == nil || *run.Output != "异常部分" || calls.Load() != 1 || count != 0 || countErr != nil {
		t.Fatalf("run=%+v error=%+v calls=%d continuations=%d err=%v", run, run.Error, calls.Load(), count, countErr)
	}
}

// TestLengthOutputExcludesEarlierOrdinaryRound 验证最终正文只拼接最后普通轮及续写，历史保留此前正文。
func TestLengthOutputExcludesEarlierOrdinaryRound(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		switch calls.Add(1) {
		case 1:
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"旧轮正文\"}}]}\n\n")
			emitTools(w, []chat.ToolRequest{toolRequest("query", "get_schedule", `{"id":1}`)})
		case 2:
			emitLengthDelta(w, "content", "最后轮", "length")
		default:
			emitLengthDelta(w, "content", "续写", "stop")
		}
	}))
	defer upstream.Close()
	service, _, _ := newToolFixture(t, upstream.URL)
	run := startToolRun(t, service, "last-round")
	if run.Status != chat.StatusSucceeded || run.Output == nil || *run.Output != "最后轮续写" || calls.Load() != 3 {
		t.Fatalf("run=%+v calls=%d", run, calls.Load())
	}
	page := eventPage(t, service, run.RunID, 0, 100)
	var text string
	for _, event := range page.Events {
		if event.Type == chat.EventText {
			var data chat.TextData
			if err := json.Unmarshal(event.Data, &data); err != nil {
				t.Fatal(err)
			}
			text += data.Content
		}
	}
	if text != "旧轮正文最后轮续写" {
		t.Fatalf("timeline lost original text: %s", text)
	}
}

// TestLengthFeedbackKeepsPartialValuesAndBoundsDepth 验证补全不猜残缺字面值，深层片段仍能完成同批配对。
func TestLengthFeedbackKeepsPartialValuesAndBoundsDepth(t *testing.T) {
	cases := []struct {
		id   string
		raw  string
		want string
	}{
		{
			id:   "string",
			raw:  `{"id":1,"name":"会议`,
			want: `{"id":1,"name":"会议"}`,
		},
		{
			id:   "literal",
			raw:  `{"id":1,"flag":tru`,
			want: `{"id":1}`,
		},
		{
			id:   "number",
			raw:  `{"id":1,"revision":12e`,
			want: `{"id":1}`,
		},
		{
			id:   "escape",
			raw:  `{"id":1,"name":"会议\u4`,
			want: `{"id":1}`,
		},
		{
			id:   "deep",
			raw:  `{"extra":` + strings.Repeat("[", 4096),
			want: `{"extra":` + strings.Repeat("[", 4096) + strings.Repeat("]", 4096) + "}",
		},
		{
			id:   "too-deep",
			raw:  `{"extra":` + strings.Repeat("[", 10000),
			want: "null",
		},
		{
			id:   "validation-budget",
			raw:  `{"id":?` + strings.Repeat(`,"extra":1`, 2048),
			want: "null",
		},
	}
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if calls.Add(1) == 1 {
			var tools []chat.ToolRequest
			for _, item := range cases {
				tools = append(tools, toolRequest(item.id, "get_schedule", item.raw))
			}
			emitToolFinish(w, tools, "length")
			return
		}
		var request chat.ModelRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		for _, item := range cases {
			found := false
			for _, message := range request.Messages {
				if message.ToolCallID != item.id {
					continue
				}
				found = true
				var feedback struct {
					Code    string          `json:"code"`
					Context json.RawMessage `json:"context_arguments"`
				}
				if err := json.Unmarshal([]byte(message.Content), &feedback); err != nil || feedback.Code != "tool_argument_parse_error" || string(feedback.Context) != item.want {
					t.Errorf("%s: context length=%d want length=%d error=%v", item.id, len(feedback.Context), len(item.want), err)
				}
			}
			if !found {
				t.Errorf("missing feedback %s", item.id)
			}
		}
		emitText(w, "已收到参数错误")
	}))
	defer upstream.Close()
	service, _, _ := newToolFixture(t, upstream.URL)
	run := startToolRun(t, service, "partial-json")
	if run.Status != chat.StatusSucceeded || calls.Load() != 2 {
		t.Fatalf("run=%+v calls=%d", run, calls.Load())
	}
}

// TestLengthFeedbackLongWhitespacePreservesRawFacts 验证长空白候选合并、字符串空白保留与超预算安全降级。
func TestLengthFeedbackLongWhitespacePreservesRawFacts(t *testing.T) {
	cases := []struct {
		id   string
		raw  string
		want string
	}{
		{
			id:   "whitespace",
			raw:  `{"id":` + strings.Repeat(" \t\r\n", 32<<10),
			want: "{}",
		},
		{
			id:   "string-spaces",
			raw:  `{"name":"会议` + strings.Repeat(" ", 128<<10),
			want: `{"name":"会议` + strings.Repeat(" ", 128<<10) + `"}`,
		},
		{
			id:   "scan-budget",
			raw:  `{"id":` + strings.Repeat(" ", 1<<20),
			want: "null",
		},
	}
	inputs := make(chan chat.ModelRequest, 1)
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if calls.Add(1) == 1 {
			requests := []chat.ToolRequest{toolRequest("complete", "get_schedule", `{"id":1}`)}
			for _, item := range cases {
				requests = append(requests, toolRequest(item.id, "get_schedule", item.raw))
			}
			emitToolFinish(w, requests, "length")
			return
		}
		var request chat.ModelRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		inputs <- request
		emitText(w, "已保留原始错误及同批结果")
	}))
	defer upstream.Close()
	old, storage, application := newToolFixture(t, upstream.URL)
	old.Stop()
	committed := make(chan error, 1)
	storage.Execution = observedSlowFinish{
		ExecutionRepository: storage.Execution,
		committed:           committed,
	}
	settings := func() ([]chat.ModelConfig, error) {
		configs, err := toolSettings(upstream.URL)()
		// 压力输入由可控上游构造，扩大预算与投影阈值，独立验证参数补全原文。
		configs[0].ContextWindow = 2 << 20
		configs[0].ToolResultMaxTokens = 1 << 20
		return configs, err
	}
	service := chat.NewService(storage, completion.NewClient(nil), settings, queryRegistry(t, application))
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Stop)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	receipt, err := service.CreateRun(ctx, chat.CreateRunInput{
		SessionID:      "whitespace-feedback",
		Input:          "查询日程",
		Provider:       "test",
		Model:          "model",
		IdempotencyKey: "query",
	})
	if err != nil {
		t.Fatal(err)
	}
	// 按真实事务回执同步，不将大参数的落库耗时误当成补全性能契约。
	select {
	case err := <-committed:
		if err != nil {
			t.Fatalf("终态提交失败: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("未收到终态提交回执: %v", ctx.Err())
	}
	run, err := service.GetRun(ctx, receipt.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != chat.StatusSucceeded || calls.Load() != 2 {
		t.Fatalf("run=%+v calls=%d", run, calls.Load())
	}
	request := <-inputs
	if len(request.Messages) != 6 {
		t.Fatalf("unexpected message count: %d", len(request.Messages))
	}
	var originals []chat.ToolRequest
	if err := json.Unmarshal(request.Messages[1].ToolCalls, &originals); err != nil || len(originals) != 4 {
		t.Fatalf("original calls: count=%d err=%v", len(originals), err)
	}
	for index, item := range cases {
		if originals[index+1].ID != item.id || originals[index+1].Function.Arguments != item.raw {
			t.Fatalf("%s: original arguments changed", item.id)
		}
		found := false
		for _, message := range request.Messages[2:] {
			if message.ToolCallID != item.id {
				continue
			}
			found = true
			var feedback struct {
				Code    string          `json:"code"`
				Context json.RawMessage `json:"context_arguments"`
				Notice  string          `json:"notice"`
			}
			if err := json.Unmarshal([]byte(message.Content), &feedback); err != nil || feedback.Code != "tool_argument_parse_error" || string(feedback.Context) != item.want || !strings.Contains(feedback.Notice, "未执行业务") {
				t.Fatalf("%s: feedback context length=%d want=%d err=%v", item.id, len(feedback.Context), len(item.want), err)
			}
		}
		if !found {
			t.Fatalf("missing feedback %s", item.id)
		}
	}
	page := eventPage(t, service, run.RunID, 0, 100)
	results := map[string]bool{}
	for _, event := range page.Events {
		if event.Type != chat.EventToolResult {
			continue
		}
		var data chat.ToolResultData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			t.Fatal(err)
		}
		if results[data.ToolCallID] {
			t.Fatalf("duplicate result %s", data.ToolCallID)
		}
		results[data.ToolCallID] = true
		if data.ToolCallID == "complete" {
			if data.Error != nil || !strings.Contains(string(data.Output), "真实会议") {
				t.Fatalf("valid sibling failed: %s", event.Data)
			}
		} else if data.Error == nil || data.Error.Code != "tool_argument_parse_error" || string(data.Output) != "null" || strings.Contains(string(event.Data), "context_arguments") {
			t.Fatalf("raw error changed: %s", event.Data)
		}
	}
	if len(results) != 4 {
		t.Fatalf("missing results: %v", results)
	}
}
