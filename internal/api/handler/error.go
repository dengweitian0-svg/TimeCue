package handler

import (
	"errors"
	"net/http"

	logger "github.com/dengweitian0-svg/TimeCue/internal/log"
	"github.com/gin-gonic/gin"
)

// apiError 是所有 HTTP 接口共用的错误内容。
type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type errorResponse struct {
	Error apiError `json:"error"`
}

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

// WriteError 输出统一的 HTTP 错误结构。
func WriteError(c *gin.Context, status int, detail apiError) {
	if status >= http.StatusBadRequest && status < http.StatusInternalServerError {
		logger.Warn("HTTP request rejected",
			"method", c.Request.Method,
			"route", c.FullPath(),
			"status", status,
			"code", detail.Code,
			"message", detail.Message,
		)
	}
	c.JSON(status, errorResponse{
		Error: detail,
	})
}

// WriteForbiddenHost 输出 Host 校验失败响应。
func WriteForbiddenHost(c *gin.Context) {
	WriteError(c, http.StatusForbidden, errForbiddenHost)
}

// WriteRouteNotFound 输出路径不存在响应。
func WriteRouteNotFound(c *gin.Context) {
	WriteError(c, http.StatusNotFound, errRouteNotFound)
}

// WriteMethodNotAllowed 输出方法不支持响应。
func WriteMethodNotAllowed(c *gin.Context) {
	WriteError(c, http.StatusMethodNotAllowed, errMethodNotAllowed)
}

// WriteInternalError 输出不泄露内部细节的服务器错误。
func WriteInternalError(c *gin.Context) {
	WriteError(c, http.StatusInternalServerError, errInternal)
}
