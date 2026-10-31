package chat_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
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
	logger "github.com/dengweitian0-svg/TimeCue/internal/log"
	"github.com/dengweitian0-svg/TimeCue/internal/store"
)

type terminalAttempt struct {
	at   time.Time
	call chat.ModelCall
	err  error
}

type terminalTransactionFault struct {
	chat.ExecutionRepository
	failures int32
	count    atomic.Int32
	attempts chan terminalAttempt
}

// Finish 用非法运行状态触发真实事务回滚，成功后重复提交验证幂等。
func (f *terminalTransactionFault) Finish(ctx context.Context, call chat.ModelCall, record chat.Record) error {
	at := time.Now()
	if f.count.Add(1) <= f.failures {
		record.State = "injected_invalid_state"
	}
	err := f.ExecutionRepository.Finish(ctx, call, record)
	if err == nil {
		err = f.ExecutionRepository.Finish(ctx, call, record)
	}
	f.attempts <- terminalAttempt{
		at:   at,
		call: call,
		err:  err,
	}
	return err
}

type resultTransactionFault struct {
	chat.ToolRepository
	failed     atomic.Bool
	beforeFail <-chan struct{}
}

type retryTransactionFault struct{ chat.ToolRepository }

// WaitToolRetry 通过非法 JSON 使真实退避事务失败，保留调用方的原始错误。
func (f retryTransactionFault) WaitToolRetry(ctx context.Context, tool chat.ToolCall) error {
	tool.Output = json.RawMessage(`invalid`)
	return f.ToolRepository.WaitToolRetry(ctx, tool)
}

// TestPersistenceFailureRetainsActualErrorWhenRetrySaveFails 验证退避保存失败不丢失实际错误，也不启动业务重试。
func TestPersistenceFailureRetainsActualErrorWhenRetrySaveFails(t *testing.T) {
	var attempts atomic.Int32
	fixture := newAttemptFixture(t, []chat.ToolRequest{toolRequest("actual-error", "temporary", `{}`)}, nil, attemptTool{
		name: "temporary",
		run: func(context.Context, string) (string, error) {
			attempts.Add(1)
			return "", &tools.Failure{
				Code:    "tool_temporary_error",
				Message: "业务端实际返回的暂时故障，可能已经生效",
			}
		},
	})
	fixture.service.Stop()
	fixture.storage.Tools = retryTransactionFault{ToolRepository: fixture.storage.Tools}
	fixture.restart()
	receipt := createAttemptRun(t, fixture.service, "retry-save-failure")
	run := awaitFinishedRun(t, fixture.service, receipt.RunID)
	fixture.service.Stop()
	if run.Status != chat.StatusFailed || run.Error == nil || run.Error.Code != "persistence_failed" || attempts.Load() != 1 || fixture.models.Load() != 1 {
		t.Fatalf("退避保存故障未停止推进: %+v attempts=%d", run, attempts.Load())
	}
	page := eventPage(t, fixture.service, receipt.RunID, 0, 100)
	var result chat.ToolResultData
	if len(page.Events) != 5 || json.Unmarshal(page.Events[2].Data, &result) != nil || result.Error == nil || result.Error.Code != "tool_temporary_error" || !strings.Contains(result.Error.Message, "可能已经生效") {
		t.Fatalf("退避保存失败丢失实际错误: %s", mustJSON(page))
	}
}

type deadlineTerminalFault struct {
	chat.ExecutionRepository
	count    atomic.Int32
	waitSave bool
	entered  chan struct{}
}

// Finish 在初次真实事务回滚后，可选择让重试保存等待共享退出期限。
func (f *deadlineTerminalFault) Finish(ctx context.Context, call chat.ModelCall, record chat.Record) error {
	if f.count.Add(1) == 1 {
		record.State = "injected_invalid_state"
		err := f.ExecutionRepository.Finish(ctx, call, record)
		if !f.waitSave {
			close(f.entered)
		}
		return err
	}
	close(f.entered)
	<-ctx.Done()
	return ctx.Err()
}

