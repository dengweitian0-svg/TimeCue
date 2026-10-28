package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/agent/completion"
	"github.com/dengweitian0-svg/TimeCue/internal/agent/tools"
	"github.com/dengweitian0-svg/TimeCue/internal/api"
	"github.com/dengweitian0-svg/TimeCue/internal/app"
)

// TestCancelAcknowledgesBeforeInflightResults 验证取消先持久化、保留占用与订阅，再接纳实际结果和跳过项。
func TestCancelAcknowledgesBeforeInflightResults(t *testing.T) {
	started := make(chan context.Context, 2)
	release := make(chan struct{})
	var once sync.Once
	fixture := newAttemptFixture(t, []chat.ToolRequest{
		toolRequest("first", "blocked", `{}`),
		toolRequest("second", "blocked", `{}`),
		toolRequest("skipped", "get_schedule", `{"id":1}`),
	}, func(cfg *chat.ModelConfig) { cfg.ToolConcurrency = 2 }, attemptTool{
		name:     "blocked",
		parallel: true,
		run: func(ctx context.Context, _ string) (string, error) {
			started <- ctx
			<-release
			return `{"actual":true}`, nil
		},
	})
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	receipt := createAttemptRun(t, fixture.service, "cancel-inflight")
	for range 2 {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("工具没有开始")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	status, err := fixture.service.CancelRun(ctx, receipt.RunID)
	if err != nil || status != chat.StatusCancelled {
		t.Fatalf("取消应立即受理: %s %v", status, err)
	}
	record, err := fixture.storage.Runs.ReadRun(ctx, receipt.RunID, true)
	if err != nil || record.State != chat.StateWaitForCancel || record.FinishedAt != nil {
		t.Fatalf("取消等待未持久化: %+v %v", record, err)
	}
	if _, err = fixture.service.CreateRun(ctx, admissionInput("cancel-inflight", "next")); !errors.Is(err, chat.ErrSessionBusy) {
		t.Fatalf("取消提前释放会话: %v", err)
	}
	replay, err := fixture.service.CreateRun(ctx, admissionInput("cancel-inflight", "key"))
	if err != nil || replay.RunID != receipt.RunID || replay.Status != chat.StatusCancelled {
		t.Fatalf("取消投影重放错误: %+v %v", replay, err)
	}
	// 重复取消不能关闭 SSE 或提前结束。
	if _, err = fixture.service.CancelRun(ctx, receipt.RunID); err != nil {
		t.Fatal(err)
	}
	sub, err := fixture.service.Subscribe(ctx, receipt.RunID, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	once.Do(func() { close(release) })
	var seq int64
	results, ends := 0, 0
	for event := range sub.Events {
		if event.ID != seq+1 {
			t.Fatalf("序号不连续: %+v", event)
		}
		seq = event.ID
		if event.Type == chat.EventToolResult {
			results++
		}
		if event.Type == chat.EventEnd {
			ends++
		}
	}
	run := awaitFinishedRun(t, fixture.service, receipt.RunID)
	if results != 3 || ends != 1 || run.Status != chat.StatusCancelled || fixture.models.Load() != 1 {
		t.Fatalf("取消结果不完整: results=%d ends=%d run=%+v", results, ends, run)
	}
	call, _ := fixture.storage.Execution.ReadCall(context.Background(), receipt.RunID)
	stored, _ := fixture.storage.Tools.ReadTools(context.Background(), call.ID)
	for _, tool := range stored {
		if tool.Request.ID == "skipped" {
			if tool.State != "skipped" || tool.Error == nil || tool.Error.Code != "tool_skipped" {
				t.Fatalf("未跳过: %+v", tool)
			}
		} else if tool.State != "succeeded" || string(tool.Output) != `{"actual":true}` {
			t.Fatalf("实际结果丢失: %+v", tool)
		}
	}
}

// TestCancelWaitsForOriginalTimeout 验证取消不重置或缩短在途期限，超时后不重试且迟到成功不能覆盖结果。
func TestCancelWaitsForOriginalTimeout(t *testing.T) {
	started := make(chan context.Context, 1)
	late, returned := make(chan struct{}), make(chan struct{})
	var once sync.Once
	fixture := newAttemptFixture(t, []chat.ToolRequest{toolRequest("timeout", "blocked", `{}`)}, func(cfg *chat.ModelConfig) {
		cfg.ToolTimeout = 500 * time.Millisecond
	}, attemptTool{
		name: "blocked",
		run: func(ctx context.Context, _ string) (string, error) {
			started <- ctx
			<-late
			close(returned)
			return `true`, nil
		},
	})
	t.Cleanup(func() { once.Do(func() { close(late) }) })
	receipt := createAttemptRun(t, fixture.service, "cancel-timeout")
	var attempt context.Context
	select {
	case attempt = <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("工具未启动")
	}
	deadline, _ := attempt.Deadline()
	if _, err := fixture.service.CancelRun(context.Background(), receipt.RunID); err != nil {
		t.Fatal(err)
	}
	if attempt.Err() != nil {
		t.Fatal("取消提前中断在途工具")
	}
	run := awaitFinishedRun(t, fixture.service, receipt.RunID)
	if run.FinishedAt.Before(deadline) || run.Status != chat.StatusCancelled || fixture.models.Load() != 1 {
		t.Fatalf("没有等待原期限: %+v deadline=%v", run, deadline)
	}
	once.Do(func() { close(late) })
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("迟到工具未返回")
	}
	call, _ := fixture.storage.Execution.ReadCall(context.Background(), receipt.RunID)
	stored, err := fixture.storage.Tools.ReadTools(context.Background(), call.ID)
	if err != nil || len(stored) != 1 || stored[0].RetryCount != 0 || stored[0].State != "failed" || stored[0].Error == nil || stored[0].Error.Code != "tool_timeout" {
		t.Fatalf("取消后重试或丢失超时: %+v %v", stored, err)
	}
}

type cancelWriteFailure struct {
	chat.RunRepository
	fail atomic.Bool
}

// TestCancelRetainsPersistenceFailureAndActualToolResult 验证取消不吞掉运行自身保存故障，重试收尾仍保存实际结果。
func TestCancelRetainsPersistenceFailureAndActualToolResult(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	fixture := newAttemptFixture(t, []chat.ToolRequest{toolRequest("actual", "blocked", `{}`)}, nil, attemptTool{
		name: "blocked",
		run: func(context.Context, string) (string, error) {
			close(started)
			<-release
			return `{"actual":true}`, nil
		},
	})
	fixture.service.Stop()
	fixture.storage.Tools = &failToolResultOnce{ToolRepository: fixture.storage.Tools}
	fixture.restart()
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	receipt := createAttemptRun(t, fixture.service, "cancel-persistence")
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("工具未启动")
	}
	if _, err := fixture.service.CancelRun(context.Background(), receipt.RunID); err != nil {
		t.Fatal(err)
	}
	once.Do(func() { close(release) })
	run := awaitFinishedRun(t, fixture.service, receipt.RunID)
	if run.Status != chat.StatusFailed || run.Error == nil || run.Error.Code != "persistence_failed" {
		t.Fatalf("取消吞掉运行保存错误: %+v", run)
	}
	call, err := fixture.storage.Execution.ReadCall(context.Background(), receipt.RunID)
	if err != nil || call == nil {
		t.Fatal(call, err)
	}
	stored, err := fixture.storage.Tools.ReadTools(context.Background(), call.ID)
	if err != nil || len(stored) != 1 || stored[0].State != "succeeded" || string(stored[0].Output) != `{"actual":true}` {
		t.Fatalf("收尾丢失实际结果: %+v %v", stored, err)
	}
}

