package chat

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestMockSubscriptionReplaysAndStreamsInOrder 验证历史回放与实时事件按 ID 顺序衔接。
func TestMockSubscriptionReplaysAndStreamsInOrder(t *testing.T) {
	ctx := context.Background()
	mock := NewMockWithOptions(MockOptions{})
	sessionID := uuid.NewString()
	runID, err := mock.CreateRun(ctx, CreateRunInput{
		SessionID: sessionID, Input: "  原样输入\n", Model: mockModel,
		IdempotencyKey: "create_session:" + uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := mock.PublishEvent(runID, EventStart, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	subscription, err := mock.Subscribe(ctx, runID, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()
	if _, err := mock.PublishEvent(runID, EventText, json.RawMessage(`{"content":"新\n增文本","error":null}`)); err != nil {
		t.Fatal(err)
	}
	if err := mock.ConfirmRun(runID, StatusSucceeded, stringPointer("完整回复"), nil, json.RawMessage(`{"stop_reason":"stop","error":null}`)); err != nil {
		t.Fatal(err)
	}

	var got []Event
	for event := range subscription.Events {
		got = append(got, event)
	}
	if len(got) != 3 {
		t.Fatalf("got %d events, want 3", len(got))
	}
	for index, event := range got {
		if event.ID != int64(index+1) {
			t.Fatalf("event %d has ID %d", index, event.ID)
		}
	}
	if got[0].ID != first.ID || got[0].Type != EventStart || got[1].Type != EventText || got[2].Type != EventEnd {
		t.Fatalf("unexpected event order: %#v", got)
	}
	run, err := mock.GetRun(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	offset := 0
	if run.FinishedAt != nil {
		_, offset = run.FinishedAt.Zone()
	}
	if run.Status != StatusSucceeded || run.Output == nil || *run.Output != "完整回复" || run.FinishedAt == nil || offset != 8*60*60 {
		t.Fatalf("unexpected terminal run: %#v", run)
	}
	if run.Input != "  原样输入\n" {
		t.Fatalf("input was changed: %q", run.Input)
	}
	if _, err := mock.Subscribe(ctx, runID, 0); !errors.Is(err, ErrRunTerminal) {
		t.Fatalf("Subscribe terminal error = %v", err)
	}
	if _, err := mock.Subscribe(ctx, runID, int64(len(got))); !errors.Is(err, ErrRunTerminal) {
		t.Fatalf("Subscribe terminal with valid cursor error = %v", err)
	}
	if _, err := mock.Subscribe(ctx, runID, int64(len(got)+1)); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Subscribe terminal with cursor past maximum error = %v", err)
	}
}

// TestMockDeleteHidesSessionAndRunImmediately 验证删除后 session 与 run 立即不可见。
func TestMockDeleteHidesSessionAndRunImmediately(t *testing.T) {
	ctx := context.Background()
	mock := NewMockWithOptions(MockOptions{})
	sessionID := uuid.NewString()
	runID, err := mock.CreateRun(ctx, CreateRunInput{
		SessionID: sessionID, Input: "输入", Model: mockModel,
		IdempotencyKey: "create_session:" + uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := mock.DeleteSession(ctx, sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := mock.GetRun(ctx, runID); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("GetRun after deletion error = %v", err)
	}
	if sessions, err := mock.ListSessions(ctx); err != nil || len(sessions) != 0 {
		t.Fatalf("ListSessions after deletion = %v, %v", sessions, err)
	}
	if err := mock.DeleteSession(ctx, sessionID); err != nil {
		t.Fatalf("repeated DeleteSession error = %v", err)
	}
	if _, err := mock.CreateRun(ctx, CreateRunInput{
		SessionID: sessionID, Input: "输入", Model: mockModel,
		IdempotencyKey: "create_session:" + uuid.NewString(),
	}); !errors.Is(err, ErrSessionDeleted) {
		t.Fatalf("CreateRun for deleted session error = %v", err)
	}
}

// TestMockGetSessionReturnsNamedSortedCopy 验证 session 名称原样返回，run ID 排序且不泄漏内部切片。
func TestMockGetSessionReturnsNamedSortedCopy(t *testing.T) {
	ctx := context.Background()
	mock := NewMockWithOptions(MockOptions{})
	sessionID := uuid.NewString()
	created := []time.Time{
		time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	var runIDs []string
	for _, createdAt := range created {
		mock.now = func() time.Time { return createdAt }
		runID, err := mock.CreateRun(ctx, CreateRunInput{
			SessionID: sessionID, Input: "输入", Model: mockModel,
			IdempotencyKey: "create_session:" + uuid.NewString(),
		})
		if err != nil {
			t.Fatal(err)
		}
		runIDs = append(runIDs, runID)
		if err := mock.ConfirmRun(runID, StatusSucceeded, nil, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	mock.mu.Lock()
	mock.sessions[sessionID].name = "中文会话名"
	mock.mu.Unlock()

	session, err := mock.GetSession(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if session.SessionID != sessionID || session.SessionName != "中文会话名" {
		t.Fatalf("session identity/name = %#v", session)
	}
	if len(session.RunIDs) != 2 {
		t.Fatalf("run IDs = %v, want 2 IDs", session.RunIDs)
	}
	if session.RunIDs[0] != runIDs[1] {
		t.Fatalf("run IDs are not sorted by creation time: %v", session.RunIDs)
	}
	session.RunIDs[0] = "changed"
	again, err := mock.GetSession(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if again.RunIDs[0] == "changed" {
		t.Fatal("mutating returned run IDs changed internal session state")
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := mock.GetSession(cancelled, sessionID); !errors.Is(err, context.Canceled) {
		t.Fatalf("GetSession canceled context error = %v", err)
	}
	if _, err := mock.GetSession(ctx, uuid.NewString()); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("GetSession missing session error = %v", err)
	}
	if err := mock.DeleteSession(ctx, sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := mock.GetSession(ctx, sessionID); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("GetSession deleted session error = %v", err)
	}
}

// TestMockCancelRemainsRunningUntilConfirmed 验证取消请求在终态确认前不改变 run 状态。
func TestMockCancelRemainsRunningUntilConfirmed(t *testing.T) {
	ctx := context.Background()
	mock := NewMockWithOptions(MockOptions{})
	runID, err := mock.CreateRun(ctx, CreateRunInput{
		SessionID: uuid.NewString(), Input: "输入", Model: mockModel,
		IdempotencyKey: "create_session:" + uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}
	status, err := mock.CancelRun(ctx, runID)
	if err != nil || status != StatusRunning {
		t.Fatalf("CancelRun = %q, %v", status, err)
	}
	run, err := mock.GetRun(ctx, runID)
	if err != nil || run.Status != StatusRunning || run.FinishedAt != nil {
		t.Fatalf("run changed before confirmation: %#v, %v", run, err)
	}
	if err := mock.ConfirmRun(runID, StatusCancelled, stringPointer("检查点"), nil, json.RawMessage(`{"stop_reason":"cancelled","error":null}`)); err != nil {
		t.Fatal(err)
	}
	run, err = mock.GetRun(ctx, runID)
	if err != nil || run.Status != StatusCancelled || run.Output == nil || *run.Output != "检查点" || !run.FinishedAt.After(time.Time{}) {
		t.Fatalf("run was not confirmed cancelled: %#v, %v", run, err)
	}
}
