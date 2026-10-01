package handler

import (
	"context"
	"errors"
	"net/http"

	logger "github.com/dengweitian0-svg/TimeCue/internal/log"
	"github.com/dengweitian0-svg/TimeCue/internal/store"
	"github.com/gin-gonic/gin"
)

type HealthCheck func(context.Context) error

type healthData struct {
	Status string    `json:"status"`
	Error  *apiError `json:"error,omitempty"`
}

// HealthHandler 在每次请求时复查数据库连接和迁移结构。
func HealthHandler(check HealthCheck) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := check(c.Request.Context()); err != nil {
			detail := errDatabaseUnavailable
			if errors.Is(err, store.ErrSchemaInvalid) {
				detail = errSchemaInvalid
			}
			logger.Error("HTTP health check failed", "code", detail.Code, "error", err)
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"data": healthData{
					Status: "error",
					Error:  &detail,
				},
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"data": healthData{
				Status: "ok",
			},
		})
		logger.Debug("HTTP health check passed")
	}
}
