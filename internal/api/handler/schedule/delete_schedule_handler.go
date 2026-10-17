package schedule

import (
	"errors"
	application "github.com/dengweitian0-svg/TimeCue/internal/app"
	service "github.com/dengweitian0-svg/TimeCue/internal/service/schedule"
	"net/http"

	"github.com/dengweitian0-svg/TimeCue/internal/middleware"
	"github.com/gin-gonic/gin"
)

// DeleteScheduleHandler 软删除日程及其未来待投递提醒。
func DeleteScheduleHandler(app *application.App) middleware.HTTPHandler {
	return func(c *gin.Context) middleware.HTTPResult {
		id, failure := parseScheduleID(c)
		if failure != nil {
			return *failure
		}
		deleted, err := app.DeleteScheduleWithResult(c.Request.Context(), id)
		if err != nil {
			if errors.Is(err, service.ErrScheduleNotFound) {
				return middleware.ErrorResult(http.StatusNotFound, errScheduleNotFound, nil)
			}
			return internalErrorResult(err)
		}
		return withScheduleLog(middleware.NoContentResult(), deleted)
	}
}
