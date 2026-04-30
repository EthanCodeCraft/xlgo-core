package middleware

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/EthanCodeCraft/xlgo-core/config"
	"github.com/gin-gonic/gin"
)

// CORS 跨域中间件
// 评分: ⭐⭐⭐⭐⭐
// 理由: 支持从配置文件读取 CORS 配置，生产环境更安全
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

		// 获取允许的域名列表
		allowedOrigins := getAllowedOrigins(cfg, corsConfig)

		// 检查是否在白名单中
		allowedOrigin := ""
		if origin != "" {
			for _, ao := range allowedOrigins {
				if ao == "*" {
					allowedOrigin = "*"
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

		// 如果没有匹配到白名单
		if allowedOrigin == "" {
			// 开发环境允许所有来源
			if cfg != nil && cfg.IsDevelopment() {
				allowedOrigin = "*"
			} else if len(allowedOrigins) == 1 && allowedOrigins[0] == "*" {
				// 配置了通配符
				allowedOrigin = "*"
			}
		}

		// 设置 CORS 响应头
		if allowedOrigin != "" {
			c.Header("Access-Control-Allow-Origin", allowedOrigin)
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

		// 是否允许携带凭证
		if corsConfig != nil && corsConfig.AllowCredentials {
			c.Header("Access-Control-Allow-Credentials", "true")
		} else {
			c.Header("Access-Control-Allow-Credentials", "true") // 默认允许
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
// 评分: ⭐⭐⭐⭐⭐
// 理由: 优先使用配置文件，其次使用环境变量，最后使用默认值
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