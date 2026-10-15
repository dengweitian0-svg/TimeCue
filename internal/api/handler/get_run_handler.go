package handler

import (
	"net/http"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/middleware"
	"github.com/gin-gonic/gin"
)

// RunResponse 是查询 run 的稳定九字段视图。
type RunResponse struct {
	RunID      string         `json:"run_id"`
	SessionID  string         `json:"session_id"`
	Model      string         `json:"model"`
	Input      string         `json:"input"`
	Status     chat.Status    `json:"status"`
	Output     *string        `json:"output"`
	Error      *chat.RunError `json:"error"`
	CreatedAt  string         `json:"created_at"`
	FinishedAt *string        `json:"finished_at"`
}

// GetRunHandler 返回应用层提供的 run 公开视图。
func GetRunHandler(service chat.Service) middleware.HTTPHandler {
	return func(c *gin.Context) middleware.HTTPResult {
		runID, failure := parseChatRunID(c)
		if failure != nil {
			return *failure
		}
		run, err := service.GetRun(c.Request.Context(), runID)
		if err != nil {
			return chatError(err)
		}
		if err := validateChatRun(run); err != nil {
			return internalErrorResult(err)
		}
		response := RunResponse{
			RunID: run.RunID, SessionID: run.SessionID, Model: run.Model, Input: run.Input,
			Status: run.Status, CreatedAt: chatTime(run.CreatedAt),
		}
		switch run.Status {
		case chat.StatusRunning:
			// running 状态不暴露执行中的累计内容。
		case chat.StatusSucceeded, chat.StatusCancelled:
			response.Output = run.Output
		case chat.StatusFailed:
			response.Error = run.Error
		}
		if run.Status != chat.StatusRunning && run.FinishedAt != nil {
			finished := chatTime(*run.FinishedAt)
			response.FinishedAt = &finished
		}
		return middleware.JSONResult(http.StatusOK, response)
	}
}
