package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/agent/completion"
	"github.com/dengweitian0-svg/TimeCue/internal/agent/tools"
)

type attemptTool struct {
	name      string
	parallel  bool
	dangerous bool
	run       func(context.Context, string) (string, error)
}

// Info 提供故障注入工具的固定定义。
func (t attemptTool) Info(context.Context) (*tools.ToolInfo, error) {
	return &tools.ToolInfo{
		Name:        t.name,
		Description: "可控执行边界",
		Parallel:    t.parallel,
		Dangerous:   t.dangerous,
		Parameters:  tools.Schema{Type: "object"},
	}, nil
}

// InvokableRun 在真实协调器之外控制一次执行的阻塞和错误。
func (t attemptTool) InvokableRun(ctx context.Context, raw string) (string, error) {
	return t.run(ctx, raw)
}

// TestToolTimeoutReleasesSlotAndIgnoresLateSuccess 验证忽略取消的工具不能阻止即时超时结算或覆盖最终结果。
func TestToolTimeoutReleasesSlotAndIgnoresLateSuccess(t *testing.T) {
	late := make(chan struct{})
	returned := make(chan struct{})
	var releaseLate sync.Once
	var attempts, models atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if models.Add(1) == 1 {
			emitTools(w, []chat.ToolRequest{toolRequest("slow", "blocked", `{}`), toolRequest("ready", "get_schedule", `{"id":1}`)})
			return
		}
		var request chat.ModelRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		paired := map[string]string{}
		for _, message := range request.Messages {
			if message.Role == "tool" {
				paired[message.ToolCallID] = message.Content
			}
		}
		var failure chat.RunError
		if json.Unmarshal([]byte(paired["slow"]), &failure) != nil || failure.Code != "tool_timeout" || paired["ready"] == "" {
			t.Errorf("最终回填错误: %v", paired)
		}
		emitText(w, "已完成")
	}))
	defer upstream.Close()
	old, storage, application := newToolFixture(t, upstream.URL)
	old.Stop()
	registry := queryRegistry(t, application)
	err := registry.Register(context.Background(), attemptTool{
		name:     "blocked",
		parallel: false,
		run: func(ctx context.Context, _ string) (string, error) {
			attempts.Add(1)
			<-late
			if ctx.Err() == nil {
				t.Error("超时未向业务传递取消")
			}
			close(returned)
			return `"late success"`, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	zero := 0
	settings := func() ([]chat.ModelConfig, error) {
		cfg, err := toolSettings(upstream.URL)()
		cfg[0].ToolConcurrency = 1
		cfg[0].ToolTimeout = 40 * time.Millisecond
		cfg[0].ToolMaxRetry = &zero
		return cfg, err
	}
	service := chat.NewService(storage, completion.NewClient(nil), settings, registry)
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Stop)
	t.Cleanup(func() { releaseLate.Do(func() { close(late) }) })
	run := startToolRun(t, service, "timeout")
	if run.Status != chat.StatusSucceeded || attempts.Load() != 1 || models.Load() != 2 {
		t.Fatalf("run=%+v attempts=%d models=%d", run, attempts.Load(), models.Load())
	}
	releaseLate.Do(func() { close(late) })
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("可控旧尝试未返回")
	}
	time.Sleep(50 * time.Millisecond)
	page, _ := service.GetRunEvents(context.Background(), run.RunID, 0, 100)
	results := 0
	for _, event := range page.Events {
		if event.Type == chat.EventToolResult {
			results++
		}
	}
	if results != 2 {
		t.Fatalf("工具重复结算: %s", mustJSON(page))
	}
}

