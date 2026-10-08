package middleware

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	logger "github.com/dengweitian0-svg/TimeCue/internal/log"
	"github.com/gin-gonic/gin"
)

const httpLogDetailsKey = "timecue.http_log_details"

// httpLogDetails 只在响应适配层与日志 middleware 之间传递请求结果事实。
type httpLogDetails struct {
	Fields         []slog.Attr
	Error          error
	Panicked       bool
	PanicValue     any
	SuccessMessage string
}

// setHTTPLogDetails 合并当前请求的日志素材，不直接输出日志。
// 多次调用时保留已有错误及 panic 信息，避免写错误响应时丢失原始原因。
func setHTTPLogDetails(c *gin.Context, details httpLogDetails) {
	current := getHTTPLogDetails(c)
	current.Fields = append(current.Fields, details.Fields...)
	if details.SuccessMessage != "" {
		current.SuccessMessage = details.SuccessMessage
	}
	if details.Error != nil {
		if current.Error == nil {
			current.Error = details.Error
		} else {
			current.Error = errors.Join(current.Error, details.Error)
		}
	}
	if details.Panicked {
		current.Panicked = true
		current.PanicValue = details.PanicValue
	}
	c.Set(httpLogDetailsKey, current)
}

func getHTTPLogDetails(c *gin.Context) httpLogDetails {
	value, _ := c.Get(httpLogDetailsKey)
	details, _ := value.(httpLogDetails)
	return details
}

type httpEventKey struct {
	method string
	route  string
}

type httpEvent struct {
	level   slog.Level
	success string
	failure string
}

// 路由事件只描述 HTTP 语义，不依赖 handler、服务层或响应体。
var httpEvents = map[httpEventKey]httpEvent{
	{http.MethodPut, "/api/v1/scheduler/schedules/:id"}:         {slog.LevelInfo, "schedule updated", "failed to update schedule"},
	{http.MethodPost, "/api/v1/scheduler/schedules"}:            {slog.LevelInfo, "schedule created", "failed to create schedule"},
	{http.MethodDelete, "/api/v1/scheduler/schedules/:id"}:      {slog.LevelInfo, "schedule deleted", "failed to delete schedule"},
	{http.MethodGet, "/api/v1/scheduler/schedules/:id"}:         {slog.LevelDebug, "schedule retrieved", "failed to get schedule"},
	{http.MethodGet, "/api/v1/scheduler/schedules"}:             {slog.LevelDebug, "schedules listed", "failed to list schedules"},
	{http.MethodGet, "/api/v1/notifications"}:                   {slog.LevelDebug, "notifications listed", "failed to list notifications"},
	{http.MethodGet, "/api/v1/notifications/:id"}:               {slog.LevelDebug, "notification retrieved", "failed to get notification"},
	{http.MethodPost, "/api/v1/notifications/acknowledgements"}: {slog.LevelInfo, "notifications acknowledged", "failed to acknowledge notifications"},
	{http.MethodGet, "/health"}:                                 {slog.LevelDebug, "HTTP health check passed", "HTTP health check failed"},
}

func httpLogEvent(c *gin.Context, details httpLogDetails) (slog.Level, string) {
	event, matched := httpEvents[httpEventKey{
		c.Request.Method,
		c.FullPath(),
	}]
	switch {
	case details.Panicked:
		return slog.LevelError, "HTTP panic"
	case details.Error != nil || c.Writer.Status() >= http.StatusInternalServerError:
		if matched {
			return slog.LevelError, event.failure
		}
		return slog.LevelError, "HTTP request failed"
	case c.Writer.Status() >= http.StatusBadRequest:
		return slog.LevelWarn, "HTTP request rejected"
	case details.SuccessMessage != "":
		if matched {
			return event.level, details.SuccessMessage
		}
		return slog.LevelInfo, details.SuccessMessage
	case matched:
		return event.level, event.success
	default:
		return slog.LevelInfo, "HTTP request completed"
	}
}

// HTTPLogger 在下游处理完成后输出唯一的 HTTP 结果日志。
// 应在 Host 校验和 panic 恢复之前注册，以读取最终响应状态。
func HTTPLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		started := time.Now()
		c.Next()
		duration := time.Since(started)
		details := getHTTPLogDetails(c)
		level, message := httpLogEvent(c, details)
		args := []any{
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"route", c.FullPath(),
			"status", c.Writer.Status(),
			"duration_ms", float64(duration) / float64(time.Millisecond),
		}
		for _, field := range details.Fields {
			args = append(args, field)
		}
		if details.Panicked {
			args = append(args, "error", details.PanicValue)
		} else if details.Error != nil {
			args = append(args, "error", details.Error)
		}
		message = "[http] " + message
		switch level {
		case slog.LevelDebug:
			logger.Debug(message, args...)
		case slog.LevelInfo:
			logger.Info(message, args...)
		case slog.LevelWarn:
			logger.Warn(message, args...)
		case slog.LevelError:
			logger.Error(message, args...)
		}
	}
}
