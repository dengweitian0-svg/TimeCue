package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/service"
	"github.com/gin-gonic/gin"
)

const (
	testSessionID  = "550e8400-e29b-41d4-a716-446655440000"
	otherSessionID = "650e8400-e29b-41d4-a716-446655440000"
	testRunID      = "d9428888-122b-4f6f-9b47-5ea7b2915210"
	testKeyUUID    = "c9bf9e57-1685-4c89-bafb-ff5af830be8a"
)

type chatServiceStub struct {
	chat.Service
	create             func(context.Context, chat.CreateRunInput) (string, error)
	getRun             func(context.Context, string) (chat.Run, error)
	cancel             func(context.Context, string) (chat.Status, error)
	getSession         func(context.Context, string) ([]string, error)
	listSessions       func(context.Context) ([]string, error)
	deleteSession      func(context.Context, string) error
	subscribe          func(context.Context, string, int64) (*chat.Subscription, error)
	createCalls        int
	createInput        chat.CreateRunInput
	createInputs       []chat.CreateRunInput
	getRunCalls        int
	cancelCalls        int
	getSessionCalls    int
	listSessionsCalls  int
	deleteSessionCalls int
	subscribeCalls     int
}

// CreateRun 记录创建请求并调用测试预设的创建结果。
func (s *chatServiceStub) CreateRun(ctx context.Context, input chat.CreateRunInput) (string, error) {
	s.createCalls++
	s.createInput = input
	s.createInputs = append(s.createInputs, input)
	if s.create == nil {
		return "", errors.New("unexpected CreateRun call")
	}
	return s.create(ctx, input)
}

// GetRun 记录查询调用并返回测试预设的 run 视图。
func (s *chatServiceStub) GetRun(ctx context.Context, id string) (chat.Run, error) {
	s.getRunCalls++
	if s.getRun == nil {
		return chat.Run{}, errors.New("unexpected GetRun call")
	}
	return s.getRun(ctx, id)
}

// CancelRun 记录取消调用并返回测试预设的状态或错误。
func (s *chatServiceStub) CancelRun(ctx context.Context, id string) (chat.Status, error) {
	s.cancelCalls++
	if s.cancel == nil {
		return "", errors.New("unexpected CancelRun call")
	}
	return s.cancel(ctx, id)
}

// GetSession 记录会话查询并返回测试预设的 run ID 列表。
func (s *chatServiceStub) GetSession(ctx context.Context, id string) ([]string, error) {
	s.getSessionCalls++
	if s.getSession == nil {
		return nil, errors.New("unexpected GetSession call")
	}
	return s.getSession(ctx, id)
}

// ListSessions 记录会话列表查询并返回测试预设的结果。
func (s *chatServiceStub) ListSessions(ctx context.Context) ([]string, error) {
	s.listSessionsCalls++
	if s.listSessions == nil {
		return nil, errors.New("unexpected ListSessions call")
	}
	return s.listSessions(ctx)
}

// DeleteSession 记录删除会话调用并返回测试预设的错误。
func (s *chatServiceStub) DeleteSession(ctx context.Context, id string) error {
	s.deleteSessionCalls++
	if s.deleteSession == nil {
		return errors.New("unexpected DeleteSession call")
	}
	return s.deleteSession(ctx, id)
}

// Subscribe 记录事件订阅调用并返回测试预设的流或错误。
func (s *chatServiceStub) Subscribe(ctx context.Context, id string, after int64) (*chat.Subscription, error) {
	s.subscribeCalls++
	if s.subscribe == nil {
		return nil, errors.New("unexpected Subscribe call")
	}
	return s.subscribe(ctx, id, after)
}

func newChatHTTPTestRouter(s chat.Service) *gin.Engine {
	gin.SetMode(gin.TestMode)
	return NewRouter(&service.App{ChatService: s}, nil)
}

func chatRequest(router http.Handler, method, target string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	req.Host = "127.0.0.1:10086"
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	return response
}

func jsonHeaders(extra map[string]string) map[string]string {
	result := map[string]string{"Content-Type": "application/json"}
	for key, value := range extra {
		result[key] = value
	}
	return result
}

