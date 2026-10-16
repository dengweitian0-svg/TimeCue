package utils

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"

	"github.com/dengweitian0-svg/TimeCue/internal/middleware"
	"github.com/gin-gonic/gin"
)

// APIError 是 HTTP 接口公开的稳定错误结构。
type APIError = middleware.APIError

// NewAPIError 创建稳定错误码和消息。
func NewAPIError(code, message string) APIError {
	return APIError{Code: code, Message: message}
}

var (
	ErrForbiddenHost        = NewAPIError("forbidden_host", "Host must be 127.0.0.1:10086")
	ErrRouteNotFound        = NewAPIError("route_not_found", "route not found")
	ErrMethodNotAllowed     = NewAPIError("method_not_allowed", "method not allowed")
	ErrInternal             = NewAPIError("internal_error", "internal server error")
	ErrDatabaseUnavailable  = NewAPIError("database_unavailable", "database is unavailable")
	ErrSchemaInvalid        = NewAPIError("schema_invalid", "database schema is invalid")
	ErrUnsupportedMediaType = NewAPIError("unsupported_media_type", "Content-Type must be application/json")
	ErrPayloadTooLarge      = NewAPIError("payload_too_large", "request body exceeds 1 MiB")
	ErrInvalidJSON          = NewAPIError("invalid_request", "invalid JSON request")
)

// ForbiddenHostResult 返回 Host 校验失败的响应事实。
func ForbiddenHostResult() middleware.HTTPResult {
	return middleware.ErrorResult(http.StatusForbidden, ErrForbiddenHost, nil)
}

// RouteNotFoundResult 返回未注册路由的响应事实。
func RouteNotFoundResult() middleware.HTTPResult {
	return middleware.ErrorResult(http.StatusNotFound, ErrRouteNotFound, nil)
}

// MethodNotAllowedResult 返回不支持 HTTP 方法的响应事实。
func MethodNotAllowedResult() middleware.HTTPResult {
	return middleware.ErrorResult(http.StatusMethodNotAllowed, ErrMethodNotAllowed, nil)
}

// InternalErrorResult 返回不暴露内部细节的错误响应。
func InternalErrorResult(cause error) middleware.HTTPResult {
	return middleware.ErrorResult(http.StatusInternalServerError, ErrInternal, cause)
}

// ReadJSON 校验媒体类型、大小、JSON 对象和允许字段，并将失败转换为 HTTP 响应。
func ReadJSON(c *gin.Context, target any, keys ...string) *middleware.HTTPResult {
	mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || mediaType != "application/json" {
		failure := middleware.ErrorResult(http.StatusUnsupportedMediaType, ErrUnsupportedMediaType, nil)
		return &failure
	}
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20))
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			failure := middleware.ErrorResult(http.StatusRequestEntityTooLarge, ErrPayloadTooLarge, nil)
			return &failure
		}
		failure := middleware.ErrorResult(http.StatusBadRequest, ErrInvalidJSON, nil)
		return &failure
	}
	if err := RequireJSONKeys(body, keys...); err != nil || DecodeStrict(bytes.NewReader(body), target) != nil {
		failure := middleware.ErrorResult(http.StatusBadRequest, ErrInvalidJSON, nil)
		return &failure
	}
	return nil
}

// DecodeStrict 解码单个 JSON 值并拒绝未知字段及尾随 JSON 值。
func DecodeStrict(r io.Reader, target any) error {
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

// RequireJSONKeys 验证 JSON 顶层对象且只含允许字段。
func RequireJSONKeys(body []byte, allowed ...string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return err
	}
	if fields == nil {
		return errors.New("JSON object required")
	}
	accepted := make(map[string]bool, len(allowed))
	for _, key := range allowed {
		accepted[key] = true
	}
	for key := range fields {
		if !accepted[key] {
			return errors.New("unknown JSON field")
		}
	}
	return nil
}
