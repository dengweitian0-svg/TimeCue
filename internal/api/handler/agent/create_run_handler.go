package agent

import (
	"github.com/dengweitian0-svg/TimeCue/internal/api/handler/utils"
	"net/http"
	"strings"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/middleware"
	"github.com/gin-gonic/gin"
)

type createRunRequest struct {
	SessionID *string `json:"session_id"`
	Input     *string `json:"input"`
	Model     *string `json:"model"`
}

// CreateRunResponse 是创建 run 的直接响应 DTO。
type CreateRunResponse struct {
	RunID string `json:"run_id"`
}

// CreateRunHandler 校验新一轮输入并请求应用层创建 run。
func CreateRunHandler(service chat.Service) middleware.HTTPHandler {
	return func(c *gin.Context) middleware.HTTPResult {
		var request createRunRequest
		if failure := utils.ReadJSON(c, &request, "session_id", "input", "model"); failure != nil {
			return *failure
		}
		if request.SessionID == nil || request.Input == nil || request.Model == nil ||
			strings.TrimSpace(*request.SessionID) == "" || strings.TrimSpace(*request.Input) == "" || strings.TrimSpace(*request.Model) == "" {
			return utils.ChatInvalidRequest("session_id, input and model are required non-empty strings")
		}
		sessionID, ok := utils.ParseChatUUID(*request.SessionID)
		if !ok {
			return utils.ChatInvalidRequest("session_id must be a canonical UUID v4")
		}
		if *request.Model != chatModel {
			return utils.ChatInvalidRequest("model must be deepseek-flash")
		}
		key := c.GetHeader("Idempotency-Key")
		if !validIdempotencyKey(key) {
			return utils.ChatInvalidRequest("Idempotency-Key must use create_session:<uuid>")
		}
		runID, err := service.CreateRun(c.Request.Context(), chat.CreateRunInput{
			SessionID: sessionID, Input: *request.Input, Model: *request.Model, IdempotencyKey: key,
		})
		if err != nil {
			return utils.ChatError(err)
		}
		if _, valid := utils.ParseChatUUID(runID); !valid {
			return utils.InternalErrorResult(errInvalidRunID)
		}
		return middleware.JSONResult(http.StatusAccepted, CreateRunResponse{RunID: runID})
	}
}
