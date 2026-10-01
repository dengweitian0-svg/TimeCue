package handler

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/middleware"
	"github.com/dengweitian0-svg/TimeCue/internal/model"
	"github.com/dengweitian0-svg/TimeCue/internal/service"
	"github.com/gin-gonic/gin"
)

type singleResponse struct {
	NotifyTime string `json:"notifyTime"`
}
type periodResponse struct {
	Open bool     `json:"open"`
	Cron []string `json:"cron"`
}
type ScheduleResponse struct {
	ID          int                `json:"id"`
	Name        string             `json:"name"`
	Type        model.ScheduleType `json:"type"`
	Description *string            `json:"description"`
	Single      *singleResponse    `json:"single"`
	Period      *periodResponse    `json:"period"`
	Cancelled   bool               `json:"cancelled"`
	CreatedAt   string             `json:"createdAt"`
	UpdatedAt   string             `json:"updatedAt"`
}

func scheduleToResponse(schedule *model.Schedule) ScheduleResponse {
	response := ScheduleResponse{
		ID:          schedule.ID,
		Name:        schedule.Name,
		Type:        schedule.Type,
		Description: schedule.Description,
		Cancelled:   schedule.Cancelled,
		CreatedAt:   schedule.CreatedAt.In(model.BeijingLocation()).Format(time.RFC3339Nano),
		UpdatedAt:   schedule.UpdatedAt.In(model.BeijingLocation()).Format(time.RFC3339Nano),
	}
	if schedule.Single != nil {
		response.Single = &singleResponse{
			NotifyTime: schedule.Single.NotifyTime.In(model.BeijingLocation()).Format(time.RFC3339Nano),
		}
	}
	if schedule.Period != nil {
		response.Period = &periodResponse{
			Open: schedule.Period.Open,
			Cron: schedule.Period.Cron,
		}
	}
	return response
}

func parseScheduleID(c *gin.Context) (int, *middleware.HTTPResult) {
	invalid := middleware.ErrorResult(http.StatusBadRequest, errScheduleIDInvalid, nil)
	raw := c.Param("id")
	if raw == "" {
		return 0, &invalid
	}
	for _, digit := range raw {
		if digit < '0' || digit > '9' {
			return 0, &invalid
		}
	}
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 {
		return 0, &invalid
	}
	return id, nil
}

// GetScheduleHandler 返回一条未删除日程。
func GetScheduleHandler(app *service.App) middleware.HTTPHandler {
	return func(c *gin.Context) middleware.HTTPResult {
		id, failure := parseScheduleID(c)
		if failure != nil {
			return *failure
		}
		schedule, err := app.GetSchedule(c.Request.Context(), id)
		if err != nil {
			if errors.Is(err, service.ErrScheduleNotFound) {
				return middleware.ErrorResult(http.StatusNotFound, errScheduleNotFound, nil)
			}
			return internalErrorResult(err)
		}
		return middleware.DataResult(http.StatusOK, scheduleToResponse(schedule))
	}
}
