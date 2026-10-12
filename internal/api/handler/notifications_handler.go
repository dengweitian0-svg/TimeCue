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
		query := c.Request.URL.Query()
		_, scoped := query["scope"]
		allowed := map[string]bool{"state": true, "scope": true, "cursor": true, "deliveryTaskId": true, "attemptId": true}
		for key, values := range c.Request.URL.Query() {
			// 原待确认模式保持忽略额外参数的兼容行为，任务模式使用严格字段契约。
			if !allowed[key] && c.Query("deliveryTaskId") == "" && !scoped {
				continue
			}
			if !allowed[key] || len(values) != 1 || (key != "cursor" && values[0] == "") {
				return resourceError(service.ErrInvalidQuery)
			}
		}
		if scoped {
			if c.Query("scope") != "today" && c.Query("scope") != "history" {
				return resourceError(service.ErrInvalidQuery)
			}
			if _, exists := query["deliveryTaskId"]; exists {
				return resourceError(service.ErrInvalidQuery)
			}
			if _, exists := query["attemptId"]; exists {
				return resourceError(service.ErrInvalidQuery)
			}
		}
		if task := c.Query("deliveryTaskId"); task != "" {
			if _, exists := c.Request.URL.Query()["state"]; exists {
				return resourceError(service.ErrInvalidQuery)
			}
			data, err := app.QueryDeliveryMembers(c.Request.Context(), task, c.Query("attemptId"), c.Query("cursor"), time.Now())
			if err != nil {
				return resourceError(err)
			}
			return middleware.DataResult(http.StatusOK, data)
		}
		if _, exists := c.Request.URL.Query()["attemptId"]; exists {
			return resourceError(service.ErrInvalidQuery)
		}
		if c.Query("state") != "unacknowledged" {
			return resourceError(service.ErrInvalidQuery)
		}
		data, err := app.QueryUnacknowledgedScope(c.Request.Context(), c.Query("scope"), c.Query("cursor"), time.Now())
		if err != nil {
			return resourceError(err)
		}
		return middleware.DataResult(http.StatusOK, data)
	}
}
