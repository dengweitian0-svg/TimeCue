package handler

import (
	"net/http"
	"strconv"

	logger "github.com/dengweitian0-svg/TimeCue/internal/log"
	"github.com/dengweitian0-svg/TimeCue/internal/model"
	"github.com/dengweitian0-svg/TimeCue/internal/service"
	"github.com/gin-gonic/gin"
)

func queryInteger(c *gin.Context, key string, fallback int, minimum int, maximum int) (int, bool) {
	raw, exists := c.GetQuery(key)
	if !exists {
		return fallback, true
	}
	if raw == "" {
		WriteError(c, http.StatusBadRequest, queryIntegerInvalid(key))
		return 0, false
	}
	for _, digit := range raw {
		if digit < '0' || digit > '9' {
			WriteError(c, http.StatusBadRequest, queryIntegerInvalid(key))
			return 0, false
		}
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimum || (maximum > 0 && value > maximum) {
		WriteError(c, http.StatusBadRequest, queryParameterInvalid(key))
		return 0, false
	}
	return value, true
}

// ListScheduleHandler 按名称、类型及分页条件查询日程。
func ListScheduleHandler(app *service.App) gin.HandlerFunc {
	return func(c *gin.Context) {
		limit, ok := queryInteger(c, "limit", 50, 1, 50)
		if !ok {
			return
		}
		offset, ok := queryInteger(c, "offset", 0, 0, 0)
		if !ok {
			return
		}
		options := []model.QueryOption{
			model.WithScheduleLimit(limit),
			model.WithScheduleOffset(offset),
		}
		if name := c.Query("name"); name != "" {
			options = append(options, model.WithScheduleName(name))
		}
		if kind, exists := c.GetQuery("type"); exists {
			if kind != string(model.SingleSchedule) && kind != string(model.PeriodSchedule) {
				WriteError(c, http.StatusBadRequest, errScheduleTypeInvalid)
				return
			}
			options = append(options, model.WithScheduleType(model.ScheduleType(kind)))
		}
		schedules, err := app.ListSchedules(c.Request.Context(), options...)
		if err != nil {
			logger.Error("failed to list schedules", "error", err)
			WriteInternalError(c)
			return
		}
		data := make([]scheduleResponse, 0, len(schedules))
		for _, schedule := range schedules {
			data = append(data, scheduleToResponse(schedule))
		}
		c.JSON(http.StatusOK, gin.H{
			"data": data,
		})
		logger.Debug("schedules listed", "count", len(data), "limit", limit, "offset", offset)
	}
}