// TestToolRetryBackoffSurvivesRestartAndUsesLastBudget 验证默认三次退避、重启期限、固定预算及最后一次许可。
func TestToolRetryBackoffSurvivesRestartAndUsesLastBudget(t *testing.T) {
	starts := make(chan time.Time, 4)
	var attempts atomic.Int32
	var disableRetry atomic.Bool
	fixture := newAttemptFixture(t, []chat.ToolRequest{
		toolRequest("retry", "temporary", ` { } `),
		toolRequest("sibling", "get_schedule", `{"id":1}`),
	}, func(cfg *chat.ModelConfig) {
		cfg.ToolConcurrency = 1
		if disableRetry.Load() {
			zero := 0
			cfg.ToolMaxRetry = &zero
		}
	}, attemptTool{
		name: "temporary",
		run: func(_ context.Context, raw string) (string, error) {
			if raw != ` { } ` {
				t.Errorf("重试改写原参数: %q", raw)
			}
			starts <- time.Now()
			if attempts.Add(1) < 4 {
				return "", &tools.Failure{
					Code:    "tool_temporary_error",
					Message: "明确暂时故障",
				}
			}
			return `{"actual":"success"}`, nil
		},
	})
	receipt := createAttemptRun(t, fixture.service, "backoff")
	first := <-starts
	call, saved := awaitToolState(t, fixture.storage, receipt.RunID, "retry", "retry_wait")
	if saved.MaxRetry != 3 || saved.RetryCount != 0 || saved.NextRetryAt == nil || time.Until(*saved.NextRetryAt) < 4*time.Second {
		t.Fatalf("首次退避错误: %+v", saved)
	}
	// 同批真实查询在串行工具退避时仍可完成，但模型不能提前继续。
	_, _ = awaitToolState(t, fixture.storage, receipt.RunID, "sibling", "succeeded")
	current, _ := fixture.storage.Execution.ReadCall(context.Background(), receipt.RunID)
	if current.ToolCompleted("retry") || !current.ToolCompleted("sibling") || fixture.models.Load() != 1 {
		t.Fatalf("临时故障被提前回填: %+v models=%d", current, fixture.models.Load())
	}
	fixture.service.Stop()
	disableRetry.Store(true)
	fixture.restart()
	_, resumed := awaitToolState(t, fixture.storage, receipt.RunID, "retry", "retry_wait")
	if resumed.ID != saved.ID || !resumed.NextRetryAt.Equal(*saved.NextRetryAt) || resumed.RetryCount != 0 || resumed.MaxRetry != 3 {
		t.Fatalf("重启重置原计划: %+v", resumed)
	}
	previous := first
	for _, delay := range []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second} {
		select {
		case at := <-starts:
			if at.Sub(previous) < delay-100*time.Millisecond {
				t.Errorf("退避不足: got=%s want>=%s", at.Sub(previous), delay)
			}
			previous = at
		case <-time.After(delay + 5*time.Second):
			t.Fatal("预算内重试未执行")
		}
	}
	run := awaitFinishedRun(t, fixture.service, receipt.RunID)
	if run.Status != chat.StatusSucceeded || attempts.Load() != 4 || fixture.models.Load() != 2 {
		t.Fatalf("run=%+v attempts=%d models=%d", run, attempts.Load(), fixture.models.Load())
	}
	stored, err := fixture.storage.Tools.ReadTools(context.Background(), call.ID)
	if err != nil || len(stored) != 2 {
		t.Fatal(stored, err)
	}
	for _, tool := range stored {
		if tool.Request.ID == "retry" && (tool.ID != saved.ID || tool.RetryCount != 3 || tool.MaxRetry != 3 || tool.State != "succeeded" || tool.NextRetryAt != nil) {
			t.Fatalf("最后一次许可未沿原记录结算: %+v", tool)
		}
	}
	assertAttemptEvents(t, fixture.service, receipt.RunID, 2)
}

// TestToolPermanentAndArgumentFailuresDoNotRetry 验证未分类、业务和参数错误即使有默认预算也只形成最终结果。
func TestToolPermanentAndArgumentFailuresDoNotRetry(t *testing.T) {
	var executions atomic.Int32
	fixture := newAttemptFixture(t, []chat.ToolRequest{
		toolRequest("unknown", "unknown_error", `{}`),
		toolRequest("business", "business_error", `{}`),
		toolRequest("syntax", "unknown_error", `{"id":`),
		toolRequest("schema", "unknown_error", `[]`),
	}, nil,
		attemptTool{
			name:     "unknown_error",
			parallel: true,
			run:      func(context.Context, string) (string, error) { executions.Add(1); return "", errors.New("unknown") },
		},
		attemptTool{
			name:     "business_error",
			parallel: true,
			run: func(context.Context, string) (string, error) {
				executions.Add(1)
				return "", &tools.Failure{
					Code:    "revision_conflict",
					Message: "真实版本冲突",
				}
			},
		},
	)
	receipt := createAttemptRun(t, fixture.service, "permanent")
	run := awaitFinishedRun(t, fixture.service, receipt.RunID)
	if run.Status != chat.StatusSucceeded || executions.Load() != 2 {
		t.Fatalf("run=%+v executions=%d", run, executions.Load())
	}
	assertAttemptEvents(t, fixture.service, receipt.RunID, 4)
}

