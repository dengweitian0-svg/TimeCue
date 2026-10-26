package session

import (
	"net/http"

	"github.com/dengweitian0-svg/TimeCue/internal/api/handler/utils"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/middleware"
	"github.com/gin-gonic/gin"
)

// SessionResponse 返回 session 名称及按创建时间排序的 run ID 列表。
type SessionResponse struct {
	SessionID   string   `json:"session_id"`
	SessionName string   `json:"session_name"`
	RunIDs      []string `json:"run_ids"`
}

// GetSessionHandler 返回 session 名称及其 run 列表。
func GetSessionHandler(service chat.SessionService) middleware.HTTPHandler {
	return func(c *gin.Context) middleware.HTTPResult {
		sessionID, failure := parseSessionID(c)
		if failure != nil {
			return *failure
		}
		session, err := service.GetSession(c.Request.Context(), sessionID)
		if err != nil {
			return utils.ChatError(err)
		}
		if session.RunIDs == nil {
			session.RunIDs = []string{}
		}
		return middleware.JSONResult(http.StatusOK, SessionResponse{SessionID: sessionID, SessionName: session.SessionName, RunIDs: session.RunIDs})
	}
}