// RequestCancel 在取消意图提交边界注入失败，不改变已有运行事实。
func (f *cancelWriteFailure) RequestCancel(ctx context.Context, id string) (chat.RunState, error) {
	if f.fail.Load() {
		return "", errors.New("injected cancellation persistence failure")
	}
	return f.RunRepository.RequestCancel(ctx, id)
}

// TestCancelPersistenceFailureDoesNotStopRun 验证无法保存取消意图时保持原运行，可安全重试取消。
func TestCancelPersistenceFailureDoesNotStopRun(t *testing.T) {
	_, ports, _ := admissionDatabase(t)
	fault := &cancelWriteFailure{RunRepository: ports.Runs}
	fault.fail.Store(true)
	ports.Runs = fault
	model := &admissionModel{
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	service := startAdmissionService(t, ports, model, admissionSettings)
	receipt, err := service.CreateRun(context.Background(), admissionInput("cancel-failure", "key"))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-model.started:
	case <-time.After(3 * time.Second):
		t.Fatal("模型未启动")
	}
	if _, err = service.CancelRun(context.Background(), receipt.RunID); err == nil {
		t.Fatal("取消保存失败却返回成功")
	}
	run, err := service.GetRun(context.Background(), receipt.RunID)
	if err != nil || run.Status != chat.StatusRunning || run.FinishedAt != nil {
		t.Fatalf("取消失败改写了运行: %+v %v", run, err)
	}
	fault.fail.Store(false)
	if _, err = service.CancelRun(context.Background(), receipt.RunID); err != nil {
		t.Fatal(err)
	}
	if run := awaitFinishedRun(t, service, receipt.RunID); run.Status != chat.StatusCancelled {
		t.Fatal(run)
	}
}

