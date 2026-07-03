package middleware

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/EthanCodeCraft/xlgo-core/response"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
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

		// 最后从 JSON body 获取。
		// P1 #9：用 ShouldBindBodyWith(binding.JSON) 而非 ShouldBindJSON——后者会读干
		// c.Request.Body，导致下游 handler 再 ShouldBindJSON 拿到空 body(EOF)。前者把原始
		// body 缓存进 gin context，下游可重复读取。
		if clientToken == "" {
			var body map[string]any
			if err := c.ShouldBindBodyWith(&body, binding.JSON); err == nil {
				if token, ok := body[cfg.FormField].(string); ok {
					clientToken = token
				}
			}
		}

		// 验证 Token 是否匹配（H-7: 恒定时间比较防时序侧信道）
		if len(clientToken) == 0 || subtle.ConstantTimeCompare([]byte(clientToken), []byte(cookieToken)) != 1 {
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
	// H-7: comma-ok 防裸断言 panic。csrf_token 虽由本包以 string 写入，
	// 但 gin context 是共享 map，下游误写其他类型即 panic。
	s, ok := token.(string)
	if !ok {
		return ""
	}
	return s
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
// 客户端需要先调用 GenerateAPIToken 获取 Token，随后在每个非安全方法请求的
// X-CSRF-Token 头中携带。Token 单次消费（验证通过即删除）且受 TTL 约束，
// 防止重放与内存无限增长。
//
// 注意：存储为进程内内存，仅适用于单实例部署。多实例请自行用 Redis
// SETEX + GETDEL 实现等价语义。
func CSRFForAPI() gin.HandlerFunc {
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

		// 单次消费 + TTL：校验通过即删除，防止同一 token 被重放。
		// 写锁内完成“查—删—过期清理”以保证原子性。
		apiTokensMu.Lock()
		issuedAt, ok := apiTokens[clientToken]
		if ok {
			delete(apiTokens, clientToken)
		}
		// 懒清理：map 较大时顺带淘汰过期项，避免内存无限增长。
		if len(apiTokens) > 256 {
			now := time.Now()
			for t, at := range apiTokens {
				if now.Sub(at) > apiTokenTTL {
					delete(apiTokens, t)
				}
			}
		}
		apiTokensMu.Unlock()

		if !ok || time.Since(issuedAt) > apiTokenTTL {
			response.Fail(c, "CSRF Token 无效")
			c.Abort()
			return
		}

		c.Next()
	}
}

// GenerateAPIToken 生成 API CSRF Token（用于 API 模式）
// 颁发的 Token 写入进程内存储，供 CSRFForAPI 校验。
func GenerateAPIToken(c *gin.Context) {
	token, err := generateCSRFToken(CSRFTokenLength)
	if err != nil {
		response.ServerError(c, "生成 Token 失败")
		return
	}

	apiTokensMu.Lock()
	apiTokens[token] = time.Now()
	apiTokensMu.Unlock()

	response.Success(c, gin.H{
		"csrf_token": token,
	})
}

// API 模式 CSRF Token 存储：token -> 颁发时间。
// 受 apiTokensMu 保护，单次消费 + TTL（见 CSRFForAPI）。
var (
	apiTokens   = make(map[string]time.Time)
	apiTokensMu sync.RWMutex
)

// apiTokenTTL API 模式 CSRF Token 有效期
const apiTokenTTL = 30 * time.Minute

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
		// 检查是否标记为 exempt（P1 #9：comma-ok 防裸断言 panic——gin context 为共享 map，
		// 下游若误以非 bool 写入 csrf_exempt，exempt.(bool) 会 panic）。
		if exempt, exists := c.Get("csrf_exempt"); exists {
			if b, ok := exempt.(bool); ok && b {
				return true
			}
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
				// 双重提交模式要求前端 JS 读取 cookie 并回填 X-CSRF-Token 头，
				// 故 HttpOnly 必须为 false（与 CSRF() cookie 模式相反）。
				c.SetCookie(CSRFCookieName, token, 3600, "/", "", false, false)
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

		// 验证 Cookie 和 Header 中的 Token 是否一致（H-7: 恒定时间比较防时序侧信道）
		if subtle.ConstantTimeCompare([]byte(cookieToken), []byte(headerToken)) != 1 {
			response.Fail(c, "CSRF Token 不匹配")
			c.Abort()
			return
		}

		c.Next()
	}
}
