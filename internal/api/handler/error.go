package handler

import (
	"errors"
	"net/http"

	"github.com/dengweitian0-svg/TimeCue/internal/middleware"
)

// apiError 是所有 HTTP 接口共用的错误内容。
type apiError = middleware.APIError

func newAPIError(code, message string) apiError {
	return apiError{
		Code:    code,
		Message: message,
	}
}

var (
	errMultipleJSONValues = errors.New("multiple JSON values")
	errJSONObjectRequired = errors.New("JSON object required")
	errUnknownJSONField   = errors.New("unknown JSON field")

	errForbiddenHost       = newAPIError("forbidden_host", "Host must be 127.0.0.1:10086")
	errRouteNotFound       = newAPIError("route_not_found", "route not found")
	errMethodNotAllowed    = newAPIError("method_not_allowed", "method not allowed")
	errInternal            = newAPIError("internal_error", "internal server error")
	errScheduleNotFound    = newAPIError("schedule_not_found", "schedule not found")
	errDatabaseUnavailable = newAPIError("database_unavailable", "database is unavailable")
	errSchemaInvalid       = newAPIError("schema_invalid", "database schema is invalid")

	errUnsupportedMediaType = newAPIError("unsupported_media_type", "Content-Type must be application/json")
	errPayloadTooLarge      = newAPIError("payload_too_large", "request body exceeds 1 MiB")
	errInvalidJSON          = newAPIError("invalid_request", "invalid JSON request")
	errNameRequired         = newAPIError("invalid_request", "name is required")
	errSingleRuleRequired   = newAPIError("invalid_request", "single rule is required and period must be absent")
	errSingleRuleInvalid    = newAPIError("invalid_request", "invalid single rule")
	errNotifyTimeInvalid    = newAPIError("invalid_request", "notifyTime must use RFC3339 with +08:00")
	errPeriodRuleRequired   = newAPIError("invalid_request", "period rule is required and single must be absent")
	errPeriodRuleInvalid    = newAPIError("invalid_request", "invalid period rule")
	errPeriodCronInvalid    = newAPIError("invalid_request", "period cron must be a nonempty array")
	errScheduleTypeInvalid  = newAPIError("invalid_request", "type must be single or period")
	errScheduleRuleInvalid  = newAPIError("invalid_request", "invalid schedule rule")
	errScheduleIDInvalid    = newAPIError("invalid_request", "id must be a positive decimal integer")
)

func queryIntegerInvalid(key string) apiError {
	return apiError{
		Code:    "invalid_request",
		Message: key + " must be a decimal integer",
	}
}

func queryParameterInvalid(key string) apiError {
	return apiError{
		Code:    "invalid_request",
		Message: "invalid " + key,
	}
}

// ForbiddenHostResult 返回 Host 校验失败的响应事实。
func ForbiddenHostResult() middleware.HTTPResult {
	return middleware.ErrorResult(http.StatusForbidden, errForbiddenHost, nil)
}

func RouteNotFoundResult() middleware.HTTPResult {
	return middleware.ErrorResult(http.StatusNotFound, errRouteNotFound, nil)
}

func MethodNotAllowedResult() middleware.HTTPResult {
	return middleware.ErrorResult(http.StatusMethodNotAllowed, errMethodNotAllowed, nil)
}

func internalErrorResult(cause error) middleware.HTTPResult {
	return middleware.ErrorResult(http.StatusInternalServerError, errInternal, cause)
}
