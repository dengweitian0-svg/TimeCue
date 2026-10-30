package chat_test

import (
	"context"
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

type slowTimeoutSettlement struct {
	chat.ToolRepository
	timeout      *atomic.Int64
	settling     chan struct{}
	peerContext  chan context.Context
	peerReturned chan struct{}
	settled      chan struct{}
	abort        chan struct{}
	modelID      atomic.Int64
}

// BeginTool 在首次开始提交后为其他尝试设置较长的独立期限。
func (g *slowTimeoutSettlement) BeginTool(ctx context.Context, call chat.ModelCall, tool chat.ToolCall) (chat.ModelCall, chat.ToolCall, error) {
	progress, saved, err := g.ToolRepository.BeginTool(ctx, call, tool)
	g.modelID.Store(call.ID)
	if tool.Request.ID == "expired" {
		g.timeout.Store(int64(5 * time.Second))
	}
	return progress, saved, err
}

// FinishTool 只延迟一个超时尝试的保存，期间其他工具仍能在原期限内返回。
func (g *slowTimeoutSettlement) FinishTool(ctx context.Context, call chat.ModelCall, tool chat.ToolCall) (chat.ModelCall, error) {
	if tool.Request.ID == "expired" {
		close(g.settling)
		// 先确认其他工具已实际返回，再等其原期限经过，精确构造保存阻塞窗口。
		var peer context.Context
		select {
		case peer = <-g.peerContext:
		case <-g.abort:
			return call, context.Canceled
		}
		for _, signal := range []<-chan struct{}{g.peerReturned, peer.Done()} {
			select {
			case <-signal:
			case <-g.abort:
				return call, context.Canceled
			}
		}
	}
	progress, err := g.ToolRepository.FinishTool(ctx, call, tool)
	if tool.Request.ID == "expired" {
		close(g.settled)
	}
	return progress, err
}

// TestToolTimelyResultsSurviveSlowTimeoutSettlement 验证阻塞结算期间已及时返回的其他结果不会被误判超时。
func TestToolTimelyResultsSurviveSlowTimeoutSettlement(t *testing.T) {
	// 多个并发请求和重复轮次覆盖 map 遍历中超时项位于不同位置的情况。
	for round := 0; round < 6; round++ {
		t.Run(fmt.Sprintf("round_%d", round), func(t *testing.T) {
			_, storage, _ := admissionDatabase(t)
			var timeout atomic.Int64
			timeout.Store(int64(50 * time.Millisecond))
			gate := &slowTimeoutSettlement{
				ToolRepository: storage.Tools,
				timeout:        &timeout,
				settling:       make(chan struct{}),
				peerContext:    make(chan context.Context, 1),
				peerReturned:   make(chan struct{}),
				settled:        make(chan struct{}),
				abort:          make(chan struct{}),
			}
			storage.Tools = gate
			release := make(chan struct{})
			returned := make(chan struct{})
			registry, err := tools.NewRegistry(context.Background(), attemptTool{
				name:     "expired",
				parallel: true,
				run:      func(context.Context, string) (string, error) { <-release; close(returned); return `true`, nil },
			}, attemptTool{
				name:     "timely",
				parallel: true,
				run: func(ctx context.Context, _ string) (string, error) {
					gate.peerContext <- ctx
					select {
					case <-gate.settling:
					case <-gate.abort:
						return "", context.Canceled
					}
					if ctx.Err() != nil {
						t.Error("工具未能在原期限内返回，复现前提不成立")
					}
					close(gate.peerReturned)
					return `"actual success"`, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			requests := []chat.ToolRequest{toolRequest("expired", "expired", `{}`)}
			requests = append(requests, toolRequest("timely", "timely", `{}`))
			var models atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				if models.Add(1) == 1 {
					emitTools(w, requests)
					return
				}
				paired := requestToolResults(t, r)
				for _, request := range requests[1:] {
					if string(paired[request.ID]) != `"actual success"` {
						t.Errorf("及时成功未回填: %s=%s", request.ID, paired[request.ID])
					}
				}
				emitText(w, "done")
			}))
			t.Cleanup(upstream.Close)
			settings := func() ([]chat.ModelConfig, error) {
				cfg, err := toolSettings(upstream.URL)()
				zero := 0
				cfg[0].ToolMaxRetry = &zero
				cfg[0].ToolConcurrency = 2
				cfg[0].ToolTimeout = time.Duration(timeout.Load())
				return cfg, err
			}
			service := chat.NewService(storage, completion.NewClient(nil), settings, registry)
			if err := service.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(service.Stop)
			t.Cleanup(func() {
				close(gate.abort)
				close(release)
				select {
				case <-returned:
				case <-time.After(time.Second):
					t.Error("旧尝试未退出")
				}
			})
			receipt := createAttemptRun(t, service, "slow-timeout")
			select {
			case <-gate.settled:
			case <-time.After(30 * time.Second):
				t.Fatal("未完成超时保存边界")
			}
			run := awaitFinishedRunWithin(t, service, receipt.RunID, 30*time.Second)
			stored, err := storage.Tools.ReadTools(context.Background(), gate.modelID.Load())
			if err != nil || len(stored) != 2 {
				t.Fatalf("工具记录: %+v %v", stored, err)
			}
			for _, tool := range stored {
				if tool.Request.ID != "expired" && (tool.State != "succeeded" || string(tool.Output) != `"actual success"` || tool.RetryCount != 0) {
					t.Errorf("及时成功被误报超时: %+v", tool)
				}
			}
			if run.Status != chat.StatusSucceeded || models.Load() != 2 {
				t.Fatalf("运行未按整批真实结果完成: %+v models=%d", run, models.Load())
			}
			assertAttemptEvents(t, service, receipt.RunID, 2)
		})
	}
}

// TestToolShutdownPreservesRetryableAttempt 验证正常退出后保留原逻辑工具、期限及剩余预算供重启继续。
func TestToolShutdownPreservesRetryableAttempt(t *testing.T) {
	for _, cause := range []string{"temporary", "timeout"} {
		t.Run(cause, func(t *testing.T) {
			started := make(chan time.Time, 2)
			release := make(chan struct{})
			var attempts atomic.Int32
			fixture := newAttemptFixture(t, []chat.ToolRequest{toolRequest("resumable", "resumable", ` { } `)}, func(cfg *chat.ModelConfig) {
				one := 1
				cfg.ToolMaxRetry = &one
				cfg.ToolTimeout = 200 * time.Millisecond
			}, attemptTool{
				name:     "resumable",
				parallel: true,
				run: func(ctx context.Context, raw string) (string, error) {
					started <- time.Now()
					if raw != ` { } ` {
						t.Errorf("恢复改写原参数: %q", raw)
					}
					if attempts.Add(1) > 1 {
						return `"recovered"`, nil
					}
					if cause == "timeout" {
						<-ctx.Done()
						return "", ctx.Err()
					}
					<-release
					return "", &tools.Failure{
						Code:    "tool_temporary_error",
						Message: "退出期间的暂时故障",
					}
				},
			})
			var once sync.Once
			t.Cleanup(func() { once.Do(func() { close(release) }) })
			receipt := createAttemptRun(t, fixture.service, "shutdown-retry")
			<-started
			stopped := make(chan struct{})
			go func() { fixture.service.Stop(); close(stopped) }()
			// 原请求重放不创建运行，可安全确认 Stop 已停止准入后再返回暂时错误。
			deadline := time.Now().Add(time.Second)
			for {
				_, err := fixture.service.CreateRun(context.Background(), admissionInput("shutdown-retry", "key"))
				if errors.Is(err, chat.ErrStopping) {
					break
				}
				if err != nil || time.Now().After(deadline) {
					t.Fatalf("退出未停止准入: %v", err)
				}
				time.Sleep(time.Millisecond)
			}
			once.Do(func() { close(release) })
			select {
			case <-stopped:
			case <-time.After(2 * time.Second):
				t.Fatal("正常退出没有完成")
			}
			call, err := fixture.storage.Execution.ReadCall(context.Background(), receipt.RunID)
			if err != nil || call == nil {
				t.Fatal(call, err)
			}
			stored, err := fixture.storage.Tools.ReadTools(context.Background(), call.ID)
			if err != nil || len(stored) != 1 {
				t.Fatal(stored, err)
			}
			original := stored[0]
			if original.State != "retry_wait" || original.RetryCount != 0 || original.MaxRetry != 1 || original.NextRetryAt == nil || call.ToolCompleted("resumable") || call.CheckpointStage != "tool_start" {
				t.Fatalf("退出提前终结仍可重试的工具: tool=%+v stage=%s completed=%v", original, call.CheckpointStage, call.CompletedTools)
			}
			page, err := fixture.service.GetRunEvents(context.Background(), receipt.RunID, 0, 100)
			if err != nil || len(page.Events) != 2 || page.Events[1].Type != chat.EventToolStart {
				t.Fatalf("退出生成最终结果: %s %v", mustJSON(page), err)
			}
			fixture.restart()
			select {
			case at := <-started:
				if at.Before(*original.NextRetryAt) {
					t.Fatal("重启未保留退避期限")
				}
			case <-time.After(7 * time.Second):
				t.Fatal("重启没有使用剩余预算")
			}
			run := awaitFinishedRun(t, fixture.service, receipt.RunID)
			stored, err = fixture.storage.Tools.ReadTools(context.Background(), call.ID)
			if err != nil || len(stored) != 1 || stored[0].ID != original.ID || stored[0].Request != original.Request || stored[0].State != "succeeded" || stored[0].RetryCount != 1 || stored[0].NextRetryAt != nil || run.Status != chat.StatusSucceeded || attempts.Load() != 2 || fixture.models.Load() != 2 {
				t.Fatalf("恢复未沿原逻辑调用完成: %+v run=%+v attempts=%d models=%d err=%v", stored, run, attempts.Load(), fixture.models.Load(), err)
			}
			assertAttemptEvents(t, fixture.service, receipt.RunID, 1)
		})
	}
}

type slowCancelStorage struct{ chat.RunRepository }

// RequestCancel 在真实取消保存入口模拟慢存储，避免用毫秒级请求期限断言业务取消行为。
func (s slowCancelStorage) RequestCancel(ctx context.Context, id string) (chat.RunState, error) {
	timer := time.NewTimer(50 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-timer.C:
		return s.RunRepository.RequestCancel(ctx, id)
	}
}

// TestToolCancellationDoesNotPreserveRetryOnShutdown 验证用户取消优先于正常退出，不保留后续重试。
func TestToolCancellationDoesNotPreserveRetryOnShutdown(t *testing.T) {
	for _, stop := range []bool{false, true} {
		t.Run(fmt.Sprintf("shutdown_%t", stop), func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			var attempts atomic.Int32
			fixture := newAttemptFixture(t, []chat.ToolRequest{toolRequest("cancelled", "cancelled", `{}`)}, nil, attemptTool{
				name:     "cancelled",
				parallel: true,
				run: func(context.Context, string) (string, error) {
					attempts.Add(1)
					close(started)
					<-release
					return "", &tools.Failure{
						Code:    "tool_temporary_error",
						Message: "取消期间的实际故障",
					}
				},
			})
			fixture.service.Stop()
			fixture.storage.Runs = slowCancelStorage{RunRepository: fixture.storage.Runs}
			fixture.restart()
			var once sync.Once
			t.Cleanup(func() { once.Do(func() { close(release) }) })
			receipt := createAttemptRun(t, fixture.service, "cancel-shutdown")
			<-started
			if stop {
				go fixture.service.Stop()
				deadline := time.Now().Add(time.Second)
				for {
					_, err := fixture.service.CreateRun(context.Background(), admissionInput("cancel-shutdown", "key"))
					if errors.Is(err, chat.ErrStopping) {
						break
					}
					if err != nil || time.Now().After(deadline) {
						t.Fatalf("退出未停止准入: %v", err)
					}
					time.Sleep(time.Millisecond)
				}
			}
			// 请求期限只防止测试挂起，不把取消意图的真实落库限制在二十毫秒内。
			cancelCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if status, err := fixture.service.CancelRun(cancelCtx, receipt.RunID); err != nil || status != chat.StatusCancelled {
				t.Fatalf("未进入在途取消: status=%s err=%v", status, err)
			}
			once.Do(func() { close(release) })
			run := awaitFinishedRunWithin(t, fixture.service, receipt.RunID, 30*time.Second)
			call, err := fixture.storage.Execution.ReadCall(context.Background(), receipt.RunID)
			if err != nil || call == nil {
				t.Fatal(call, err)
			}
			stored, err := fixture.storage.Tools.ReadTools(context.Background(), call.ID)
			if err != nil || len(stored) != 1 || stored[0].State != "failed" || stored[0].RetryCount != 0 || stored[0].NextRetryAt != nil || !call.ToolCompleted("cancelled") || run.Status != chat.StatusCancelled || attempts.Load() != 1 || fixture.models.Load() != 1 {
				t.Fatalf("用户取消被退出恢复覆盖: %+v run=%+v err=%v", stored, run, err)
			}
			assertAttemptEvents(t, fixture.service, receipt.RunID, 1)
		})
	}
}
