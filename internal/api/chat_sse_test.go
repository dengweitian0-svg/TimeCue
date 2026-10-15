package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/api/handler"
	"github.com/dengweitian0-svg/TimeCue/internal/middleware"
	"github.com/gin-gonic/gin"
)

const sseWaitLimit = 2 * time.Second

type sseFrame struct {
	id    string
	event string
	data  string
}

type sseReadResult struct {
	frame sseFrame
	err   error
}

func newSSEFixture(t *testing.T) (*chat.Mock, string, *httptest.Server) {
	t.Helper()
	mock := chat.NewMockWithOptions(chat.MockOptions{})
	runID, err := mock.CreateRun(context.Background(), chat.CreateRunInput{SessionID: testSessionID, Input: "SSE input", Model: "deepseek-flash", IdempotencyKey: "create_session:" + testKeyUUID})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(newChatHTTPTestRouter(mock))
	t.Cleanup(server.Close)
	return mock, runID, server
}

func openSSE(t *testing.T, server *httptest.Server, runID, cursor string) (*http.Response, context.CancelFunc, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/v1/run/"+runID+"/event", nil)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	req.Host = "127.0.0.1:10086"
	if cursor != "" {
		req.Header.Set("Last-Event-ID", cursor)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	return resp, cancel, nil
}

func readSSEFrameWithin(t *testing.T, reader *bufio.Reader, timeout time.Duration) sseFrame {
	t.Helper()
	result := make(chan sseReadResult, 1)
	go func() {
		var frame sseFrame
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				result <- sseReadResult{err: err}
				return
			}
			line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
			if line == "" {
				result <- sseReadResult{frame: frame}
				return
			}
			key, value, ok := strings.Cut(line, ":")
			if !ok {
				result <- sseReadResult{err: fmt.Errorf("malformed SSE line %q", line)}
				return
			}
			value = strings.TrimPrefix(value, " ")
			switch key {
			case "id":
				frame.id = value
			case "event":
				frame.event = value
			case "data":
				if frame.data != "" {
					frame.data += "\n"
				}
				frame.data += value
			}
		}
	}()
	select {
	case got := <-result:
		if got.err != nil {
			t.Fatalf("read SSE frame: %v", got.err)
		}
		return got.frame
	case <-time.After(timeout):
		t.Fatal("timed out waiting for the next SSE frame")
		return sseFrame{}
	}
}

func assertSSEJSON(t *testing.T, frame sseFrame, wantID, wantEvent string, destination any) {
	t.Helper()
	if frame.id != wantID || frame.event != wantEvent {
		t.Fatalf("frame id/event=%q/%q want=%q/%q data=%s", frame.id, frame.event, wantID, wantEvent, frame.data)
	}
	if err := json.Unmarshal([]byte(frame.data), destination); err != nil {
		t.Fatalf("invalid SSE JSON data %q: %v", frame.data, err)
	}
}

func publishSSE(t *testing.T, mock *chat.Mock, runID string, eventType chat.EventType, payload string) {
	t.Helper()
	if _, err := mock.PublishEvent(runID, eventType, json.RawMessage(payload)); err != nil {
		t.Fatal(err)
	}
}

