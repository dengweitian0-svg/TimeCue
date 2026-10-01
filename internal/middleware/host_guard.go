package middleware

import "github.com/gin-gonic/gin"

// HostGuard 只允许指定 Host；拒绝结果由 API 层提供，避免依赖领域错误码。
func HostGuard(expectedHost string, rejection HTTPResult) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Host != expectedHost {
			WriteHTTPResult(c, rejection, nil)
			c.Abort()
			return
		}
		c.Next()
	}
}