// TestPersistenceFailureShutdownSharesDeadline 验证退避等待和在途收尾保存均受宿主剩余退出预算约束。
func TestPersistenceFailureShutdownSharesDeadline(t *testing.T) {
	for _, waitSave := range []bool{false, true} {
		t.Run(map[bool]string{false: "waiting_retry", true: "saving_retry"}[waitSave], func(t *testing.T) {
			var models atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				models.Add(1)
				emitTools(w, []chat.ToolRequest{toolRequest("actual-create", "create_schedule", `{"name":"退出前已生效","type":"single","notifyTime":"2099-01-02 09:00"}`)})
			}))
			t.Cleanup(upstream.Close)
			old, application, storage := newWriteFixture(t, upstream.URL, nil)
			old.Stop()
			fault := &deadlineTerminalFault{
				ExecutionRepository: storage.Execution,
				waitSave:            waitSave,
				entered:             make(chan struct{}),
			}
			storage.Execution = fault
			storage.Tools = &resultTransactionFault{ToolRepository: storage.Tools}
			registry, err := tools.NewRegistry(context.Background(), append(tools.ScheduleTools(application), tools.ScheduleWriteTools(application, nil)...)...)
			if err != nil {
				t.Fatal(err)
			}
			service := chat.NewService(storage, completion.NewClient(nil), toolSettings(upstream.URL), registry)
			if err := service.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(service.Stop)
			receipt := createAttemptRun(t, service, "shutdown-failure")
			select {
			case <-fault.entered:
			case <-time.After(10 * time.Second):
				t.Fatal("未到达收尾等待边界")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
			defer cancel()
			done := make(chan struct{})
			go func() { service.Shutdown(ctx); close(done) }()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("收尾重试超出宿主共享退出期限")
			}
			run, err := service.GetRun(context.Background(), receipt.RunID)
			if err != nil || run.Status != chat.StatusRunning || run.FinishedAt != nil || models.Load() != 1 {
				t.Fatalf("退出伪造已持久化终态或重发模型: %+v %v", run, err)
			}
			want := int32(1)
			if waitSave {
				want++
			}
			if fault.count.Load() != want {
				t.Fatalf("退出后继续保存重试: %d", fault.count.Load())
			}
			page := eventPage(t, service, receipt.RunID, 0, 100)
			if len(page.Events) != 2 || page.Events[1].Type != chat.EventToolStart {
				t.Fatalf("退出推送未提交内容: %s", mustJSON(page))
			}
			values, err := application.ListSchedules(context.Background())
			if err != nil || len(values) != 1 {
				t.Fatalf("退出重复实际业务: %+v %v", values, err)
			}
		})
	}
}

// FinishTool 在真实工具结果事务注入 JSON 约束故障，原业务结果仍由协调器持有。
func (f *resultTransactionFault) FinishTool(ctx context.Context, call chat.ModelCall, tool chat.ToolCall) (chat.ModelCall, error) {
	if !f.failed.Swap(true) {
		if f.beforeFail != nil {
			select {
			case <-f.beforeFail:
			case <-ctx.Done():
				return call, ctx.Err()
			}
		}
		tool.Output = json.RawMessage(`invalid`)
	}
	return f.ToolRepository.FinishTool(ctx, call, tool)
}

