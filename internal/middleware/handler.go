package middleware

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	// LogFields 保存已完成业务操作的素材，即使响应失败也必须保留。
	LogFields []slog.Attr
	// SuccessMessage 仅在 HTTP 正常完成时覆盖成功消息。
	SuccessMessage string
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

// WriteHTTPResult 是响应的唯一出口，先序列化和收集素材，再提交响应。
func WriteHTTPResult(c *gin.Context, result HTTPResult, fields HTTPResultFields) {
	// 在序列化和响应检查前保存业务事实，后续失败不撤销已提交的事务。
	setHTTPLogDetails(c, httpLogDetails{
		Fields: result.LogFields, SuccessMessage: result.SuccessMessage,
	})
	if c.Writer.Written() {
		setHTTPLogDetails(c, httpLogDetails{
			Error: errors.New("handler wrote response before returning HTTPResult"),
		})
		c.Abort()
		return
	}
	if result.Status < http.StatusOK || result.Status > 599 {
		result = internalErrorResult(fmt.Errorf("handler returned invalid HTTP status: %d", result.Status))
		fields = nil
		c.Abort()
	}
	setHTTPLogDetails(c, httpLogDetails{
		Error: result.Cause,
	})

	var body []byte
	if result.Status != http.StatusNoContent && result.Status != http.StatusNotModified {
		var err error
		body, err = json.Marshal(result.Body)
		if err != nil {
			result = internalErrorResult(err)
			// 兜底响应只含固定字符串，序列化不会失败。
			body, _ = json.Marshal(result.Body)
			fields = nil
			setHTTPLogDetails(c, httpLogDetails{
				Error: err,
			})
			c.Abort()
		}
	}

	var attrs []slog.Attr
	if detail, ok := result.Body.(ErrorResponse); ok {
		attrs = append(attrs, slog.String("code", detail.Error.Code), slog.String("message", detail.Error.Message))
	}
	if fields != nil {
		attrs = append(attrs, fields(c, result)...)
	}
	setHTTPLogDetails(c, httpLogDetails{
		Fields: attrs,
	})

	// 提取规则也接收 Gin 上下文；同样防止它提前写响应。
	if c.Writer.Written() {
		setHTTPLogDetails(c, httpLogDetails{
			Error: errors.New("HTTP result fields wrote response before response commit"),
		})
		c.Abort()
		return
	}
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
	// 明确提交，避免返回结果后其他 Gin handler 改写已经选定的状态。
	c.Writer.WriteHeaderNow()
	if body != nil {
		n, err := c.Writer.Write(body)
		if err == nil && n != len(body) {
			err = io.ErrShortWrite
		}
		if err != nil {
			setHTTPLogDetails(c, httpLogDetails{
				Error: err,
			})
			c.Abort()
		}
	}
}
