package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/agent/completion"
	"github.com/dengweitian0-svg/TimeCue/internal/store"
)

func TestRealConversationSurvivesRestart(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"reply-1\",\"choices\":[{\"delta\":{\"reasoning_content\":\"想\"}}]}\n\ndata: {\"id\":\"reply-1\",\"choices\":[{\"delta\":{\"content\":\"真实回复\"},\"finish_reason\":\"stop\"}],\"usage\":{\"total_tokens\":9}}\n\ndata: [DONE]\n\n")
	}))
	defer upstream.Close()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "chat.db")
	db := store.NewSqlite()
	if err := db.Connect(ctx, path); err != nil {
		t.Fatal(err)
	}
	if err := db.PrepareSchema(ctx); err != nil {
		t.Fatal(err)
	}
	repo := store.NewChatStorage(db)
	settings := func() ([]chat.ModelConfig, error) {
		return []chat.ModelConfig{{Provider: "test", Model: "model", BaseURL: upstream.URL, Key: "secret", ContextWindow: 32768, MaxOutputTokens: 8192}}, nil
	}
	service := chat.NewService(repo, completion.NewClient(nil), settings)
	if err := service.Start(ctx); err != nil {
		t.Fatal(err)
	}
	sessions, err := service.ListSessions(ctx)
	if err != nil || len(sessions) != 0 {
		t.Fatalf("empty database: %v %v", sessions, err)
	}
	receipt, err := service.CreateRun(ctx, chat.CreateRunInput{SessionID: "550e8400-e29b-41d4-a716-446655440000", Input: "你好", Provider: "test", Model: "model", IdempotencyKey: "first"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		run, err := service.GetRun(ctx, receipt.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if run.Status != chat.StatusRunning {
			if run.Status != chat.StatusSucceeded || run.Output == nil || *run.Output != "真实回复" {
				t.Fatalf("run: %+v", run)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("run did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	service.Stop()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.Connect(ctx, path); err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service = chat.NewService(repo, completion.NewClient(nil), settings)
	if err := service.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer service.Stop()
	run, err := service.GetRun(ctx, receipt.RunID)
	if err != nil || run.Output == nil || *run.Output != "真实回复" {
		t.Fatalf("persisted run: %+v %v", run, err)
	}
	page, err := service.GetRunEvents(ctx, receipt.RunID, 0, 10)
	if err != nil || len(page.Events) != 4 || page.Events[0].Type != chat.EventStart || page.Events[3].Type != chat.EventEnd {
		t.Fatalf("events: %+v %v", page, err)
	}
}

func TestOversizedInputFailsWithoutCallingModel(t *testing.T) {
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"不应请求\"},\"finish_reason\":\"stop\"}]}\n\n")
	}))
	defer upstream.Close()
	service := newFixture(t, upstream.URL)
	receipt, err := service.CreateRun(context.Background(), chat.CreateRunInput{SessionID: "session", Input: strings.Repeat("x", 140000), Provider: "test", Model: "model", IdempotencyKey: "key"})
	if err != nil {
		t.Fatal(err)
	}
	run := awaitRun(t, service, receipt.RunID)
	if run.Status != chat.StatusFailed || run.Error == nil || run.Error.Code != "context_budget_exceeded" || requests.Load() != 0 {
		t.Fatalf("status=%s error=%v requests=%d", run.Status, run.Error, requests.Load())
	}
	page, err := service.GetRunEvents(context.Background(), receipt.RunID, 0, 10)
	if err != nil || len(page.Events) != 3 {
		t.Fatalf("events=%+v err=%v", page, err)
	}
}

func newFixture(t *testing.T, base string) *chat.Runtime {
	t.Helper()
	db := store.NewSqlite()
	ctx := context.Background()
	if err := db.Connect(ctx, filepath.Join(t.TempDir(), "chat.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PrepareSchema(ctx); err != nil {
		t.Fatal(err)
	}
	service := chat.NewService(store.NewChatStorage(db), completion.NewClient(nil), func() ([]chat.ModelConfig, error) {
		return []chat.ModelConfig{{Provider: "test", Model: "model", BaseURL: base, Key: "secret", ContextWindow: 32768, MaxOutputTokens: 8192}}, nil
	})
	if err := service.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Stop)
	return service
}

func awaitRun(t *testing.T, service chat.RunService, id string) chat.Run {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		run, err := service.GetRun(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if run.Status != chat.StatusRunning {
			return run
		}
		if time.Now().After(deadline) {
			t.Fatal("run did not finish")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestCancelledModelRetainsPartialReply(t *testing.T) {
	received := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"partial\",\"choices\":[{\"delta\":{\"content\":\"未完成的回复\"}}]}\n\n")
		w.(http.Flusher).Flush()
		close(received)
		<-r.Context().Done()
	}))
	defer upstream.Close()
	service := newFixture(t, upstream.URL)
	ctx := context.Background()
	receipt, err := service.CreateRun(ctx, chat.CreateRunInput{SessionID: "s", Provider: "test", Model: "model", Input: "x", IdempotencyKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-received:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream not called")
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		page, err := service.GetRunEvents(ctx, receipt.RunID, 0, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Events) >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("partial output not saved")
		}
		time.Sleep(time.Millisecond)
	}
	status, err := service.CancelRun(ctx, receipt.RunID)
	if err != nil || status != chat.StatusCancelled {
		t.Fatal(status, err)
	}
	run, err := service.GetRun(ctx, receipt.RunID)
	if err != nil || run.Output == nil || *run.Output != "未完成的回复" || run.Error != nil || run.FinishedAt == nil {
		t.Fatalf("partial cancellation: %+v %v", run, err)
	}
	if _, err = service.CancelRun(ctx, receipt.RunID); err != nil {
		t.Fatal(err)
	}
	page, err := service.GetRunEvents(ctx, receipt.RunID, 0, 10)
	if err != nil || len(page.Events) != 3 {
		t.Fatalf("duplicate terminal event: %+v %v", page, err)
	}
}

