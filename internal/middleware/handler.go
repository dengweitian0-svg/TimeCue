package middleware

import (
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
