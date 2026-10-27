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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/agent/completion"
	"github.com/dengweitian0-svg/TimeCue/internal/store"
)

// TestBudgetRejectedInputDoesNotPoisonFollowUp 验证预算拒绝记录保留展示但不阻塞后续短消息。
func TestBudgetRejectedInputDoesNotPoisonFollowUp(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"reply\",\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer upstream.Close()
	service := newFixture(t, upstream.URL)
	ctx := context.Background()
	input := chat.CreateRunInput{
		SessionID:      "s",
		Input:          strings.Repeat("x", 140000),
		Provider:       "test",
		Model:          "model",
		IdempotencyKey: "oversized",
	}
	receipt, err := service.CreateRun(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	first := awaitRun(t, service, receipt.RunID)
	if first.Error == nil || first.Error.Code != "context_budget_exceeded" || len(first.Input) != 140000 {
		t.Fatalf("原始拒绝记录不完整: status=%s error=%v input_length=%d", first.Status, first.Error, len(first.Input))
	}
	input.Input, input.IdempotencyKey = "hello", "short"
	receipt, err = service.CreateRun(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	second := awaitRun(t, service, receipt.RunID)
	if second.Status != chat.StatusSucceeded || calls.Load() != 1 {
		t.Fatalf("后续短消息仍被拒绝: status=%s error=%v upstream_requests=%d", second.Status, second.Error, calls.Load())
	}
	session, err := service.GetSession(ctx, input.SessionID)
	if err != nil || len(session.RunIDs) != 2 {
		t.Fatalf("原始会话历史丢失: %+v %v", session, err)
	}
}

// TestRecoveredOutputHandlesEmptyReplacementAttempts 验证无输出失败或退出保留原回复，完整空响应替换旧结果。
func TestRecoveredOutputHandlesEmptyReplacementAttempts(t *testing.T) {
	for _, mode := range []string{"upstream_error", "second_shutdown", "empty_success"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			secondStarted := make(chan struct{})
			inputs := make(chan chat.ModelRequest, 3)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var input chat.ModelRequest
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Error(err)
					return
				}
				inputs <- input
				n := calls.Add(1)
				if n == 1 {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "data: {\"id\":\"original\",\"choices\":[{\"delta\":{\"reasoning_content\":\"saved thinking\",\"content\":\"saved partial\"}}]}\n\n")
					w.(http.Flusher).Flush()
					<-r.Context().Done()
					return
				}
				if n == 2 {
					close(secondStarted)
					if mode == "empty_success" {
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprint(w, "data: {\"id\":\"empty-replacement\",\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"total_tokens\":7}}\n\ndata: [DONE]\n\n")
						return
					}
					if mode == "second_shutdown" {
						<-r.Context().Done()
						return
					}
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(401)
					fmt.Fprint(w, `{"error":{"message":"fixture authentication failure","type":"authentication_error"}}`)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"id\":\"next\",\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
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
			ports := store.NewChatStorage(db)
			models := []chat.ModelConfig{{
				Provider:        "test",
				Model:           "model",
				BaseURL:         upstream.URL,
				Key:             "fixture-only",
				ContextWindow:   32768,
				MaxOutputTokens: 8192,
			}}
			settings := func() ([]chat.ModelConfig, error) { return models, nil }
			service := chat.NewService(ports, completion.NewClient(nil), settings, nil)
			if err := service.Start(ctx); err != nil {
				t.Fatal(err)
			}
			defer service.Stop()
			receipt, err := service.CreateRun(ctx, chat.CreateRunInput{
				SessionID:      "s",
				Input:          "original",
				Provider:       "test",
				Model:          "model",
				IdempotencyKey: "one",
			})
			if err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(8 * time.Second)
			for {
				page, e := service.GetRunEvents(ctx, receipt.RunID, 0, 10)
				if e != nil {
					t.Fatal(e)
				}
				if len(page.Events) >= 3 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("original partial missing")
				}
				time.Sleep(time.Millisecond)
			}
			service.Stop()
			resumed := chat.NewService(ports, completion.NewClient(nil), settings, nil)
			if err = resumed.Start(ctx); err != nil {
				t.Fatal(err)
			}
			defer resumed.Stop()
			select {
			case <-secondStarted:
			case <-time.After(3 * time.Second):
				t.Fatal("replacement request missing")
			}
			if mode == "second_shutdown" {
				resumed.Stop()
				models = nil
				again := chat.NewService(ports, completion.NewClient(nil), settings, nil)
				if err = again.Start(ctx); err != nil {
					t.Fatal(err)
				}
				defer again.Stop()
				run := awaitRun(t, again, receipt.RunID)
				if run.Output == nil || *run.Output != "saved partial" {
					t.Fatalf("after second shutdown: overview_output=%v status=%s", run.Output, run.Status)
				}
				return
			}
			completed := awaitRun(t, resumed, receipt.RunID)
			if mode == "empty_success" {
				if completed.Status != chat.StatusSucceeded || completed.Output != nil {
					t.Fatalf("完整空响应被旧回复冒充: status=%s has_output=%t", completed.Status, completed.Output != nil)
				}
				call, err := ports.Execution.ReadCall(ctx, receipt.RunID)
				if err != nil || call == nil || call.Output == nil || call.Output.ID != "empty-replacement" || string(call.Output.Usage) != `{"total_tokens":7}` {
					t.Fatalf("完整空响应的身份或实际用量丢失: call=%+v error=%v", call, err)
				}
			}
			next, err := resumed.CreateRun(ctx, chat.CreateRunInput{
				SessionID:      "s",
				Input:          "follow up",
				Provider:       "test",
				Model:          "model",
				IdempotencyKey: "two",
			})
			if err != nil {
				t.Fatal(err)
			}
			awaitRun(t, resumed, next.RunID)
			<-inputs
			<-inputs
			last := <-inputs
			preserved := false
			for _, m := range last.Messages {
				if m.Role == "assistant" && m.Content == "saved partial" && m.ReasoningContent == "saved thinking" {
					preserved = true
				}
			}
			if preserved != (mode != "empty_success") {
				t.Fatalf("overview_preserved=%t next_request_has_saved_assistant=%t messages=%d", completed.Output != nil && *completed.Output == "saved partial", preserved, len(last.Messages))
			}
		})
	}
}

