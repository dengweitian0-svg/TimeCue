package handler

import (
	"errors"
	"net/http"

	"github.com/dengweitian0-svg/TimeCue/internal/middleware"
	"github.com/dengweitian0-svg/TimeCue/internal/service"
	"github.com/gin-gonic/gin"
)

// DeleteScheduleHandler 软删除日程及其未来待投递提醒。
func DeleteScheduleHandler(app *service.App) middleware.HTTPHandler {
	return func(c *gin.Context) middleware.HTTPResult {
		id, failure := parseScheduleID(c)
		if failure != nil {
			return *failure
		}
		if err := app.DeleteSchedule(c.Request.Context(), id); err != nil {
			if errors.Is(err, service.ErrScheduleNotFound) {
				return middleware.ErrorResult(http.StatusNotFound, errScheduleNotFound, nil)
			}
			return internalErrorResult(err)
		}
		return middleware.NoContentResult()
	}
}
