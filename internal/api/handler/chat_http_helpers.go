package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/middleware"
	"github.com/dengweitian0-svg/TimeCue/internal/model"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// 正式接入数据库后需要从 .env 中读取
const chatModel = "deepseek-flash"

var errInvalidRunID = errors.New("chat service returned invalid run id")

func chatInvalidRequest(message string) middleware.HTTPResult {
	return middleware.ErrorResult(http.StatusBadRequest, newAPIError("invalid_request", message), nil)
}

func parseChatUUID(raw string) (string, bool) {
	id, err := uuid.Parse(raw)
	if err != nil || id.Version() != 4 || id.Variant() != uuid.RFC4122 || !strings.EqualFold(id.String(), raw) {
		return "", false
	}
	return raw, true
}

func parseChatRunID(c *gin.Context) (string, *middleware.HTTPResult) {
	id, ok := parseChatUUID(c.Param("run_id"))
	if !ok {
		failure := chatInvalidRequest("run_id must be a canonical UUID v4")
		return "", &failure
	}
	return id, nil
}

func parseChatSessionID(c *gin.Context) (string, *middleware.HTTPResult) {
	id, ok := parseChatUUID(c.Param("session_id"))
	if !ok {
		failure := chatInvalidRequest("session_id must be a canonical UUID v4")
		return "", &failure
	}
	return id, nil
}

func validIdempotencyKey(value string) bool {
	const prefix = "create_session:"
	if !strings.HasPrefix(value, prefix) {
		return false
	}
	id, err := uuid.Parse(strings.TrimPrefix(value, prefix))
	return err == nil && strings.EqualFold(id.String(), strings.TrimPrefix(value, prefix))
}

func chatError(err error) middleware.HTTPResult {
	switch {
	case errors.Is(err, chat.ErrInvalidRequest):
		return chatInvalidRequest("invalid chat request")
	case errors.Is(err, chat.ErrRunNotFound):
		return middleware.ErrorResult(http.StatusNotFound, newAPIError("run_not_found", "run not found"), nil)
	case errors.Is(err, chat.ErrSessionNotFound):
		return middleware.ErrorResult(http.StatusBadRequest, newAPIError("session_not_found", "session not found"), nil)
	case errors.Is(err, chat.ErrSessionDeleted):
		return middleware.ErrorResult(http.StatusBadRequest, newAPIError("session_deleted", "session has been deleted"), nil)
	case errors.Is(err, chat.ErrSessionBusy):
		return middleware.ErrorResult(http.StatusConflict, newAPIError("session_busy", "session already has a running run"), nil)
	case errors.Is(err, chat.ErrSessionConcurrencyLimit):
		return middleware.ErrorResult(http.StatusConflict, newAPIError("session_concurrency_limit", "session concurrency limit reached"), nil)
	case errors.Is(err, chat.ErrIdempotencyConflict):
		return middleware.ErrorResult(http.StatusConflict, newAPIError("idempotency_key_conflict", "idempotency key was used with different input"), nil)
	case errors.Is(err, chat.ErrRunTerminal):
		return middleware.ErrorResult(http.StatusConflict, newAPIError("run_terminal", "run is already terminal"), nil)
	case errors.Is(err, chat.ErrSSEConnectionExists):
		return middleware.ErrorResult(http.StatusConflict, newAPIError("sse_connection_exists", "run already has an SSE connection"), nil)
	case errors.Is(err, chat.ErrEventBufferFull):
		return middleware.ErrorResult(http.StatusServiceUnavailable, newAPIError("event_buffer_full", "event subscriber buffer is full"), nil)
	default:
		return internalErrorResult(err)
	}
}

func validateChatRun(run chat.Run) error {
	switch run.Status {
	case chat.StatusRunning:
	case chat.StatusSucceeded, chat.StatusFailed, chat.StatusCancelled:
		if run.FinishedAt == nil {
			return fmt.Errorf("chat service returned terminal run without finish time")
		}
	default:
		return fmt.Errorf("chat service returned unknown run status")
	}
	return nil
}

func chatTime(value time.Time) string { return value.In(model.BeijingLocation()).Format(time.RFC3339) }
