package middleware

import (
	"fmt"
	"net/http"
	"runtime/debug"

	"github.com/EthanCodeCraft/xlgo-core/logger"
	"github.com/EthanCodeCraft/xlgo-core/response"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Recover panic恢复中间件，捕获panic并返回统一错误响应
func Recover() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				// 获取请求ID
				requestID := GetRequestID(c)

				// 记录错误日志
				logger.Error("Panic recovered",
					zap.String("request_id", requestID),
					zap.String("error", fmt.Sprintf("%v", err)),
					zap.String("path", c.Request.URL.Path),
					zap.String("method", c.Request.Method),
					zap.String("stack", string(debug.Stack())),
				)

				// 返回错误响应
				response.FailWithCode(c, response.CodeServerError, "服务器内部错误")
				c.AbortWithStatus(http.StatusInternalServerError)
			}
		}()
		c.Next()
	}
}

// RecoverWithDetail panic恢复中间件（详细版本，返回更多信息）
// 注意: 生产环境不应使用，会暴露敏感信息
func RecoverWithDetail() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				requestID := GetRequestID(c)

				logger.Error("Panic recovered",
					zap.String("request_id", requestID),
					zap.String("error", fmt.Sprintf("%v", err)),
					zap.String("path", c.Request.URL.Path),
					zap.String("method", c.Request.Method),
					zap.String("stack", string(debug.Stack())),
				)

				// 开发环境返回详细错误
				response.FailWithCode(c, response.CodeServerError, fmt.Sprintf("Panic: %v", err))
				c.AbortWithStatus(http.StatusInternalServerError)
			}
		}()
		c.Next()
	}
}