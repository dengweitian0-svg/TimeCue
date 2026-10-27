package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/agent/completion"
	"github.com/dengweitian0-svg/TimeCue/internal/api"
	"github.com/dengweitian0-svg/TimeCue/internal/app"
	"github.com/dengweitian0-svg/TimeCue/internal/store"
	"github.com/gin-gonic/gin"
)

const testSessionID = "550e8400-e29b-41d4-a716-446655440000"
const testKeyUUID = "c9bf9e57-1685-4c89-bafb-ff5af830be8a"

func chatHTTPFixture(t *testing.T, key string, handler http.HandlerFunc, configure ...func(*chat.Storage)) (http.Handler, *chat.Runtime) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	upstream := httptest.NewServer(handler)
	t.Cleanup(upstream.Close)
	db := store.NewSqlite()
	if err := db.Connect(context.Background(), filepath.Join(t.TempDir(), "chat.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PrepareSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	ports := store.NewChatStorage(db)
	for _, apply := range configure {
		apply(&ports)
	}
	service := chat.NewService(ports, completion.NewClient(nil), func() ([]chat.ModelConfig, error) {
		return []chat.ModelConfig{{
			Provider:        "test",
			Model:           "configured-model",
			Key:             key,
			BaseURL:         upstream.URL,
			ContextWindow:   32768,
			MaxOutputTokens: 8192,
		}}, nil
	})
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Stop)
	return api.NewRouter(&app.App{ChatServices: chat.Services{Model: service, Run: service, Session: service}}, nil), service
}
func chatRequest(router http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = "127.0.0.1:10086"
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Idempotency-Key", "create_session:"+testKeyUUID)
	result := httptest.NewRecorder()
	router.ServeHTTP(result, req)
	return result
}
func completedUpstream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprint(w, "data: {\"id\":\"reply\",\"choices\":[{\"delta\":{\"reasoning_content\":\"想\",\"content\":\"回答\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
}

