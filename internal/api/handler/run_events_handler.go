package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/middleware"
	"github.com/gin-gonic/gin"
)

// RunEventsHandler 建立带回放游标的 SSE 订阅。
func RunEventsHandler(service chat.Service) middleware.HTTPHandler {
	return func(c *gin.Context) middleware.HTTPResult {
		runID, failure := parseChatRunID(c)
		if failure != nil {
			return *failure
		}
		cursor, result := parseLastEventID(c.Request)
		if result != nil {
			return *result
		}
		subscription, err := service.Subscribe(c.Request.Context(), runID, cursor)
		if err != nil {
			if errors.Is(err, chat.ErrRunTerminal) {
				return middleware.NoContentResult()
			}
			return chatError(err)
		}
		if subscription == nil || subscription.Events == nil || subscription.Close == nil {
			if subscription != nil && subscription.Close != nil {
				subscription.Close()
			}
			return internalErrorResult(fmt.Errorf("chat service returned invalid event subscription"))
		}
		stream := &chatEventStream{subscription: subscription}
		return middleware.HTTPResult{
			Status: http.StatusOK,
			Headers: http.Header{
				"Cache-Control":     []string{"no-cache"},
				"X-Accel-Buffering": []string{"no"},
			},
			Stream: stream,
		}
	}
}

func parseLastEventID(request *http.Request) (int64, *middleware.HTTPResult) {
	values, present := request.Header[http.CanonicalHeaderKey("Last-Event-ID")]
	if !present {
		return 0, nil
	}
	if len(values) != 1 || values[0] == "" {
		failure := chatInvalidRequest("Last-Event-ID must be a non-negative decimal integer")
		return 0, &failure
	}
	for _, ch := range values[0] {
		if ch < '0' || ch > '9' {
			failure := chatInvalidRequest("Last-Event-ID must be a non-negative decimal integer")
			return 0, &failure
		}
	}
	cursor, err := strconv.ParseInt(values[0], 10, 64)
	if err != nil {
		failure := chatInvalidRequest(err.Error())
		return 0, &failure
	}
	return cursor, nil
}

type chatEventStream struct {
	subscription *chat.Subscription
	once         sync.Once
}

// Write 按订阅顺序编码并发送聊天事件，每条事件发送后刷新响应。
func (s *chatEventStream) Write(ctx context.Context, writer io.Writer, flush func() error) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-s.subscription.Events:
			if !ok {
				return fmt.Errorf("chat event subscription closed before end event")
			}
			frame, err := middleware.EncodeSSE(event.ID, string(event.Type), event.Data)
			if err != nil {
				return fmt.Errorf("encode chat event: %w", err)
			}
			written, err := writer.Write(frame)
			if err == nil && written != len(frame) {
				err = io.ErrShortWrite
			}
			if err != nil {
				return err
			}
			if err := flush(); err != nil {
				return err
			}
			if event.Type == chat.EventEnd {
				return nil
			}
		}
	}
}

// Close 只关闭一次下层事件订阅。
func (s *chatEventStream) Close() {
	s.once.Do(s.subscription.Close)
}
