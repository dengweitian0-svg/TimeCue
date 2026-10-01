package handler

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	logger "github.com/dengweitian0-svg/TimeCue/internal/log"
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
type scheduleResponse struct {
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

func scheduleToResponse(schedule *model.Schedule) scheduleResponse {
	response := scheduleResponse{
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

func parseScheduleID(c *gin.Context) (int, bool) {
	raw := c.Param("id")
	if raw == "" {
		WriteError(c, http.StatusBadRequest, errScheduleIDInvalid)
		return 0, false
	}
	for _, digit := range raw {
		if digit < '0' || digit > '9' {
			WriteError(c, http.StatusBadRequest, errScheduleIDInvalid)
			return 0, false
		}
	}
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 {
		WriteError(c, http.StatusBadRequest, errScheduleIDInvalid)
		return 0, false
	}
	return id, true
}

// GetScheduleHandler 返回一条未删除日程。
func GetScheduleHandler(app *service.App) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := parseScheduleID(c)
		if !ok {
			return
		}
		schedule, err := app.GetSchedule(c.Request.Context(), id)
		if err != nil {
			if errors.Is(err, service.ErrScheduleNotFound) {
				WriteError(c, http.StatusNotFound, errScheduleNotFound)
			} else {
				logger.Error("failed to get schedule", "error", err)
				WriteInternalError(c)
			}
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"data": scheduleToResponse(schedule),
		})
		logger.Debug("schedule retrieved", "schedule_id", schedule.ID)
	}
}
