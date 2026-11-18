package api_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/google/uuid"
)

type httpCancelFinishGate struct {
	chat.ExecutionRepository
	entered chan struct{}
	release chan struct{}
}

// Finish 在真实取消终态事务前暂停，验证 HTTP 不把取消投影当成已结束。
func (f *httpCancelFinishGate) Finish(ctx context.Context, call chat.ModelCall, r chat.Record) error {
	close(f.entered)
	<-f.release
	return f.ExecutionRepository.Finish(ctx, call, r)
}

// TestHTTPCancellationReplayRemainsActive 验证取消投影重放为四字段 200，概况保留空 finished_at，仍拒绝会话重入。
func TestHTTPCancellationReplayRemainsActive(t *testing.T) {
	id := uuid.NewString()
	f := &httpCancelFinishGate{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	router, _ := chatHTTPFixture(t, "secret", completedUpstream, func(ports *chat.Storage) {
		if err := ports.Runs.Create(context.Background(), chat.Record{
			Run: chat.Run{
				RunID:     id,
				SessionID: testSessionID,
				Provider:  "test",
				Model:     "configured-model",
				Input:     "input",
				CreatedAt: time.Now(),
			},
			State:          chat.StateWaitForCancel,
			IdempotencyKey: "create_session:" + testKeyUUID,
		}); err != nil {
			t.Fatal(err)
		}
		f.ExecutionRepository = ports.Execution
		ports.Execution = f
	})
	defer close(f.release)
	select {
	case <-f.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("取消未进入收尾")
	}
	body := `{"session_id":"` + testSessionID + `","input":"input","provider":"removed","model":"removed"}`
	response := chatRequest(router, http.MethodPost, "/v1/run", body)
	var receipt map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if response.Code != 200 || len(receipt) != 4 || receipt["run_id"] != id || receipt["status"] != "cancelled" || receipt["output"] != nil || receipt["error"] != nil {
		t.Fatalf("取消回执: %d %s", response.Code, response.Body)
	}
	response = chatRequest(router, http.MethodGet, "/v1/run/"+id, "")
	var run map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &run); err != nil {
		t.Fatal(err)
	}
	if response.Code != 200 || len(run) != 10 || run["status"] != "cancelled" || run["finished_at"] != nil {
		t.Fatalf("取消收尾概况: %d %s", response.Code, response.Body)
	}
	body = strings.ReplaceAll(body, "removed", "test")
	body = strings.Replace(body, `"model":"test"`, `"model":"configured-model"`, 1)
	body = strings.Replace(body, `"input":"input"`, `"input":"next"`, 1)
	req := admissionHTTPRequest(router, body, "create_session:"+uuid.NewString())
	if req.code != 409 || !strings.Contains(req.body, "session_busy") {
		t.Fatalf("取消收尾提前释放名额: %d %s", req.code, req.body)
	}
	readLogs := captureHTTPLogs(t)
	started := time.Now()
	response = chatRequest(router, http.MethodPost, "/v1/run/"+id+"/cancel", "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"cancelled"`) {
		t.Fatalf("取消受理回执: %d %s", response.Code, response.Body)
	}
	requireHTTPRecord(t, readLogs(), http.MethodPost, "/v1/run/"+id+"/cancel", "INFO", "run cancellation requested", http.StatusOK, time.Since(started))
}

type admissionHTTPResponse struct {
	code int
	body string
}

