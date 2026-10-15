package handler

import (
	"net/http"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/middleware"
	"github.com/gin-gonic/gin"
)

// SessionResponse 返回 session 及按创建时间排序的 run ID 列表。
type SessionResponse struct {
	SessionID string   `json:"session_id"`
	RunIDs    []string `json:"run_ids"`
}

// GetSessionHandler 返回 session 的 run 列表。
func GetSessionHandler(service chat.Service) middleware.HTTPHandler {
	return func(c *gin.Context) middleware.HTTPResult {
		sessionID, failure := parseChatSessionID(c)
		if failure != nil {
			return *failure
		}
		runIDs, err := service.GetSession(c.Request.Context(), sessionID)
		if err != nil {
			return chatError(err)
		}
		if runIDs == nil {
			runIDs = []string{}
		}
		return middleware.JSONResult(http.StatusOK, SessionResponse{SessionID: sessionID, RunIDs: runIDs})
	}
}