// TestToolConcurrencyReloadAndSerialExclusion 验证默认三个重叠区间、动态缩扩容及串行互斥。
func TestToolConcurrencyReloadAndSerialExclusion(t *testing.T) {
	started := make(chan string, 6)
	release := make(chan struct{})
	var limit, active atomic.Int32
	var controlled []attemptTool
	var requests []chat.ToolRequest
	for i := 0; i < 6; i++ {
		name := fmt.Sprintf("parallel_%d", i)
		parallel := i != 3
		if !parallel {
			name = "serial"
		}
		requests = append(requests, toolRequest(name, name, `{}`))
		controlled = append(controlled, attemptTool{
			name:     name,
			parallel: parallel,
			run: func(ctx context.Context, _ string) (string, error) {
				n := active.Add(1)
				defer active.Add(-1)
				if !parallel && n != 1 {
					t.Errorf("串行与有效尝试重叠: %d", n)
				}
				started <- name
				select {
				case <-release:
					return `true`, nil
				case <-ctx.Done():
					return "", ctx.Err()
				}
			},
		})
	}
	fixture := newAttemptFixture(t, requests, func(cfg *chat.ModelConfig) { cfg.ToolConcurrency = int(limit.Load()) }, controlled...)
	t.Cleanup(func() { close(release) })
	receipt := createAttemptRun(t, fixture.service, "concurrency")
	for i := 0; i < 3; i++ {
		receiveAttemptStart(t, started)
	}
	select {
	case name := <-started:
		t.Fatalf("突破默认三个名额: %s", name)
	case <-time.After(150 * time.Millisecond):
	}
	limit.Store(1)
	// 调低上限不取消三个在途尝试，也不允许新的启动。
	time.Sleep(150 * time.Millisecond)
	if active.Load() != 3 {
		t.Fatalf("缩容取消在途: %d", active.Load())
	}
	limit.Store(4)
	if name := receiveAttemptStart(t, started); name == "serial" {
		t.Fatal("串行工具在并行执行中启动")
	}
	select {
	case name := <-started:
		t.Fatalf("突破热更新四个名额: %s", name)
	case <-time.After(150 * time.Millisecond):
	}
	if fixture.models.Load() != 1 {
		t.Fatal("整批未完成便继续模型")
	}
	// 四个并行尝试逐个退出；缩回一后不得补充，直到实际数量降到上限以下。
	limit.Store(1)
	time.Sleep(120 * time.Millisecond)
	for i := 0; i < 3; i++ {
		release <- struct{}{}
		select {
		case name := <-started:
			t.Fatalf("缩容期间启动: %s", name)
		case <-time.After(100 * time.Millisecond):
		}
	}
	release <- struct{}{}
	for i := 0; i < 2; i++ {
		receiveAttemptStart(t, started)
		release <- struct{}{}
	}
	run := awaitFinishedRun(t, fixture.service, receipt.RunID)
	if run.Status != chat.StatusSucceeded {
		t.Fatal(run)
	}
	assertAttemptEvents(t, fixture.service, receipt.RunID, 6)
}

