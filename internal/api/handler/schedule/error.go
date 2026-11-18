package schedule

import (
	"errors"
	utils "github.com/dengweitian0-svg/TimeCue/internal/api/handler/utils"
	"github.com/dengweitian0-svg/TimeCue/internal/middleware"
	"github.com/dengweitian0-svg/TimeCue/internal/model"
	notification "github.com/dengweitian0-svg/TimeCue/internal/service/notification"
	schedulesvc "github.com/dengweitian0-svg/TimeCue/internal/service/schedule"
	"net/http"
)

var (
	errScheduleNotFound    = utils.NewAPIError("schedule_not_found", "schedule not found")
	errNameRequired        = utils.NewAPIError("invalid_request", "name is required")
	errSingleRuleRequired  = utils.NewAPIError("invalid_request", "single rule is required and period must be absent")
	errSingleRuleInvalid   = utils.NewAPIError("invalid_request", "invalid single rule")
	errNotifyTimeInvalid   = utils.NewAPIError("invalid_request", "notifyTime must use RFC3339 with +08:00")
	errPeriodRuleRequired  = utils.NewAPIError("invalid_request", "period rule is required and single must be absent")
	errPeriodRuleInvalid   = utils.NewAPIError("invalid_request", "invalid period rule")
	errPeriodCronInvalid   = utils.NewAPIError("invalid_request", "period cron must be a nonempty array")
	errScheduleTypeInvalid = utils.NewAPIError("invalid_request", "type must be single or period")
	errScheduleRuleInvalid = utils.NewAPIError("invalid_request", "invalid schedule rule")
	errScheduleIDInvalid   = utils.NewAPIError("invalid_request", "id must be a positive decimal integer")
)

func queryIntegerInvalid(key string) utils.APIError {
	return utils.NewAPIError("invalid_request", key+" must be a decimal integer")
}

func queryParameterInvalid(key string) utils.APIError {
	return utils.NewAPIError("invalid_request", "invalid "+key)
}

func internalErrorResult(cause error) middleware.HTTPResult {
	return utils.InternalErrorResult(cause)
}

func resourceError(err error) middleware.HTTPResult {
	if errors.Is(err, notification.ErrTaskNotFound) {
		return middleware.ErrorResult(http.StatusNotFound, utils.NewAPIError("delivery_task_not_found", "task is not valid in this process"), nil)
	}
	if errors.Is(err, notification.ErrMembersChanged) {
		return middleware.ErrorResult(http.StatusConflict, utils.NewAPIError("delivery_members_changed", "refresh task members from first page"), nil)
	}
	if errors.Is(err, notification.ErrAttemptNotFound) || errors.Is(err, notification.ErrDeliveryConflict) {
		return deliveryError(err)
	}
	if errors.Is(err, schedulesvc.ErrInvalidQuery) {
		return middleware.ErrorResult(http.StatusBadRequest, utils.NewAPIError("invalid_request", "invalid view, include, date, month, state or cursor"), nil)
	}
	if errors.Is(err, model.ErrRecordNotFound) {
		return middleware.ErrorResult(http.StatusNotFound, utils.NewAPIError("notification_not_found", "notification not found"), nil)
	}
	if errors.Is(err, model.ErrInvalidAcknowledgement) {
		return middleware.ErrorResult(http.StatusConflict, utils.NewAPIError("invalid_acknowledgement", "expected 1 to 500 existing, nondeleted, due notification IDs"), nil)
	}
	return internalErrorResult(err)
}