// TestPersistenceFailureExhaustionKeepsDatabaseTruth 验证耗尽释放、订阅关闭、新准入及旧运行重启只收尾。
func TestPersistenceFailureExhaustionKeepsDatabaseTruth(t *testing.T) {
	logPath := capturePersistenceLogs(t)
	service, storage, application, fault, models := newPersistenceFixture(t, 4)
	receipt := createAttemptRun(t, service, "persistence-exhaustion")
	first := awaitTerminalAttempt(t, fault)
	subscription, err := service.Subscribe(context.Background(), receipt.RunID, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()
	stream := make(chan []chat.Event, 1)
	go func() {
		var events []chat.Event
		for event := range subscription.Events {
			events = append(events, event)
		}
		stream <- events
	}()
	previous := first
	for retry := 1; retry <= 3; retry++ {
		if _, err := service.CreateRun(context.Background(), admissionInput("persistence-exhaustion", "next")); !errors.Is(err, chat.ErrSessionBusy) {
			t.Fatalf("第 %d 次重试前释放占用: %v", retry, err)
		}
		current := awaitTerminalAttempt(t, fault)
		if current.err == nil || current.at.Sub(previous.at) < 5*time.Second {
			t.Fatalf("未按五秒间隔执行真实事务重试: %+v", current)
		}
		previous = current
	}
	select {
	case events := <-stream:
		if len(events) != 2 || events[0].Type != chat.EventStart || events[1].Type != chat.EventToolStart {
			t.Fatalf("SSE 推送了未落库结果或终态: %s", mustJSON(events))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("耗尽后未释放现有订阅")
	}
	run, err := service.GetRun(context.Background(), receipt.RunID)
	if err != nil || run.Status != chat.StatusRunning || run.FinishedAt != nil || run.Error != nil || models.Load() != 1 || fault.count.Load() != 4 {
		t.Fatalf("耗尽后伪造数据库终态或继续推进: %+v %v", run, err)
	}
	entries := readPersistenceLogs(t, logPath, receipt.RunID)
	retries, exhausted := 0, 0
	for index, entry := range entries {
		if entry["session_id"] != run.SessionID || entry["model_call_id"] != float64(first.call.ID) || entry["stage"] == nil || entry["error"] == nil {
			t.Errorf("故障日志缺少上下文: %s", mustJSON(entry))
		}
		if entry["level"] == "ERROR" {
			exhausted++
			if index != len(entries)-1 || entry["retry"] != float64(3) {
				t.Errorf("耗尽前记录 error 或重试序号错误: %s", mustJSON(entry))
			}
		} else if entry["stage"] == "finish" && entry["retry"].(float64) > 0 {
			retries++
		}
	}
	if retries != 3 || exhausted != 1 {
		t.Fatalf("未按三次 warning、一次耗尽 error 记录: %s", mustJSON(entries))
	}
	// 幂等重放遵循数据库原状态，不因内存已释放而创建新任务。
	replay, err := service.CreateRun(context.Background(), admissionInput(run.SessionID, "key"))
	if err != nil || replay.RunID != receipt.RunID || replay.Status != chat.StatusRunning {
		t.Fatalf("原键重放改变实际状态: %+v %v", replay, err)
	}
	var next chat.Receipt
	deadline := time.Now().Add(3 * time.Second)
	for {
		next, err = service.CreateRun(context.Background(), admissionInput(run.SessionID, "next"))
		if !errors.Is(err, chat.ErrSessionBusy) || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if err != nil {
		t.Fatalf("耗尽后未释放会话名额: %v", err)
	}
	if nextRun := awaitFinishedRun(t, service, next.RunID); nextRun.Status != chat.StatusSucceeded {
		t.Fatal(nextRun)
	}
	service.Stop()
	// 已有后续 run 时，旧 running 不再执行创建工具或模型。
	restarted := chat.NewService(storage, &admissionModel{}, admissionSettings, nil)
	if err := restarted.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.Stop)
	run = awaitFinishedRun(t, restarted, receipt.RunID)
	if run.Status != chat.StatusFailed || run.Error == nil || run.Error.Code != "run_superseded" || models.Load() != 2 {
		t.Fatalf("旧 running 重启继续业务: %+v models=%d", run, models.Load())
	}
	values, err := application.ListSchedules(context.Background())
	if err != nil || len(values) != 1 || values[0].Revision != 1 {
		t.Fatalf("耗尽或重启重执业务: %+v %v", values, err)
	}
	page := eventPage(t, restarted, receipt.RunID, 0, 100)
	if len(page.Events) != 5 || page.Events[3].Type != chat.EventError || page.Events[4].Type != chat.EventEnd {
		t.Fatalf("重启未按实际状态收尾: %s", mustJSON(page))
	}
	var result chat.ToolResultData
	if json.Unmarshal(page.Events[2].Data, &result) != nil || result.Error == nil || result.Error.Code != "tool_result_unconfirmed" {
		t.Fatalf("丢失内存结果被当作业务未生效: %s", page.Events[2].Data)
	}
}

// TestPersistenceFailureIsolatesInflightAndRetriedTools 验证首次或重试结果故障立即收尾，迟到实际业务不能重开运行。
func TestPersistenceFailureIsolatesInflightAndRetriedTools(t *testing.T) {
	for _, retry := range []bool{false, true} {
		t.Run(map[bool]string{false: "initial", true: "retried"}[retry], func(t *testing.T) {
			var models, attempts atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				models.Add(1)
				emitTools(w, []chat.ToolRequest{
					toolRequest("actual", "controlled_query", `{}`),
					toolRequest("late", "late_query", `{}`),
					toolRequest("queued", "create_schedule", `{"name":"不应创建","type":"single","notifyTime":"2099-01-02 09:00"}`),
				})
			}))
			t.Cleanup(upstream.Close)
			old, storage, application := newToolFixture(t, upstream.URL)
			old.Stop()
			started, release, returned := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			registry, err := tools.NewRegistry(context.Background(), append(tools.ScheduleTools(application), tools.ScheduleWriteTools(application, nil)...)...)
			if err != nil {
				t.Fatal(err)
			}
			for _, controlled := range []attemptTool{
				{
					name:     "controlled_query",
					parallel: true,
					run: func(ctx context.Context, _ string) (string, error) {
						if attempts.Add(1) == 1 && retry {
							return "", &tools.Failure{
								Code:    "tool_temporary_error",
								Message: "受控的临时查询故障",
							}
						}
						values, err := application.ListSchedules(ctx)
						encoded, _ := json.Marshal(values)
						return string(encoded), err
					},
				},
				{
					name:     "late_query",
					parallel: true,
					run: func(ctx context.Context, _ string) (string, error) {
						close(started)
						<-release
						// 故意忽略取消，仍执行真实查询并迟到返回。
						values, err := application.ListSchedules(context.WithoutCancel(ctx))
						encoded, _ := json.Marshal(values)
						close(returned)
						return string(encoded), err
					},
				},
			} {
				if err := registry.Register(context.Background(), controlled); err != nil {
					t.Fatal(err)
				}
			}
			storage.Tools = &resultTransactionFault{
				ToolRepository: storage.Tools,
				beforeFail:     started,
			}
			settings := func() ([]chat.ModelConfig, error) {
				configs, err := toolSettings(upstream.URL)()
				configs[0].ToolConcurrency = 2
				return configs, err
			}
			service := chat.NewService(storage, completion.NewClient(nil), settings, registry)
			if err := service.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(service.Stop)
			t.Cleanup(func() { once.Do(func() { close(release) }) })
			receipt := createAttemptRun(t, service, "inflight-failure")
			run := awaitFinishedRunWithin(t, service, receipt.RunID, 10*time.Second)
			wantAttempts := int32(1)
			if retry {
				wantAttempts++
			}
			if run.Status != chat.StatusFailed || run.Error == nil || run.Error.Code != "persistence_failed" || attempts.Load() != wantAttempts || models.Load() != 1 {
				t.Fatalf("保存故障继续推进或等待其他在途工具: %+v attempts=%d models=%d", run, attempts.Load(), models.Load())
			}
			before := eventPage(t, service, receipt.RunID, 0, 100)
			results := map[string]chat.ToolResultData{}
			for _, event := range before.Events {
				if event.Type == chat.EventToolResult {
					var value chat.ToolResultData
					if err := json.Unmarshal(event.Data, &value); err != nil {
						t.Fatal(err)
					}
					results[value.ToolCallID] = value
				}
			}
			if len(results) != 3 || results["actual"].Error != nil || string(results["actual"].Output) == "" {
				t.Fatalf("实际结果未保存: %s", mustJSON(results))
			}
			for _, id := range []string{"late", "queued"} {
				if results[id].Error == nil || results[id].Error.Code != "tool_result_unconfirmed" {
					t.Fatalf("未取得结果被当作业务未生效: %s", mustJSON(results))
				}
			}
			once.Do(func() { close(release) })
			select {
			case <-returned:
			case <-time.After(3 * time.Second):
				t.Fatal("迟到实际查询未返回")
			}
			service.Stop()
			after := eventPage(t, service, receipt.RunID, 0, 100)
			if mustJSON(after) != mustJSON(before) || models.Load() != 1 || attempts.Load() != wantAttempts {
				t.Fatal("迟到回调重开运行或覆盖终态")
			}
			values, err := application.ListSchedules(context.Background())
			if err != nil || len(values) != 1 {
				t.Fatalf("故障后启动排队的创建业务: %+v %v", values, err)
			}
		})
	}
}

