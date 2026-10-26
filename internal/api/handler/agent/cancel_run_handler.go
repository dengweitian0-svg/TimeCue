package agent

import (
	"fmt"
	"github.com/dengweitian0-svg/TimeCue/internal/api/handler/utils"
	"log/slog"
	"net/http"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/middleware"
	"github.com/gin-gonic/gin"
)

// CancelRunResponse 是取消请求的直接响应 DTO。
type CancelRunResponse struct {
	RunID  string      `json:"run_id"`
	Status chat.Status `json:"status"`
}

// CancelRunHandler 请求取消运行中的 run，不等待应用层确认终态。
func CancelRunHandler(service chat.RunService) middleware.HTTPHandler {
	return func(c *gin.Context) middleware.HTTPResult {
		runID, failure := parseRunID(c)
		if failure != nil {
			return *failure
		}
		status, err := service.CancelRun(c.Request.Context(), runID)
		if err != nil {
			return utils.ChatError(err)
		}
		response := CancelRunResponse{RunID: runID, Status: status}
		resultStatus := http.StatusOK
		if status == chat.StatusRunning {
			resultStatus = http.StatusAccepted
		} else if status != chat.StatusSucceeded && status != chat.StatusFailed && status != chat.StatusCancelled {
			return utils.InternalErrorResult(fmt.Errorf("chat service returned unknown run status"))
		}
		result := middleware.JSONResult(resultStatus, response)
		if status != chat.StatusRunning {
			level := slog.LevelWarn
			result.LogLevel = &level
		}
		return result
	}
}