// TestToolTimeoutRetryIsolatesOldAttempt 验证单次期限固定、重试热读期限与在途旧结果隔离。
func TestToolTimeoutRetryIsolatesOldAttempt(t *testing.T) {
	started := make(chan time.Time, 2)
	firstLate, secondRelease, firstReturned := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var attempts atomic.Int32
	var timeout atomic.Int64
	timeout.Store(int64(150 * time.Millisecond))
	fixture := newAttemptFixture(t, []chat.ToolRequest{toolRequest("isolated", "isolated", `{}`)}, func(cfg *chat.ModelConfig) {
		one := 1
		cfg.ToolMaxRetry = &one
		cfg.ToolTimeout = time.Duration(timeout.Load())
	}, attemptTool{
		name:     "isolated",
		parallel: true,
		run: func(ctx context.Context, _ string) (string, error) {
			attempt := attempts.Add(1)
			deadline, _ := ctx.Deadline()
			started <- deadline
			if attempt == 1 {
				<-firstLate
				close(firstReturned)
				return `"stale"`, nil
			}
			select {
			case <-secondRelease:
				return `"current"`, nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		},
	})
	var releaseFirst, releaseSecond sync.Once
	t.Cleanup(func() {
		releaseFirst.Do(func() { close(firstLate) })
		releaseSecond.Do(func() { close(secondRelease) })
	})
	receipt := createAttemptRun(t, fixture.service, "late-retry")
	firstDeadline := <-started
	timeout.Store(int64(time.Second))
	call, original := awaitToolState(t, fixture.storage, receipt.RunID, "isolated", "retry_wait")
	if time.Now().Before(firstDeadline) {
		t.Fatal("未达到原期限便超时")
	}
	select {
	case deadline := <-started:
		if time.Until(deadline) < 800*time.Millisecond {
			t.Fatal("重试未读取最新期限")
		}
	case <-time.After(7 * time.Second):
		t.Fatal("超时重试未执行")
	}
	releaseFirst.Do(func() { close(firstLate) })
	<-firstReturned
	time.Sleep(100 * time.Millisecond)
	_, current := awaitToolState(t, fixture.storage, receipt.RunID, "isolated", "running")
	if current.ID != original.ID || current.RetryCount != 1 || fixture.models.Load() != 1 {
		t.Fatalf("迟到结果推进运行: %+v", current)
	}
	releaseSecond.Do(func() { close(secondRelease) })
	run := awaitFinishedRun(t, fixture.service, receipt.RunID)
	stored, err := fixture.storage.Tools.ReadTools(context.Background(), call.ID)
	if err != nil || len(stored) != 1 || string(stored[0].Output) != `"current"` || run.Status != chat.StatusSucceeded || attempts.Load() != 2 {
		t.Fatalf("原尝试覆盖重试结果: %+v run=%+v err=%v", stored, run, err)
	}
	assertAttemptEvents(t, fixture.service, receipt.RunID, 1)
}

func receiveAttemptStart(t *testing.T, started <-chan string) string {
	t.Helper()
	select {
	case name := <-started:
		return name
	case <-time.After(3 * time.Second):
		t.Fatal("就绪工具未启动")
		return ""
	}
}

// retryWaitCommitGate 在真实提交边界同步测试与协调器，不替代退避状态保存。
type retryWaitCommitGate struct {
	chat.ToolRepository
	committed chan chat.ToolCall
	release   chan struct{}
}

// WaitToolRetry 在真实退避提交后暂停，避免测试通过轮询竞争短暂状态。
func (g *retryWaitCommitGate) WaitToolRetry(ctx context.Context, tool chat.ToolCall) error {
	if err := g.ToolRepository.WaitToolRetry(ctx, tool); err != nil {
		return err
	}
	g.committed <- tool
	<-g.release
	return nil
}

// TestToolRetryExhaustionAndCancelWait 验证最后一次失败如实回填，取消退避不再尝试也不提前完成。
func TestToolRetryExhaustionAndCancelWait(t *testing.T) {
	for _, cancelWait := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel_%t", cancelWait), func(t *testing.T) {
			var attempts atomic.Int32
			fixture := newAttemptFixture(t, []chat.ToolRequest{toolRequest("retry", "temporary", `{}`)}, func(cfg *chat.ModelConfig) {
				one := 1
				cfg.ToolMaxRetry = &one
			}, attemptTool{
				name: "temporary",
				run: func(context.Context, string) (string, error) {
					attempts.Add(1)
					return "", &tools.Failure{
						Code:    "tool_temporary_error",
						Message: "暂时故障的实际说明",
					}
				},
			})
			fixture.service.Stop()
			gate := &retryWaitCommitGate{
				ToolRepository: fixture.storage.Tools,
				committed:      make(chan chat.ToolCall, 1),
				release:        make(chan struct{}),
			}
			fixture.storage.Tools = gate
			fixture.restart()
			var once sync.Once
			t.Cleanup(func() { once.Do(func() { close(gate.release) }) })
			receipt := createAttemptRun(t, fixture.service, "exhausted")
			var waiting chat.ToolCall
			select {
			case waiting = <-gate.committed:
			case <-time.After(30 * time.Second):
				t.Fatal("工具未提交退避状态")
			}
			if cancelWait {
				// 协调器仍停在退避提交边界，先请求取消，再释放它进行收尾。
				cancelCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if _, err := fixture.service.CancelRun(cancelCtx, receipt.RunID); !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal(err)
				}
			}
			once.Do(func() { close(gate.release) })
			run := awaitFinishedRunWithin(t, fixture.service, receipt.RunID, 30*time.Second)
			stored, err := fixture.storage.Tools.ReadTools(context.Background(), waiting.ModelCallID)
			if err != nil || len(stored) != 1 || stored[0].Error == nil || stored[0].NextRetryAt != nil {
				t.Fatalf("最终记录: %+v %v", stored, err)
			}
			if cancelWait {
				if attempts.Load() != 1 || run.Status != chat.StatusCancelled || stored[0].State != "skipped" || stored[0].Error.Code != "tool_skipped" || fixture.models.Load() != 1 {
					t.Fatalf("取消继续重试: run=%+v tool=%+v", run, stored[0])
				}
			} else if attempts.Load() != 2 || run.Status != chat.StatusSucceeded || stored[0].RetryCount != 1 || stored[0].State != "failed" || stored[0].Error.Code != "tool_temporary_error" {
				t.Fatalf("耗尽没有回填实际错误: run=%+v tool=%+v", run, stored[0])
			}
			assertAttemptEvents(t, fixture.service, receipt.RunID, 1)
		})
	}
}

