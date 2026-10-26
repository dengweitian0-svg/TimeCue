package agent

import (
	"errors"
	"strings"

	"github.com/dengweitian0-svg/TimeCue/internal/api/handler/utils"
	"github.com/dengweitian0-svg/TimeCue/internal/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

var errInvalidRunID = errors.New("chat service returned invalid run id")

func parseRunID(c *gin.Context) (string, *middleware.HTTPResult) {
	id, ok := utils.ParseChatUUID(c.Param("run_id"))
	if !ok {
		failure := utils.ChatInvalidRequest("run_id must be a canonical UUID v4")
		return "", &failure
	}
	return id, nil
}

func validIdempotencyKey(value string) bool {
	const prefix = "create_session:"
	if !strings.HasPrefix(value, prefix) {
		return false
	}
	rawID := strings.TrimPrefix(value, prefix)
	id, err := uuid.Parse(rawID)
	return err == nil && strings.EqualFold(id.String(), rawID)
}
