package handler

import (
	"errors"
	"net/http"

	logger "github.com/dengweitian0-svg/TimeCue/internal/log"
	"github.com/dengweitian0-svg/TimeCue/internal/service"
	"github.com/gin-gonic/gin"
)

// DeleteScheduleHandler 软删除日程及其未来待投递提醒。
func DeleteScheduleHandler(app *service.App) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := parseScheduleID(c)
		if !ok {
			return
		}
		if err := app.DeleteSchedule(c.Request.Context(), id); err != nil {
			if errors.Is(err, service.ErrScheduleNotFound) {
				WriteError(c, http.StatusNotFound, errScheduleNotFound)
			} else {
				logger.Error("failed to delete schedule", "error", err)
				WriteInternalError(c)
			}
			return
		}
		c.Status(http.StatusNoContent)
		logger.Info("schedule deleted", "schedule_id", id)
	}
}