type deleteTransientFailure struct {
	tools.Tool
	attempts atomic.Int32
}

// InvokableRun 仅在正式删除已提交后的响应边界注入一次暂时故障。
func (f *deleteTransientFailure) InvokableRun(ctx context.Context, raw string) (string, error) {
	output, err := f.Tool.InvokableRun(ctx, raw)
	if err == nil && f.attempts.Add(1) == 1 {
		return "", &tools.Failure{
			Code:    "tool_temporary_error",
			Message: "业务已提交后的暂时响应故障",
		}
	}
	return output, err
}

// TestToolRetryKeepsDeleteReceiptIdentity 验证真实删除提交后重试复用原工具键与业务回执。
func TestToolRetryKeepsDeleteReceiptIdentity(t *testing.T) {
	_, storage, application, _ := deleteFixture(t)
	var models atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if models.Add(1) == 1 {
			emitTools(w, []chat.ToolRequest{toolRequest("delete-retry", "delete_schedule", ` {"id":1,"expectedRevision":1} `)})
			return
		}
		results := requestToolResults(t, r)
		var result map[string]any
		if json.Unmarshal(results["delete-retry"], &result) != nil || result["deleted"] != true || result["revision"] != float64(2) {
			t.Errorf("未复用实际删除: %s", results["delete-retry"])
		}
		emitText(w, "已删除")
	}))
	t.Cleanup(upstream.Close)
	fault := &deleteTransientFailure{Tool: tools.ScheduleDeleteTools(application)[0]}
	registry, err := tools.NewRegistry(context.Background(), fault)
	if err != nil {
		t.Fatal(err)
	}
	service := chat.NewService(storage, completion.NewClient(nil), toolSettings(upstream.URL), registry)
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Stop)
	receipt := createAttemptRun(t, service, "delete-retry")
	call, original := awaitToolState(t, storage, receipt.RunID, "delete-retry", "retry_wait")
	assertDeleteKey(t, original)
	before, err := application.ReadScheduleDeleteReceipt(context.Background(), 1, 1, original.IdempotencyKey)
	if err != nil || before == nil {
		t.Fatalf("正式业务未提交: %+v %v", before, err)
	}
	deadline := time.Now().Add(8 * time.Second)
	for fault.attempts.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	run := awaitFinishedRun(t, service, receipt.RunID)
	stored, err := storage.Tools.ReadTools(context.Background(), call.ID)
	if err != nil || len(stored) != 1 || stored[0].ID != original.ID || stored[0].IdempotencyKey != original.IdempotencyKey || stored[0].Request != original.Request || stored[0].RetryCount != 1 || stored[0].State != "succeeded" || run.Status != chat.StatusSucceeded {
		t.Fatalf("删除重试身份错误: %+v run=%+v err=%v", stored, run, err)
	}
	after, err := application.ReadScheduleDeleteReceipt(context.Background(), 1, 1, original.IdempotencyKey)
	if err != nil || mustJSON(before) != mustJSON(after) {
		t.Fatalf("重试改写业务回执: before=%+v after=%+v err=%v", before, after, err)
	}
	assertAttemptEvents(t, service, receipt.RunID, 1)
}

type attemptFixture struct {
	service         *chat.Runtime
	storage         chat.Storage
	models          atomic.Int32
	settingsFailure atomic.Bool
	restart         func()
}

