package middleware

import "github.com/gin-gonic/gin"

// Recovery 恢复下游 panic，必须注册在 HTTPLogger 之后。
func Recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if recovered := recover(); recovered != nil {
				WriteHTTPPanic(c, recovered)
			}
		}()
		c.Next()
	}
}

// WriteHTTPPanic 保留原始 panic 和已提交的响应；未提交时输出安全的 500。
func WriteHTTPPanic(c *gin.Context, recovered any) {
	setHTTPLogDetails(c, httpLogDetails{
		Panicked:   true,
		PanicValue: recovered,
	})
	if !c.Writer.Written() {
		WriteHTTPResult(c, internalErrorResult(nil), nil)
	}
	c.Abort()
}
