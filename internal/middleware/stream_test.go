package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

type testStream struct {
	write  func(context.Context, io.Writer, func() error) error
	closed atomic.Bool
	done   chan struct{}
}

type responseControllerFailureWriter struct {
	recorder    *httptest.ResponseRecorder
	deadlineErr error
	flushErr    error
	failOnFlush int
	flushes     int
}

// Header 返回测试响应记录器的响应头。
func (w *responseControllerFailureWriter) Header() http.Header { return w.recorder.Header() }

// Write 将响应数据写入测试响应记录器。
func (w *responseControllerFailureWriter) Write(body []byte) (int, error) {
	return w.recorder.Write(body)
}

// WriteHeader 将状态码写入测试响应记录器。
func (w *responseControllerFailureWriter) WriteHeader(status int) {
	w.recorder.WriteHeader(status)
}

// SetWriteDeadline 返回测试指定的写入期限设置错误。
func (w *responseControllerFailureWriter) SetWriteDeadline(time.Time) error { return w.deadlineErr }

// FlushError 按测试设置刷新响应，或返回指定的刷新错误。
func (w *responseControllerFailureWriter) FlushError() error {
	w.flushes++
	if w.flushErr != nil && (w.failOnFlush == 0 || w.flushes == w.failOnFlush) {
		return w.flushErr
	}
	w.recorder.Flush()
	return nil
}

// Write 将测试流内容写入给定响应 writer。
func (s *testStream) Write(ctx context.Context, w io.Writer, flush func() error) error {
	return s.write(ctx, w, flush)
}

// Close 标记测试流已关闭，并通知等待关闭的测试。
func (s *testStream) Close() {
	s.closed.Store(true)
	if s.done != nil {
		close(s.done)
	}
}

// TestEncodeSSE 验证事件帧格式、事件类型限制和 JSON 编码错误。
func TestEncodeSSE(t *testing.T) {
	data := json.RawMessage(`{"content":"中文\ndata"}`)
	frame, err := EncodeSSE(12, "text", data)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(frame), "id: 12\nevent: text\ndata: {\"content\":\"中文\\ndata\"}\n\n"; got != want {
		t.Fatalf("frame=%q want=%q", got, want)
	}
	for _, tc := range []struct {
		id    int64
		event string
		data  any
	}{
		{0, "text", nil}, {1, "bad\nevent", nil}, {1, "tool", make(chan int)},
	} {
		if _, err := EncodeSSE(tc.id, tc.event, tc.data); err == nil {
			t.Fatalf("EncodeSSE(%d, %q) unexpectedly succeeded", tc.id, tc.event)
		}
	}
}

// TestWriteHTTPStreamFlushesEachFrameAndCloses 验证流帧写入及资源关闭。
func TestWriteHTTPStreamFlushesEachFrameAndCloses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stream := &testStream{write: func(_ context.Context, w io.Writer, flush func() error) error {
		for _, frame := range []string{"id: 1\nevent: start\ndata: {}\n\n", "id: 2\nevent: end\ndata: {}\n\n"} {
			if _, err := io.WriteString(w, frame); err != nil {
				return err
			}
			if err := flush(); err != nil {
				return err
			}
		}
		return nil
	}}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/events", nil)
	WriteHTTPResult(ctx, HTTPResult{Status: http.StatusOK, Stream: stream}, nil)
	if !stream.closed.Load() || recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != "text/event-stream; charset=utf-8" {
		t.Fatalf("stream response not finalized: closed=%v code=%d headers=%v", stream.closed.Load(), recorder.Code, recorder.Header())
	}
	if got := recorder.Body.String(); !strings.Contains(got, "event: start") || !strings.Contains(got, "event: end") {
		t.Fatalf("stream frames missing: %q", got)
	}
}

