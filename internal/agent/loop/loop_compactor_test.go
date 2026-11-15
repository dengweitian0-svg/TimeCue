package loop_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/agent/loop"
	"github.com/dengweitian0-svg/TimeCue/internal/app"
	"github.com/dengweitian0-svg/TimeCue/internal/model"
	"github.com/dengweitian0-svg/TimeCue/internal/store"
)

type compactHistoryChange struct {
	chat.ExecutionRepository
	changed atomic.Bool
}

// History 模拟恢复后有效历史与已登记请求前缀不一致的情况。
func (h *compactHistoryChange) History(ctx context.Context, session string) ([]chat.Message, error) {
	if h.changed.Load() {
		return h.ExecutionRepository.History(ctx, session)
	}
	return []chat.Message{{
		Role:    "user",
		Content: "重复输入",
	}, {
		Role:    "assistant",
		Content: "较早的答复",
	}}, nil
}

// TestLoopCompactsActualRequestPrefix 验证真实输入位置、同模型连接选择、摘要续写及切换后的请求正文。
func TestLoopCompactsActualRequestPrefix(t *testing.T) {
	ctx := context.Background()
	db, storage, _ := admissionDatabase(t)
	history := &compactHistoryChange{ExecutionRepository: storage.Execution}
	storage.Execution = history
	var calls atomic.Int32
	bodies := make(chan string, 8)
	selected := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("连接使用错误的密钥")
		}
		bodies <- string(body)
		switch calls.Add(1) {
		case 1:
			history.changed.Store(true)
			emitTools(w, []chat.ToolRequest{toolRequest("large", "get_schedule", `{"id":1}`)})
		case 2:
			emitLengthDelta(w, "content", "工具已查询；", "length")
		case 3:
			emitText(w, "保留真实结果。")
		default:
			emitText(w, "普通答复")
		}
	}))
	defer selected.Close()
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("请求发给了另一条同模型连接")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer other.Close()
	settings := func() ([]chat.ModelConfig, error) {
		configs, err := compactSettings(selected.URL, nil)()
		unselected := configs[0]
		unselected.Provider, unselected.BaseURL, unselected.Key = "other", other.URL, "other-secret"
		return append([]chat.ModelConfig{unselected}, configs...), err
	}
	schedules, err := store.NewScheduleStore(db)
	if err != nil {
		t.Fatal(err)
	}
	application := &app.App{Schedule: schedules}
	description := strings.Repeat("工具事实🙂", 5000)
	if _, _, err := application.CreateSchedule(ctx, model.Schedule{
		Name:        "大型日程",
		Description: &description,
		Type:        model.SingleSchedule,
		Single:      &model.Single{NotifyTime: time.Now().Add(24 * time.Hour).Truncate(time.Minute)},
	}); err != nil {
		t.Fatal(err)
	}
	service, err := loop.New(ctx, storage, settings, application)
	if err != nil {
		t.Fatal(err)
	}
	defer shutdownContinuationLoop(service)
	if err := service.Activate(ctx); err != nil {
		t.Fatal(err)
	}
	input := admissionInput("compact-prefix", "first")
	input.Input = "重复输入"
	receipt, err := service.CreateRun(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	run := awaitFinishedRun(t, service, receipt.RunID)
	if run.Status != chat.StatusSucceeded || run.Output == nil || *run.Output != "普通答复" || calls.Load() != 4 {
		t.Fatalf("run=%+v error=%+v calls=%d", run, run.Error, calls.Load())
	}
	read := func() (chat.ModelRequest, string) {
		t.Helper()
		body := <-bodies
		var request chat.ModelRequest
		if err := json.Unmarshal([]byte(body), &request); err != nil {
			t.Fatal(err)
		}
		return request, body
	}
	first, _ := read()
	summary, _ := read()
	continuation, _ := read()
	ordinary, sent := read()
	var facts []chat.Message
	if err := json.Unmarshal([]byte(summary.Messages[1].Content), &facts); err != nil {
		t.Fatal(err)
	}
	if len(facts) != 4 || facts[0].Role != "user" || facts[0].Content != input.Input ||
		facts[1].Content != "较早的答复" || facts[2].Role != "assistant" || facts[3].ToolCallID != "large" {
		t.Fatalf("摘要没有保留此前同名输入及原工具配对：%.500s", mustJSON(facts))
	}
	if len(first.Tools) == 0 || len(summary.Tools) != 0 || len(continuation.Tools) != 0 ||
		len(continuation.Messages) != 4 || continuation.Messages[2].Content != "工具已查询；" ||
		len(ordinary.Messages) != 3 || ordinary.Messages[1].Content != "工具已查询；保留真实结果。" ||
		ordinary.Messages[2].Content != input.Input || len(ordinary.Tools) == 0 {
		t.Fatalf("摘要切换后的请求错误：%s", sent)
	}
	saved, err := storage.Execution.ReadCall(ctx, receipt.RunID)
	if err != nil || saved == nil || saved.InputBody != sent {
		t.Fatalf("登记与发送正文不一致：%v", err)
	}
}

