package schedule

import (
	application "github.com/dengweitian0-svg/TimeCue/internal/app"
	"net/http"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/middleware"
	"github.com/gin-gonic/gin"
)

// NotificationHandler 读取受控查询，不修改任何业务事实。
func NotificationHandler(app *application.App) middleware.HTTPHandler {
	return func(c *gin.Context) middleware.HTTPResult {
		id, failure := parseScheduleID(c)
		if failure != nil {
			return *failure
		}
		data, err := app.GetNotification(c.Request.Context(), id, time.Now())
		if err != nil {
			return resourceError(err)
		}
		return middleware.DataResult(http.StatusOK, data)
	}
}
