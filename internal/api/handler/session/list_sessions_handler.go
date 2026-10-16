package session

import (
	"github.com/dengweitian0-svg/TimeCue/internal/api/handler/utils"
	"net/http"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/middleware"
	"github.com/gin-gonic/gin"
)

// SessionsResponse 返回按最新 run 创建时间倒序的 session ID 列表。
type SessionsResponse struct {
	Sessions []string `json:"sessions"`
}

// ListSessionsHandler 返回未删除的 session ID。
func ListSessionsHandler(service chat.Service) middleware.HTTPHandler {
	return func(c *gin.Context) middleware.HTTPResult {
		sessions, err := service.ListSessions(c.Request.Context())
		if err != nil {
			return utils.ChatError(err)
		}
		if sessions == nil {
			sessions = []string{}
		}
		return middleware.JSONResult(http.StatusOK, SessionsResponse{Sessions: sessions})
	}
}