// TestHTTPAdmissionAndRejectedKeys 验证运行中回执、会话/全局门禁、非法键及被拒绝键的安全重用。
func TestHTTPAdmissionAndRejectedKeys(t *testing.T) {
	router, service := chatHTTPFixture(t, "secret", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	key := "create_session:" + uuid.NewString()
	sessions := []string{uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()}
	makeBody := func(session, input, provider, model string) string {
		body, err := json.Marshal(map[string]string{
			"session_id": session,
			"input":      input,
			"provider":   provider,
			"model":      model,
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	var first string
	for i, session := range sessions[:3] {
		response := admissionHTTPRequest(router, makeBody(session, "hold", "test", "configured-model"), key)
		var receipt map[string]any
		if err := json.Unmarshal([]byte(response.body), &receipt); err != nil {
			t.Fatal(err)
		}
		if response.code != 202 || len(receipt) != 4 || receipt["status"] != "running" || receipt["output"] != nil || receipt["error"] != nil {
			t.Fatalf("新建回执: %d %s", response.code, response.body)
		}
		if i == 0 {
			first = receipt["run_id"].(string)
		}
	}
	response := admissionHTTPRequest(router, makeBody(sessions[0], "hold", "removed", "removed"), key)
	if response.code != 202 || !strings.Contains(response.body, first) {
		t.Fatalf("满额下运行中重放未先于模型门禁: %d %s", response.code, response.body)
	}
	busyKey := "create_session:" + uuid.NewString()
	for _, tc := range []struct {
		name      string
		body      string
		key       string
		code      int
		errorCode string
	}{
		{
			name:      "同键不同输入",
			body:      makeBody(sessions[0], "changed", "removed", "removed"),
			key:       key,
			code:      409,
			errorCode: "idempotency_key_conflict",
		},
		{
			name:      "同会话新键",
			body:      makeBody(sessions[0], "next", "test", "configured-model"),
			key:       busyKey,
			code:      409,
			errorCode: "session_busy",
		},
		{
			name:      "第四会话",
			body:      makeBody(sessions[3], "hold", "test", "configured-model"),
			key:       key,
			code:      409,
			errorCode: "session_concurrency_limit",
		},
		{
			name:      "缺少键",
			body:      makeBody(sessions[3], "hold", "test", "configured-model"),
			code:      400,
			errorCode: "invalid_request",
		},
		{
			name:      "非法键",
			body:      makeBody(sessions[3], "hold", "test", "configured-model"),
			key:       "bad-key",
			code:      400,
			errorCode: "invalid_request",
		},
		{
			name:      "非法请求体",
			body:      `{"input":"hold"}`,
			key:       key,
			code:      400,
			errorCode: "invalid_request",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := admissionHTTPRequest(router, tc.body, tc.key)
			if response.code != tc.code || !strings.Contains(response.body, tc.errorCode) {
				t.Fatalf("拒绝响应: %d %s", response.code, response.body)
			}
		})
	}
	if _, err := service.CancelRun(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	// 取消回执只受理请求，真实终态落库后才能复用全局名额。
	deadline := time.Now().Add(3 * time.Second)
	for {
		run, err := service.GetRun(context.Background(), first)
		if err != nil {
			t.Fatal(err)
		}
		if run.FinishedAt != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("取消收尾没有完成")
		}
		time.Sleep(time.Millisecond)
	}
	response = admissionHTTPRequest(router, makeBody(sessions[3], "changed after rejection", "test", "configured-model"), key)
	if response.code != 202 {
		t.Fatalf("被拒绝键不能使用: %d %s", response.code, response.body)
	}
	response = admissionHTTPRequest(router, makeBody(sessions[0], "next", "test", "configured-model"), busyKey)
	if response.code != 409 || !strings.Contains(response.body, "session_concurrency_limit") {
		t.Fatalf("忙碌键被错误记录或重复释放名额: %d %s", response.code, response.body)
	}
	for _, session := range sessions {
		_ = service.DeleteSession(context.Background(), session)
	}
	response = admissionHTTPRequest(router, makeBody(sessions[0], "different", "removed", "removed"), key)
	if response.code != 400 || !strings.Contains(response.body, "session_deleted") {
		t.Fatalf("删除会话未先于重放拒绝: %d %s", response.code, response.body)
	}
}

func admissionHTTPRequest(router http.Handler, body, key string) admissionHTTPResponse {
	req := httptest.NewRequest(http.MethodPost, "/v1/run", strings.NewReader(body))
	req.Host = "127.0.0.1:10086"
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return admissionHTTPResponse{
		code: w.Code,
		body: w.Body.String(),
	}
}
