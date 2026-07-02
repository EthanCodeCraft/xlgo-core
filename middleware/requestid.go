package middleware

import (
	"github.com/EthanCodeCraft/xlgo-core/utils"
	"github.com/gin-gonic/gin"
)

// 请求 ID 校验约束（M15 修复：不无条件信任客户端 X-Request-ID，防头注入/日志伪造）。
const (
	// requestIDMaxLen 客户端传入请求 ID 的最大长度。超长视为非法并重新生成，避免日志膨胀/注入。
	requestIDMaxLen = 128
)

// sanitizeRequestID 校验客户端传入的 X-Request-ID：仅允许可见 ASCII（0x20-0x7e）且
// 不含 CR/LF（防 HTTP 头注入与日志换行伪造），长度不超过 requestIDMaxLen。
// 非法或为空时返回 ""，由调用方重新生成。
func sanitizeRequestID(id string) string {
	if len(id) == 0 || len(id) > requestIDMaxLen {
		return ""
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if c < 0x20 || c > 0x7e {
			return ""
		}
	}
	return id
}

// RequestID 请求ID中间件，为每个请求生成唯一ID便于追踪。
//
// 客户端可经 X-Request-ID 头传入 ID 用于链路串联，但会做校验（M15 修复）：
// 仅接受可见 ASCII、无换行、长度 ≤128 的值，否则忽略并重新生成——防头注入与日志伪造。
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := sanitizeRequestID(c.GetHeader("X-Request-ID"))
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