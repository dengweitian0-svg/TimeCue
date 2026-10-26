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
	Provider  *string `json:"provider"`
}

// CreateRunResponse 是创建 run 的直接响应 DTO。
type CreateRunResponse = chat.Receipt

// CreateRunHandler 校验新一轮输入并请求应用层创建 run。
func CreateRunHandler(service chat.RunService) middleware.HTTPHandler {
	return func(c *gin.Context) middleware.HTTPResult {
		var request createRunRequest
		if failure := utils.ReadJSON(c, &request, "session_id", "input", "provider", "model"); failure != nil {
			return *failure
		}
		if request.SessionID == nil || request.Input == nil || request.Model == nil || request.Provider == nil || strings.TrimSpace(*request.Provider) == "" ||
			strings.TrimSpace(*request.SessionID) == "" || strings.TrimSpace(*request.Input) == "" || strings.TrimSpace(*request.Model) == "" {
			return utils.ChatInvalidRequest("session_id, input, provider and model are required non-empty strings")
		}
		sessionID, ok := utils.ParseChatUUID(*request.SessionID)
		if !ok {
			return utils.ChatInvalidRequest("session_id must be a canonical UUID v4")
		}
		key := c.GetHeader("Idempotency-Key")
		if !validIdempotencyKey(key) {
			return utils.ChatInvalidRequest("Idempotency-Key must use create_session:<uuid>")
		}
		receipt, err := service.CreateRun(c.Request.Context(), chat.CreateRunInput{
			SessionID: sessionID, Input: *request.Input, Provider: *request.Provider, Model: *request.Model, IdempotencyKey: key,
		})
		if err != nil {
			return utils.ChatError(err)
		}
		if _, valid := utils.ParseChatUUID(receipt.RunID); !valid {
			return utils.InternalErrorResult(errInvalidRunID)
		}
		status := http.StatusOK
		if receipt.Status == chat.StatusRunning {
			status = http.StatusAccepted
		}
		return middleware.JSONResult(status, receipt)
	}
}
