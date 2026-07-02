package middleware

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
)

// Timeout 请求级超时中间件（#19）。
//
// 与 http.Server.ReadTimeout（连接级）不同，这是业务级超时：
// 为每个请求的 context 设置 deadline，下游 GORM / Redis / HTTP 调用
// 走 c.Request.Context() 即可级联取消，避免单个慢请求拖垮协程。
//
// ⚠️ 软超时语义（M14 文档化）：本中间件仅注入带 deadline 的 ctx，不会主动中断 handler。
// 生效前提是下游实际消费 ctx——GORM/Redis/HTTP 客户端等会响应 ctx 取消；但若 handler
// 是纯 CPU 循环、阻塞于不查 ctx 的调用、或不读 c.Request.Context()，则 deadline 到期
// 也不会停。需要硬中断的场景请配合 http.Server.WriteTimeout 或在 handler 内显式 select ctx.Done。
//
// 用法：
//
//	r.Use(middleware.Timeout(5 * time.Second))
//
// d <= 0 时不启用超时（直接 Next）。
func Timeout(d time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		if d <= 0 {
			c.Next()
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), d)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}
