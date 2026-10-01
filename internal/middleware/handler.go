package middleware

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
)

// HTTPResult 是 handler 返回的响应事实；仅 Body 会作为响应体序列化。
type HTTPResult struct {
	Status  int
	Headers http.Header
	Body    any
	Cause   error
}

// HTTPHandler 读取请求并返回结果，不直接写响应或日志。
type HTTPHandler func(*gin.Context) HTTPResult

// APIError 只包含允许向客户端公开的稳定错误信息。
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ErrorResponse struct {
	Error APIError `json:"error"`
}

type DataResponse[T any] struct {
	Data T `json:"data"`
}

func JSONResult(status int, body any) HTTPResult {
	return HTTPResult{
		Status: status,
		Body:   body,
	}
}

func DataResult[T any](status int, data T) HTTPResult {
	return JSONResult(status, DataResponse[T]{
		Data: data,
	})
}

// ErrorResult 将公开错误与只供日志使用的内部原因分开保存。
func ErrorResult(status int, detail APIError, cause error) HTTPResult {
	return HTTPResult{
		Status: status,
		Body: ErrorResponse{
			Error: detail,
		},
		Cause: cause,
	}
}

func NoContentResult() HTTPResult {
	return HTTPResult{
		Status: http.StatusNoContent,
	}
}

// HTTPResultFields 由 API 层注册，仅提取允许记录的结果事实。
type HTTPResultFields func(*gin.Context, HTTPResult) []slog.Attr

func WrapHTTPHandler(handler HTTPHandler, fields HTTPResultFields) gin.HandlerFunc {
	return func(c *gin.Context) {
		WriteHTTPResult(c, handler(c), fields)
	}
}

func internalErrorResult(cause error) HTTPResult {
	return ErrorResult(http.StatusInternalServerError, APIError{
		Code:    "internal_error",
		Message: "internal server error",
	}, cause)
}

// WriteHTTPResult 先序列化和收集素材，再提交响应。
func WriteHTTPResult(c *gin.Context, result HTTPResult, fields HTTPResultFields) {
	setHTTPLogDetails(c, httpLogDetails{Error: result.Cause})
	var body []byte
	if result.Status != http.StatusNoContent && result.Status != http.StatusNotModified {
		var err error
		body, err = json.Marshal(result.Body)
		if err != nil {
			panic(err)
		}
	}
	var attrs []slog.Attr
	if detail, ok := result.Body.(ErrorResponse); ok {
		attrs = append(attrs, slog.String("code", detail.Error.Code), slog.String("message", detail.Error.Message))
	}
	if fields != nil {
		attrs = append(attrs, fields(c, result)...)
	}
	setHTTPLogDetails(c, httpLogDetails{Fields: attrs})
	for name, values := range result.Headers {
		c.Writer.Header().Del(name)
		for _, value := range values {
			c.Writer.Header().Add(name, value)
		}
	}
	if body != nil && c.Writer.Header().Get("Content-Type") == "" {
		c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	}
	c.Status(result.Status)
	c.Writer.WriteHeaderNow()
	if body != nil {
		if _, err := c.Writer.Write(body); err != nil {
			setHTTPLogDetails(c, httpLogDetails{Error: err})
			c.Abort()
		}
	}
}