// TestBudgetFailureHTTPKeepsCreationAndReplayStatus 验证预算失败首次创建仍为 202，终态重放为 200，公开回执固定四字段。
func TestBudgetFailureHTTPKeepsCreationAndReplayStatus(t *testing.T) {
	router, service := chatHTTPFixture(t, "secret", func(http.ResponseWriter, *http.Request) {
		t.Error("预算拒绝不能请求上游")
	})
	input := " " + strings.Repeat("中🙂", 20000) + "\n"
	encoded, err := json.Marshal(map[string]string{
		"session_id": testSessionID,
		"input":      input,
		"provider":   "test",
		"model":      "configured-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	first := chatRequest(router, "POST", "/v1/run", string(encoded))
	if first.Code != http.StatusAccepted {
		t.Fatalf("首次预算失败必须保留 202: %d %s", first.Code, first.Body)
	}
	var receipt map[string]json.RawMessage
	if err = json.Unmarshal(first.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if len(receipt) != 4 || string(receipt["status"]) != `"failed"` || string(receipt["output"]) != "null" || !bytes.Contains(receipt["error"], []byte(`"context_budget_exceeded"`)) {
		t.Fatalf("预算失败四字段回执错误: %s", first.Body)
	}
	var id string
	if err = json.Unmarshal(receipt["run_id"], &id); err != nil {
		t.Fatal(err)
	}
	run, err := service.GetRun(context.Background(), id)
	if err != nil || run.Input != input || run.FinishedAt == nil {
		t.Fatalf("预算失败查询未保留原始输入或终态: %+v %v", run, err)
	}
	replay := chatRequest(router, "POST", "/v1/run", string(encoded))
	if replay.Code != http.StatusOK || !bytes.Equal(first.Body.Bytes(), replay.Body.Bytes()) {
		t.Fatalf("终态重放必须 200 且回执不变: %d %s", replay.Code, replay.Body)
	}
}
func TestRealChatHTTPContract(t *testing.T) {
	router, service := chatHTTPFixture(t, "secret", completedUpstream)
	body := `{"session_id":"` + testSessionID + `","input":"你好","provider":"test","model":"configured-model"}`
	response := chatRequest(router, "POST", "/v1/run", body)
	if response.Code != 202 {
		t.Fatalf("create: %d %s", response.Code, response.Body)
	}
	var receipt map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if len(receipt) != 4 {
		t.Fatalf("receipt: %v", receipt)
	}
	id := receipt["run_id"].(string)
	deadline := time.Now().Add(3 * time.Second)
	for {
		run, err := service.GetRun(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if run.Status != chat.StatusRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("not finished")
		}
		time.Sleep(time.Millisecond)
	}
	response = chatRequest(router, "GET", "/v1/run/"+id, "")
	var run map[string]any
	json.Unmarshal(response.Body.Bytes(), &run)
	if response.Code != 200 || len(run) != 10 || run["provider"] != "test" || run["output"] != "回答" {
		t.Fatalf("run: %d %v", response.Code, run)
	}
	response = chatRequest(router, "POST", "/v1/run", body)
	if response.Code != 200 {
		t.Fatalf("terminal replay: %d", response.Code)
	}
	response = chatRequest(router, "GET", "/v1/model", "")
	if response.Code != 200 || !bytes.Contains(response.Body.Bytes(), []byte(`"available":true`)) {
		t.Fatalf("models: %d %s", response.Code, response.Body)
	}
	response = chatRequest(router, "GET", "/v1/run/"+id+"/events?limit=2", "")
	if response.Code != 200 || !bytes.Contains(response.Body.Bytes(), []byte(`"has_more":true`)) {
		t.Fatalf("page: %d %s", response.Code, response.Body)
	}
	response = chatRequest(router, "GET", "/v1/run/"+id+"/event", "")
	if response.Code != 204 {
		t.Fatal(response.Code)
	}
	response = chatRequest(router, "DELETE", "/v1/session/"+testSessionID, "")
	if response.Code != 200 {
		t.Fatal(response.Code)
	}
	response = chatRequest(router, "GET", "/v1/run/"+id, "")
	if response.Code != 404 {
		t.Fatal(response.Code)
	}
	response = chatRequest(router, "POST", "/v1/run", body)
	if response.Code != 400 || !strings.Contains(response.Body.String(), "session_deleted") {
		t.Fatal(response.Body)
	}
}
func TestChatRejectsInvalidHTTPAndMissingKey(t *testing.T) {
	router, _ := chatHTTPFixture(t, "", completedUpstream)
	for _, body := range []string{`{}`, `{"session_id":null}`, `{"session_id":"` + testSessionID + `","input":"x","provider":"test","model":"configured-model","context":[]}`} {
		response := chatRequest(router, "POST", "/v1/run", body)
		if response.Code != 400 {
			t.Fatalf("invalid: %d %s", response.Code, response.Body)
		}
	}
	response := chatRequest(router, "POST", "/v1/run", `{"session_id":"`+testSessionID+`","input":"x","provider":"test","model":"configured-model"}`)
	if response.Code != 503 || !strings.Contains(response.Body.String(), "model_config_unavailable") {
		t.Fatalf("missing key: %d %s", response.Code, response.Body)
	}
	response = chatRequest(router, "GET", "/v1/session", "")
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"sessions":[]`) {
		t.Fatal(response.Body)
	}
}

func TestRealChatSSEPreservesPartialOutputAndError(t *testing.T) {
	released := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(released) }) }
	defer release()
	router, service := chatHTTPFixture(t, "secret", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"partial\",\"choices\":[{\"delta\":{\"content\":\"部分正文\"}}]}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-released:
		case <-r.Context().Done():
		}
	})
	receipt, err := service.CreateRun(context.Background(), chat.CreateRunInput{SessionID: testSessionID, Input: "x", Provider: "test", Model: "configured-model", IdempotencyKey: "key"})
	if err != nil {
		t.Fatal(err)
	}
	invalid := httptest.NewRequest(http.MethodGet, "/v1/run/"+receipt.RunID+"/event", nil)
	invalid.Host = "127.0.0.1:10086"
	invalid.Header.Set("Last-Event-ID", "999999")
	rejected := httptest.NewRecorder()
	router.ServeHTTP(rejected, invalid)
	if rejected.Code != http.StatusBadRequest || !strings.Contains(rejected.Body.String(), "invalid_request") {
		t.Fatalf("超前 SSE 游标未返回 400 invalid_request: %d %s", rejected.Code, rejected.Body)
	}
	server := httptest.NewServer(router)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/v1/run/"+receipt.RunID+"/event", nil)
	request.Host = "127.0.0.1:10086"
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	release()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{"start", "text", "error", "end"} {
		if !strings.Contains(string(body), "event: "+event+"\n") {
			t.Fatalf("missing %s in SSE: %s", event, body)
		}
	}
	run, err := service.GetRun(context.Background(), receipt.RunID)
	if err != nil || run.Status != chat.StatusFailed || run.Output == nil || *run.Output != "部分正文" || run.Error == nil || run.Error.Code != "model_network_error" {
		t.Fatalf("partial error: %+v %v", run, err)
	}
}
