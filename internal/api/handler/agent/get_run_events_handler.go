package agent

import (
	"github.com/dengweitian0-svg/TimeCue/internal/agent/chat"
	"github.com/dengweitian0-svg/TimeCue/internal/api/handler/utils"
	"github.com/dengweitian0-svg/TimeCue/internal/middleware"
	"github.com/gin-gonic/gin"
	"net/http"
	"strconv"
)

// GetRunEventsHandler 按游标返回完整原始事件，不拆分或重编号。
func GetRunEventsHandler(service chat.RunService) middleware.HTTPHandler {
	return func(c *gin.Context) middleware.HTTPResult {
		id, failure := parseRunID(c)
		if failure != nil {
			return *failure
		}
		values := c.Request.URL.Query()
		for name, list := range values {
			if (name != "after_seq" && name != "limit") || len(list) != 1 {
				return utils.ChatInvalidRequest("invalid event pagination")
			}
		}
		parse := func(key string, defaultValue int64) (int64, error) {
			raw, ok := values[key]
			if !ok {
				return defaultValue, nil
			}
			if len(raw[0]) == 0 {
				return -1, strconv.ErrSyntax
			}
			for _, r := range raw[0] {
				if r < '0' || r > '9' {
					return -1, strconv.ErrSyntax
				}
			}
			return strconv.ParseInt(raw[0], 10, 64)
		}
		after, err := parse("after_seq", 0)
		if err != nil {
			return utils.ChatInvalidRequest("invalid after_seq")
		}
		limit, err := parse("limit", 10)
		if err != nil || limit < 1 || limit > 100 {
			return utils.ChatInvalidRequest("limit must be between 1 and 100")
		}
		page, err := service.GetRunEvents(c.Request.Context(), id, after, int(limit))
		if err != nil {
			return utils.ChatError(err)
		}
		return middleware.JSONResult(http.StatusOK, page)
	}
}
