package utils

import (
	"context"
	"errors"
	"net/http"

	"github.com/dengweitian0-svg/TimeCue/internal/app"
	"github.com/dengweitian0-svg/TimeCue/internal/middleware"
	"github.com/dengweitian0-svg/TimeCue/internal/store"
	"github.com/gin-gonic/gin"
)

// HealthCheck 检查 API 服务依赖是否可用。
type HealthCheck func(context.Context) error

// HealthData 是健康检查接口的响应结构。
type HealthData struct {
	*app.RuntimeInfo
	Status string    `json:"status"`
	Error  *APIError `json:"error,omitempty"`
}

// HealthHandler 在每次请求时复查数据库连接和迁移结构。
func HealthHandler(check HealthCheck, info ...app.RuntimeInfo) middleware.HTTPHandler {
	var runtime *app.RuntimeInfo
	if len(info) > 0 {
		value := info[0]
		value.Capabilities = append([]string(nil), value.Capabilities...)
		runtime = &value
	}
	return func(c *gin.Context) middleware.HTTPResult {
		if err := check(c.Request.Context()); err != nil {
			detail := ErrDatabaseUnavailable
			if errors.Is(err, store.ErrSchemaInvalid) {
				detail = ErrSchemaInvalid
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
