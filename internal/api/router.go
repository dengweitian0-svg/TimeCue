package api

import (
	"log/slog"
	"net/http"

	"github.com/dengweitian0-svg/TimeCue/internal/api/handler"
	"github.com/dengweitian0-svg/TimeCue/internal/middleware"
	"github.com/dengweitian0-svg/TimeCue/internal/service"
	"github.com/gin-gonic/gin"
)

// NewRouter 注册本地健康检查和日程 HTTP 接口。
func NewRouter(app *service.App, healthCheck handler.HealthCheck) *gin.Engine {
	router := gin.New()
	router.HandleMethodNotAllowed = true
	router.RedirectTrailingSlash = false
	router.Use(middleware.HTTPLogger())
	router.Use(middleware.HostGuard("127.0.0.1:10086", handler.ForbiddenHostResult()))
	router.Use(middleware.Recovery())
	router.NoRoute(middleware.WrapHTTPHandler(func(*gin.Context) middleware.HTTPResult {
		return handler.RouteNotFoundResult()
	}, nil))
	router.NoMethod(middleware.WrapHTTPHandler(func(*gin.Context) middleware.HTTPResult {
		return handler.MethodNotAllowedResult()
	}, nil))
	router.GET("/health", middleware.WrapHTTPHandler(handler.HealthHandler(healthCheck), healthFields))

	scheduler := router.Group("/api/v1/scheduler")
	scheduler.POST("/schedules", middleware.WrapHTTPHandler(handler.CreateScheduleHandler(app), nil))
	scheduler.GET("/schedules", middleware.WrapHTTPHandler(handler.ListScheduleHandler(app), listScheduleFields))
	scheduler.GET("/schedules/:id", middleware.WrapHTTPHandler(handler.GetScheduleHandler(app), getScheduleFields))
	scheduler.PUT("/schedules/:id", middleware.WrapHTTPHandler(handler.UpdateScheduleHandler(app), nil))
	scheduler.DELETE("/schedules/:id", middleware.WrapHTTPHandler(handler.DeleteScheduleHandler(app), nil))

	notify := router.Group("/api/v1")
	notify.GET("/notifications", middleware.WrapHTTPHandler(handler.NotificationsHandler(app), listNotificationFields))
	notify.GET("/notifications/:id", middleware.WrapHTTPHandler(handler.NotificationHandler(app), getNotificationFields))
	notify.POST("/notifications/acknowledgements", middleware.WrapHTTPHandler(handler.AcknowledgementsHandler(app), acknowledgeNotificationFields))
	return router
}

// 提取规则只读取明确的响应 DTO 和已校验的请求事实，不解析 JSON。
func getScheduleFields(_ *gin.Context, result middleware.HTTPResult) []slog.Attr {
	if result.Status != http.StatusOK {
		return nil
	}
	response := result.Body.(middleware.DataResponse[handler.ScheduleResponse])
	return []slog.Attr{slog.Int("schedule_id", response.Data.ID)}
}

func listScheduleFields(_ *gin.Context, result middleware.HTTPResult) []slog.Attr {
	if result.Status != http.StatusOK {
		return nil
	}
	switch response := result.Body.(type) {
	case handler.ScheduleListResponse:
		return []slog.Attr{
			slog.Int("count", len(response.Data)),
			slog.Int("limit", response.Limit),
			slog.Int("offset", response.Offset),
		}
	case middleware.DataResponse[service.OccurrencePage]:
		return []slog.Attr{slog.Int("count", len(response.Data.Items))}
	case middleware.DataResponse[*service.ScheduleQueryResult]:
		return []slog.Attr{slog.Int("count", len(response.Data.Occurrences.Items))}
	case middleware.DataResponse[[]string]:
		return []slog.Attr{slog.Int("count", len(response.Data))}
	default:
		panic("successful schedule query has unexpected response type")
	}
}

func listNotificationFields(_ *gin.Context, result middleware.HTTPResult) []slog.Attr {
	if result.Status != http.StatusOK {
		return nil
	}
	response := result.Body.(middleware.DataResponse[service.OccurrencePage])
	return []slog.Attr{
		slog.Int("count", len(response.Data.Items)),
		slog.Int("total", response.Data.Total),
	}
}

func getNotificationFields(_ *gin.Context, result middleware.HTTPResult) []slog.Attr {
	if result.Status != http.StatusOK {
		return nil
	}
	response := result.Body.(middleware.DataResponse[*service.OccurrenceView])
	return []slog.Attr{
		slog.Int("notification_id", *response.Data.NotificationID),
		slog.Int("schedule_id", response.Data.ScheduleID),
	}
}

func acknowledgeNotificationFields(_ *gin.Context, result middleware.HTTPResult) []slog.Attr {
	if result.Status != http.StatusOK {
		return nil
	}
	response := result.Body.(middleware.DataResponse[[]handler.AcknowledgementResponse])
	return []slog.Attr{slog.Int("count", len(response.Data))}
}

func healthFields(_ *gin.Context, result middleware.HTTPResult) []slog.Attr {
	if result.Status != http.StatusServiceUnavailable {
		return nil
	}
	response := result.Body.(middleware.DataResponse[handler.HealthData])
	return []slog.Attr{slog.String("code", response.Data.Error.Code)}
}
