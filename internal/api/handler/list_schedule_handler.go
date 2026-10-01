package handler

import (
	"net/http"
	"strconv"

	"github.com/dengweitian0-svg/TimeCue/internal/middleware"
	"github.com/dengweitian0-svg/TimeCue/internal/model"
	"github.com/dengweitian0-svg/TimeCue/internal/service"
	"github.com/gin-gonic/gin"
)

// ScheduleListResponse 保留实际使用的分页值，但不将其输出到客户端。
type ScheduleListResponse struct {
	Data   []ScheduleResponse `json:"data"`
	Limit  int                `json:"-"`
	Offset int                `json:"-"`
}

func queryInteger(c *gin.Context, key string, fallback int, minimum int, maximum int) (int, *middleware.HTTPResult) {
	raw, exists := c.GetQuery(key)
	if !exists {
		return fallback, nil
	}
	if raw == "" {
		failure := middleware.ErrorResult(http.StatusBadRequest, queryIntegerInvalid(key), nil)
		return 0, &failure
	}
	for _, digit := range raw {
		if digit < '0' || digit > '9' {
			failure := middleware.ErrorResult(http.StatusBadRequest, queryIntegerInvalid(key), nil)
			return 0, &failure
		}
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimum || (maximum > 0 && value > maximum) {
		failure := middleware.ErrorResult(http.StatusBadRequest, queryParameterInvalid(key), nil)
		return 0, &failure
	}
	return value, nil
}

// ListScheduleHandler 按名称、类型及分页条件查询日程。
func ListScheduleHandler(app *service.App) middleware.HTTPHandler {
	return func(c *gin.Context) middleware.HTTPResult {
		limit, failure := queryInteger(c, "limit", 50, 1, 50)
		if failure != nil {
			return *failure
		}
		offset, failure := queryInteger(c, "offset", 0, 0, 0)
		if failure != nil {
			return *failure
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
				return middleware.ErrorResult(http.StatusBadRequest, errScheduleTypeInvalid, nil)
			}
			options = append(options, model.WithScheduleType(model.ScheduleType(kind)))
		}
		schedules, err := app.ListSchedules(c.Request.Context(), options...)
		if err != nil {
			return internalErrorResult(err)
		}
		data := make([]ScheduleResponse, 0, len(schedules))
		for _, schedule := range schedules {
			data = append(data, scheduleToResponse(schedule))
		}
		return middleware.JSONResult(http.StatusOK, ScheduleListResponse{
			Data:   data,
			Limit:  limit,
			Offset: offset,
		})
	}
}