func capturePersistenceLogs(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "persistence.jsonl")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	logger.Upgrade(file)
	t.Cleanup(func() {
		logger.Upgrade(os.Stderr)
		_ = file.Close()
	})
	return path
}

func readPersistenceLogs(t *testing.T, path, runID string) []map[string]any {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var entries []map[string]any
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var entry map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			t.Fatal(err)
		}
		if entry["run_id"] == runID {
			entries = append(entries, entry)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return entries
}

func awaitTerminalAttempt(t *testing.T, fault *terminalTransactionFault) terminalAttempt {
	t.Helper()
	select {
	case attempt := <-fault.attempts:
		return attempt
	case <-time.After(10 * time.Second):
		t.Fatal("未到达终态事务边界")
		return terminalAttempt{}
	}
}

func newPersistenceFixture(t *testing.T, failures int32) (*chat.Runtime, chat.Storage, *app.App, *terminalTransactionFault, *atomic.Int32) {
	t.Helper()
	db, storage, _ := admissionDatabase(t)
	schedules, err := store.NewScheduleStore(db)
	if err != nil {
		t.Fatal(err)
	}
	application := &app.App{Schedule: schedules}
	models := &atomic.Int32{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if models.Add(1) == 1 {
			emitTools(w, []chat.ToolRequest{toolRequest("actual-create", "create_schedule", ` {"name":"故障后保留的日程","type":"single","notifyTime":"2099-01-02 09:00"} `)})
			return
		}
		emitText(w, "新的运行")
	}))
	t.Cleanup(upstream.Close)
	definitions := append(tools.ScheduleTools(application), tools.ScheduleWriteTools(application, nil)...)
	registry, err := tools.NewRegistry(context.Background(), definitions...)
	if err != nil {
		t.Fatal(err)
	}
	fault := &terminalTransactionFault{
		ExecutionRepository: storage.Execution,
		failures:            failures,
		attempts:            make(chan terminalAttempt, 16),
	}
	storage.Execution = fault
	storage.Tools = &resultTransactionFault{ToolRepository: storage.Tools}
	service := chat.NewService(storage, completion.NewClient(nil), toolSettings(upstream.URL), registry)
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Stop)
	return service, storage, application, fault, models
}

