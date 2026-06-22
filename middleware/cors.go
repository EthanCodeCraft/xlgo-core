package middleware

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/EthanCodeCraft/xlgo-core/config"
	"github.com/gin-gonic/gin"
)

// CORS 跨域中间件
// 支持从配置文件读取 CORS 配置；遵循 W3C CORS 规范：
// 当 AllowCredentials=true 时，Access-Control-Allow-Origin 必须回显为具体 Origin，
// 不能使用 "*"（浏览器会拒绝携带凭证的响应）。
func CORS() gin.HandlerFunc {
	return CORSWithConfig(nil)
}

// CORSWithConfig 使用自定义配置的 CORS 中间件
func CORSWithConfig(corsCfg *config.CORSConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")

		// 获取配置
		cfg := config.Get()
		var corsConfig *config.CORSConfig
		if corsCfg != nil {
			corsConfig = corsCfg
		} else if cfg != nil {
			corsConfig = &cfg.CORS
		}

		// 是否允许携带凭证（影响 Origin 回显策略）
		allowCredentials := corsConfig != nil && corsConfig.AllowCredentials

		// 获取允许的域名列表
		allowedOrigins := getAllowedOrigins(cfg, corsConfig)

		// 匹配 Origin
		// 注意：当 allowCredentials=true 且匹配到通配符时，
		// 必须回显具体 Origin 而非 "*"，否则浏览器会拒绝响应。
		allowedOrigin := ""
		matchedWildcard := false
		if origin != "" {
			for _, ao := range allowedOrigins {
				if ao == "*" {
					matchedWildcard = true
					break
				}
				// 支持通配符匹配（如 *.example.com）
				if strings.HasPrefix(ao, "*.") {
					domain := ao[2:]
					if strings.HasSuffix(origin, domain) {
						allowedOrigin = origin
						break
					}
				}
				if ao == origin {
					allowedOrigin = origin
					break
				}
			}
		}

		// 处理通配符 + 兜底策略
		if allowedOrigin == "" {
			if matchedWildcard {
				if allowCredentials && origin != "" {
					// AllowCredentials=true 时 spec 禁止 "*"，回显具体 Origin
					allowedOrigin = origin
				} else {
					allowedOrigin = "*"
				}
			} else if cfg != nil && cfg.IsDevelopment() && origin != "" {
				// 开发环境兜底：回显具体 Origin（兼容 credentials）
				allowedOrigin = origin
			}
		}

		// 设置 CORS 响应头
		if allowedOrigin != "" {
			c.Header("Access-Control-Allow-Origin", allowedOrigin)
			// Origin 不是 "*" 时，下游缓存（CDN / 网关）必须按 Origin 区分缓存
			if allowedOrigin != "*" {
				c.Header("Vary", "Origin")
			}
		}

		// 从配置获取允许的方法、请求头等
		methods := corsConfig.GetAllowedMethods()
		headers := corsConfig.GetAllowedHeaders()
		exposedHeaders := corsConfig.GetExposedHeaders()
		maxAge := corsConfig.GetMaxAge()

		c.Header("Access-Control-Allow-Methods", strings.Join(methods, ", "))
		c.Header("Access-Control-Allow-Headers", strings.Join(headers, ", "))
		c.Header("Access-Control-Expose-Headers", strings.Join(exposedHeaders, ", "))
		c.Header("Access-Control-Max-Age", strconv.Itoa(maxAge))

		// 仅在显式启用且 Origin 不是 "*" 时才发 Allow-Credentials
		// （CORS 规范：Allow-Origin: * 时禁止携带凭证）
		if allowCredentials && allowedOrigin != "" && allowedOrigin != "*" {
			c.Header("Access-Control-Allow-Credentials", "true")
		}

		// 处理预检请求
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}

// getAllowedOrigins 获取允许的域名列表
// 优先使用配置文件，生产环境必须显式配置，开发环境提供 localhost 兜底。
func getAllowedOrigins(cfg *config.Config, corsConfig *config.CORSConfig) []string {
	// 优先使用 CORS 配置
	if corsConfig != nil && len(corsConfig.AllowedOrigins) > 0 {
		return corsConfig.AllowedOrigins
	}

	// 生产环境：必须配置具体的域名
	if cfg != nil && cfg.IsProduction() {
		// 返回空列表，生产环境必须配置
		return []string{}
	}

	// 开发环境允许 localhost
	return []string{
		"http://localhost:3000",
		"http://localhost:5173",
		"http://localhost:8080",
		"http://localhost:4200",
		"http://127.0.0.1:3000",
		"http://127.0.0.1:5173",
		"http://127.0.0.1:8080",
		"http://127.0.0.1:4200",
	}
}

// CORSWithOrigins 使用指定域名列表的 CORS 中间件
func CORSWithOrigins(origins []string) gin.HandlerFunc {
	return CORSWithConfig(&config.CORSConfig{
		AllowedOrigins: origins,
	})
}

// CORSWithWildcard 允许所有来源的 CORS 中间件（仅用于开发环境）
// 注意：生产环境不建议使用
func CORSWithWildcard() gin.HandlerFunc {
	return CORSWithConfig(&config.CORSConfig{
		AllowedOrigins: []string{"*"},
	})
}

// CORSForAPI 适用于 API 的 CORS 中间件
func CORSForAPI() gin.HandlerFunc {
	return CORSWithConfig(&config.CORSConfig{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE"},
		AllowedHeaders:   []string{"Content-Type", "Authorization", "X-Requested-With"},
		AllowCredentials: false, // API 模式不允许凭证
	})
}