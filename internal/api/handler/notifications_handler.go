package handler

import (
	"net/http"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/middleware"
	"github.com/dengweitian0-svg/TimeCue/internal/service"
	"github.com/gin-gonic/gin"
)

// NotificationsHandler 读取受控查询，不修改任何业务事实。
func NotificationsHandler(app *service.App) middleware.HTTPHandler {
	return func(c *gin.Context) middleware.HTTPResult {
		if c.Query("state") != "unacknowledged" {
			return resourceError(service.ErrInvalidQuery)
		}
		data, err := app.QueryUnacknowledged(c.Request.Context(), c.Query("cursor"), time.Now())
		if err != nil {
			return resourceError(err)
		}
		return middleware.DataResult(http.StatusOK, data)
	}
}
