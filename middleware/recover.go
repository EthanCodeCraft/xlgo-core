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
				logger.Error("panic 已恢复",
					zap.String("request_id", requestID),
					zap.String("error", fmt.Sprintf("%v", err)),
					zap.String("path", c.Request.URL.Path),
					zap.String("method", c.Request.Method),
					zap.String("stack", string(debug.Stack())),
				)

				// 返回错误响应。
				// 必须用 response.Custom 显式写 HTTP 500：FailWithCode 经 writeResp→httpStatusFor，
				// 在默认 ModeBusiness 下对 CodeServerError 返回 200，c.JSON(200,...) 会先 flush
				// 锁定状态，随后 AbortWithStatus(500) 因 w.Written()==true 沦为 no-op，
				// 导致客户端收到 HTTP 200 + body code:500，网关/APM 按 status 看不到 panic。
				// Custom 不受 Mode 影响，直接写 500 且保留 RequestID。
				//
				// M-B 修复：若 panic 发生在响应已开始写出之后（流式/部分写），c.Writer.Written()==true，
				// gin 拒绝再次写响应，response.Custom 沦为 no-op，客户端会收到截断响应而非 500。
				// 此时不再尝试写 500（无效），仅 Abort + 记录，避免无意义的二次写入。
				if !c.Writer.Written() {
					response.Custom(c, http.StatusInternalServerError, response.CodeServerError, "服务器内部错误", nil)
				} else {
					logger.Warn("panic 发生在响应已开始写入后，无法写入 500，客户端将收到截断响应",
						zap.String("request_id", requestID),
						zap.String("path", c.Request.URL.Path),
						zap.Int("bytes_written", c.Writer.Size()),
					)
				}
				c.Abort()
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

				logger.Error("panic 已恢复",
					zap.String("request_id", requestID),
					zap.String("error", fmt.Sprintf("%v", err)),
					zap.String("path", c.Request.URL.Path),
					zap.String("method", c.Request.Method),
					zap.String("stack", string(debug.Stack())),
				)

				// 开发环境返回详细错误。同样用 Custom 显式写 500（见 Recover 注释）。
				// M-B 修复：响应已写出时不重复写 500（同 Recover）。
				if !c.Writer.Written() {
					response.Custom(c, http.StatusInternalServerError, response.CodeServerError, fmt.Sprintf("服务器内部错误: %v", err), nil)
				} else {
					logger.Warn("panic 发生在响应已开始写入后，无法写入 500，客户端将收到截断响应",
						zap.String("request_id", requestID),
						zap.String("path", c.Request.URL.Path),
					)
				}
				c.Abort()
			}
		}()
		c.Next()
	}
}