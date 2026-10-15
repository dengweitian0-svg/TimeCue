package handler

import (
	"net/http"

	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/middleware"
	"github.com/gin-gonic/gin"
)

// DeleteSessionResponse 是同步删除 session 后立即返回的视图。
type DeleteSessionResponse struct {
	SessionID string `json:"session_id"`
	Status    string `json:"status"`
}

// DeleteSessionHandler 同步隐藏 session，并由应用层异步清理其 run。
func DeleteSessionHandler(service chat.Service) middleware.HTTPHandler {
	return func(c *gin.Context) middleware.HTTPResult {
		sessionID, failure := parseChatSessionID(c)
		if failure != nil {
			return *failure
		}
		if err := service.DeleteSession(c.Request.Context(), sessionID); err != nil {
			return chatError(err)
		}
		return middleware.JSONResult(http.StatusOK, DeleteSessionResponse{SessionID: sessionID, Status: "deleted"})
	}
}
