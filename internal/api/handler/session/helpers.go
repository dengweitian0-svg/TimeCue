package session

import (
	"github.com/dengweitian0-svg/TimeCue/internal/api/handler/utils"
	"github.com/dengweitian0-svg/TimeCue/internal/middleware"
	"github.com/gin-gonic/gin"
)

func parseSessionID(c *gin.Context) (string, *middleware.HTTPResult) {
	id, ok := utils.ParseChatUUID(c.Param("session_id"))
	if !ok {
		failure := utils.ChatInvalidRequest("session_id must be a canonical UUID v4")
		return "", &failure
	}
	return id, nil
}