func newAttemptFixture(t *testing.T, calls []chat.ToolRequest, configure func(*chat.ModelConfig), controlled ...attemptTool) *attemptFixture {
	t.Helper()
	fixture := &attemptFixture{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if fixture.models.Add(1) == 1 {
			emitTools(w, calls)
			return
		}
		var request chat.ModelRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		paired := map[string]bool{}
		for _, message := range request.Messages {
			if message.Role == "tool" {
				if paired[message.ToolCallID] {
					t.Errorf("重复回填: %s", message.ToolCallID)
				}
				paired[message.ToolCallID] = true
			}
		}
		if len(paired) != len(calls) {
			t.Errorf("整批未完成即继续模型: %s", mustJSON(request.Messages))
		}
		emitText(w, "整批完成")
	}))
	t.Cleanup(upstream.Close)
	old, storage, application := newToolFixture(t, upstream.URL)
	old.Stop()
	fixture.storage = storage
	registry := queryRegistry(t, application)
	for _, tool := range controlled {
		if err := registry.Register(context.Background(), tool); err != nil {
			t.Fatal(err)
		}
	}
	settings := func() ([]chat.ModelConfig, error) {
		if fixture.settingsFailure.Load() {
			return nil, errors.New("injected settings read failure")
		}
		cfg, err := toolSettings(upstream.URL)()
		if configure != nil {
			configure(&cfg[0])
		}
		return cfg, err
	}
	fixture.restart = func() {
		fixture.service = chat.NewService(fixture.storage, completion.NewClient(nil), settings, registry)
		if err := fixture.service.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	fixture.restart()
	t.Cleanup(func() { fixture.service.Stop() })
	return fixture
}

// TestToolSettingsReadFailureKeepsEffectiveLimits 验证密钥读取故障不扩大现有并发及零重试权限。
func TestToolSettingsReadFailureKeepsEffectiveLimits(t *testing.T) {
	started := make(chan string, 2)
	release := make(chan struct{})
	fixture := newAttemptFixture(t, []chat.ToolRequest{
		toolRequest("first", "first", `{}`),
		toolRequest("second", "second", `{}`),
	}, func(cfg *chat.ModelConfig) {
		zero := 0
		cfg.ToolConcurrency = 1
		cfg.ToolMaxRetry = &zero
	}, attemptTool{
		name:     "first",
		parallel: true,
		run:      func(context.Context, string) (string, error) { started <- "first"; <-release; return `true`, nil },
	}, attemptTool{
		name:     "second",
		parallel: true,
		run: func(context.Context, string) (string, error) {
			started <- "second"
			return "", &tools.Failure{
				Code:    "tool_temporary_error",
				Message: "不允许重试",
			}
		},
	})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	receipt := createAttemptRun(t, fixture.service, "config-failure")
	if receiveAttemptStart(t, started) != "first" {
		t.Fatal("并发上限未生效")
	}
	fixture.settingsFailure.Store(true)
	select {
	case name := <-started:
		t.Fatalf("配置读取失败扩大并发: %s", name)
	case <-time.After(250 * time.Millisecond):
	}
	once.Do(func() { close(release) })
	receiveAttemptStart(t, started)
	run := awaitFinishedRun(t, fixture.service, receipt.RunID)
	_, saved := awaitToolState(t, fixture.storage, receipt.RunID, "second", "failed")
	if saved.MaxRetry != 0 || saved.RetryCount != 0 || run.Error == nil || run.Error.Code != "model_config_unavailable" {
		t.Fatalf("读取失败重置预算或模型门禁: tool=%+v run=%+v", saved, run)
	}
}

func createAttemptRun(t *testing.T, service *chat.Runtime, session string) chat.Receipt {
	t.Helper()
	receipt, err := service.CreateRun(context.Background(), admissionInput(session, "key"))
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func awaitToolState(t *testing.T, storage chat.Storage, run, id, state string) (*chat.ModelCall, chat.ToolCall) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		call, err := storage.Execution.ReadCall(context.Background(), run)
		if err != nil {
			t.Fatal(err)
		}
		if call != nil {
			stored, err := storage.Tools.ReadTools(context.Background(), call.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, tool := range stored {
				if tool.Request.ID == id && tool.State == state {
					return call, tool
				}
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("工具 %s 未进入 %s", id, state)
	return nil, chat.ToolCall{}
}

func assertAttemptEvents(t *testing.T, service *chat.Runtime, run string, count int) {
	t.Helper()
	page, err := service.GetRunEvents(context.Background(), run, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	starts, results := 0, 0
	for _, event := range page.Events {
		if event.Type == chat.EventToolStart {
			starts++
		}
		if event.Type == chat.EventToolResult {
			results++
		}
	}
	if starts != count || results != count {
		t.Fatalf("重试重复事件或丢失结果: %s", mustJSON(page))
	}
}
