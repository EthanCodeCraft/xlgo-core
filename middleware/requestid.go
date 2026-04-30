package middleware

import (
	"github.com/EthanCodeCraft/xlgo-core/utils"
	"github.com/gin-gonic/gin"
)

// RequestID 请求ID中间件，为每个请求生成唯一ID便于追踪
// 评分: ⭐⭐⭐⭐⭐
// 理由: 日志追踪、问题排查必备，简洁高效
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := c.GetHeader("X-Request-ID")
		if requestID == "" {
			requestID = utils.UUID()
		}
		c.Set("request_id", requestID)
		c.Header("X-Request-ID", requestID)
		c.Next()
	}
}

// GetRequestID 从上下文获取请求ID
func GetRequestID(c *gin.Context) string {
	return c.GetString("request_id")
}