func assertErrorResponse(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status = %d, want %d; body: %s", response.Code, status, response.Body.String())
	}
	var payload struct {
		Error struct {
			Code    any `json:"code"`
			Message any `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if payload.Error.Code != code {
		t.Fatalf("error.code = %#v, want %q", payload.Error.Code, code)
	}
	if _, ok := payload.Error.Code.(string); !ok {
		t.Fatalf("error.code type = %T, want string", payload.Error.Code)
	}
	if _, ok := payload.Error.Message.(string); !ok {
		t.Fatalf("error.message type = %T, want string", payload.Error.Message)
	}
}

func decodeChatObject(t *testing.T, body []byte) map[string]json.RawMessage {
	t.Helper()
	var value map[string]json.RawMessage
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatalf("decode JSON object: %v; body=%s", err, body)
	}
	return value
}

// TestChatHTTPRoutesAndDirectResponses_HTTP01_HTTP02 验证七个接口路由和直接响应结构。
func TestChatHTTPRoutesAndDirectResponses_HTTP01_HTTP02(t *testing.T) {
	stub := &chatServiceStub{
		create: func(context.Context, chat.CreateRunInput) (string, error) { return testRunID, nil },
		getRun: func(context.Context, string) (chat.Run, error) {
			return chat.Run{RunID: testRunID, SessionID: testSessionID, Model: "deepseek-flash", Input: "x", Status: chat.StatusRunning, CreatedAt: time.Date(2026, 10, 15, 15, 0, 0, 0, time.FixedZone("UTC+08:00", 8*3600))}, nil
		},
		getSession:    func(context.Context, string) ([]string, error) { return []string{}, nil },
		listSessions:  func(context.Context) ([]string, error) { return []string{}, nil },
		deleteSession: func(context.Context, string) error { return nil },
		cancel:        func(context.Context, string) (chat.Status, error) { return chat.StatusRunning, nil },
	}
	router := newChatHTTPTestRouter(stub)
	cases := []struct {
		name, method, path string
		body               []byte
		headers            map[string]string
		status             int
	}{
		{"create run", "POST", "/v1/run", []byte(fmt.Sprintf(`{"session_id":%q,"input":"x","model":"deepseek-flash"}`, testSessionID)), jsonHeaders(map[string]string{"Idempotency-Key": "create_session:" + testKeyUUID}), 202},
		{"get run", "GET", "/v1/run/" + testRunID, nil, nil, 200},
		{"get session", "GET", "/v1/session/" + testSessionID, nil, nil, 200},
		{"delete session", "DELETE", "/v1/session/" + testSessionID, nil, nil, 200},
		{"list sessions", "GET", "/v1/session", nil, nil, 200},
		{"cancel run", "POST", "/v1/run/" + testRunID + "/cancel", nil, nil, 202},
		{"no standalone delete run", "DELETE", "/v1/run/" + testRunID, nil, nil, 405},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			response := chatRequest(router, tc.method, tc.path, tc.body, tc.headers)
			if response.Code != tc.status {
				t.Fatalf("status=%d want=%d body=%s", response.Code, tc.status, response.Body.String())
			}
			if tc.status == 200 || tc.status == 202 {
				object := decodeChatObject(t, response.Body.Bytes())
				if _, wrapped := object["data"]; wrapped {
					t.Fatalf("unexpected data wrapper: %s", response.Body.String())
				}
			}
		})
	}
}

// TestCreateRunValidationAndRawInput_HTTP05_HTTP06_HTTP07_CREATE01_CREATE02_CREATE03 验证创建参数、模型、幂等键和原文输入。
func TestCreateRunValidationAndRawInput_HTTP05_HTTP06_HTTP07_CREATE01_CREATE02_CREATE03(t *testing.T) {
	stub := &chatServiceStub{create: func(context.Context, chat.CreateRunInput) (string, error) { return testRunID, nil }}
	router := newChatHTTPTestRouter(stub)
	valid := fmt.Sprintf(`{"session_id":%q,"input":"  中文\nsecond line  ","model":"deepseek-flash"}`, testSessionID)
	goodHeaders := jsonHeaders(map[string]string{"Idempotency-Key": "create_session:" + testKeyUUID})
	response := chatRequest(router, "POST", "/v1/run", []byte(valid), goodHeaders)
	if response.Code != 202 {
		t.Fatalf("valid create status=%d body=%s", response.Code, response.Body.String())
	}
	if stub.createInput.Input != "  中文\nsecond line  " {
		t.Fatalf("input changed: %q", stub.createInput.Input)
	}
	if stub.createInput.SessionID != testSessionID || stub.createInput.Model != "deepseek-flash" || stub.createInput.IdempotencyKey != goodHeaders["Idempotency-Key"] {
		t.Fatalf("incomplete create input: %+v", stub.createInput)
	}
	if stub.createCalls != 1 {
		t.Fatalf("create calls=%d want 1", stub.createCalls)
	}

	badBodies := []string{
		fmt.Sprintf(`{"input":"x","model":"deepseek-flash"}`),
		fmt.Sprintf(`{"session_id":%q,"model":"deepseek-flash"}`, testSessionID),
		fmt.Sprintf(`{"session_id":%q,"input":"x"}`, testSessionID),
		`{"session_id":"bad","input":"x","model":"deepseek-flash"}`,
		fmt.Sprintf(`{"session_id":null,"input":"x","model":"deepseek-flash"}`),
		fmt.Sprintf(`{"session_id":1,"input":"x","model":"deepseek-flash"}`),
		fmt.Sprintf(`{"session_id":%q,"input":null,"model":"deepseek-flash"}`, testSessionID),
		fmt.Sprintf(`{"session_id":%q,"input":{},"model":"deepseek-flash"}`, testSessionID),
		fmt.Sprintf(`{"session_id":%q,"input":"x","model":null}`, testSessionID),
		fmt.Sprintf(`{"session_id":%q,"input":"x","model":[]}`, testSessionID),
		fmt.Sprintf(`{"session_id":%q,"input":1,"model":"deepseek-flash"}`, testSessionID),
		fmt.Sprintf(`{"session_id":%q,"input":"x","model":1}`, testSessionID),
		fmt.Sprintf(`{"session_id":%q,"input":"","model":"deepseek-flash"}`, testSessionID),
		fmt.Sprintf(`{"session_id":%q,"input":"  \t\n","model":"deepseek-flash"}`, testSessionID),
		fmt.Sprintf(`{"session_id":%q,"input":"x","model":"  "}`, testSessionID),
		fmt.Sprintf(`{"session_id":%q,"input":"x","model":"future-model"}`, testSessionID),
		fmt.Sprintf(`{"session_id":%q,"input":"x","model":"deepseek-flash","extra":1}`, testSessionID),
		`{"session_id":`,
	}
	for i, body := range badBodies {
		t.Run(fmt.Sprintf("invalid body %02d", i+1), func(t *testing.T) {
			before := stub.createCalls
			headers := goodHeaders
			status := 400
			if i == len(badBodies)-2 {
				headers = jsonHeaders(map[string]string{"Idempotency-Key": "create_session:" + testKeyUUID})
			}
			response := chatRequest(router, "POST", "/v1/run", []byte(body), headers)
			assertErrorResponse(t, response, status, "invalid_request")
			if stub.createCalls != before {
				t.Fatalf("invalid body called CreateRun")
			}
		})
	}
	response = chatRequest(router, "POST", "/v1/run", []byte(valid), map[string]string{"Idempotency-Key": "create_session:" + testKeyUUID})
	assertErrorResponse(t, response, 415, "unsupported_media_type")
	tooLarge := fmt.Sprintf(`{"session_id":%q,"input":"%s","model":"deepseek-flash"}`, testSessionID, strings.Repeat("a", 1<<20))
	response = chatRequest(router, "POST", "/v1/run", []byte(tooLarge), goodHeaders)
	assertErrorResponse(t, response, 413, "payload_too_large")
	for _, key := range []string{"", "bad", "create_session:bad"} {
		before := stub.createCalls
		headers := jsonHeaders(map[string]string{})
		if key != "" {
			headers["Idempotency-Key"] = key
		}
		response = chatRequest(router, "POST", "/v1/run", []byte(valid), headers)
		assertErrorResponse(t, response, 400, "invalid_request")
		if stub.createCalls != before {
			t.Fatal("invalid idempotency key called service")
		}
	}
}

// TestCreateRunBusinessMappingsAndIdempotency_CREATE04_CREATE05_CREATE06_CREATE07_CREATE08_CREATE09_CREATE10_HTTP03 验证创建错误映射与幂等参数传递。
func TestCreateRunBusinessMappingsAndIdempotency_CREATE04_CREATE05_CREATE06_CREATE07_CREATE08_CREATE09_CREATE10_HTTP03(t *testing.T) {
	otherSession := "650e8400-e29b-41d4-a716-446655440000"
	sequence := []struct {
		key, sessionID, input, runID string
		err                          error
		wantStatus                   int
		wantCode                     string
	}{
		{"create_session:" + testKeyUUID, testSessionID, "same input", testRunID, nil, 202, ""}, // 首次成功
		{"create_session:" + testKeyUUID, testSessionID, "same input", testRunID, nil, 202, ""}, // CREATE-04 同内容重放
		{"create_session:" + testKeyUUID, testSessionID, "changed input", "", chat.ErrIdempotencyConflict, 409, "idempotency_key_conflict"},
		{"create_session:" + testKeyUUID, otherSession, "same input", "", chat.ErrIdempotencyConflict, 409, "idempotency_key_conflict"},
		{"create_session:c9bf9e57-1685-4c89-bafb-ff5af830be81", testSessionID, "busy", "", chat.ErrSessionBusy, 409, "session_busy"},
		{"create_session:c9bf9e57-1685-4c89-bafb-ff5af830be82", otherSession, "limit", "", chat.ErrSessionConcurrencyLimit, 409, "session_concurrency_limit"},
		{"create_session:c9bf9e57-1685-4c89-bafb-ff5af830be83", otherSession, "retry", "", chat.ErrSessionConcurrencyLimit, 409, "session_concurrency_limit"},
		{"create_session:c9bf9e57-1685-4c89-bafb-ff5af830be83", otherSession, "retry", testRunID, nil, 202, ""},              // CREATE-08 同 key 和内容重试
		{"create_session:c9bf9e57-1685-4c89-bafb-ff5af830be84", testSessionID, "failed history", testRunID, nil, 202, ""},    // CREATE-09 下层重放 failed run
		{"create_session:c9bf9e57-1685-4c89-bafb-ff5af830be85", testSessionID, "cancelled history", testRunID, nil, 202, ""}, // CREATE-09 下层重放 cancelled run
		{"create_session:" + testKeyUUID, testSessionID, "same input", "", chat.ErrSessionDeleted, 400, "session_deleted"},
	}
	call := 0
	stub := &chatServiceStub{create: func(context.Context, chat.CreateRunInput) (string, error) {
		i := call
		call++
		if i >= len(sequence) {
			return "", errors.New("unexpected call")
		}
		return sequence[i].runID, sequence[i].err
	}}
	router := newChatHTTPTestRouter(stub)
	for i, tc := range sequence {
		t.Run(fmt.Sprintf("CREATE case %02d", i+1), func(t *testing.T) {
			input := fmt.Sprintf(`{"session_id":%q,"input":%q,"model":"deepseek-flash"}`, tc.sessionID, tc.input)
			response := chatRequest(router, "POST", "/v1/run", []byte(input), jsonHeaders(map[string]string{"Idempotency-Key": tc.key}))
			if tc.wantCode != "" {
				assertErrorResponse(t, response, tc.wantStatus, tc.wantCode)
				return
			}
			if response.Code != tc.wantStatus {
				t.Fatalf("status=%d want=%d body=%s", response.Code, tc.wantStatus, response.Body.String())
			}
			obj := decodeChatObject(t, response.Body.Bytes())
			var got string
			if err := json.Unmarshal(obj["run_id"], &got); err != nil || got != tc.runID {
				t.Fatalf("run_id=%q want=%q err=%v", got, tc.runID, err)
			}
		})
	}
	if stub.createCalls != len(sequence) {
		t.Fatalf("calls=%d want=%d", stub.createCalls, len(sequence))
	}
	for i, input := range stub.createInputs {
		want := sequence[i]
		if input.SessionID != want.sessionID || input.Input != want.input || input.Model != "deepseek-flash" || input.IdempotencyKey != want.key {
			t.Errorf("CREATE case %02d parameters=%+v", i+1, input)
		}
	}
}

// TestRunQueryStatesAndVisibility_RUN01_RUN02_RUN03_RUN04_RUN05_RUN06_HTTP04_HTTP08 验证 run 状态视图、可见性和时间格式。
func TestRunQueryStatesAndVisibility_RUN01_RUN02_RUN03_RUN04_RUN05_RUN06_HTTP04_HTTP08(t *testing.T) {
	instant := time.Date(2026, 10, 15, 15, 0, 0, 0, time.FixedZone("UTC+08:00", 8*3600))
	finished := instant.Add(time.Minute)
	output := "complete"
	stub := &chatServiceStub{getRun: func(_ context.Context, id string) (chat.Run, error) {
		if id == "00000000-0000-4000-8000-000000000000" {
			return chat.Run{}, chat.ErrRunNotFound
		}
		switch id {
		case testRunID:
			return chat.Run{RunID: id, SessionID: testSessionID, Model: "deepseek-flash", Input: "hello", Status: chat.StatusRunning, Output: &output, Error: &chat.RunError{Code: "hidden", Message: "hidden"}, CreatedAt: instant, FinishedAt: &finished}, nil
		case "d9428888-122b-4f6f-9b47-5ea7b2915211":
			return chat.Run{RunID: id, SessionID: testSessionID, Model: "deepseek-flash", Input: "hello", Status: chat.StatusSucceeded, Output: &output, CreatedAt: instant, FinishedAt: &finished}, nil
		case "d9428888-122b-4f6f-9b47-5ea7b2915212":
			return chat.Run{RunID: id, SessionID: testSessionID, Model: "deepseek-flash", Input: "hello", Status: chat.StatusFailed, Error: &chat.RunError{Code: "model_failed", Message: "failed"}, CreatedAt: instant, FinishedAt: &finished}, nil
		default:
			checkpoint := "checkpoint"
			return chat.Run{RunID: id, SessionID: testSessionID, Model: "deepseek-flash", Input: "hello", Status: chat.StatusCancelled, Output: &checkpoint, CreatedAt: instant, FinishedAt: &finished}, nil
		}
	}}
	router := newChatHTTPTestRouter(stub)
	ids := []string{testRunID, "d9428888-122b-4f6f-9b47-5ea7b2915211", "d9428888-122b-4f6f-9b47-5ea7b2915212", "d9428888-122b-4f6f-9b47-5ea7b2915213"}
	for _, id := range ids {
		t.Run("RUN state "+id[len(id)-1:], func(t *testing.T) {
			response := chatRequest(router, "GET", "/v1/run/"+id, nil, nil)
			if response.Code != 200 {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			obj := decodeChatObject(t, response.Body.Bytes())
			want := []string{"run_id", "session_id", "model", "input", "status", "output", "error", "created_at", "finished_at"}
			if len(obj) != len(want) {
				t.Fatalf("field count=%d body=%s", len(obj), response.Body.String())
			}
			for _, key := range want {
				if _, ok := obj[key]; !ok {
					t.Errorf("missing %s", key)
				}
			}
			var input string
			_ = json.Unmarshal(obj["input"], &input)
			if input != "hello" {
				t.Errorf("input=%q", input)
			}
			var status chat.Status
			_ = json.Unmarshal(obj["status"], &status)
			if status == chat.StatusRunning {
				if string(obj["output"]) != "null" || string(obj["error"]) != "null" {
					t.Errorf("running leaked output/error: %s", response.Body.String())
				}
			}
			if status == chat.StatusSucceeded && (string(obj["output"]) != `"complete"` || string(obj["error"]) != "null") {
				t.Errorf("succeeded output/error=%s/%s", obj["output"], obj["error"])
			}
			if status == chat.StatusFailed {
				if string(obj["output"]) != "null" || !strings.Contains(string(obj["error"]), "model_failed") {
					t.Errorf("failed output/error=%s/%s", obj["output"], obj["error"])
				}
				if response.Code != 200 {
					t.Errorf("failed run must be HTTP 200")
				}
			}
			if status == chat.StatusCancelled && (string(obj["output"]) != `"checkpoint"` || string(obj["error"]) != "null") {
				t.Errorf("cancelled output/error=%s/%s", obj["output"], obj["error"])
			}
			if string(obj["created_at"]) == "null" || !strings.Contains(string(obj["created_at"]), "+08:00") {
				t.Errorf("created_at not RFC3339 +08:00: %s", obj["created_at"])
			}
			if status == chat.StatusRunning {
				if string(obj["finished_at"]) != "null" {
					t.Errorf("running finished_at=%s", obj["finished_at"])
				}
			} else if string(obj["finished_at"]) == "null" || !strings.Contains(string(obj["finished_at"]), "+08:00") {
				t.Errorf("terminal finished_at=%s", obj["finished_at"])
			}
		})
	}
	response := chatRequest(router, "GET", "/v1/run/00000000-0000-4000-8000-000000000000", nil, nil)
	assertErrorResponse(t, response, 404, "run_not_found")
	for _, id := range []string{"bad", "not-a-uuid"} {
		before := stub.getRunCalls
		response = chatRequest(router, "GET", "/v1/run/"+id, nil, nil)
		assertErrorResponse(t, response, 400, "invalid_request")
		if stub.getRunCalls != before {
			t.Fatal("invalid UUID called service")
		}
	}
	response = chatRequest(router, "GET", "/v1/run/"+strings.ToUpper(ids[1]), nil, nil)
	if response.Code != 200 {
		t.Fatalf("uppercase standard UUID rejected: status=%d body=%s", response.Code, response.Body.String())
	}
}

// TestCancelRunStatusAndNotFound_CANCEL01_CANCEL02_CANCEL03_CANCEL04 验证取消受理、终态响应和不可见 run 错误。
func TestCancelRunStatusAndNotFound_CANCEL01_CANCEL02_CANCEL03_CANCEL04(t *testing.T) {
	status := chat.StatusRunning
	stub := &chatServiceStub{cancel: func(context.Context, string) (chat.Status, error) { return status, nil }}
	router := newChatHTTPTestRouter(stub)
	for _, tc := range []struct {
		status chat.Status
		code   int
	}{{chat.StatusRunning, 202}, {chat.StatusSucceeded, 200}, {chat.StatusFailed, 200}, {chat.StatusCancelled, 200}} {
		t.Run(string(tc.status), func(t *testing.T) {
			status = tc.status
			response := chatRequest(router, "POST", "/v1/run/"+testRunID+"/cancel", nil, nil)
			if response.Code != tc.code {
				t.Fatalf("status=%d want=%d body=%s", response.Code, tc.code, response.Body.String())
			}
			var body struct {
				RunID  string      `json:"run_id"`
				Status chat.Status `json:"status"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.RunID != testRunID || body.Status != tc.status {
				t.Fatalf("body=%+v", body)
			}
		})
	}
	stub.cancel = func(context.Context, string) (chat.Status, error) { return "", chat.ErrRunNotFound }
	response := chatRequest(router, "POST", "/v1/run/"+testRunID+"/cancel", nil, nil)
	assertErrorResponse(t, response, 404, "run_not_found")
	before := stub.cancelCalls
	response = chatRequest(router, "POST", "/v1/run/not-a-uuid/cancel", nil, nil)
	assertErrorResponse(t, response, 400, "invalid_request")
	if stub.cancelCalls != before {
		t.Fatal("invalid run id called cancel")
	}
}