// TestDeleteActiveSessionHidesQueriesAndKeepsExistingSSE 验证删除立即隐藏查询，保留实际日程、执行记录与原订阅收尾。
func TestDeleteActiveSessionHidesQueriesAndKeepsExistingSSE(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		emitTools(w, []chat.ToolRequest{toolRequest("inflight", "blocked", `{}`)})
	}))
	t.Cleanup(upstream.Close)
	old, ports, application := newToolFixture(t, upstream.URL)
	old.Stop()
	registry := queryRegistry(t, application)
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	if err := registry.Register(context.Background(), attemptTool{
		name: "blocked",
		run:  func(context.Context, string) (string, error) { close(started); <-release; return `true`, nil },
	}); err != nil {
		t.Fatal(err)
	}
	service := chat.NewService(ports, completion.NewClient(nil), toolSettings(upstream.URL), registry)
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Stop)
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	receipt := createAttemptRun(t, service, "550e8400-e29b-41d4-a716-446655440000")
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("工具未开始")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	sub, err := service.Subscribe(ctx, receipt.RunID, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	router := api.NewRouter(&app.App{
		ChatServices: chat.Services{
			Model:   service,
			Run:     service,
			Session: service,
		},
	}, nil)
	request := func(method, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		req.Host = "127.0.0.1:10086"
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}
	for range 2 {
		response := request(http.MethodDelete, "/v1/session/550e8400-e29b-41d4-a716-446655440000")
		if response.Code != http.StatusOK {
			t.Fatalf("删除不幂等: %d %s", response.Code, response.Body)
		}
	}
	for _, path := range []string{"/v1/session/550e8400-e29b-41d4-a716-446655440000", "/v1/run/" + receipt.RunID, "/v1/run/" + receipt.RunID + "/events", "/v1/run/" + receipt.RunID + "/event"} {
		response := request(http.MethodGet, path)
		expected := http.StatusNotFound
		if strings.HasPrefix(path, "/v1/session/") {
			expected = http.StatusBadRequest
		}
		if response.Code != expected {
			t.Fatalf("删除后仍可查询 %s: %d %s", path, response.Code, response.Body)
		}
	}
	response := request(http.MethodGet, "/v1/session")
	if !strings.Contains(response.Body.String(), `"sessions":[]`) {
		t.Fatalf("列表仍可见: %s", response.Body)
	}
	if _, err := service.CreateRun(ctx, admissionInput("550e8400-e29b-41d4-a716-446655440000", "key")); !errors.Is(err, chat.ErrSessionDeleted) {
		t.Fatalf("删除后可重放: %v", err)
	}
	record, err := ports.Runs.ReadRun(ctx, receipt.RunID, false)
	if err != nil || record.State != chat.StateWaitForCancel || record.FinishedAt != nil {
		t.Fatalf("隐藏时已释放运行: %+v %v", record, err)
	}
	once.Do(func() { close(release) })
	ends, results := 0, 0
	for event := range sub.Events {
		if event.Type == chat.EventEnd {
			ends++
		}
		if event.Type == chat.EventToolResult {
			results++
		}
	}
	record, err = ports.Runs.ReadRun(ctx, receipt.RunID, false)
	if err != nil || record.State != chat.StateCancelled || record.FinishedAt == nil || ends != 1 || results != 1 {
		t.Fatalf("后台收尾丢失: %+v %v ends=%d results=%d", record, err, ends, results)
	}
	if schedule, err := application.GetSchedule(ctx, 1); err != nil || schedule.Revision != 1 {
		t.Fatalf("删除会话影响日程: %+v %v", schedule, err)
	}
}

