package api

import (
	"github.com/dengweitian0-svg/TimeCue/internal/api/handler"
	logger "github.com/dengweitian0-svg/TimeCue/internal/log"
	"github.com/dengweitian0-svg/TimeCue/internal/service"
	"github.com/gin-gonic/gin"
)

// NewRouter 注册本地健康检查和日程 HTTP 接口。
func NewRouter(app *service.App, healthCheck handler.HealthCheck) *gin.Engine {
	router := gin.New()
	router.HandleMethodNotAllowed = true
	router.RedirectTrailingSlash = false
	router.Use(func(c *gin.Context) {
		if c.Request.Host != "127.0.0.1:10086" {
			handler.WriteForbiddenHost(c)
			c.Abort()
			return
		}
		c.Next()
	})
	router.Use(func(c *gin.Context) {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.Error("HTTP panic", "error", recovered)
				if !c.Writer.Written() {
					handler.WriteInternalError(c)
				}
				c.Abort()
			}
		}()
		c.Next()
	})
	router.NoRoute(func(c *gin.Context) { handler.WriteRouteNotFound(c) })
	router.NoMethod(func(c *gin.Context) {
		handler.WriteMethodNotAllowed(c)
	})
	router.GET("/health", handler.HealthHandler(healthCheck))

	scheduler := router.Group("/api/v1/scheduler")
	scheduler.POST("/schedules", handler.CreateScheduleHandler(app))
	scheduler.GET("/schedules", handler.ListScheduleHandler(app))
	scheduler.GET("/schedules/:id", handler.GetScheduleHandler(app))
	scheduler.DELETE("/schedules/:id", handler.DeleteScheduleHandler(app))
	return router
}
