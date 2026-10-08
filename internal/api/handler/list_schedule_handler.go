package handler

import (
	"net/http"
	"strconv"
	"time"

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

// ListScheduleHandler 默认查询规则，按 view 参数提供日期触发和月份标记。
func ListScheduleHandler(app *service.App) middleware.HTTPHandler {
	return func(c *gin.Context) middleware.HTTPResult {
		if _, exists := c.Request.URL.Query()["view"]; exists {
			return scheduleView(app, c)
		}
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

// 查询投影复用日程资源入口，组合快照仍由服务层在同一读取事务内取得。
func scheduleView(app *service.App, c *gin.Context) middleware.HTTPResult {
	allowed := map[string]bool{"view": true}
	switch c.Query("view") {
	case "occurrences":
		allowed["date"], allowed["cursor"], allowed["include"] = true, true, true
	case "calendar":
		allowed["month"] = true
	default:
		return resourceError(service.ErrInvalidQuery)
	}
	for key, values := range c.Request.URL.Query() {
		if !allowed[key] || len(values) != 1 {
			return resourceError(service.ErrInvalidQuery)
		}
	}
	now := time.Now()
	if c.Query("view") == "calendar" {
		if c.Query("month") == "" {
			return resourceError(service.ErrInvalidQuery)
		}
		data, err := app.QueryCalendar(c.Request.Context(), c.Query("month"), now)
		if err != nil {
			return resourceError(err)
		}
		return middleware.DataResult(http.StatusOK, data)
	}
	if includes, exists := c.Request.URL.Query()["include"]; exists {
		if includes[0] != "unacknowledged" {
			return resourceError(service.ErrInvalidQuery)
		}
		if _, exists := c.Request.URL.Query()["cursor"]; exists {
			return resourceError(service.ErrInvalidQuery)
		}
		data, err := app.QueryScheduleState(c.Request.Context(), c.Query("date"), now)
		if err != nil {
			return resourceError(err)
		}
		return middleware.DataResult(http.StatusOK, data)
	}
	data, err := app.QueryOccurrences(c.Request.Context(), c.Query("date"), c.Query("cursor"), now)
	if err != nil {
		return resourceError(err)
	}
	return middleware.DataResult(http.StatusOK, data)
}
