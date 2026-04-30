package middleware

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"strings"
	"sync"

	"github.com/EthanCodeCraft/xlgo-core/response"
	"github.com/gin-gonic/gin"
)

const (
	// CSRFTokenLength CSRF Token 长度
	CSRFTokenLength = 32
	// CSRFHeaderName CSRF Header 名称
	CSRFHeaderName = "X-CSRF-Token"
	// CSRFCookieName CSRF Cookie 名称
	CSRFCookieName = "csrf_token"
	// CSRFFormField 表单字段名称
	CSRFFormField = "_csrf"
)

// CSRFConfig CSRF 配置
type CSRFConfig struct {
	// TokenLength Token 长度
	TokenLength int
	// HeaderName Header 名称
	HeaderName string
	// CookieName Cookie 名称
	CookieName string
	// FormField 表单字段名
	FormField string
	// Secure Cookie 是否启用 Secure
	Secure bool
	// HTTPOnly Cookie 是否启用 HttpOnly
	HTTPOnly bool
	// SameSite Cookie SameSite 属性
	SameSite http.SameSite
	// Domain Cookie 域名
	Domain string
	// Path Cookie 路径
	Path string
	// MaxAge Cookie 有效期（秒）
	MaxAge int
	// ErrorFunc 错误处理函数
	ErrorFunc func(c *gin.Context)
	// SkipFunc 跳过检查函数
	SkipFunc func(c *gin.Context) bool
}

// DefaultCSRFConfig 默认 CSRF 配置
var DefaultCSRFConfig = CSRFConfig{
	TokenLength: CSRFTokenLength,
	HeaderName:  CSRFHeaderName,
	CookieName:  CSRFCookieName,
	FormField:   CSRFFormField,
	Secure:      false,
	HTTPOnly:    true,
	SameSite:    http.SameSiteLaxMode,
	Path:        "/",
	MaxAge:      3600, // 1 小时
	ErrorFunc:   defaultCSRFError,
	SkipFunc:    nil,
}

// generateCSRFToken 生成 CSRF Token
func generateCSRFToken(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(bytes), nil
}

// defaultCSRFError 默认错误处理
func defaultCSRFError(c *gin.Context) {
	response.Fail(c, "CSRF Token 无效，请刷新页面重试")
	c.Abort()
}

// CSRF 创建 CSRF 中间件
func CSRF(config ...CSRFConfig) gin.HandlerFunc {
	cfg := DefaultCSRFConfig
	if len(config) > 0 {
		cfg = config[0]
	}

	// 设置默认值
	if cfg.TokenLength == 0 {
		cfg.TokenLength = CSRFTokenLength
	}
	if cfg.HeaderName == "" {
		cfg.HeaderName = CSRFHeaderName
	}
	if cfg.CookieName == "" {
		cfg.CookieName = CSRFCookieName
	}
	if cfg.ErrorFunc == nil {
		cfg.ErrorFunc = defaultCSRFError
	}

	return func(c *gin.Context) {
		// 检查是否跳过
		if cfg.SkipFunc != nil && cfg.SkipFunc(c) {
			c.Next()
			return
		}

		// 获取或生成 Token
		cookieToken, err := c.Cookie(cfg.CookieName)
		if err != nil || cookieToken == "" {
			// 生成新 Token
			token, err := generateCSRFToken(cfg.TokenLength)
			if err != nil {
				cfg.ErrorFunc(c)
				return
			}
			cookieToken = token

			// 设置 Cookie
			c.SetCookie(
				cfg.CookieName,
				token,
				cfg.MaxAge,
				cfg.Path,
				cfg.Domain,
				cfg.Secure,
				cfg.HTTPOnly,
			)
		}

		// 将 Token 存入上下文，供前端使用
		c.Set("csrf_token", cookieToken)

		// 安全方法（GET, HEAD, OPTIONS, TRACE）不需要验证
		if isSafeMethod(c.Request.Method) {
			c.Next()
			return
		}

		// 验证 Token
		clientToken := ""

		// 优先从 Header 获取
		clientToken = c.GetHeader(cfg.HeaderName)

		// 其次从表单获取
		if clientToken == "" {
			clientToken = c.PostForm(cfg.FormField)
		}

		// 最后从 JSON body 获取
		if clientToken == "" {
			var body map[string]any
			if err := c.ShouldBindJSON(&body); err == nil {
				if token, ok := body[cfg.FormField].(string); ok {
					clientToken = token
				}
			}
		}

		// 验证 Token 是否匹配
		if clientToken == "" || clientToken != cookieToken {
			cfg.ErrorFunc(c)
			return
		}

		c.Next()
	}
}

// isSafeMethod 判断是否为安全方法
func isSafeMethod(method string) bool {
	switch strings.ToUpper(method) {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	default:
		return false
	}
}

