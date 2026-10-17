package api

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/dengweitian0-svg/TimeCue/internal/api/handler/schedule"
	"github.com/dengweitian0-svg/TimeCue/internal/api/handler/utils"
	application "github.com/dengweitian0-svg/TimeCue/internal/app"
	"github.com/dengweitian0-svg/TimeCue/internal/middleware"
	"github.com/dengweitian0-svg/TimeCue/internal/model"
	"github.com/gin-gonic/gin"
)

// NewRouter 注册本地业务接口；聊天路由仅在 App 提供聊天服务时启用。
func NewRouter(app *application.App, healthCheck utils.HealthCheck, info ...application.RuntimeInfo) *gin.Engine {
	return NewRouterWithPort(app, healthCheck, 10086, info...)
}

// NewRouterWithPort 注册绑定指定本机端口的业务接口。
func NewRouterWithPort(app *application.App, healthCheck utils.HealthCheck, port int, info ...application.RuntimeInfo) *gin.Engine {
	router := gin.New()
	router.HandleMethodNotAllowed = true
	router.RedirectTrailingSlash = false
	router.Use(middleware.HTTPLogger())
	router.Use(middleware.HostGuard("127.0.0.1:"+strconv.Itoa(port), utils.ForbiddenHostResultForPort(port)))
	router.Use(middleware.Recovery())
	router.NoRoute(middleware.WrapHTTPHandler(func(*gin.Context) middleware.HTTPResult {
		return utils.RouteNotFoundResult()
	}, nil))
	router.NoMethod(middleware.WrapHTTPHandler(func(*gin.Context) middleware.HTTPResult {
		return utils.MethodNotAllowedResult()
	}, nil))
	router.GET("/health", middleware.WrapHTTPHandler(utils.HealthHandler(healthCheck, info...), healthFields))

	scheduler := router.Group("/api/v1/scheduler")
	scheduler.POST("/schedules", middleware.WrapHTTPHandler(schedule.CreateScheduleHandler(app), nil))
	scheduler.GET("/schedules", middleware.WrapHTTPHandler(schedule.ListScheduleHandler(app), listScheduleFields))
	scheduler.GET("/schedules/:id", middleware.WrapHTTPHandler(schedule.GetScheduleHandler(app), getScheduleFields))
	scheduler.PUT("/schedules/:id", middleware.WrapHTTPHandler(schedule.UpdateScheduleHandler(app), nil))
	scheduler.DELETE("/schedules/:id", middleware.WrapHTTPHandler(schedule.DeleteScheduleHandler(app), nil))

	notify := router.Group("/api/v1")
	notify.GET("/notifications", middleware.WrapHTTPHandler(schedule.NotificationsHandler(app), listNotificationFields))
	notify.GET("/notifications/:id", middleware.WrapHTTPHandler(schedule.NotificationHandler(app), getNotificationFields))
	notify.POST("/notifications/acknowledgements", middleware.WrapHTTPHandler(schedule.AcknowledgementsHandler(app), acknowledgeNotificationFields))
	notify.POST("/notifications/claim", middleware.WrapHTTPHandler(schedule.ClaimDeliveryHandler(app), nil))
	runtimeInfo := application.RuntimeInfo{}
	if len(info) > 0 {
		runtimeInfo = info[0]
	}
	notify.POST("/notifications/executor-exit", middleware.WrapHTTPHandler(schedule.DeliveryExecutorExitHandler(app, runtimeInfo), nil))
	notify.POST("/notifications/delivery-attempts/:id/start", middleware.WrapHTTPHandler(schedule.StartDeliveryHandler(app), nil))
	notify.POST("/notifications/delivery-attempts/:id/results", middleware.WrapHTTPHandler(schedule.DeliveryResultsHandler(app), nil))
	if app != nil && app.ChatService != nil {
		registerChatRoutes(router, app.ChatService)
	}
	return router
}

// 提取规则只读取明确的响应 DTO 和已校验的请求事实，不解析 JSON。
func getScheduleFields(_ *gin.Context, result middleware.HTTPResult) []slog.Attr {
	if result.Status != http.StatusOK {
		return nil
	}
	response := result.Body.(middleware.DataResponse[schedule.ScheduleResponse])
	return []slog.Attr{slog.Int("schedule_id", response.Data.ID)}
}

func listScheduleFields(_ *gin.Context, result middleware.HTTPResult) []slog.Attr {
	if result.Status != http.StatusOK {
		return nil
	}
	switch response := result.Body.(type) {
	case schedule.ScheduleListResponse:
		return []slog.Attr{
			slog.Int("count", len(response.Data)),
			slog.Int("limit", response.Limit),
			slog.Int("offset", response.Offset),
		}
	case middleware.DataResponse[model.OccurrencePage]:
		return []slog.Attr{slog.Int("count", len(response.Data.Items))}
	case middleware.DataResponse[*model.ScheduleQueryResult]:
		return append([]slog.Attr{slog.Int("count", len(response.Data.Occurrences.Items))}, recoveryFields(response.Data.Recovery)...)
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
	response := result.Body.(middleware.DataResponse[model.OccurrencePage])
	return append([]slog.Attr{
		slog.Int("count", len(response.Data.Items)),
		slog.Int("total", response.Data.Total),
	}, recoveryFields(response.Data.Recovery)...)
}

func recoveryFields(view *model.RecoveryView) []slog.Attr {
	if view == nil {
		return nil
	}
	return []slog.Attr{slog.String("recovery_id", view.RecoveryID), slog.String("recovery_state", view.State), slog.Int("materialized_count", view.MaterializedCount)}
}

func getNotificationFields(_ *gin.Context, result middleware.HTTPResult) []slog.Attr {
	if result.Status != http.StatusOK {
		return nil
	}
	response := result.Body.(middleware.DataResponse[*model.OccurrenceView])
	return []slog.Attr{
		slog.Int("notification_id", *response.Data.NotificationID),
		slog.Int("schedule_id", response.Data.ScheduleID),
	}
}

func acknowledgeNotificationFields(_ *gin.Context, result middleware.HTTPResult) []slog.Attr {
	if result.Status != http.StatusOK {
		return nil
	}
	response := result.Body.(middleware.DataResponse[[]schedule.AcknowledgementResponse])
	return []slog.Attr{slog.Int("count", len(response.Data))}
}

func healthFields(_ *gin.Context, result middleware.HTTPResult) []slog.Attr {
	if result.Status != http.StatusServiceUnavailable {
		return nil
	}
	response := result.Body.(middleware.DataResponse[utils.HealthData])
	return []slog.Attr{slog.String("code", response.Data.Error.Code)}
}
