package agent

import (
	"github.com/dengweitian0-svg/TimeCue/internal/api/handler/utils"
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
		runID, failure := parseRunID(c)
		if failure != nil {
			return *failure
		}
		run, err := service.GetRun(c.Request.Context(), runID)
		if err != nil {
			return utils.ChatError(err)
		}
		if err := utils.ValidateChatRun(run); err != nil {
			return utils.InternalErrorResult(err)
		}
		response := RunResponse{
			RunID: run.RunID, SessionID: run.SessionID, Model: run.Model, Input: run.Input,
			Status: run.Status, CreatedAt: utils.ChatTime(run.CreatedAt),
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
			finished := utils.ChatTime(*run.FinishedAt)
			response.FinishedAt = &finished
		}
		return middleware.JSONResult(http.StatusOK, response)
	}
}