// TestPersistenceFailureRetriesOnlyTerminalWithContext 验证真实业务不重执、失败收尾间隔和日志级别及身份。
func TestPersistenceFailureRetriesOnlyTerminalWithContext(t *testing.T) {
	logPath := capturePersistenceLogs(t)
	service, storage, application, fault, models := newPersistenceFixture(t, 2)
	receipt := createAttemptRun(t, service, "persistence-retry")
	first := awaitTerminalAttempt(t, fault)
	if first.err == nil {
		t.Fatal("终态事务未发生真实约束失败")
	}
	run, err := service.GetRun(context.Background(), receipt.RunID)
	if err != nil || run.Status != chat.StatusRunning || run.FinishedAt != nil {
		t.Fatalf("回滚终态被推送: %+v %v", run, err)
	}
	page := eventPage(t, service, receipt.RunID, 0, 100)
	if len(page.Events) != 2 || page.Events[1].Type != chat.EventToolStart {
		t.Fatalf("未提交结果泄漏: %s", mustJSON(page))
	}
	if _, err := service.CreateRun(context.Background(), admissionInput("persistence-retry", "next")); !errors.Is(err, chat.ErrSessionBusy) {
		t.Fatalf("收尾重试期间释放占用: %v", err)
	}
	previous := first
	for i := 0; i < 2; i++ {
		current := awaitTerminalAttempt(t, fault)
		if current.at.Sub(previous.at) < 5*time.Second || current.call.ID != first.call.ID || current.call.RetryCount != first.call.RetryCount || mustJSON(current.call.PendingTools) != mustJSON(first.call.PendingTools) {
			t.Fatalf("保存重试改变身份、实际结果或预算，或未等待五秒: %+v", current)
		}
		previous = current
	}
	run = awaitFinishedRun(t, service, receipt.RunID)
	service.Stop()
	if run.Status != chat.StatusFailed || run.Error == nil || run.Error.Code != "persistence_failed" || models.Load() != 1 || fault.count.Load() != 3 {
		t.Fatalf("故障继续推进或收尾重复: %+v models=%d attempts=%d", run, models.Load(), fault.count.Load())
	}
	values, err := application.ListSchedules(context.Background())
	if err != nil || len(values) != 1 || values[0].Name != "故障后保留的日程" || values[0].Revision != 1 {
		t.Fatalf("业务被重执或丢失: %+v %v", values, err)
	}
	call, err := storage.Execution.ReadCall(context.Background(), receipt.RunID)
	if err != nil || call == nil {
		t.Fatal(call, err)
	}
	stored, err := storage.Tools.ReadTools(context.Background(), call.ID)
	if err != nil || len(stored) != 1 || stored[0].State != "succeeded" || stored[0].RetryCount != 0 || !strings.Contains(string(stored[0].Output), "故障后保留的日程") {
		t.Fatalf("终态未保留实际工具结果: %+v %v", stored, err)
	}
	page = eventPage(t, service, receipt.RunID, 0, 100)
	if len(page.Events) != 5 || page.Events[2].Type != chat.EventToolResult || page.Events[3].Type != chat.EventError || page.Events[4].Type != chat.EventEnd {
		t.Fatalf("重复或缺失终态: %s", mustJSON(page))
	}
	entries := readPersistenceLogs(t, logPath, receipt.RunID)
	stages, retries := map[string]int{}, 0
	for _, entry := range entries {
		if entry["level"] != "WARN" || entry["session_id"] != "persistence-retry" || entry["model_call_id"] != float64(call.ID) || entry["error"] == nil {
			t.Errorf("可恢复故障日志缺少上下文或过早记 error: %s", mustJSON(entry))
		}
		stage, _ := entry["stage"].(string)
		stages[stage]++
		if stage == "finish" && entry["retry"] != nil && entry["retry"].(float64) > 0 {
			retries++
		}
	}
	if stages["tool_result"] != 1 || stages["finish"] != 3 || retries != 2 {
		t.Fatalf("故障或重试日志次数错误: %s", mustJSON(entries))
	}
}