// TestLoopCompactsOnRecoveryBeforeFirstCall 验证重启时活动运行无 checkpoint 仍先压缩旧历史。
func TestLoopCompactsOnRecoveryBeforeFirstCall(t *testing.T) {
	ctx := context.Background()
	_, storage, _ := admissionDatabase(t)
	requests := make(chan chat.ModelRequest, 8)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request chat.ModelRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		requests <- request
		switch {
		case len(request.Tools) == 0:
			emitText(w, "已有历史的有效摘要")
		case len(request.Messages) == 2:
			emitText(w, strings.Repeat("长期历史-", 2100))
		default:
			emitText(w, "恢复后的答复")
		}
	}))
	t.Cleanup(upstream.Close)
	start := func() *loop.Run {
		t.Helper()
		service, err := loop.New(ctx, storage, compactSettings(upstream.URL, nil), nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { shutdownContinuationLoop(service) })
		if err := service.Activate(ctx); err != nil {
			t.Fatal(err)
		}
		return service
	}

	service := start()
	initial, err := service.CreateRun(ctx, admissionInput("compact-recovery", "initial"))
	if err != nil {
		t.Fatal(err)
	}
	if run := awaitFinishedRun(t, service, initial.RunID); run.Status != chat.StatusSucceeded {
		t.Fatalf("初始化历史失败：%+v", run)
	}
	shutdownContinuationLoop(service)
	<-requests

	input := admissionInput("compact-recovery", "pending")
	input.Input = "重启后仍保留的用户输入"
	record := chat.Record{
		Run: chat.Run{
			RunID:     "pending-compact",
			SessionID: input.SessionID,
			Provider:  input.Provider,
			Model:     input.Model,
			Input:     input.Input,
			CreatedAt: time.Now(),
		},
		State:          chat.StateRunning,
		IdempotencyKey: input.IdempotencyKey,
	}
	if err := storage.Runs.Create(ctx, record); err != nil {
		t.Fatal(err)
	}
	service = start()
	run := awaitFinishedRun(t, service, record.RunID)
	if run.Status != chat.StatusSucceeded || run.Output == nil || *run.Output != "恢复后的答复" || len(requests) != 2 {
		t.Fatalf("run=%+v error=%+v requests=%d", run, run.Error, len(requests))
	}
	summary, ordinary := <-requests, <-requests
	if len(summary.Tools) != 0 || len(summary.Messages) != 2 ||
		!strings.Contains(summary.Messages[1].Content, "长期历史") ||
		strings.Contains(summary.Messages[1].Content, input.Input) {
		t.Fatal("无 checkpoint 恢复时摘要未保留旧历史边界")
	}
	if len(ordinary.Tools) == 0 || len(ordinary.Messages) != 3 ||
		ordinary.Messages[1].Content != "已有历史的有效摘要" || ordinary.Messages[2].Content != input.Input {
		t.Fatalf("无 checkpoint 恢复时摘要切换错误：%s", mustJSON(ordinary.Messages))
	}
}
