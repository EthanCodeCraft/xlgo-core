package middleware

import (
	"bytes"
	"io"
	"strings"
	"time"

	"github.com/EthanCodeCraft/xlgo-core/logger"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// LoggerConfig 日志中间件配置
type LoggerConfig struct {
	// LogRequestBody 是否记录请求体
	LogRequestBody bool
	// LogResponseBody 是否记录响应体
	LogResponseBody bool
	// MaxBodyLength 最大记录的请求/响应体长度（字节）
	MaxBodyLength int
	// SkipPaths 不记录日志的路径
	SkipPaths []string
	// SkipPathPrefixes 不记录日志的路径前缀
	SkipPathPrefixes []string
	// SlowRequestThreshold 慢请求阈值（超过此时间记录警告）
	SlowRequestThreshold time.Duration
}

// DefaultLoggerConfig 默认日志配置
var DefaultLoggerConfig = LoggerConfig{
	LogRequestBody:       false, // 默认不记录请求体（敏感信息风险）
	LogResponseBody:      false, // 默认不记录响应体
	MaxBodyLength:        1024,  // 最大 1KB
	SkipPaths:            []string{"/health", "/swagger"},
	SkipPathPrefixes:     []string{"/public", "/static"},
	SlowRequestThreshold: 500 * time.Millisecond,
}

// Logger 日志中间件（使用默认配置）
func Logger() gin.HandlerFunc {
	return LoggerWithConfig(DefaultLoggerConfig)
}

// LoggerWithConfig 使用自定义配置的日志中间件
func LoggerWithConfig(cfg LoggerConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 检查是否跳过此路径
		path := c.Request.URL.Path
		if shouldSkipPath(path, cfg) {
			c.Next()
			return
		}

		start := time.Now()

		// 记录请求体（可选）
		var requestBody []byte
		if cfg.LogRequestBody && c.Request.Body != nil {
			requestBody, _ = io.ReadAll(c.Request.Body)
			// 恢复请求体供后续处理
			c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
			// 限制记录长度
			if len(requestBody) > cfg.MaxBodyLength {
				requestBody = requestBody[:cfg.MaxBodyLength]
			}
		}

		// 记录响应体（可选）
		var responseBody []byte
		if cfg.LogResponseBody {
			// 使用 ResponseWriter 包装器捕获响应体
			blw := &bodyLogWriter{body: bytes.NewBufferString(""), ResponseWriter: c.Writer}
			c.Writer = blw
			c.Next()
			responseBody = blw.body.Bytes()
			if len(responseBody) > cfg.MaxBodyLength {
				responseBody = responseBody[:cfg.MaxBodyLength]
			}
		} else {
			c.Next()
		}

		latency := time.Since(start)

		// 构建日志字段
		fields := []zap.Field{
			zap.Int("status", c.Writer.Status()),
			zap.String("method", c.Request.Method),
			zap.String("path", path),
			zap.String("query", c.Request.URL.RawQuery),
			zap.String("ip", c.ClientIP()),
			zap.Duration("latency", latency),
			zap.String("user-agent", c.Request.UserAgent()),
			zap.Int("body_size", c.Writer.Size()),
			zap.String("request_id", GetRequestID(c)),
		}

		// 添加请求体
		if cfg.LogRequestBody && len(requestBody) > 0 {
			// 过滤敏感字段
			safeBody := filterSensitiveFields(requestBody)
			fields = append(fields, zap.String("request_body", safeBody))
		}

		// 添加响应体
		if cfg.LogResponseBody && len(responseBody) > 0 {
			fields = append(fields, zap.String("response_body", string(responseBody)))
		}

		// 添加用户信息（如果已登录）
		userID := GetUserID(c)
		if userID > 0 {
			fields = append(fields,
				zap.Uint("user_id", userID),
				zap.String("username", GetUsername(c)),
				zap.String("user_type", GetUserType(c)),
			)
		}

		// 根据状态码和延迟选择日志级别
		status := c.Writer.Status()

		if latency > cfg.SlowRequestThreshold {
			// 慢请求警告
			fields = append(fields, zap.Bool("slow_request", true))
			logger.APILog().Warn("Slow API Request", fields...)
		} else if status >= 500 {
			logger.APILog().Error("API Request Error", fields...)
		} else if status >= 400 {
			logger.APILog().Warn("API Request Client Error", fields...)
		} else {
			logger.APILog().Info("API Request", fields...)
		}
	}
}

// bodyLogWriter 响应体记录包装器
type bodyLogWriter struct {
	gin.ResponseWriter
	body *bytes.Buffer
}

// Write 捕获响应体
func (w *bodyLogWriter) Write(b []byte) (int, error) {
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}

// WriteString 捕获字符串响应
func (w *bodyLogWriter) WriteString(s string) (int, error) {
	w.body.WriteString(s)
	return w.ResponseWriter.WriteString(s)
}

// shouldSkipPath 检查是否跳过路径
func shouldSkipPath(path string, cfg LoggerConfig) bool {
	// 检查完整路径
	for _, p := range cfg.SkipPaths {
		if path == p {
			return true
		}
	}

	// 检查路径前缀
	for _, prefix := range cfg.SkipPathPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}

	return false
}

// filterSensitiveFields 过滤敏感字段（密码、token等）
func filterSensitiveFields(body []byte) string {
	bodyStr := string(body)

	// 过滤常见敏感字段（简单字符串替换）
	sensitiveFields := []string{
		"password", "passwd", "pwd",
		"token", "access_token", "refresh_token",
		"secret", "api_key", "apikey",
		"credit_card", "card_number",
	}

	for _, field := range sensitiveFields {
		// 检查是否包含敏感字段
		keyPattern := `"` + field + `":`
		if strings.Contains(bodyStr, keyPattern) {
			// 简单替换：将值替换为 [FILTERED]
			// 注意：这是一个简化的实现，复杂的 JSON 可能需要更精确的处理
			bodyStr = strings.ReplaceAll(bodyStr, keyPattern, keyPattern+"\"[FILTERED]\"")
		}
	}

	return bodyStr
}

// LoggerForAPI API 专用日志中间件（更详细）
func LoggerForAPI() gin.HandlerFunc {
	cfg := DefaultLoggerConfig
	cfg.LogRequestBody = true
	cfg.LogResponseBody = false // 响应体通常较大
	return LoggerWithConfig(cfg)
}

// LoggerForDebug 调试专用日志中间件（最详细）
func LoggerForDebug() gin.HandlerFunc {
	cfg := LoggerConfig{
		LogRequestBody:       true,
		LogResponseBody:      true,
		MaxBodyLength:        4096, // 4KB
		SkipPaths:            []string{"/health"},
		SkipPathPrefixes:     []string{},
		SlowRequestThreshold: 200 * time.Millisecond,
	}
	return LoggerWithConfig(cfg)
}

// LoggerMinimal 最简日志中间件（只记录基本信息）
func LoggerMinimal() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path

		// 跳过健康检查和静态资源
		if path == "/health" || strings.HasPrefix(path, "/public") || strings.HasPrefix(path, "/static") {
			c.Next()
			return
		}

		start := time.Now()
		c.Next()

		logger.APILog().Info("Request",
			zap.Int("status", c.Writer.Status()),
			zap.String("method", c.Request.Method),
			zap.String("path", path),
			zap.String("ip", c.ClientIP()),
			zap.Duration("latency", time.Since(start)),
		)
	}
}