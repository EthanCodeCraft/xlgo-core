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