// TestWriteHTTPStreamClosesOnAllEarlyFailures 验证流在提前失败时仍会关闭。
func TestWriteHTTPStreamClosesOnAllEarlyFailures(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Run("fields panic", func(t *testing.T) {
		stream := &testStream{write: func(context.Context, io.Writer, func() error) error { return nil }}
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodGet, "/events", nil)
		func() {
			defer func() { _ = recover() }()
			WriteHTTPResult(ctx, HTTPResult{Status: 200, Stream: stream}, func(*gin.Context, HTTPResult) []slog.Attr { panic("field extraction") })
		}()
		if !stream.closed.Load() {
			t.Fatal("stream was not closed after fields panic")
		}
	})
	t.Run("stream panic", func(t *testing.T) {
		stream := &testStream{write: func(context.Context, io.Writer, func() error) error { panic("stream panic") }}
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodGet, "/events", nil)
		func() {
			defer func() { _ = recover() }()
			WriteHTTPResult(ctx, HTTPResult{Status: 200, Stream: stream}, nil)
		}()
		if !stream.closed.Load() {
			t.Fatal("stream was not closed after Write panic")
		}
	})
	t.Run("encoding error", func(t *testing.T) {
		stream := &testStream{write: func(context.Context, io.Writer, func() error) error {
			_, err := EncodeSSE(1, "text", make(chan int))
			return err
		}}
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodGet, "/events", nil)
		WriteHTTPResult(ctx, HTTPResult{Status: 200, Stream: stream}, nil)
		if !stream.closed.Load() {
			t.Fatal("stream was not closed after event encoding error")
		}
	})
	t.Run("write error", func(t *testing.T) {
		writeErr := errors.New("stream write failed")
		stream := &testStream{write: func(context.Context, io.Writer, func() error) error { return writeErr }}
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodGet, "/events", nil)
		WriteHTTPResult(ctx, HTTPResult{Status: 200, Stream: stream}, nil)
		if !stream.closed.Load() || !errors.Is(getHTTPLogDetails(ctx).Error, writeErr) {
			t.Fatalf("write failure or close lost: closed=%v details=%+v", stream.closed.Load(), getHTTPLogDetails(ctx))
		}
		level, message := httpLogEvent(ctx, getHTTPLogDetails(ctx))
		if level != slog.LevelError || message != "HTTP request failed" {
			t.Fatalf("stream write failure logged at wrong level: %s %q", level, message)
		}
	})
	t.Run("context canceled", func(t *testing.T) {
		stream := &testStream{write: func(ctx context.Context, _ io.Writer, _ func() error) error { return ctx.Err() }}
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		request, cancel := context.WithCancel(context.Background())
		cancel()
		ctx.Request = httptest.NewRequest(http.MethodGet, "/events", nil).WithContext(request)
		WriteHTTPResult(ctx, HTTPResult{Status: 200, Stream: stream}, nil)
		if !stream.closed.Load() || !errors.Is(getHTTPLogDetails(ctx).Error, context.Canceled) {
			t.Fatalf("cancellation or close lost: closed=%v details=%+v", stream.closed.Load(), getHTTPLogDetails(ctx))
		}
	})
}

// TestHTTPResultLogLevelOverride 验证成功响应可覆盖默认日志级别。
func TestHTTPResultLogLevelOverride(t *testing.T) {
	level := slog.LevelWarn
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/run/abc/cancel", nil)
	ctx.Params = gin.Params{{Key: "run_id", Value: "abc"}}
	ctx.Set(httpLogDetailsKey, httpLogDetails{LogLevel: &level})
	got, message := httpLogEvent(ctx, getHTTPLogDetails(ctx))
	if got != slog.LevelWarn || message != "HTTP request completed" {
		t.Fatalf("override ignored: %s %q", got, message)
	}
}

