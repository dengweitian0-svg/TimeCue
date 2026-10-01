package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	logger "github.com/dengweitian0-svg/TimeCue/internal/log"
	"github.com/dengweitian0-svg/TimeCue/internal/model"
	"github.com/dengweitian0-svg/TimeCue/internal/service"
	"github.com/gin-gonic/gin"
)

type createScheduleRequest struct {
	Name        string             `json:"name"`
	Type        model.ScheduleType `json:"type"`
	Description *string            `json:"description"`
	Single      json.RawMessage    `json:"single,omitempty"`
	Period      json.RawMessage    `json:"period,omitempty"`
}

type createSingleRequest struct {
	NotifyTime string `json:"notifyTime"`
}
type createPeriodRequest struct {
	Cron []string `json:"cron"`
}

func decodeStrict(r io.Reader, target any) error {
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errMultipleJSONValues
		}
		return err
	}
	return nil
}

func requireJSONKeys(body []byte, allowed ...string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return err
	}
	if fields == nil {
		return errJSONObjectRequired
	}
	accepted := make(map[string]bool, len(allowed))
	for _, key := range allowed {
		accepted[key] = true
	}
	for key := range fields {
		if !accepted[key] {
			return errUnknownJSONField
		}
	}
	return nil
}

// CreateScheduleHandler 校验 JSON 输入并创建日程及首条提醒。
func CreateScheduleHandler(app *service.App) gin.HandlerFunc {
	return func(c *gin.Context) {
		mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
		if err != nil || mediaType != "application/json" {
			WriteError(c, http.StatusUnsupportedMediaType, errUnsupportedMediaType)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20))
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				WriteError(c, http.StatusRequestEntityTooLarge, errPayloadTooLarge)
			} else {
				WriteError(c, http.StatusBadRequest, errInvalidJSON)
			}
			return
		}

		var request createScheduleRequest
		if err := requireJSONKeys(body, "name", "type", "description", "single", "period"); err != nil {
			WriteError(c, http.StatusBadRequest, errInvalidJSON)
			return
		}
		if err := decodeStrict(bytes.NewReader(body), &request); err != nil {
			WriteError(c, http.StatusBadRequest, errInvalidJSON)
			return
		}
		schedule := model.Schedule{
			Name:        strings.TrimSpace(request.Name),
			Type:        request.Type,
			Description: request.Description,
		}
		if schedule.Name == "" {
			WriteError(c, http.StatusBadRequest, errNameRequired)
			return
		}
		switch request.Type {
		case model.SingleSchedule:
			if len(request.Single) == 0 || string(request.Single) == "null" || len(request.Period) != 0 {
				WriteError(c, http.StatusBadRequest, errSingleRuleRequired)
				return
			}
			var single createSingleRequest
			if err := requireJSONKeys(request.Single, "notifyTime"); err != nil {
				WriteError(c, http.StatusBadRequest, errSingleRuleInvalid)
				return
			}
			if err := decodeStrict(bytes.NewReader(request.Single), &single); err != nil {
				WriteError(c, http.StatusBadRequest, errSingleRuleInvalid)
				return
			}
			notifyTime, err := time.Parse(time.RFC3339, single.NotifyTime)
			_, offset := notifyTime.Zone()
			if err != nil || offset != 8*3600 || !strings.HasSuffix(single.NotifyTime, "+08:00") {
				WriteError(c, http.StatusBadRequest, errNotifyTimeInvalid)
				return
			}
			schedule.Single = &model.Single{
				NotifyTime: notifyTime,
			}
		case model.PeriodSchedule:
			if len(request.Period) == 0 || string(request.Period) == "null" || len(request.Single) != 0 {
				WriteError(c, http.StatusBadRequest, errPeriodRuleRequired)
				return
			}
			var period createPeriodRequest
			if err := requireJSONKeys(request.Period, "cron"); err != nil {
				WriteError(c, http.StatusBadRequest, errPeriodRuleInvalid)
				return
			}
			if err := decodeStrict(bytes.NewReader(request.Period), &period); err != nil || len(period.Cron) == 0 {
				WriteError(c, http.StatusBadRequest, errPeriodCronInvalid)
				return
			}
			schedule.Period = &model.Period{
				Open: true,
				Cron: period.Cron,
			}
		default:
			WriteError(c, http.StatusBadRequest, errScheduleTypeInvalid)
			return
		}
		created, _, err := app.CreateSchedule(c.Request.Context(), schedule)
		if err != nil {
			if errors.Is(err, service.ErrInvalidSchedule) {
				WriteError(c, http.StatusBadRequest, errScheduleRuleInvalid)
			} else {
				logger.Error("failed to create schedule", "error", err)
				WriteInternalError(c)
			}
			return
		}
		c.Header("Location", "/api/v1/scheduler/schedules/"+strconv.Itoa(created.ID))
		c.JSON(http.StatusCreated, gin.H{
			"data": scheduleToResponse(created),
		})
		logger.Info("schedule created", "schedule_id", created.ID, "type", created.Type)
	}
}