// TestSessionQueriesAndDeletion_SESSION01_SESSION02_SESSION03_DELETE01_DELETE02_DELETE03_DELETE04 验证会话查询、空列表及删除结果。
func TestSessionQueriesAndDeletion_SESSION01_SESSION02_SESSION03_DELETE01_DELETE02_DELETE03_DELETE04(t *testing.T) {
	deleted := false
	stub := &chatServiceStub{
		getSession: func(context.Context, string) ([]string, error) {
			return []string{testRunID, "d9428888-122b-4f6f-9b47-5ea7b2915211"}, nil
		},
		listSessions: func(context.Context) ([]string, error) {
			if deleted {
				return []string{}, nil
			}
			return []string{otherSessionID, testSessionID}, nil
		},
		deleteSession: func(context.Context, string) error { deleted = true; return nil },
	}
	router := newChatHTTPTestRouter(stub)
	response := chatRequest(router, "GET", "/v1/session/"+testSessionID, nil, nil)
	if response.Code != 200 {
		t.Fatalf("get session status=%d", response.Code)
	}
	obj := decodeChatObject(t, response.Body.Bytes())
	if _, ok := obj["data"]; ok {
		t.Fatal("unexpected wrapper")
	}
	var ids []string
	_ = json.Unmarshal(obj["run_ids"], &ids)
	if len(ids) != 2 || ids[0] != testRunID || ids[1] != "d9428888-122b-4f6f-9b47-5ea7b2915211" {
		t.Fatalf("run_ids=%v", ids)
	}
	response = chatRequest(router, "GET", "/v1/session", nil, nil)
	obj = decodeChatObject(t, response.Body.Bytes())
	var sessions []string
	_ = json.Unmarshal(obj["sessions"], &sessions)
	if len(sessions) != 2 || sessions[0] != otherSessionID || sessions[1] != testSessionID {
		t.Fatalf("sessions=%v", sessions)
	}
	response = chatRequest(router, "DELETE", "/v1/session/"+testSessionID, nil, nil)
	if response.Code != 200 {
		t.Fatalf("delete status=%d", response.Code)
	}
	var del struct {
		SessionID string `json:"session_id"`
		Status    string `json:"status"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &del); err != nil {
		t.Fatal(err)
	}
	if del.SessionID != testSessionID || del.Status != "deleted" {
		t.Fatalf("delete body=%+v", del)
	}
	response = chatRequest(router, "DELETE", "/v1/session/"+testSessionID, nil, nil)
	if response.Code != 200 {
		t.Fatalf("repeated delete status=%d", response.Code)
	}
	response = chatRequest(router, "GET", "/v1/session", nil, nil)
	obj = decodeChatObject(t, response.Body.Bytes())
	if string(obj["sessions"]) != "[]" {
		t.Fatalf("empty sessions must be []: %s", response.Body.String())
	}
	stub.getSession = func(context.Context, string) ([]string, error) { return []string{}, nil }
	response = chatRequest(router, "GET", "/v1/session/"+testSessionID, nil, nil)
	obj = decodeChatObject(t, response.Body.Bytes())
	if string(obj["run_ids"]) != "[]" {
		t.Fatalf("empty run_ids must be []: %s", response.Body.String())
	}
	stub.deleteSession = func(context.Context, string) error { return chat.ErrSessionNotFound }
	response = chatRequest(router, "DELETE", "/v1/session/"+testSessionID, nil, nil)
	assertErrorResponse(t, response, 400, "session_not_found")
	stub.getSession = func(context.Context, string) ([]string, error) { return nil, chat.ErrSessionNotFound }
	response = chatRequest(router, "GET", "/v1/session/"+testSessionID, nil, nil)
	assertErrorResponse(t, response, 400, "session_not_found")
}

// TestInvalidUUIDPathsDoNotCallChatService_HTTP04 验证路径 ID 格式错误时不会调用应用服务。
func TestInvalidUUIDPathsDoNotCallChatService_HTTP04(t *testing.T) {
	stub := &chatServiceStub{}
	router := newChatHTTPTestRouter(stub)
	for _, tc := range []struct{ method, path string }{
		{"GET", "/v1/run/not-a-uuid"},
		{"GET", "/v1/run/00000000-0000-4000-0000-000000000000"},
		{"GET", "/v1/run/not-a-uuid/event"},
		{"POST", "/v1/run/not-a-uuid/cancel"},
		{"GET", "/v1/session/not-a-uuid"},
		{"DELETE", "/v1/session/not-a-uuid"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			response := chatRequest(router, tc.method, tc.path, nil, nil)
			assertErrorResponse(t, response, 400, "invalid_request")
		})
	}
	if stub.getRunCalls+stub.subscribeCalls+stub.cancelCalls+stub.getSessionCalls+stub.deleteSessionCalls != 0 {
		t.Fatalf("invalid path called chat service: %+v", stub)
	}
}

// TestRunAndSessionBusinessErrorMappings_HTTP03 验证 run 和 session 查询错误的 HTTP 映射。
func TestRunAndSessionBusinessErrorMappings_HTTP03(t *testing.T) {
	stub := &chatServiceStub{getRun: func(context.Context, string) (chat.Run, error) { return chat.Run{}, chat.ErrRunNotFound }, getSession: func(context.Context, string) ([]string, error) { return nil, chat.ErrSessionNotFound }, listSessions: func(context.Context) ([]string, error) { return nil, chat.ErrInvalidRequest }}
	router := newChatHTTPTestRouter(stub)
	for _, tc := range []struct {
		method, path string
		status       int
		code         string
	}{{"GET", "/v1/run/" + testRunID, 404, "run_not_found"}, {"GET", "/v1/session/" + testSessionID, 400, "session_not_found"}, {"GET", "/v1/session", 400, "invalid_request"}} {
		response := chatRequest(router, tc.method, tc.path, nil, nil)
		assertErrorResponse(t, response, tc.status, tc.code)
	}
}

// TestCancelRunConfirmedCheckpointFlow_CANCEL04 验证取消确认前后状态及 checkpoint 输出。
func TestCancelRunConfirmedCheckpointFlow_CANCEL04(t *testing.T) {
	mock := chat.NewMockWithOptions(chat.MockOptions{})
	runID, err := mock.CreateRun(context.Background(), chat.CreateRunInput{SessionID: testSessionID, Input: "cancel flow", Model: "deepseek-flash", IdempotencyKey: "create_session:" + testKeyUUID})
	if err != nil {
		t.Fatal(err)
	}
	router := newChatHTTPTestRouter(mock)
	response := chatRequest(router, "POST", "/v1/run/"+runID+"/cancel", nil, nil)
	if response.Code != 202 {
		t.Fatalf("cancel acceptance status=%d body=%s", response.Code, response.Body.String())
	}
	response = chatRequest(router, "GET", "/v1/run/"+runID, nil, nil)
	if response.Code != 200 {
		t.Fatalf("running query status=%d", response.Code)
	}
	var running chat.Run
	if err := json.Unmarshal(response.Body.Bytes(), &running); err != nil {
		t.Fatal(err)
	}
	if running.Status != chat.StatusRunning || running.FinishedAt != nil {
		t.Fatalf("cancel changed run before confirmation: %+v", running)
	}
	checkpoint := "checkpoint before cancel"
	if err := mock.ConfirmRun(runID, chat.StatusCancelled, &checkpoint, nil, json.RawMessage(`{"stop_reason":"cancelled","error":null}`)); err != nil {
		t.Fatal(err)
	}
	response = chatRequest(router, "GET", "/v1/run/"+runID, nil, nil)
	if response.Code != 200 {
		t.Fatalf("cancelled query status=%d", response.Code)
	}
	var cancelled chat.Run
	if err := json.Unmarshal(response.Body.Bytes(), &cancelled); err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != chat.StatusCancelled || cancelled.Output == nil || *cancelled.Output != checkpoint || cancelled.Error != nil || cancelled.FinishedAt == nil {
		t.Fatalf("cancelled checkpoint response=%+v", cancelled)
	}
}

// TestCancelTerminalRunWarningsContainOnlyRunAndStatus_CANCEL02 验证终态取消告警只包含 run ID 和状态。
func TestCancelTerminalRunWarningsContainOnlyRunAndStatus_CANCEL02(t *testing.T) {
	logs := captureHTTPLogs(t)
	status := chat.StatusSucceeded
	stub := &chatServiceStub{cancel: func(context.Context, string) (chat.Status, error) { return status, nil }}
	router := newChatHTTPTestRouter(stub)
	for _, value := range []chat.Status{chat.StatusSucceeded, chat.StatusFailed, chat.StatusCancelled} {
		status = value
		response := chatRequest(router, "POST", "/v1/run/"+testRunID+"/cancel", nil, nil)
		if response.Code != 200 {
			t.Fatalf("terminal cancel status=%d", response.Code)
		}
	}
	lines := bytes.Split(bytes.TrimSpace(logs()), []byte("\n"))
	if len(lines) != 3 {
		t.Fatalf("got %d log records, want three terminal warnings: %s", len(lines), logs())
	}
	for i, line := range lines {
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatal(err)
		}
		if record["level"] != "WARN" && record["level"] != "warn" {
			t.Errorf("terminal cancel level=%v", record["level"])
		}
		if record["run_id"] != testRunID || record["run_status"] != string([]chat.Status{chat.StatusSucceeded, chat.StatusFailed, chat.StatusCancelled}[i]) {
			t.Errorf("warning lacks run ID/status: %v", record)
		}
		if bytes.Contains(line, []byte("private-original-input")) {
			t.Errorf("warning exposed run input: %s", line)
		}
	}
}