// GetCSRFToken 从上下文获取 CSRF Token
func GetCSRFToken(c *gin.Context) string {
	token, exists := c.Get("csrf_token")
	if !exists {
		return ""
	}
	return token.(string)
}

// CSRFToken 返回 CSRF Token 的处理器（用于 API 模式）
func CSRFToken(c *gin.Context) {
	token := GetCSRFToken(c)
	if token == "" {
		var err error
		token, err = generateCSRFToken(CSRFTokenLength)
		if err != nil {
			response.ServerError(c, "生成 Token 失败")
			return
		}
	}

	response.Success(c, gin.H{
		"csrf_token": token,
	})
}

// CSRFWithSkip 跳过指定路径的 CSRF 检查
func CSRFWithSkip(skipPaths []string) gin.HandlerFunc {
	cfg := DefaultCSRFConfig
	cfg.SkipFunc = func(c *gin.Context) bool {
		path := c.Request.URL.Path
		for _, p := range skipPaths {
			if strings.HasPrefix(path, p) {
				return true
			}
		}
		return false
	}
	return CSRF(cfg)
}

// CSRFForAPI 适用于 API 的 CSRF 中间件（不使用 Cookie）
// 客户端需要先调用 /csrf-token 获取 Token
func CSRFForAPI() gin.HandlerFunc {
	tokens := make(map[string]bool)
	var mu sync.RWMutex

	return func(c *gin.Context) {
		// 安全方法不需要验证
		if isSafeMethod(c.Request.Method) {
			c.Next()
			return
		}

		// 从 Header 获取 Token
		clientToken := c.GetHeader(CSRFHeaderName)
		if clientToken == "" {
			response.Fail(c, "缺少 CSRF Token")
			c.Abort()
			return
		}

		// 验证 Token
		mu.RLock()
		valid := tokens[clientToken]
		mu.RUnlock()

		if !valid {
			response.Fail(c, "CSRF Token 无效")
			c.Abort()
			return
		}

		c.Next()
	}
}

// GenerateAPIToken 生成 API CSRF Token（用于 API 模式）
func GenerateAPIToken(c *gin.Context) {
	token, err := generateCSRFToken(CSRFTokenLength)
	if err != nil {
		response.ServerError(c, "生成 Token 失败")
		return
	}

	// 存储 Token（实际应用中应使用 Redis）
	// 这里简化为内存存储
	mu.Lock()
	tokens[token] = true
	mu.Unlock()

	response.Success(c, gin.H{
		"csrf_token": token,
	})
}

// 内存存储（用于 API 模式）
var (
	tokens = make(map[string]bool)
	mu     sync.RWMutex
)

// CSRFExempt 标记路由不需要 CSRF 保护
// 使用方法：在路由组上使用此中间件
func CSRFExempt() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("csrf_exempt", true)
		c.Next()
	}
}

// CSRFWithExempt 支持 exempt 标记的 CSRF 中间件
func CSRFWithExempt(config ...CSRFConfig) gin.HandlerFunc {
	cfg := DefaultCSRFConfig
	if len(config) > 0 {
		cfg = config[0]
	}

	originalSkipFunc := cfg.SkipFunc
	cfg.SkipFunc = func(c *gin.Context) bool {
		// 检查是否标记为 exempt
		if exempt, exists := c.Get("csrf_exempt"); exists && exempt.(bool) {
			return true
		}
		// 调用原始 SkipFunc
		if originalSkipFunc != nil {
			return originalSkipFunc(c)
		}
		return false
	}

	return CSRF(cfg)
}

// DoubleSubmitCookie 双重提交 Cookie 模式（无需服务器存储）
// 适用于无状态 API
func DoubleSubmitCookie() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 安全方法不需要验证
		if isSafeMethod(c.Request.Method) {
			// 设置 Cookie（如果不存在）
			cookieToken, err := c.Cookie(CSRFCookieName)
			if err != nil || cookieToken == "" {
				token, _ := generateCSRFToken(CSRFTokenLength)
				c.SetCookie(CSRFCookieName, token, 3600, "/", "", false, true)
				c.Set("csrf_token", token)
			} else {
				c.Set("csrf_token", cookieToken)
			}
			c.Next()
			return
		}

		// 获取 Cookie 中的 Token
		cookieToken, err := c.Cookie(CSRFCookieName)
		if err != nil {
			response.Fail(c, "CSRF Token 缺失")
			c.Abort()
			return
		}

		// 获取 Header 中的 Token
		headerToken := c.GetHeader(CSRFHeaderName)
		if headerToken == "" {
			response.Fail(c, "缺少 CSRF Token")
			c.Abort()
			return
		}

		// 验证 Cookie 和 Header 中的 Token 是否一致
		if cookieToken != headerToken {
			response.Fail(c, "CSRF Token 不匹配")
			c.Abort()
			return
		}

		c.Next()
	}
}