// TestHTTPStreamClearsServerWriteTimeout 验证长流可在服务器写入超时后继续发送。
func TestHTTPStreamClearsServerWriteTimeout(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stream := &testStream{done: make(chan struct{}), write: func(_ context.Context, w io.Writer, flush func() error) error {
		time.Sleep(40 * time.Millisecond)
		if _, err := io.WriteString(w, "data: ready\n\n"); err != nil {
			return err
		}
		return flush()
	}}
	router := gin.New()
	router.GET("/events", WrapHTTPHandler(func(*gin.Context) HTTPResult {
		return HTTPResult{Status: 200, Stream: stream}
	}, nil))
	server := httptest.NewUnstartedServer(router)
	server.Config.WriteTimeout = 10 * time.Millisecond
	server.Start()
	defer server.Close()
	response, err := server.Client().Get(server.URL + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || !strings.Contains(string(body), "data: ready") {
		t.Fatalf("timed stream response failed: status=%d body=%q", response.StatusCode, body)
	}
	select {
	case <-stream.done:
	case <-time.After(time.Second):
		t.Fatal("stream was not closed after request completed")
	}
}

// TestHTTPStreamDeadlineSetupFailureReturnsJSON500 验证写入期限设置失败返回统一 JSON 错误。
func TestHTTPStreamDeadlineSetupFailureReturnsJSON500(t *testing.T) {
	gin.SetMode(gin.TestMode)
	deadlineErr := errors.New("cannot clear write deadline")
	stream := &testStream{write: func(context.Context, io.Writer, func() error) error {
		t.Fatal("stream should not start after deadline setup failure")
		return nil
	}}
	router := gin.New()
	var details httpLogDetails
	router.Use(func(c *gin.Context) {
		c.Next()
		details = getHTTPLogDetails(c)
	})
	router.GET("/events", WrapHTTPHandler(func(*gin.Context) HTTPResult {
		return HTTPResult{Status: http.StatusOK, Stream: stream}
	}, nil))
	w := &responseControllerFailureWriter{recorder: httptest.NewRecorder(), deadlineErr: deadlineErr}
	request := httptest.NewRequest(http.MethodGet, "/events", nil)
	router.ServeHTTP(w, request)
	if w.recorder.Code != http.StatusInternalServerError || w.recorder.Body.String() != `{"error":{"code":"internal_error","message":"internal server error"}}` {
		t.Fatalf("unexpected deadline setup response: status=%d body=%q", w.recorder.Code, w.recorder.Body.String())
	}
	if w.recorder.Header().Get("Content-Type") != "application/json; charset=utf-8" || !stream.closed.Load() || !errors.Is(details.Error, deadlineErr) {
		t.Fatalf("deadline failure facts lost: headers=%v closed=%v details=%+v", w.recorder.Header(), stream.closed.Load(), details)
	}
}

// TestHTTPStreamInitialFlushPrecedesStreamWrite 验证首次刷新发生在流写入之前。
func TestHTTPStreamInitialFlushPrecedesStreamWrite(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := &responseControllerFailureWriter{recorder: httptest.NewRecorder()}
	started := false
	stream := &testStream{write: func(context.Context, io.Writer, func() error) error {
		if w.flushes != 1 {
			t.Fatal("stream started before initial response flush")
		}
		started = true
		return nil
	}}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Next()
	})
	router.GET("/events", WrapHTTPHandler(func(*gin.Context) HTTPResult {
		return HTTPResult{Status: http.StatusOK, Stream: stream}
	}, nil))
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/events", nil))
	if w.recorder.Code != http.StatusOK || !started || !stream.closed.Load() || w.flushes != 1 {
		t.Fatalf("initial flush ordering failed: status=%d started=%v closed=%v flushes=%d", w.recorder.Code, started, stream.closed.Load(), w.flushes)
	}
}

// TestHTTPStreamInitialFlushFailureClosesWithoutStarting 验证首次刷新失败会关闭且不启动流。
func TestHTTPStreamInitialFlushFailureClosesWithoutStarting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	flushErr := errors.New("initial flush failed")
	w := &responseControllerFailureWriter{recorder: httptest.NewRecorder(), flushErr: flushErr, failOnFlush: 1}
	stream := &testStream{write: func(context.Context, io.Writer, func() error) error {
		t.Fatal("stream started after initial flush failure")
		return nil
	}}
	router := gin.New()
	var details httpLogDetails
	router.Use(func(c *gin.Context) {
		c.Next()
		details = getHTTPLogDetails(c)
	})
	router.GET("/events", WrapHTTPHandler(func(*gin.Context) HTTPResult {
		return HTTPResult{Status: http.StatusOK, Stream: stream}
	}, nil))
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/events", nil))
	if w.recorder.Code != http.StatusOK || w.flushes != 1 || !stream.closed.Load() || !errors.Is(details.Error, flushErr) {
		t.Fatalf("initial flush failure mishandled: status=%d flushes=%d closed=%v details=%+v", w.recorder.Code, w.flushes, stream.closed.Load(), details)
	}
}

// TestHTTPStreamEventFlushFailureIsReportedAndClosed 验证事件刷新失败会记录错误并关闭流。
func TestHTTPStreamEventFlushFailureIsReportedAndClosed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	flushErr := errors.New("event flush failed")
	w := &responseControllerFailureWriter{recorder: httptest.NewRecorder(), flushErr: flushErr, failOnFlush: 2}
	stream := &testStream{write: func(_ context.Context, writer io.Writer, flush func() error) error {
		if _, err := io.WriteString(writer, "data: event\n\n"); err != nil {
			return err
		}
		return flush()
	}}
	router := gin.New()
	var details httpLogDetails
	router.Use(func(c *gin.Context) {
		c.Next()
		details = getHTTPLogDetails(c)
	})
	router.GET("/events", WrapHTTPHandler(func(*gin.Context) HTTPResult {
		return HTTPResult{Status: http.StatusOK, Stream: stream}
	}, nil))
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/events", nil))
	if w.recorder.Code != http.StatusOK || w.flushes != 2 || !strings.Contains(w.recorder.Body.String(), "data: event") || !stream.closed.Load() || !errors.Is(details.Error, flushErr) {
		t.Fatalf("event flush failure mishandled: status=%d flushes=%d closed=%v details=%+v body=%q", w.recorder.Code, w.flushes, stream.closed.Load(), details, w.recorder.Body.String())
	}
}
