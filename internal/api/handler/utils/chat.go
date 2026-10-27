package utils

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/middleware"
	"github.com/dengweitian0-svg/TimeCue/internal/model"
	"github.com/google/uuid"
)

// ChatInvalidRequest 返回聊天接口的输入错误。
func ChatInvalidRequest(message string) middleware.HTTPResult {
	return middleware.ErrorResult(http.StatusBadRequest, NewAPIError("invalid_request", message), nil)
}

// ParseChatUUID 校验规范格式的 UUID v4。
func ParseChatUUID(raw string) (string, bool) {
	id, err := uuid.Parse(raw)
	if err != nil || id.Version() != 4 || id.Variant() != uuid.RFC4122 || !strings.EqualFold(id.String(), raw) {
		return "", false
	}
	return raw, true
}

// ChatError 将聊天服务错误映射为稳定 HTTP 响应。
func ChatError(err error) middleware.HTTPResult {
	switch {
	case errors.Is(err, chat.ErrInvalidRequest):
		return ChatInvalidRequest("invalid chat request")
	case errors.Is(err, chat.ErrRunNotFound):
		return middleware.ErrorResult(http.StatusNotFound, NewAPIError("run_not_found", "run not found"), nil)
	case errors.Is(err, chat.ErrSessionNotFound):
		return middleware.ErrorResult(http.StatusBadRequest, NewAPIError("session_not_found", "session not found"), nil)
	case errors.Is(err, chat.ErrSessionDeleted):
		return middleware.ErrorResult(http.StatusBadRequest, NewAPIError("session_deleted", "session has been deleted"), nil)
	case errors.Is(err, chat.ErrSessionBusy):
		return middleware.ErrorResult(http.StatusConflict, NewAPIError("session_busy", "session already has a running run"), nil)
	case errors.Is(err, chat.ErrSessionConcurrencyLimit):
		return middleware.ErrorResult(http.StatusConflict, NewAPIError("session_concurrency_limit", "session concurrency limit reached"), nil)
	case errors.Is(err, chat.ErrIdempotencyConflict):
		return middleware.ErrorResult(http.StatusConflict, NewAPIError("idempotency_key_conflict", "idempotency key was used with different input"), nil)
	case errors.Is(err, chat.ErrRunTerminal):
		return middleware.ErrorResult(http.StatusConflict, NewAPIError("run_terminal", "run is already terminal"), nil)
	case errors.Is(err, chat.ErrModelUnavailable):
		return middleware.ErrorResult(http.StatusServiceUnavailable, NewAPIError("model_config_unavailable", "selected model configuration or key unavailable"), nil)
	case errors.Is(err, chat.ErrStopping):
		return middleware.ErrorResult(http.StatusServiceUnavailable, NewAPIError("service_stopping", "chat service stopping"), nil)
	default:
		return InternalErrorResult(err)
	}
}

// ChatTime 按固定的北京时间格式化聊天时间。
func ChatTime(value time.Time) string {
	return value.In(model.BeijingLocation()).Format(time.RFC3339)
}

// ValidateChatRun 检查聊天服务返回的 run 状态和终结时间。
func ValidateChatRun(run chat.Run) error {
	switch run.Status {
	case chat.StatusRunning:
	case chat.StatusCancelled:
		// 取消投影允许尚未完成收尾，是否结束由 finished_at 或 end 表达。
	case chat.StatusSucceeded, chat.StatusFailed:
		if run.FinishedAt == nil {
			return fmt.Errorf("chat service returned terminal run without finish time")
		}
	default:
		return fmt.Errorf("chat service returned unknown run status")
	}
	return nil
}
