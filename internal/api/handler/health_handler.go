package handler

import (
	"context"
	"errors"
	"net/http"

	"github.com/dengweitian0-svg/TimeCue/internal/middleware"
	"github.com/dengweitian0-svg/TimeCue/internal/service"
	"github.com/dengweitian0-svg/TimeCue/internal/store"
	"github.com/gin-gonic/gin"
)

type HealthCheck func(context.Context) error

type HealthData struct {
	*service.RuntimeInfo
	Status string    `json:"status"`
	Error  *apiError `json:"error,omitempty"`
}

// HealthHandler 在每次请求时复查数据库连接和迁移结构。
func HealthHandler(check HealthCheck, info ...service.RuntimeInfo) middleware.HTTPHandler {
	var runtime *service.RuntimeInfo
	if len(info) > 0 {
		value := info[0]
		value.Capabilities = append([]string(nil), value.Capabilities...)
		runtime = &value
	}
	return func(c *gin.Context) middleware.HTTPResult {
		if err := check(c.Request.Context()); err != nil {
			detail := errDatabaseUnavailable
			if errors.Is(err, store.ErrSchemaInvalid) {
				detail = errSchemaInvalid
			}
			result := middleware.DataResult(http.StatusServiceUnavailable, HealthData{
				RuntimeInfo: runtime,
				Status:      "error",
				Error:       &detail,
			})
			result.Cause = err
			return result
		}
		return middleware.DataResult(http.StatusOK, HealthData{
			RuntimeInfo: runtime,
			Status:      "ok",
		})
	}
}
