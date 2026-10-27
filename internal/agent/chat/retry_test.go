package chat_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/agent/completion"
	"github.com/dengweitian0-svg/TimeCue/internal/config"
	"github.com/dengweitian0-svg/TimeCue/internal/store"
)

// TestTemporaryFailureRetriesSameLogicalCall 验证暂时故障在原逻辑调用内有限重试并成功。
func TestTemporaryFailureRetriesSameLogicalCall(t *testing.T) {
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			w.WriteHeader(503)
			fmt.Fprint(w, `{"error":{"message":"busy"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"retry-success\",\"choices\":[{\"delta\":{\"content\":\"恢复成功\"},\"finish_reason\":\"stop\"}],\"usage\":{\"total_tokens\":7}}\n\ndata: [DONE]\n\n")
	}))
	defer upstream.Close()
	repo, service := retryFixture(t, retrySettings(upstream.URL, 1))
	id := createRetryRun(t, service)
	planned := awaitRetryWait(t, repo, id, 1)
	run := awaitRetryRun(t, service, id, 8*time.Second)
	if run.Status != chat.StatusSucceeded || run.Output == nil || *run.Output != "恢复成功" {
		t.Fatalf("临时故障应恢复成功: %+v", run)
	}
	if requests.Load() != 2 {
		t.Fatalf("requests=%d", requests.Load())
	}
	call, err := repo.Execution.ReadCall(context.Background(), id)
	if err != nil || call.ID != planned.ID || call.RetryCount != 1 || call.MaxRetry != 1 || call.NextRetryAt != nil || len(call.RetryHistory) != 1 || call.RetryHistory[0].Cause.Code != "model_server_busy" || call.RetryHistory[0].Result != "succeeded" {
		t.Fatalf("逻辑调用未保存原预算及重试结果: %+v %v", call, err)
	}
}

func retryFixture(t *testing.T, settings func() ([]chat.ModelConfig, error)) (chat.Storage, *chat.Runtime) {
	t.Helper()
	db := store.NewSqlite()
	if err := db.Connect(context.Background(), filepath.Join(t.TempDir(), "chat.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PrepareSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	repo := store.NewChatStorage(db)
	return repo, startRetryService(t, repo, settings)
}

func startRetryService(t *testing.T, repo chat.Storage, settings func() ([]chat.ModelConfig, error)) *chat.Runtime {
	t.Helper()
	service := chat.NewService(repo, completion.NewClient(nil), settings, nil)
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Stop)
	return service
}

func retrySettings(url string, retries int) func() ([]chat.ModelConfig, error) {
	return func() ([]chat.ModelConfig, error) {
		return []chat.ModelConfig{{
			Provider:        "test",
			Model:           "model",
			BaseURL:         url,
			Key:             "secret",
			ContextWindow:   32768,
			MaxOutputTokens: 8192,
			MaxRetry:        retries,
		}}, nil
	}
}

func createRetryRun(t *testing.T, service chat.RunService) string {
	t.Helper()
	receipt, err := service.CreateRun(context.Background(), chat.CreateRunInput{
		SessionID:      "retry-session",
		Input:          "hello",
		Provider:       "test",
		Model:          "model",
		IdempotencyKey: "retry",
	})
	if err != nil {
		t.Fatal(err)
	}
	return receipt.RunID
}

func awaitRetryWait(t *testing.T, repo chat.Storage, id string, retry int) *chat.ModelCall {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		call, err := repo.Execution.ReadCall(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if call != nil && call.State == chat.StateRetryWait && call.RetryCount == retry {
			return call
		}
		if time.Now().After(deadline) {
			t.Fatalf("调用未进入第 %d 次退避: %+v", retry, call)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestCancelDuringRetryWaitStopsFutureRequests 验证取消退避中的运行不再请求上游。
func TestCancelDuringRetryWaitStopsFutureRequests(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(503)
		fmt.Fprint(w, `{"error":{"message":"busy"}}`)
	}))
	defer server.Close()
	repo, service := retryFixture(t, retrySettings(server.URL, 3))
	id := createRetryRun(t, service)
	awaitRetryWait(t, repo, id, 1)
	status, err := service.CancelRun(context.Background(), id)
	if err != nil || status != chat.StatusCancelled {
		t.Fatalf("取消失败: %s %v", status, err)
	}
	call, err := repo.Execution.ReadCall(context.Background(), id)
	if err != nil || call.NextRetryAt != nil || call.RetryHistory[0].Result != "cancelled" || requests.Load() != 1 {
		t.Fatalf("取消后仍保留计划重试: %+v err=%v requests=%d", call, err, requests.Load())
	}
}

func awaitRetryRun(t *testing.T, service chat.RunService, id string, timeout time.Duration) chat.Run {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		run, err := service.GetRun(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if run.Status != chat.StatusRunning {
			return run
		}
		if time.Now().After(deadline) {
			t.Fatal("等待重试终态超时")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestRetryWaitRestartsWithoutResettingBudgetOrDeadline 验证重启沿用同调用的原预算和计划期限。
func TestRetryWaitRestartsWithoutResettingBudgetOrDeadline(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			w.WriteHeader(503)
			fmt.Fprint(w, `{"error":{"message":"busy"}}`)
			return
		}
		completedUpstreamForRetry(w)
	}))
	defer server.Close()
	repo, first := retryFixture(t, retrySettings(server.URL, 1))
	id := createRetryRun(t, first)
	planned := awaitRetryWait(t, repo, id, 1)
	first.Stop()
	paused, err := repo.Execution.ReadCall(context.Background(), id)
	if err != nil || paused.State != chat.StateRetryWait || !paused.NextRetryAt.Equal(*planned.NextRetryAt) {
		t.Fatalf("退出没有保留退避事实: %+v %v", paused, err)
	}
	second := startRetryService(t, repo, retrySettings(server.URL, 0))
	run := awaitRetryRun(t, second, id, 8*time.Second)
	call, err := repo.Execution.ReadCall(context.Background(), id)
	if err != nil || run.Status != chat.StatusSucceeded || call.ID != planned.ID || call.MaxRetry != 1 || call.RetryCount != 1 || len(call.RetryHistory) != 1 || requests.Load() != 2 {
		t.Fatalf("恢复没有沿用同调用最后一次许可: run=%+v call=%+v err=%v", run, call, err)
	}
}

func completedUpstreamForRetry(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprint(w, "data: {\"id\":\"success\",\"choices\":[{\"delta\":{\"content\":\"成功\"},\"finish_reason\":\"stop\"}],\"usage\":{\"total_tokens\":7}}\n\ndata: [DONE]\n\n")
}

// TestRetriesReadLatestKeyAndTimeoutButKeepRegisteredBudget 验证重试读取最新密钥及超时并保持原预算。
func TestRetriesReadLatestKeyAndTimeoutButKeepRegisteredBudget(t *testing.T) {
	dir := t.TempDir()
	firstStarted := make(chan struct{})
	requests := make(chan string, 2)
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.Header.Get("Authorization")
		if count.Add(1) == 1 {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, ": keep-alive\n\n")
			w.(http.Flusher).Flush()
			close(firstStarted)
			<-r.Context().Done()
			return
		}
		timer := time.NewTimer(1200 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
			completedUpstreamForRetry(w)
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	base := "chat_model:\n  - provider: test\n    base_url: " + server.URL + "\n    models:\n      - model: model\n        context_window: 32768\n"
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(base+"chat:\n  max_retry: 1\n  first_timeout_seconds: 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("TEST_MODEL=original\n"), 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := config.NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if err := manager.LoadConfig(); err != nil {
		t.Fatal(err)
	}
	repo, service := retryFixture(t, manager.ChatModels)
	id := createRetryRun(t, service)
	select {
	case <-firstStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("没有开始首次请求")
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("TEST_MODEL=replacement\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(base+"chat:\n  max_retry: 0\n  first_timeout_seconds: 2\n  idle_timeout_seconds: 3\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run := awaitRetryRun(t, service, id, 10*time.Second)
	call, err := repo.Execution.ReadCall(context.Background(), id)
	if err != nil || run.Status != chat.StatusSucceeded || call.MaxRetry != 1 || call.RetryCount != 1 || call.RetryHistory[0].Cause.Code != "model_timeout" {
		t.Fatalf("重试没有重读超时或错误重置了预算: run=%+v call=%+v %v", run, call, err)
	}
	if <-requests != "Bearer original" || <-requests != "Bearer replacement" {
		t.Fatal("后续请求未使用最新 Key")
	}
}

// TestZeroBudgetAndPartialOutputsDoNotRetry 验证零重试预算、部分输出和不可重试错误。
func TestZeroBudgetAndPartialOutputsDoNotRetry(t *testing.T) {
	for _, mode := range []string{"zero", "text", "thinking", "auth", "tool"} {
		t.Run(mode, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if mode == "zero" || mode == "auth" {
					status := 503
					if mode == "auth" {
						status = 401
					}
					w.WriteHeader(status)
					fmt.Fprint(w, `{"error":{"message":"secret"}}`)
					return
				}
				delta := `"content":"部分正文"`
				if mode == "thinking" {
					delta = `"reasoning_content":"部分思考"`
				}
				if mode == "tool" {
					delta = `"tool_calls":[{"index":0,"id":"tool","type":"function","function":{"name":"query","arguments":"{"}}]`
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "data: {\"id\":\"partial\",\"choices\":[{\"delta\":{%s}}],\"usage\":{\"total_tokens\":3}}\n\n", delta)
			}))
			defer server.Close()
			retries := 3
			if mode == "zero" {
				retries = 0
			}
			repo, service := retryFixture(t, retrySettings(server.URL, retries))
			id := createRetryRun(t, service)
			run := awaitRun(t, service, id)
			call, err := repo.Execution.ReadCall(context.Background(), id)
			if err != nil || run.Status != chat.StatusFailed || call.RetryCount != 0 || requests.Load() != 1 {
				t.Fatalf("不可重试的失败进行了重试: %+v %+v %v", run, call, err)
			}
			if mode == "text" && (run.Output == nil || *run.Output != "部分正文") {
				t.Fatal("部分正文丢失")
			}
			if mode == "thinking" && call.Output.Message.ReasoningContent != "部分思考" {
				t.Fatal("部分思考丢失")
			}
			if mode == "text" || mode == "thinking" {
				if string(call.Output.Usage) != `{"total_tokens":3}` {
					t.Fatal("部分 usage 丢失")
				}
			}
		})
	}
}

// TestThreeRetriesUseExponentialBackoffAndThenFail 验证三次指数退避及预算耗尽后的唯一失败终态。
func TestThreeRetriesUseExponentialBackoffAndThenFail(t *testing.T) {
	started := make(chan time.Time, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- time.Now()
		w.WriteHeader(429)
		fmt.Fprint(w, `{"error":{"message":"limited"}}`)
	}))
	defer server.Close()
	repo, service := retryFixture(t, retrySettings(server.URL, 3))
	id := createRetryRun(t, service)
	run := awaitRetryRun(t, service, id, 45*time.Second)
	call, err := repo.Execution.ReadCall(context.Background(), id)
	if err != nil || run.Status != chat.StatusFailed || run.Error.Code != "model_rate_limited" || call.RetryCount != 3 || len(call.RetryHistory) != 3 || call.NextRetryAt != nil || len(started) != 4 {
		t.Fatalf("重试预算或耗尽终态错误: %+v %+v %v", run, call, err)
	}
	previous := <-started
	for _, minimum := range []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second} {
		at := <-started
		if at.Sub(previous) < minimum {
			t.Fatalf("退避不足: got=%s want>=%s", at.Sub(previous), minimum)
		}
		previous = at
	}
	page, err := service.GetRunEvents(context.Background(), id, 0, 100)
	if err != nil || len(page.Events) != 3 || page.Events[1].Type != chat.EventError || page.Events[2].Type != chat.EventEnd {
		t.Fatalf("重试生成了重复公开错误或终态: %+v %v", page, err)
	}
}

// TestKeyRemovedDuringRetryWaitSettlesHistory 验证退避期间删除密钥会结算历史且不再请求。
func TestKeyRemovedDuringRetryWaitSettlesHistory(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(503)
		fmt.Fprint(w, `{"error":{"message":"busy"}}`)
	}))
	defer server.Close()
	dir := t.TempDir()
	text := "chat_model:\n  - provider: test\n    base_url: " + server.URL + "\n    models:\n      - model: model\n        context_window: 32768\nchat:\n  max_retry: 1\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(dir, ".env")
	if err := os.WriteFile(envPath, []byte("TEST_MODEL=secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := config.NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if err := manager.LoadConfig(); err != nil {
		t.Fatal(err)
	}
	repo, service := retryFixture(t, manager.ChatModels)
	id := createRetryRun(t, service)
	awaitRetryWait(t, repo, id, 1)
	if err := os.Remove(envPath); err != nil {
		t.Fatal(err)
	}
	run := awaitRetryRun(t, service, id, 8*time.Second)
	call, err := repo.Execution.ReadCall(context.Background(), id)
	if err != nil || run.Status != chat.StatusFailed || run.Error == nil || run.Error.Code != "model_config_unavailable" || call.RetryHistory[0].Result != "failed" || call.RetryHistory[0].Error == nil || call.RetryHistory[0].Error.Code != "model_config_unavailable" || call.NextRetryAt != nil || requests.Load() != 1 {
		t.Fatalf("请求前失败没有完整结算: run=%+v call=%+v err=%v requests=%d", run, call, err, requests.Load())
	}
}