// TestInvalidSSECursorPreservesExistingSubscription 验证无效游标不能替换有效连接或漏过结束事件。
func TestInvalidSSECursorPreservesExistingSubscription(t *testing.T) {
	released := make(chan struct{})
	release := sync.OnceFunc(func() { close(released) })
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-released:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"reply\",\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer upstream.Close()
	defer release()
	service := newFixture(t, upstream.URL)
	ctx := context.Background()
	receipt, err := service.CreateRun(ctx, chat.CreateRunInput{
		SessionID:      "s",
		Input:          "x",
		Provider:       "test",
		Model:          "model",
		IdempotencyKey: "one",
	})
	if err != nil {
		t.Fatal(err)
	}
	old, err := service.Subscribe(ctx, receipt.RunID, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	select {
	case first := <-old.Events:
		if first.Type != chat.EventStart {
			t.Fatal("首事件不是 start")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("未收到 start")
	}
	for _, cursor := range []int64{999999, -1} {
		bad, err := service.Subscribe(ctx, receipt.RunID, cursor)
		if bad != nil {
			bad.Close()
		}
		if !errors.Is(err, chat.ErrInvalidRequest) {
			t.Fatalf("无效游标 %d 被接受: %v", cursor, err)
		}
	}
	release()
	var last int64
	for _, kind := range []chat.EventType{chat.EventText, chat.EventEnd} {
		select {
		case event, ok := <-old.Events:
			if !ok || event.Type != kind {
				t.Fatalf("有效连接被替换或事件缺失: got=%s want=%s closed=%t", event.Type, kind, !ok)
			}
			last = event.ID
		case <-time.After(3 * time.Second):
			t.Fatalf("未收到 %s", kind)
		}
	}
	if _, err = service.Subscribe(ctx, receipt.RunID, last); !errors.Is(err, chat.ErrRunTerminal) {
		t.Fatalf("终态有效游标未返回终态信号: %v", err)
	}
	if _, err = service.Subscribe(ctx, receipt.RunID, last+1); !errors.Is(err, chat.ErrInvalidRequest) {
		t.Fatalf("终态超前游标未被拒绝: %v", err)
	}
}