// TestCancellationRecoveryOnlyReadsDeleteReceipt 验证取消恢复只核对既有回执，未命中不重新执行写入。
func TestCancellationRecoveryOnlyReadsDeleteReceipt(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(map[bool]string{
			false: "unconfirmed",
			true:  "committed",
		}[committed], func(t *testing.T) {
			ctx := context.Background()
			_, ports, application, _ := deleteFixture(t)
			registry := deleteRegistry(t, application)
			record := chat.Record{
				Run: chat.Run{
					RunID:     "cancel-recovery",
					SessionID: "cancel-recovery",
					Provider:  "test",
					Model:     "model",
					Input:     "删除",
					CreatedAt: time.Now(),
				},
				State:          chat.StateWaitForCancel,
				IdempotencyKey: "original",
			}
			if err := ports.Runs.Create(ctx, record); err != nil {
				t.Fatal(err)
			}
			call := chat.ModelCall{
				RunID:     record.RunID,
				SessionID: record.SessionID,
				State:     chat.StateRunning,
				Input: chat.ModelRequest{
					Messages: []chat.Message{
						{
							Role:    "user",
							Content: "删除",
						},
					},
				},
			}
			var err error
			call.ID, err = ports.Execution.BeginCall(ctx, call)
			if err != nil {
				t.Fatal(err)
			}
			requests := []chat.ToolRequest{toolRequest("original", "delete_schedule", `{"id":1,"expectedRevision":1}`)}
			raw, _ := json.Marshal(requests)
			call.Output = &chat.ModelOutput{
				Message: chat.Message{
					Role:      "assistant",
					ToolCalls: raw,
				},
			}
			if err := ports.Tools.AcceptResponse(ctx, call); err != nil {
				t.Fatal(err)
			}
			call = call.WithResponseAccepted()
			call, tool, err := ports.Tools.BeginTool(ctx, call, chat.ToolCall{
				ModelCallID:    call.ID,
				Request:        requests[0],
				State:          "running",
				IdempotencyKey: "tool:original:original-key",
			})
			if err != nil {
				t.Fatal(err)
			}
			if committed {
				if _, failure := registry.Execute(tools.WithExecutionKey(ctx, tool.IdempotencyKey), tool.Request.Function.Name, tool.Request.Function.Arguments); failure != nil {
					t.Fatal(failure)
				}
			}
			service := chat.NewService(ports, completion.NewClient(nil), func() ([]chat.ModelConfig, error) { return nil, nil }, registry)
			if err := service.Start(ctx); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(service.Stop)
			run := awaitFinishedRun(t, service, record.RunID)
			stored, err := ports.Tools.ReadTools(ctx, call.ID)
			if err != nil || len(stored) != 1 || run.Status != chat.StatusCancelled {
				t.Fatalf("恢复失败: %+v %+v %v", run, stored, err)
			}
			if committed {
				if stored[0].State != "succeeded" || stored[0].Error != nil {
					t.Fatalf("业务回执未接纳: %+v", stored[0])
				}
			} else {
				if stored[0].Error == nil || stored[0].Error.Code != "tool_result_unconfirmed" {
					t.Fatalf("未确认结果被伪造: %+v", stored[0])
				}
				if receipt, err := application.ReadScheduleDeleteReceipt(ctx, 1, 1, tool.IdempotencyKey); err != nil || receipt != nil {
					t.Fatalf("取消恢复补做了删除: %+v %v", receipt, err)
				}
			}
		})
	}
}