func TestIdempotencyPersistsOriginalModelAndInput(t *testing.T) {
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n")
	}))
	defer upstream.Close()
	service := newFixture(t, upstream.URL)
	ctx := context.Background()
	input := chat.CreateRunInput{SessionID: "s", Input: "raw input ", Provider: "test", Model: "model", IdempotencyKey: "key"}
	receipt, err := service.CreateRun(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	awaitRun(t, service, receipt.RunID)
	input.Provider = "unconfigured"
	input.Model = "other"
	replay, err := service.CreateRun(ctx, input)
	if err != nil || replay.RunID != receipt.RunID || replay.Status != chat.StatusSucceeded || requests.Load() != 1 {
		t.Fatalf("replay: %+v %v calls=%d", replay, err, requests.Load())
	}
	input.Input = "different"
	if _, err = service.CreateRun(ctx, input); !errors.Is(err, chat.ErrIdempotencyConflict) {
		t.Fatal(err)
	}
	if err = service.DeleteSession(ctx, "s"); err != nil {
		t.Fatal(err)
	}
	if _, err = service.CreateRun(ctx, input); !errors.Is(err, chat.ErrSessionDeleted) {
		t.Fatal(err)
	}
}

func TestNormalStopResumesSameRunAfterRestart(t *testing.T) {
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if requests.Add(1) == 1 {
			fmt.Fprint(w, "data: {\"id\":\"old-partial\",\"choices\":[{\"delta\":{\"content\":\"原部分回复\"}}]}\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		fmt.Fprint(w, "data: {\"id\":\"resumed\",\"choices\":[{\"delta\":{\"content\":\"恢复完成\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer upstream.Close()
	ctx := context.Background()
	db := store.NewSqlite()
	if err := db.Connect(ctx, filepath.Join(t.TempDir(), "chat.db")); err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.PrepareSchema(ctx); err != nil {
		t.Fatal(err)
	}
	repo := store.NewChatStorage(db)
	settings := func() ([]chat.ModelConfig, error) {
		return []chat.ModelConfig{{Provider: "test", Model: "model", BaseURL: upstream.URL, Key: "secret", ContextWindow: 32768, MaxOutputTokens: 8192}}, nil
	}
	service := chat.NewService(repo, completion.NewClient(nil), settings)
	if err := service.Start(ctx); err != nil {
		t.Fatal(err)
	}
	receipt, err := service.CreateRun(ctx, chat.CreateRunInput{SessionID: "s", Input: "input", Provider: "test", Model: "model", IdempotencyKey: "key"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		page, err := service.GetRunEvents(ctx, receipt.RunID, 0, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Events) >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("partial missing")
		}
		time.Sleep(time.Millisecond)
	}
	service.Stop()
	run, err := service.GetRun(ctx, receipt.RunID)
	if err != nil || run.Status != chat.StatusRunning {
		t.Fatalf("normal stop changed state: %s %v", run.Status, err)
	}
	service = chat.NewService(repo, completion.NewClient(nil), settings)
	if err = service.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer service.Stop()
	run = awaitRun(t, service, receipt.RunID)
	if run.Status != chat.StatusSucceeded || run.Output == nil || *run.Output != "恢复完成" || requests.Load() != 2 {
		t.Fatalf("restart status=%s output=%v requests=%d", run.Status, run.Output, requests.Load())
	}
	page, err := service.GetRunEvents(ctx, receipt.RunID, 0, 10)
	if err != nil || len(page.Events) != 4 {
		t.Fatalf("retained events=%+v %v", page, err)
	}
}

type failFirstFinish struct {
	chat.ExecutionRepository
	failed   chan struct{}
	attempts atomic.Int32
}

// Finish 在存储故障边界注入一次提交失败，不替代真实存储。
func (s *failFirstFinish) Finish(ctx context.Context, call chat.ModelCall, run chat.Record) error {
	if s.attempts.Add(1) == 1 {
		close(s.failed)
		return errors.New("injected commit failure")
	}
	return s.ExecutionRepository.Finish(ctx, call, run)
}

func TestFailedFinalCommitWaitsAndDoesNotRepeatModel(t *testing.T) {
	started := time.Now()
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"真实完成\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer upstream.Close()
	db := store.NewSqlite()
	ctx := context.Background()
	if err := db.Connect(ctx, filepath.Join(t.TempDir(), "chat.db")); err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.PrepareSchema(ctx); err != nil {
		t.Fatal(err)
	}
	ports := store.NewChatStorage(db)
	fault := &failFirstFinish{ExecutionRepository: ports.Execution, failed: make(chan struct{})}
	ports.Execution = fault
	service := chat.NewService(ports, completion.NewClient(nil), func() ([]chat.ModelConfig, error) {
		return []chat.ModelConfig{{Provider: "test", Model: "model", Key: "secret", BaseURL: upstream.URL, ContextWindow: 32768, MaxOutputTokens: 8192}}, nil
	})
	if err := service.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer service.Stop()
	receipt, err := service.CreateRun(ctx, chat.CreateRunInput{SessionID: "s", Input: "input", Provider: "test", Model: "model", IdempotencyKey: "key"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-fault.failed:
	case <-time.After(3 * time.Second):
		t.Fatal("fault not reached")
	}
	run, err := service.GetRun(ctx, receipt.RunID)
	if err != nil || run.Status != chat.StatusRunning {
		t.Fatalf("未等待收尾重试: %s %v", run.Status, err)
	}
	deadline := time.Now().Add(8 * time.Second)
	for {
		run, err = service.GetRun(ctx, receipt.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if run.Status != chat.StatusRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("finish did not retry")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if run.Status != chat.StatusSucceeded || run.Output == nil || *run.Output != "真实完成" || requests.Load() != 1 {
		t.Fatalf("status=%s requests=%d", run.Status, requests.Load())
	}
	if time.Since(started) < 4*time.Second {
		t.Fatal("终态提交失败后没有按规格等待五秒再重试")
	}
}

func TestMultiRoundRequestKeepsDeepSeekReasoning(t *testing.T) {
	requests := make(chan chat.ModelRequest, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input chat.ModelRequest
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			return
		}
		requests <- input
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"reply\",\"choices\":[{\"delta\":{\"reasoning_content\":\"真实思考\",\"content\":\"回复\"},\"finish_reason\":\"stop\"}],\"usage\":{\"total_tokens\":9,\"prompt_cache_hit_tokens\":2}}\n\ndata: [DONE]\n\n")
	}))
	defer upstream.Close()
	service := newFixture(t, upstream.URL)
	ctx := context.Background()
	for _, input := range []chat.CreateRunInput{{SessionID: "s", Input: " 第一轮 ", Provider: "test", Model: "model", IdempotencyKey: "first"}, {SessionID: "s", Input: "第二轮", Provider: "test", Model: "model", IdempotencyKey: "second"}} {
		receipt, err := service.CreateRun(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		awaitRun(t, service, receipt.RunID)
	}
	<-requests
	second := <-requests
	if !second.Stream || len(second.Messages) != 3 || second.Messages[0].Content != " 第一轮 " || second.Messages[1].Role != "assistant" || second.Messages[1].ReasoningContent != "真实思考" || second.Messages[2].Content != "第二轮" {
		t.Fatalf("actual upstream messages=%+v", second.Messages)
	}
}