func closeAndWaitForSSERelease(t *testing.T, mock *chat.Mock, runID string, lastID int64, response *http.Response, cancel context.CancelFunc) {
	t.Helper()
	cancel()
	_ = response.Body.Close()
	deadline := time.Now().Add(sseWaitLimit)
	for time.Now().Before(deadline) {
		probe, err := mock.Subscribe(context.Background(), runID, lastID)
		if err == nil {
			probe.Close()
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("disconnected SSE subscription was not released")
}

// TestSSELiveEventsFlushIndividuallyAndPreserveJSON_SSE01_SSE02_SSE03_SSE04_SSE06 验证 SSE 事件逐帧发送和 JSON 内容保真。
func TestSSELiveEventsFlushIndividuallyAndPreserveJSON_SSE01_SSE02_SSE03_SSE04_SSE06(t *testing.T) {
	mock, runID, server := newSSEFixture(t)
	response, cancel, err := openSSE(t, server, runID, "")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	defer response.Body.Close()
	if response.StatusCode != 200 || !strings.Contains(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("SSE response = %d %q", response.StatusCode, response.Header.Get("Content-Type"))
	}
	reader := bufio.NewReader(response.Body)
	publishSSE(t, mock, runID, chat.EventStart, `{}`)
	start := readSSEFrameWithin(t, reader, sseWaitLimit)
	if start.id != "1" || start.event != "start" || start.data != "{}" {
		t.Fatalf("start frame=%+v", start)
	}
	var thinking chat.ThinkingData
	publishSSE(t, mock, runID, chat.EventThinking, `{"content":"思考","error":null}`)
	assertSSEJSON(t, readSSEFrameWithin(t, reader, sseWaitLimit), "2", "thinking", &thinking)
	if thinking.Content != "思考" || thinking.Error != nil {
		t.Fatalf("thinking=%+v", thinking)
	}
	content := "回复含中文、引号\"、第一行\n第二行"
	data, _ := json.Marshal(chat.TextData{Content: content})
	publishSSE(t, mock, runID, chat.EventText, string(data))
	var text chat.TextData
	assertSSEJSON(t, readSSEFrameWithin(t, reader, sseWaitLimit), "3", "text", &text)
	if text.Content != content || text.Error != nil {
		t.Fatalf("text content=%q error=%+v", text.Content, text.Error)
	}
	toolFrame, err := middleware.EncodeSSE(8, "tool", chat.ToolData{ID: "call-1", Function: chat.ToolFunction{Name: "lookup", Args: chat.ToolArgs{Name: "query", Val: map[string]any{"value": "中文"}}}})
	if err != nil {
		t.Fatal(err)
	}
	var toolPayload map[string]any
	line := strings.Split(strings.TrimSpace(string(toolFrame)), "\n")[2]
	if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &toolPayload); err != nil {
		t.Fatal(err)
	}
	function, _ := toolPayload["function"].(map[string]any)
	args, _ := function["args"].(map[string]any)
	if !strings.HasPrefix(string(toolFrame), "id: 8\n") || toolPayload["id"] != "call-1" || function["name"] != "lookup" || args["name"] != "query" || args["val"].(map[string]any)["value"] != "中文" {
		t.Fatalf("tool fixture structure=%v", toolPayload)
	}
	secondSession := "650e8400-e29b-41d4-a716-446655440000"
	otherRun, err := mock.CreateRun(context.Background(), chat.CreateRunInput{SessionID: secondSession, Input: "independent", Model: "deepseek-flash", IdempotencyKey: "create_session:c9bf9e57-1685-4c89-bafb-ff5af830be86"})
	if err != nil {
		t.Fatal(err)
	}
	otherEvent, err := mock.PublishEvent(otherRun, chat.EventStart, json.RawMessage(`{}`))
	if err != nil || otherEvent.ID != 1 {
		t.Fatalf("independent run event ID=%d err=%v", otherEvent.ID, err)
	}
	otherResponse, otherCancel, err := openSSE(t, server, otherRun, "")
	if err != nil {
		t.Fatal(err)
	}
	defer otherCancel()
	defer otherResponse.Body.Close()
	otherFrame := readSSEFrameWithin(t, bufio.NewReader(otherResponse.Body), sseWaitLimit)
	if otherFrame.id != "1" || otherFrame.event != "start" {
		t.Fatalf("second run SSE event ID did not start at 1: %+v", otherFrame)
	}
	if err := mock.ConfirmRun(runID, chat.StatusSucceeded, &content, nil, json.RawMessage(`{"stop_reason":"stop","error":null}`)); err != nil {
		t.Fatal(err)
	}
	var end chat.EndData
	assertSSEJSON(t, readSSEFrameWithin(t, reader, sseWaitLimit), "4", "end", &end)
	if end.StopReason != "stop" || end.Error != nil {
		t.Fatalf("end=%+v", end)
	}
	if _, err := reader.ReadByte(); err != io.EOF {
		t.Fatalf("stream not closed after end: %v", err)
	}
}

// TestSSEEndReasonsAndIncrementalErrorFollowLowerLayer_SSE05_SSE06 验证终止原因和增量错误由下层数据转发。
func TestSSEEndReasonsAndIncrementalErrorFollowLowerLayer_SSE05_SSE06(t *testing.T) {
	for index, tc := range []struct {
		reason string
		status chat.Status
		runErr *chat.RunError
	}{{"stop", chat.StatusSucceeded, nil}, {"insufficient_system_resource", chat.StatusFailed, &chat.RunError{Code: "resource", Message: "busy"}}, {"aborted", chat.StatusFailed, &chat.RunError{Code: "aborted", Message: "upstream stopped"}}, {"cancelled", chat.StatusCancelled, nil}} {
		t.Run(tc.reason, func(t *testing.T) {
			mock, runID, server := newSSEFixture(t)
			response, cancel, err := openSSE(t, server, runID, "")
			if err != nil {
				t.Fatal(err)
			}
			defer cancel()
			defer response.Body.Close()
			reader := bufio.NewReader(response.Body)
			if index == 1 {
				failure := &chat.RunError{Code: "resource", Message: "busy"}
				publishSSE(t, mock, runID, chat.EventText, fmt.Sprintf(`{"content":"partial","error":%s}`, mustJSONText(failure)))
				var text chat.TextData
				assertSSEJSON(t, readSSEFrameWithin(t, reader, sseWaitLimit), "1", "text", &text)
				if text.Error == nil || text.Error.Code != "resource" {
					t.Fatalf("incremental error=%+v", text.Error)
				}
			}
			var output *string
			if tc.status == chat.StatusSucceeded {
				value := "done"
				output = &value
			}
			endData, _ := json.Marshal(chat.EndData{StopReason: tc.reason, Error: tc.runErr})
			if err := mock.ConfirmRun(runID, tc.status, output, tc.runErr, endData); err != nil {
				t.Fatal(err)
			}
			var end chat.EndData
			wantID := "1"
			if index == 1 {
				wantID = "2"
			}
			assertSSEJSON(t, readSSEFrameWithin(t, reader, sseWaitLimit), wantID, "end", &end)
			if end.StopReason != tc.reason || end.Error == nil && tc.runErr != nil || end.Error != nil && tc.runErr == nil {
				t.Fatalf("end=%+v", end)
			}
			if end.Error != nil && (end.Error.Code != tc.runErr.Code || end.Error.Message != tc.runErr.Message) {
				t.Fatalf("end error=%+v want=%+v", end.Error, tc.runErr)
			}
		})
	}
}

// TestSSEThinkingErrorPrecedesMatchingEnd_SSE06 验证 thinking 错误先于匹配的 end 事件发送。
func TestSSEThinkingErrorPrecedesMatchingEnd_SSE06(t *testing.T) {
	mock, runID, server := newSSEFixture(t)
	response, cancel, err := openSSE(t, server, runID, "")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	problem := &chat.RunError{Code: "thinking_failed", Message: "thinking stopped"}
	publishSSE(t, mock, runID, chat.EventThinking, fmt.Sprintf(`{"content":"partial thought","error":%s}`, mustJSONText(problem)))
	var thinking chat.ThinkingData
	assertSSEJSON(t, readSSEFrameWithin(t, reader, sseWaitLimit), "1", "thinking", &thinking)
	if thinking.Error == nil || *thinking.Error != *problem {
		t.Fatalf("thinking error=%+v want=%+v", thinking.Error, problem)
	}
	endData, _ := json.Marshal(chat.EndData{StopReason: "aborted", Error: problem})
	if err := mock.ConfirmRun(runID, chat.StatusFailed, nil, problem, endData); err != nil {
		t.Fatal(err)
	}
	var end chat.EndData
	assertSSEJSON(t, readSSEFrameWithin(t, reader, sseWaitLimit), "2", "end", &end)
	if end.StopReason != "aborted" || end.Error == nil || *end.Error != *problem {
		t.Fatalf("end=%+v want error=%+v", end, problem)
	}
}

func mustJSONText(value any) string { data, _ := json.Marshal(value); return string(data) }

// TestSSEReplayCursorConnectionAndDisconnectLifecycle_SSE07_SSE08_SSE09_SSE10_SSE11_SSE12_SSE13_SSE15_SSE16 验证回放游标、连接限制和断线恢复。
func TestSSEReplayCursorConnectionAndDisconnectLifecycle_SSE07_SSE08_SSE09_SSE10_SSE11_SSE12_SSE13_SSE15_SSE16(t *testing.T) {
	mock, runID, server := newSSEFixture(t)
	publishSSE(t, mock, runID, chat.EventStart, `{}`)
	publishSSE(t, mock, runID, chat.EventText, `{"content":"history","error":null}`)
	// 无游标连接先完整补发历史；回放尚未读完时产生的新事件必须排在历史后面。
	response, cancel, err := openSSE(t, server, runID, "")
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 {
		t.Fatalf("no-cursor replay status=%d", response.StatusCode)
	}
	if _, err := mock.PublishEvent(runID, chat.EventText, json.RawMessage(`{"content":"live-during-replay","error":null}`)); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(response.Body)
	for i, want := range []string{"start", "text", "text"} {
		frame := readSSEFrameWithin(t, reader, sseWaitLimit)
		if frame.id != fmt.Sprint(i+1) || frame.event != want {
			t.Fatalf("no-cursor replay frame %d=%+v", i+1, frame)
		}
	}
	closeAndWaitForSSERelease(t, mock, runID, 3, response, cancel)
	// Last-Event-ID 为 0 时也应从首条历史事件开始回放。
	response, cancel, err = openSSE(t, server, runID, "0")
	if err != nil {
		t.Fatal(err)
	}
	reader = bufio.NewReader(response.Body)
	for i := 1; i <= 3; i++ {
		frame := readSSEFrameWithin(t, reader, sseWaitLimit)
		if frame.id != fmt.Sprint(i) {
			t.Fatalf("zero-cursor replay ID=%q want=%d", frame.id, i)
		}
	}
	closeAndWaitForSSERelease(t, mock, runID, 3, response, cancel)
	// 已处理 ID 1 的重连只回放后续 ID。
	response, cancel, err = openSSE(t, server, runID, "1")
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 {
		t.Fatalf("replay status=%d body=%s", response.StatusCode, readBody(response.Body))
	}
	reader = bufio.NewReader(response.Body)
	frame := readSSEFrameWithin(t, reader, sseWaitLimit)
	if frame.id != "2" || frame.event != "text" {
		t.Fatalf("cursor replay frame=%+v", frame)
	}
	frame = readSSEFrameWithin(t, reader, sseWaitLimit)
	if frame.id != "3" {
		t.Fatalf("cursor replay omitted ID 3: %+v", frame)
	}
	second, secondCancel, err := openSSE(t, server, runID, "")
	if err != nil {
		t.Fatal(err)
	}
	defer secondCancel()
	defer second.Body.Close()
	if second.StatusCode != 409 {
		t.Fatalf("second active connection status=%d", second.StatusCode)
	}
	closeAndWaitForSSERelease(t, mock, runID, 3, response, cancel)
	// 游标命中当前最大事件 ID 时连接等待新事件，后续事件沿用原序列。
	responseLive, cancelLive, err := openSSE(t, server, runID, "3")
	if err != nil {
		t.Fatal(err)
	}
	if responseLive.StatusCode != 200 {
		t.Fatalf("maximum cursor status=%d", responseLive.StatusCode)
	}
	readerLive := bufio.NewReader(responseLive.Body)
	if _, err := mock.PublishEvent(runID, chat.EventText, json.RawMessage(`{"content":"live","error":null}`)); err != nil {
		t.Fatal(err)
	}
	var live chat.TextData
	assertSSEJSON(t, readSSEFrameWithin(t, readerLive, sseWaitLimit), "4", "text", &live)
	if live.Content != "live" {
		t.Fatalf("live content=%q", live.Content)
	}
	closeAndWaitForSSERelease(t, mock, runID, 4, responseLive, cancelLive)
	// 无效游标必须在建立流式响应前被拒绝。
	for _, cursor := range []string{"wat", "-1", "99"} {
		resp, stop, err := openSSE(t, server, runID, cursor)
		if err != nil {
			t.Fatal(err)
		}
		defer stop()
		if resp.StatusCode != 400 {
			t.Fatalf("cursor %q status=%d", cursor, resp.StatusCode)
		}
		var errorBody struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&errorBody); err != nil || errorBody.Error.Code != "invalid_request" {
			t.Fatalf("cursor %q error body=%+v err=%v", cursor, errorBody, err)
		}
		_ = resp.Body.Close()
	}
	resp, stop, err := openSSE(t, server, runID, "4")
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if resp.StatusCode != 200 {
		t.Fatalf("reconnect status=%d", resp.StatusCode)
	}
	_ = resp.Body.Close()
	if err := mock.ConfirmRun(runID, chat.StatusSucceeded, nil, nil, json.RawMessage(`{"stop_reason":"stop","error":null}`)); err != nil {
		t.Fatal(err)
	}
	resp, stop, err = openSSE(t, server, runID, "")
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if resp.StatusCode != 204 {
		t.Fatalf("terminal SSE status=%d", resp.StatusCode)
	}
	if resp.ContentLength > 0 {
		t.Fatalf("terminal SSE 204 has body length %d", resp.ContentLength)
	}
	_ = resp.Body.Close()
	resp, stop, err = openSSE(t, server, "00000000-0000-4000-8000-000000000000", "")
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if resp.StatusCode != 404 {
		t.Fatalf("missing terminal run status=%d", resp.StatusCode)
	}
	_ = resp.Body.Close()
}

