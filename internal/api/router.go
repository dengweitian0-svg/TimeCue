package api

import (
	"log/slog"
	"net/http"
	"strconv"

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
	scheduler.POST("/schedules", middleware.WrapHTTPHandler(handler.CreateScheduleHandler(app), createScheduleFields))
	scheduler.GET("/schedules", middleware.WrapHTTPHandler(handler.ListScheduleHandler(app), listScheduleFields))
	scheduler.GET("/schedules/:id", middleware.WrapHTTPHandler(handler.GetScheduleHandler(app), getScheduleFields))
	scheduler.DELETE("/schedules/:id", middleware.WrapHTTPHandler(handler.DeleteScheduleHandler(app), deleteScheduleFields))
	return router
}

// 提取规则只读取明确的响应 DTO 和已校验的请求事实，不解析 JSON。
func createScheduleFields(_ *gin.Context, result middleware.HTTPResult) []slog.Attr {
	if result.Status != http.StatusCreated {
		return nil
	}
	response := result.Body.(middleware.DataResponse[handler.ScheduleResponse])
	return []slog.Attr{
		slog.Int("schedule_id", response.Data.ID),
		slog.Any("type", response.Data.Type),
	}
}

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
	response := result.Body.(handler.ScheduleListResponse)
	return []slog.Attr{
		slog.Int("count", len(response.Data)),
		slog.Int("limit", response.Limit),
		slog.Int("offset", response.Offset),
	}
}

func deleteScheduleFields(c *gin.Context, result middleware.HTTPResult) []slog.Attr {
	if result.Status != http.StatusNoContent {
		return nil
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		panic("successful deletion has invalid schedule id")
	}
	return []slog.Attr{slog.Int("schedule_id", id)}
}

func healthFields(_ *gin.Context, result middleware.HTTPResult) []slog.Attr {
	if result.Status != http.StatusServiceUnavailable {
		return nil
	}
	response := result.Body.(middleware.DataResponse[handler.HealthData])
	return []slog.Attr{slog.String("code", response.Data.Error.Code)}
}
