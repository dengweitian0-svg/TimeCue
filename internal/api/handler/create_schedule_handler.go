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

	"github.com/dengweitian0-svg/TimeCue/internal/middleware"
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
func CreateScheduleHandler(app *service.App) middleware.HTTPHandler {
	return func(c *gin.Context) middleware.HTTPResult {
		mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
		if err != nil || mediaType != "application/json" {
			return middleware.ErrorResult(http.StatusUnsupportedMediaType, errUnsupportedMediaType, nil)
		}
		body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20))
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				return middleware.ErrorResult(http.StatusRequestEntityTooLarge, errPayloadTooLarge, nil)
			}
			return middleware.ErrorResult(http.StatusBadRequest, errInvalidJSON, nil)
		}

		var request createScheduleRequest
		if err := requireJSONKeys(body, "name", "type", "description", "single", "period"); err != nil {
			return middleware.ErrorResult(http.StatusBadRequest, errInvalidJSON, nil)
		}
		if err := decodeStrict(bytes.NewReader(body), &request); err != nil {
			return middleware.ErrorResult(http.StatusBadRequest, errInvalidJSON, nil)
		}
		schedule := model.Schedule{
			Name:        strings.TrimSpace(request.Name),
			Type:        request.Type,
			Description: request.Description,
		}
		if schedule.Name == "" {
			return middleware.ErrorResult(http.StatusBadRequest, errNameRequired, nil)
		}
		switch request.Type {
		case model.SingleSchedule:
			if len(request.Single) == 0 || string(request.Single) == "null" || len(request.Period) != 0 {
				return middleware.ErrorResult(http.StatusBadRequest, errSingleRuleRequired, nil)
			}
			var single createSingleRequest
			if err := requireJSONKeys(request.Single, "notifyTime"); err != nil {
				return middleware.ErrorResult(http.StatusBadRequest, errSingleRuleInvalid, nil)
			}
			if err := decodeStrict(bytes.NewReader(request.Single), &single); err != nil {
				return middleware.ErrorResult(http.StatusBadRequest, errSingleRuleInvalid, nil)
			}
			notifyTime, err := time.Parse(time.RFC3339, single.NotifyTime)
			_, offset := notifyTime.Zone()
			if err != nil || offset != 8*3600 || !strings.HasSuffix(single.NotifyTime, "+08:00") {
				return middleware.ErrorResult(http.StatusBadRequest, errNotifyTimeInvalid, nil)
			}
			schedule.Single = &model.Single{
				NotifyTime: notifyTime,
			}
		case model.PeriodSchedule:
			if len(request.Period) == 0 || string(request.Period) == "null" || len(request.Single) != 0 {
				return middleware.ErrorResult(http.StatusBadRequest, errPeriodRuleRequired, nil)
			}
			var period createPeriodRequest
			if err := requireJSONKeys(request.Period, "cron"); err != nil {
				return middleware.ErrorResult(http.StatusBadRequest, errPeriodRuleInvalid, nil)
			}
			if err := decodeStrict(bytes.NewReader(request.Period), &period); err != nil || len(period.Cron) == 0 {
				return middleware.ErrorResult(http.StatusBadRequest, errPeriodCronInvalid, nil)
			}
			schedule.Period = &model.Period{
				Open: true,
				Cron: period.Cron,
			}
		default:
			return middleware.ErrorResult(http.StatusBadRequest, errScheduleTypeInvalid, nil)
		}
		created, _, err := app.CreateSchedule(c.Request.Context(), schedule)
		if err != nil {
			if errors.Is(err, service.ErrInvalidSchedule) {
				return middleware.ErrorResult(http.StatusBadRequest, errScheduleRuleInvalid, nil)
			}
			return internalErrorResult(err)
		}
		result := middleware.DataResult(http.StatusCreated, scheduleToResponse(created))
		result.Headers = http.Header{
			"Location": []string{
				"/api/v1/scheduler/schedules/" + strconv.Itoa(created.ID),
			},
		}
		return result
	}
}
