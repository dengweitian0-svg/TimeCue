package schedule

import (
	"bytes"
	"encoding/json"
	"errors"
	utils "github.com/dengweitian0-svg/TimeCue/internal/api/handler/utils"
	application "github.com/dengweitian0-svg/TimeCue/internal/app"
	service "github.com/dengweitian0-svg/TimeCue/internal/service/schedule"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dengweitian0-svg/TimeCue/internal/middleware"
	"github.com/dengweitian0-svg/TimeCue/internal/model"
	"github.com/gin-gonic/gin"
)

type createScheduleRequest struct {
	ExpectedRevision int                `json:"expectedRevision,omitempty"`
	Name             string             `json:"name"`
	Type             model.ScheduleType `json:"type"`
	Description      *string            `json:"description"`
	Single           json.RawMessage    `json:"single,omitempty"`
	Period           json.RawMessage    `json:"period,omitempty"`
}

type createSingleRequest struct {
	NotifyTime string `json:"notifyTime"`
}
type createPeriodRequest struct {
	Cron []string `json:"cron"`
	Open *bool    `json:"open,omitempty"`
}

// CreateScheduleHandler 校验 JSON 输入并创建日程及首条提醒。
func CreateScheduleHandler(app *application.App) middleware.HTTPHandler {
	return writeScheduleHandler(app, false)
}

func writeScheduleHandler(app *application.App, update bool) middleware.HTTPHandler {
	return func(c *gin.Context) middleware.HTTPResult {
		mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
		if err != nil || mediaType != "application/json" {
			return middleware.ErrorResult(http.StatusUnsupportedMediaType, utils.ErrUnsupportedMediaType, nil)
		}
		body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20))
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				return middleware.ErrorResult(http.StatusRequestEntityTooLarge, utils.ErrPayloadTooLarge, nil)
			}
			return middleware.ErrorResult(http.StatusBadRequest, utils.ErrInvalidJSON, nil)
		}

		var request createScheduleRequest
		allowed := []string{"name", "type", "description", "single", "period"}
		if update {
			allowed = append(allowed, "expectedRevision")
		}
		if err := utils.RequireJSONKeys(body, allowed...); err != nil {
			return middleware.ErrorResult(http.StatusBadRequest, utils.ErrInvalidJSON, nil)
		}
		if err := utils.DecodeStrict(bytes.NewReader(body), &request); err != nil {
			return middleware.ErrorResult(http.StatusBadRequest, utils.ErrInvalidJSON, nil)
		}
		schedule := model.Schedule{
			Name:        strings.TrimSpace(request.Name),
			Type:        request.Type,
			Description: request.Description,
		}
		if update {
			id, failure := parseScheduleID(c)
			if failure != nil {
				return *failure
			}
			schedule.ID = id
			if request.ExpectedRevision < 1 {
				return middleware.ErrorResult(http.StatusBadRequest, utils.ErrInvalidJSON, nil)
			}
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
			if err := utils.RequireJSONKeys(request.Single, "notifyTime"); err != nil {
				return middleware.ErrorResult(http.StatusBadRequest, errSingleRuleInvalid, nil)
			}
			if err := utils.DecodeStrict(bytes.NewReader(request.Single), &single); err != nil {
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
			if err := utils.RequireJSONKeys(request.Period, "cron", "open"); err != nil {
				return middleware.ErrorResult(http.StatusBadRequest, errPeriodRuleInvalid, nil)
			}
			if err := utils.DecodeStrict(bytes.NewReader(request.Period), &period); err != nil || len(period.Cron) == 0 {
				return middleware.ErrorResult(http.StatusBadRequest, errPeriodCronInvalid, nil)
			}
			schedule.Period = &model.Period{
				Open: true,
				Cron: period.Cron,
			}
			if period.Open != nil {
				schedule.Period.Open = *period.Open
			}
		default:
			return middleware.ErrorResult(http.StatusBadRequest, errScheduleTypeInvalid, nil)
		}
		created, err := app.WriteSchedule(c.Request.Context(), schedule, request.ExpectedRevision, c.GetHeader("Idempotency-Key"))
		if err != nil {
			if errors.Is(err, service.ErrInvalidSchedule) {
				return middleware.ErrorResult(http.StatusBadRequest, errScheduleRuleInvalid, nil)
			}
			if errors.Is(err, service.ErrScheduleNotFound) {
				return middleware.ErrorResult(http.StatusNotFound, errScheduleNotFound, nil)
			}
			if errors.Is(err, model.ErrScheduleConflict) {
				return middleware.ErrorResult(http.StatusConflict, utils.NewAPIError("revision_conflict", "日程已被修改，请重新加载最新版本。"), nil)
			}
			if errors.Is(err, model.ErrIdempotencyConflict) {
				return middleware.ErrorResult(http.StatusConflict, utils.NewAPIError("idempotency_conflict", "同一操作标识不能提交不同内容。"), nil)
			}
			return internalErrorResult(err)
		}
		status := http.StatusCreated
		if update {
			status = http.StatusOK
		}
		response := scheduleToResponse(created.Schedule)
		response.EffectiveAt = created.EffectiveAt.Format(time.RFC3339Nano)
		result := middleware.DataResult(status, response)
		result.Headers = http.Header{
			"Location": []string{
				"/api/v1/scheduler/schedules/" + strconv.Itoa(created.Schedule.ID),
			},
		}
		return withScheduleLog(result, created)
	}
}
