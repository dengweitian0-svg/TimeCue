package middleware

import (
	"time"

	logger "github.com/dengweitian0-svg/TimeCue/internal/log"
	"github.com/gin-gonic/gin"
)

// RequestDuration 记录从进入中间件到下游处理结束的服务端耗时。
// 应在 Host 校验和 panic 恢复中间件之前注册，以记录最终响应状态。
func RequestDuration() gin.HandlerFunc {
	return func(c *gin.Context) {
		started := time.Now()
		c.Next()
		duration := time.Since(started)
		logger.Info("HTTP request completed",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"duration_ms", float64(duration)/float64(time.Millisecond),
		)
	}
}