// TestSSEDeleteHidesNewRequestsButExistingConnectionGetsRealEnd_SSE14_DELETE02_DELETE05_DELETE06 验证删除隐藏查询并保留现有 SSE 收尾。
func TestSSEDeleteHidesNewRequestsButExistingConnectionGetsRealEnd_SSE14_DELETE02_DELETE05_DELETE06(t *testing.T) {
	mock, runID, server := newSSEFixture(t)
	response, cancel, err := openSSE(t, server, runID, "")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	publishSSE(t, mock, runID, chat.EventStart, `{}`)
	first := readSSEFrameWithin(t, reader, sseWaitLimit)
	if first.id != "1" || first.event != "start" {
		t.Fatalf("first=%+v", first)
	}
	deleteResponse, err := http.NewRequest(http.MethodDelete, server.URL+"/v1/session/"+testSessionID, nil)
	if err != nil {
		t.Fatal(err)
	}
	deleteResponse.Host = "127.0.0.1:10086"
	deleted, err := http.DefaultClient.Do(deleteResponse)
	if err != nil {
		t.Fatal(err)
	}
	_ = deleted.Body.Close()
	if deleted.StatusCode != 200 {
		t.Fatalf("DELETE waited/failed: %d", deleted.StatusCode)
	}
	missing, stop, err := openSSE(t, server, runID, "")
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if missing.StatusCode != 404 {
		t.Fatalf("new SSE after parent delete status=%d", missing.StatusCode)
	}
	_ = missing.Body.Close()
	getReq, _ := http.NewRequest(http.MethodGet, server.URL+"/v1/run/"+runID, nil)
	getReq.Host = "127.0.0.1:10086"
	hidden, err := http.DefaultClient.Do(getReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = hidden.Body.Close()
	if hidden.StatusCode != 404 {
		t.Fatalf("GET after parent delete status=%d", hidden.StatusCode)
	}
	if err := mock.ConfirmRun(runID, chat.StatusCancelled, nil, nil, json.RawMessage(`{"stop_reason":"cancelled","error":null}`)); err != nil {
		t.Fatal(err)
	}
	var end chat.EndData
	assertSSEJSON(t, readSSEFrameWithin(t, reader, sseWaitLimit), "2", "end", &end)
	if end.StopReason != "cancelled" {
		t.Fatalf("end=%+v", end)
	}
}

func readBody(body io.Reader) string {
	data, _ := io.ReadAll(body)
	return string(bytes.TrimSpace(data))
}

func waitForHTTPLogRecords(t *testing.T, readLogs func() []byte, count int) []byte {
	t.Helper()
	deadline := time.Now().Add(sseWaitLimit)
	for time.Now().Before(deadline) {
		logs := readLogs()
		lines := bytes.Split(bytes.TrimSpace(logs), []byte("\n"))
		if len(lines) >= count && len(lines[0]) > 0 {
			return logs
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d HTTP log records: %s", count, readLogs())
	return nil
}

type cancelDuringSSEWrite struct {
	cancel context.CancelFunc
	err    error
}

// Write 在模拟写入中先触发请求取消，再返回预设错误。
func (w cancelDuringSSEWrite) Write([]byte) (int, error) {
	w.cancel()
	return 0, w.err
}

// TestSSEIOErrorsAfterRequestCancellationAreClean 验证取消与写入或刷新竞争时正常结束流。
func TestSSEIOErrorsAfterRequestCancellationAreClean(t *testing.T) {
	for _, phase := range []string{"write", "flush"} {
		t.Run(phase, func(t *testing.T) {
			events := make(chan chat.Event, 1)
			events <- chat.Event{ID: 1, Type: chat.EventStart, Data: json.RawMessage(`{}`)}
			closed := false
			stub := &chatServiceStub{subscribe: func(context.Context, string, int64) (*chat.Subscription, error) {
				return &chat.Subscription{Events: events, Close: func() { closed = true }}, nil
			}}
			ginContext, _ := gin.CreateTestContext(httptest.NewRecorder())
			ginContext.Request = httptest.NewRequest(http.MethodGet, "/v1/run/"+testRunID+"/event", nil)
			ginContext.Params = gin.Params{{Key: "run_id", Value: testRunID}}
			result := handler.RunEventsHandler(stub)(ginContext)
			if result.Stream == nil {
				t.Fatal("expected event stream")
			}
			defer result.Stream.Close()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var err error
			if phase == "write" {
				err = result.Stream.Write(ctx, cancelDuringSSEWrite{cancel: cancel, err: io.ErrClosedPipe}, func() error { return nil })
			} else {
				err = result.Stream.Write(ctx, io.Discard, func() error {
					cancel()
					return context.Canceled
				})
			}
			if err != nil {
				t.Fatalf("canceled %s returned error: %v", phase, err)
			}
			result.Stream.Close()
			if !closed {
				t.Fatal("subscription was not closed")
			}
		})
	}
}

// TestSSEClientDisconnectIsCleanAndRunCanReconnect 验证主动断开只释放订阅、不取消 run，且可用事件游标重连。
func TestSSEClientDisconnectIsCleanAndRunCanReconnect(t *testing.T) {
	readLogs := captureHTTPLogs(t)
	mock, runID, server := newSSEFixture(t)
	response, cancel, err := openSSE(t, server, runID, "")
	if err != nil {
		t.Fatal(err)
	}
	publishSSE(t, mock, runID, chat.EventStart, `{}`)
	first := readSSEFrameWithin(t, bufio.NewReader(response.Body), sseWaitLimit)
	if first.id != "1" || first.event != "start" {
		t.Fatalf("initial event=%+v", first)
	}
	closeAndWaitForSSERelease(t, mock, runID, 1, response, cancel)

	run, err := mock.GetRun(context.Background(), runID)
	if err != nil || run.Status != chat.StatusRunning {
		t.Fatalf("run after client disconnect = %q, err=%v", run.Status, err)
	}
	response, cancel, err = openSSE(t, server, runID, "1")
	if err != nil {
		t.Fatal(err)
	}
	publishSSE(t, mock, runID, chat.EventText, `{"content":"resumed","error":null}`)
	resumed := readSSEFrameWithin(t, bufio.NewReader(response.Body), sseWaitLimit)
	if resumed.id != "2" || resumed.event != "text" {
		t.Fatalf("resumed event=%+v", resumed)
	}
	closeAndWaitForSSERelease(t, mock, runID, 2, response, cancel)

	logs := waitForHTTPLogRecords(t, readLogs, 2)
	for _, line := range bytes.Split(bytes.TrimSpace(logs), []byte("\n")) {
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatalf("decode HTTP log: %v", err)
		}
		if record["level"] != "INFO" || record["msg"] != "[http] run events streamed" {
			t.Fatalf("client disconnect logged as failure: %s", line)
		}
		if _, exists := record["error"]; exists {
			t.Fatalf("normal disconnect has error field: %s", line)
		}
	}
}

// TestSSEUnexpectedEventSourceCloseRemainsError 验证未取消请求时事件源提前关闭仍记录流错误。
func TestSSEUnexpectedEventSourceCloseRemainsError(t *testing.T) {
	readLogs := captureHTTPLogs(t)
	events := make(chan chat.Event)
	close(events)
	stub := &chatServiceStub{subscribe: func(context.Context, string, int64) (*chat.Subscription, error) {
		return &chat.Subscription{Events: events, Close: func() {}}, nil
	}}
	server := httptest.NewServer(newChatHTTPTestRouter(stub))
	t.Cleanup(server.Close)
	response, cancel, err := openSSE(t, server, testRunID, "")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()

	logs := waitForHTTPLogRecords(t, readLogs, 1)
	lines := bytes.Split(bytes.TrimSpace(logs), []byte("\n"))
	var record map[string]any
	if err := json.Unmarshal(lines[0], &record); err != nil {
		t.Fatalf("decode HTTP log: %v", err)
	}
	if record["level"] != "ERROR" || record["msg"] != "[http] failed to stream run events" {
		t.Fatalf("unexpected source close log = %s", lines[0])
	}
	if record["error"] == nil {
		t.Fatalf("unexpected source close omitted error: %s", lines[0])
	}
}
