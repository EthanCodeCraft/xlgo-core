package middleware

import (
	"net/http"
	"net/url"
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
				if matchOrigin(origin, ao) {
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
			} else if cfg != nil && cfg.IsDevelopment() && origin != "" && isLocalhostOrigin(origin) {
				// 开发环境兜底：仅对 localhost 来源回显具体 Origin（C7b 修复）。
				// 旧实现无条件回显任意 Origin，若同时 AllowCredentials=true 则构成凭据型反射。
				allowedOrigin = origin
			}
		}

		// 设置 CORS 响应头
		// 仅在 origin 匹配时发送 CORS 头（C7 收尾：未匹配 origin 不发 Allow-Methods/Headers 等，
		// 收敛信息泄露——避免向未授权 origin 暴露 API 允许的方法/头清单）。
		if allowedOrigin != "" {
			c.Header("Access-Control-Allow-Origin", allowedOrigin)
			// Origin 不是 "*" 时，下游缓存（CDN / 网关）必须按 Origin 区分缓存
			if allowedOrigin != "*" {
				c.Header("Vary", "Origin")
			}

			methods := corsConfig.GetAllowedMethods()
			headers := corsConfig.GetAllowedHeaders()
			exposedHeaders := corsConfig.GetExposedHeaders()
			maxAge := corsConfig.GetMaxAge()

			c.Header("Access-Control-Allow-Methods", strings.Join(methods, ", "))
			c.Header("Access-Control-Allow-Headers", strings.Join(headers, ", "))
			c.Header("Access-Control-Expose-Headers", strings.Join(exposedHeaders, ", "))
			c.Header("Access-Control-Max-Age", strconv.Itoa(maxAge))
		}

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

// matchOrigin 判断 origin 是否匹配允许的 Origin 模式 ao。
//
// 支持三种模式（C7a 修复）：
//  1. 精确匹配：ao == origin（scheme+host+port 全等）。
//  2. 通配子域：ao 形如 "*.example.com"，匹配 example.com 的任意子域（含 a.example.com、
//     a.b.example.com），但**不匹配 apex example.com 自身**，也**不匹配 notexample.com、
//     evil-example.com 等后缀相同但非真实子域的域名**。旧实现用 strings.HasSuffix(origin, domain)
//     未锚定 host 边界，导致上述绕过。
//  3. 通配 apex+子域：ao 形如 "*.example.com" 经本函数仅匹配子域；若需同时允许 apex，
//     配置中需显式列出 "https://example.com"。
//
// 解析 origin 的 host 做真实子域边界判断（而非字符串后缀），杜绝 notexample.com 类绕过。
func matchOrigin(origin, ao string) bool {
	if origin == "" || ao == "" {
		return false
	}
	// 精确匹配。
	if origin == ao {
		return true
	}
	// 通配子域 *.domain。
	if strings.HasPrefix(ao, "*.") {
		wildcardDomain := strings.ToLower(strings.TrimSuffix(ao[2:], ".")) // 如 "example.com"
		u, err := url.Parse(origin)
		if err != nil || u.Host == "" {
			return false
		}
		// 去端口、去尾点（FQDN 尾点表示 a.example.com. 应等同 a.example.com）。
		host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
		// host 必须是 *.domain 的真实子域：host == "x." + wildcardDomain，
		// 且 x 非空、不含点（即直接子域）或为多级子域（a.b.example.com）。
		// 关键：host 必须以 "." + wildcardDomain 结尾（锚定边界），杜绝 notexample.com。
		if !strings.HasSuffix(host, "."+wildcardDomain) {
			return false
		}
		// 排除 host == wildcardDomain 自身（apex 不由通配匹配，需显式配置）。
		if host == wildcardDomain {
			return false
		}
		return true
	}
	return false
}

// isLocalhostOrigin 判断 origin 是否为 localhost 来源（开发态兜底用，C7b 修复）。
// 仅允许 localhost / 127.0.0.1 / ::1 的任意端口，杜绝开发态回显任意 Origin。
func isLocalhostOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	host := strings.TrimSuffix(u.Hostname(), ".")
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
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