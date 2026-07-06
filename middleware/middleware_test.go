package middleware_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EthanCodeCraft/xlgo-core/config"
	"github.com/EthanCodeCraft/xlgo-core/database"
	"github.com/EthanCodeCraft/xlgo-core/logger"
	"github.com/EthanCodeCraft/xlgo-core/middleware"
	"github.com/EthanCodeCraft/xlgo-core/response"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// respCode 解析统一响应体中的业务 code 字段。
func respCode(t *testing.T, w *httptest.ResponseRecorder) int {
	t.Helper()
	var r response.Response
	if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil {
		t.Fatalf("unmarshal response %q: %v", w.Body.String(), err)
	}
	return r.Code
}

func setupTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	return r
}

// ===== RequestID Tests =====

func TestRequestID(t *testing.T) {
	r := setupTestRouter()
	r.Use(middleware.RequestID())
	r.GET("/test", func(c *gin.Context) {
		id := middleware.GetRequestID(c)
		c.JSON(200, gin.H{"request_id": id})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	// 验证响应头有 X-Request-ID
	headerID := w.Header().Get("X-Request-ID")
	if headerID == "" {
		t.Error("X-Request-ID header should be set")
	}

	// 验证响应体中的 request_id
	if w.Code != 200 {
		t.Errorf("RequestID status = %d", w.Code)
	}
}

func TestRequestIDWithExisting(t *testing.T) {
	r := setupTestRouter()
	r.Use(middleware.RequestID())
	r.GET("/test", func(c *gin.Context) {
		id := middleware.GetRequestID(c)
		c.JSON(200, gin.H{"request_id": id})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Request-ID", "custom-id-123")
	r.ServeHTTP(w, req)

	// 验证使用了传入的 ID
	headerID := w.Header().Get("X-Request-ID")
	if headerID != "custom-id-123" {
		t.Errorf("X-Request-ID = %s, want custom-id-123", headerID)
	}
}

func TestGetRequestIDEmpty(t *testing.T) {
	r := setupTestRouter()
	r.GET("/test", func(c *gin.Context) {
		id := middleware.GetRequestID(c)
		c.JSON(200, gin.H{"request_id": id})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	// 没有 RequestID 中间件时，返回空
	if w.Code != 200 {
		t.Errorf("status = %d", w.Code)
	}
}

// TestRequestIDSanitizesClientHeader_M15：含换行/超长的客户端 X-Request-ID 应被忽略并重新生成，
// 防头注入与日志伪造。合法 ASCII ID 仍沿用。
func TestRequestIDSanitizesClientHeader_M15(t *testing.T) {
	r := setupTestRouter()
	r.Use(middleware.RequestID())
	r.GET("/test", func(c *gin.Context) {
		c.JSON(200, gin.H{"request_id": middleware.GetRequestID(c)})
	})

	// 含 CRLF 的非法 ID 应被忽略、重新生成（非空、无换行）。
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Request-ID", "evil\r\nX-Forged: 1")
	r.ServeHTTP(w, req)
	got := w.Header().Get("X-Request-ID")
	if strings.Contains(got, "\n") || strings.Contains(got, "\r") || got == "" {
		t.Errorf("CRLF-injected request id not sanitized, got %q", got)
	}

	// 超长 ID 应被忽略、重新生成。
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/test", nil)
	req2.Header.Set("X-Request-ID", strings.Repeat("a", 200))
	r.ServeHTTP(w2, req2)
	if len(w2.Header().Get("X-Request-ID")) > 128 {
		t.Errorf("overlong request id not regenerated, got len %d", len(w2.Header().Get("X-Request-ID")))
	}

	// 合法 ASCII ID 仍沿用客户端值。
	w3 := httptest.NewRecorder()
	req3 := httptest.NewRequest("GET", "/test", nil)
	req3.Header.Set("X-Request-ID", "trace-abc-123")
	r.ServeHTTP(w3, req3)
	if w3.Header().Get("X-Request-ID") != "trace-abc-123" {
		t.Errorf("legit request id should be preserved, got %q", w3.Header().Get("X-Request-ID"))
	}
}

// ===== Recover Tests =====

func TestRecover(t *testing.T) {
	// 需要初始化 logger，否则 Recover 会 panic
	// 这里跳过完整测试，仅验证中间件可以正常注册
	r := setupTestRouter()
	r.Use(middleware.Recover())
	r.GET("/normal", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/normal", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("Normal status = %d, want 200", w.Code)
	}
}

func TestRecoverNoPanic(t *testing.T) {
	r := setupTestRouter()
	r.Use(middleware.Recover())
	r.GET("/normal", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/normal", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("Normal status = %d, want 200", w.Code)
	}
}

// ensureNopLogger 把全局 logger 重置为 Nop，避免 Recover 内 logger.Error
// 在 logger 未初始化时 nil deref 二次 panic 干扰断言。
func ensureNopLogger() {
	_ = logger.Close() // Close 后 Logger/apiLog/dbLog 均为 zap.NewNop()，写日志安全。
}

// 回归 C8：默认 ModeBusiness 下，真实触发 panic 必须返回 HTTP 500（而非 200）。
// 修复前：FailWithCode 经 writeResp 在 ModeBusiness 下写 200 并 flush，
// 随后 AbortWithStatus(500) 因 w.Written()==true 沦为 no-op，客户端收 200 + body code:500。
func TestRecoverPanicReturns500(t *testing.T) {
	ensureNopLogger()
	response.SetMode(response.ModeBusiness) // 默认模式，复现 bug 的模式
	defer response.SetMode(response.ModeBusiness)

	r := setupTestRouter()
	r.Use(middleware.RequestID(), middleware.Recover())
	r.GET("/panic", func(c *gin.Context) { panic("boom") })

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/panic", nil)
	r.ServeHTTP(w, req)

	if w.Code != 500 {
		t.Errorf("panic status = %d, want 500 (C8: ModeBusiness must not swallow 500 into 200)", w.Code)
	}
	if got := respCode(t, w); got != response.CodeServerError {
		t.Errorf("panic body code = %d, want %d", got, response.CodeServerError)
	}
	// Custom 保留 RequestID，便于链路追踪。
	var r2 response.Response
	if err := json.Unmarshal(w.Body.Bytes(), &r2); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if r2.RequestID == "" {
		t.Error("panic response must carry request_id for tracing")
	}
}

// 回归 C8：RecoverWithDetail 同病同治——真实 panic 必须返回 500。
func TestRecoverWithDetailPanicReturns500(t *testing.T) {
	ensureNopLogger()
	response.SetMode(response.ModeBusiness)
	defer response.SetMode(response.ModeBusiness)

	r := setupTestRouter()
	r.Use(middleware.RequestID(), middleware.RecoverWithDetail())
	r.GET("/panic", func(c *gin.Context) { panic("boom") })

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/panic", nil)
	r.ServeHTTP(w, req)

	if w.Code != 500 {
		t.Errorf("RecoverWithDetail panic status = %d, want 500", w.Code)
	}
	if got := respCode(t, w); got != response.CodeServerError {
		t.Errorf("RecoverWithDetail body code = %d, want %d", got, response.CodeServerError)
	}
}

// C8 跨模式一致性：ModeREST 下 panic 同样必须 500（修复前 REST 模式本就 500，
// 此用例锁定两模式行为一致，防止后续回归）。
func TestRecoverPanicRESTMode500(t *testing.T) {
	ensureNopLogger()
	response.SetMode(response.ModeREST)
	defer response.SetMode(response.ModeBusiness)

	r := setupTestRouter()
	r.Use(middleware.RequestID(), middleware.Recover())
	r.GET("/panic", func(c *gin.Context) { panic("boom") })

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/panic", nil)
	r.ServeHTTP(w, req)

	if w.Code != 500 {
		t.Errorf("REST mode panic status = %d, want 500", w.Code)
	}
}

// ===== CSRF Tests =====

func TestCSRF(t *testing.T) {
	r := setupTestRouter()
	r.Use(middleware.CSRF())
	r.GET("/form", func(c *gin.Context) {
		token := middleware.GetCSRFToken(c)
		c.JSON(200, gin.H{"csrf_token": token})
	})
	r.POST("/submit", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// GET 请求应该成功，并设置 cookie
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/form", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("CSRF GET status = %d", w.Code)
	}

	// 获取 cookie 中的 token
	cookies := w.Result().Cookies()
	var csrfToken string
	for _, c := range cookies {
		if c.Name == "csrf_token" {
			csrfToken = c.Value
			break
		}
	}

	// POST 无 token 应该失败
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("POST", "/submit", nil)
	r.ServeHTTP(w2, req2)

	if w2.Code != 200 {
		// 成功捕获错误，返回 200 但 code != 1
	}

	// POST 带 token 应该成功
	w3 := httptest.NewRecorder()
	req3 := httptest.NewRequest("POST", "/submit", nil)
	req3.Header.Set("X-CSRF-Token", csrfToken)
	r.ServeHTTP(w3, req3)

	// 注意：由于 cookie 需要从上一个请求传递，这里可能需要手动设置
}

func TestGetCSRFToken(t *testing.T) {
	r := setupTestRouter()
	r.Use(middleware.CSRF())
	r.GET("/token", func(c *gin.Context) {
		token := middleware.GetCSRFToken(c)
		c.JSON(200, gin.H{"token": token})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/token", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("GetCSRFToken status = %d", w.Code)
	}
}

func TestDoubleSubmitCookie(t *testing.T) {
	r := setupTestRouter()
	r.Use(middleware.DoubleSubmitCookie())
	r.GET("/get", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})
	r.POST("/post", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// GET 应该成功
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/get", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("DoubleSubmit GET status = %d", w.Code)
	}

	// POST 无 token 应该失败
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("POST", "/post", nil)
	r.ServeHTTP(w2, req2)

	// 应返回错误
	if w2.Code != 200 && w2.Code != 400 {
		t.Errorf("DoubleSubmit POST without token status = %d", w2.Code)
	}
}

// ===== C6 回归：API 模式 CSRF（map 遮蔽修复 + 单次消费 + TTL） =====

// apiCSRFToken 从 GenerateAPIToken 响应体中提取颁发的 token。
func apiCSRFToken(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var r response.Response
	if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil {
		t.Fatalf("unmarshal response %q: %v", w.Body.String(), err)
	}
	data, ok := r.Data.(map[string]any)
	if !ok {
		t.Fatalf("response data not an object: %v", r.Data)
	}
	tok, ok := data["csrf_token"].(string)
	if !ok || tok == "" {
		t.Fatalf("missing csrf_token in response: %v", r.Data)
	}
	return tok
}

func setupAPICSRFRouter() *gin.Engine {
	r := setupTestRouter()
	r.Use(middleware.CSRFForAPI())
	r.GET("/csrf-token", middleware.GenerateAPIToken)
	r.POST("/action", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})
	return r
}

// 回归 C6a：颁发 → 校验闭环。修复前颁发的 token 永不在校验 map 里，
// 所有非安全方法请求被判“CSRF Token 无效”拒绝，API CSRF 模式整体不可用。
func TestCSRFForAPIIssueValidateCycle(t *testing.T) {
	r := setupAPICSRFRouter()

	// 颁发 token
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/csrf-token", nil)
	r.ServeHTTP(w, req)
	if respCode(t, w) != response.CodeSuccess {
		t.Fatalf("issue token code = %d, want success", respCode(t, w))
	}
	token := apiCSRFToken(t, w)

	// 携带 token 的 POST 必须通过（修复前这里恒失败）
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("POST", "/action", nil)
	req2.Header.Set("X-CSRF-Token", token)
	r.ServeHTTP(w2, req2)
	if respCode(t, w2) != response.CodeSuccess {
		t.Errorf("POST with valid token code = %d, want success (issue→validate cycle broken)", respCode(t, w2))
	}
}

// 回归 C6b：单次消费——同一 token 第二次使用必须被拒绝，防止重放。
func TestCSRFForAPISingleUseConsumption(t *testing.T) {
	r := setupAPICSRFRouter()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/csrf-token", nil))
	token := apiCSRFToken(t, w)

	// 首次使用：通过
	w1 := httptest.NewRecorder()
	req1 := httptest.NewRequest("POST", "/action", nil)
	req1.Header.Set("X-CSRF-Token", token)
	r.ServeHTTP(w1, req1)
	if respCode(t, w1) != response.CodeSuccess {
		t.Fatalf("first use code = %d, want success", respCode(t, w1))
	}

	// 重放：必须拒绝
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("POST", "/action", nil)
	req2.Header.Set("X-CSRF-Token", token)
	r.ServeHTTP(w2, req2)
	if respCode(t, w2) == response.CodeSuccess {
		t.Error("replayed token must be rejected (single-use consumption broken)")
	}
}

// 回归 C6：缺失 / 伪造 token 必须被拒绝。
func TestCSRFForAPIInvalidAndMissing(t *testing.T) {
	r := setupAPICSRFRouter()

	// 无 token
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/action", nil))
	if respCode(t, w) == response.CodeSuccess {
		t.Error("POST without token must be rejected")
	}

	// 伪造 token
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("POST", "/action", nil)
	req2.Header.Set("X-CSRF-Token", "not-a-real-token")
	r.ServeHTTP(w2, req2)
	if respCode(t, w2) == response.CodeSuccess {
		t.Error("POST with forged token must be rejected")
	}
}

// 回归 C6：安全方法不校验，直接放行。
func TestCSRFForAPISafeMethodPasses(t *testing.T) {
	r := setupAPICSRFRouter()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/csrf-token", nil))
	if w.Code != 200 {
		t.Errorf("safe method status = %d, want 200", w.Code)
	}
}

// 回归 C6c：DoubleSubmitCookie 的 cookie 必须 HttpOnly=false，
// 否则前端 JS 读不到 cookie、无法回填 X-CSRF-Token 头，双重提交对真实前端不可用。
func TestDoubleSubmitCookieHttpOnlyFalse(t *testing.T) {
	r := setupTestRouter()
	r.Use(middleware.DoubleSubmitCookie())
	r.GET("/get", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/get", nil))

	for _, c := range w.Result().Cookies() {
		if c.Name == "csrf_token" {
			if c.HttpOnly {
				t.Errorf("DoubleSubmit cookie HttpOnly = true, want false (JS must read it to refill header)")
			}
			return
		}
	}
	t.Fatal("csrf_token cookie not set on GET")
}

// 回归 C6c：前端回填闭环——GET 下发 cookie，POST 携带匹配的 X-CSRF-Token 通过，不匹配拒绝。
func TestDoubleSubmitCookieFrontendRefill(t *testing.T) {
	r := setupTestRouter()
	r.Use(middleware.DoubleSubmitCookie())
	r.GET("/get", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })
	r.POST("/post", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })

	// GET 下发 cookie
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/get", nil))
	var cookieToken string
	for _, c := range w.Result().Cookies() {
		if c.Name == "csrf_token" {
			cookieToken = c.Value
		}
	}
	if cookieToken == "" {
		t.Fatal("no csrf_token cookie issued")
	}

	// 携带匹配 token 的 cookie + header：通过
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("POST", "/post", nil)
	req2.Header.Set("Cookie", "csrf_token="+cookieToken)
	req2.Header.Set("X-CSRF-Token", cookieToken)
	r.ServeHTTP(w2, req2)
	if w2.Code != 200 {
		t.Errorf("POST with matching token status = %d, want 200", w2.Code)
	}

	// 不匹配：拒绝
	w3 := httptest.NewRecorder()
	req3 := httptest.NewRequest("POST", "/post", nil)
	req3.Header.Set("Cookie", "csrf_token="+cookieToken)
	req3.Header.Set("X-CSRF-Token", "mismatched")
	r.ServeHTTP(w3, req3)
	if w3.Code == 200 && respCode(t, w3) == response.CodeSuccess {
		t.Error("POST with mismatched token must be rejected")
	}
}

// ===== CORS Tests =====

func TestCORS(t *testing.T) {
	r := setupTestRouter()
	r.Use(middleware.CORS())
	r.GET("/test", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// 使用 localhost origin（开发环境默认允许）
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	r.ServeHTTP(w, req)

	// 验证请求成功
	if w.Code != 200 {
		t.Errorf("CORS status = %d", w.Code)
	}

	// 验证其他 CORS 头始终设置
	allowMethods := w.Header().Get("Access-Control-Allow-Methods")
	if allowMethods == "" {
		t.Error("Access-Control-Allow-Methods should be set")
	}
}

func TestCORSOptions(t *testing.T) {
	r := setupTestRouter()
	r.Use(middleware.CORS())
	r.POST("/test", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// OPTIONS 预检请求
	w := httptest.NewRecorder()
	req := httptest.NewRequest("OPTIONS", "/test", nil)
	r.ServeHTTP(w, req)

	// OPTIONS 应返回 204
	if w.Code != 204 {
		t.Errorf("CORS OPTIONS status = %d, want 204", w.Code)
	}
}

// CORS 规范要求：AllowCredentials=false 时不应发送 Access-Control-Allow-Credentials 头。
// 历史 bug：旧实现在 if/else 两个分支都设了 "true"，相当于永远允许凭证。
func TestCORSAllowCredentialsDefault(t *testing.T) {
	r := setupTestRouter()
	r.Use(middleware.CORSWithConfig(&config.CORSConfig{
		AllowedOrigins:   []string{"https://example.com"},
		AllowCredentials: false,
	}))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "https://example.com")
	r.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("Access-Control-Allow-Credentials = %q, want empty when AllowCredentials=false", got)
	}
}

// AllowCredentials=true 且 Origin 在白名单内：发送凭证头并回显具体 Origin。
func TestCORSAllowCredentialsExplicitOrigin(t *testing.T) {
	r := setupTestRouter()
	r.Use(middleware.CORSWithConfig(&config.CORSConfig{
		AllowedOrigins:   []string{"https://example.com"},
		AllowCredentials: true,
	}))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "https://example.com")
	r.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("Access-Control-Allow-Credentials = %q, want \"true\"", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://example.com" {
		t.Errorf("Access-Control-Allow-Origin = %q, want \"https://example.com\"", got)
	}
	if got := w.Header().Get("Vary"); got != "Origin" {
		t.Errorf("Vary = %q, want \"Origin\"", got)
	}
}

// CORS 规范：AllowCredentials=true 时禁止使用 "*"，必须回显具体 Origin。
func TestCORSWildcardWithCredentials(t *testing.T) {
	r := setupTestRouter()
	r.Use(middleware.CORSWithConfig(&config.CORSConfig{
		AllowedOrigins:   []string{"*"},
		AllowCredentials: true,
	}))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "https://anywhere.example")
	r.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got == "*" {
		t.Errorf("Access-Control-Allow-Origin must NOT be \"*\" when credentials are allowed, got %q", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://anywhere.example" {
		t.Errorf("Access-Control-Allow-Origin = %q, want echoed origin", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("Access-Control-Allow-Credentials = %q, want \"true\"", got)
	}
}

// AllowedOrigins=["*"] 且 AllowCredentials=false：保持 "*" 通配符语义。
func TestCORSWildcardWithoutCredentials(t *testing.T) {
	r := setupTestRouter()
	r.Use(middleware.CORSWithConfig(&config.CORSConfig{
		AllowedOrigins:   []string{"*"},
		AllowCredentials: false,
	}))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "https://anywhere.example")
	r.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, want \"*\"", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("Access-Control-Allow-Credentials = %q, want empty (AllowCredentials=false)", got)
	}
}

// Origin 不在白名单时不应回显，避免反射型 CORS 漏洞。
func TestCORSOriginNotAllowed(t *testing.T) {
	r := setupTestRouter()
	r.Use(middleware.CORSWithConfig(&config.CORSConfig{
		AllowedOrigins:   []string{"https://allowed.example"},
		AllowCredentials: true,
	}))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "https://evil.example")
	r.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want empty for non-whitelisted origin", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("Access-Control-Allow-Credentials = %q, want empty for non-whitelisted origin", got)
	}
}

// ===== C7 回归：CORS 通配后缀绕过 + 开发态任意 Origin 回显 =====

// 回归 C7a：通配符 *.example.com 必须拒绝 notexample.com（后缀相同但非真实子域）。
// 旧实现 strings.HasSuffix(origin, "example.com") 接受此类绕过。
func TestCORSWildcardSuffixBypassRejected(t *testing.T) {
	r := setupTestRouter()
	r.Use(middleware.CORSWithConfig(&config.CORSConfig{
		AllowedOrigins:   []string{"*.example.com"},
		AllowCredentials: true,
	}))
	r.GET("/test", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })

	for _, evil := range []string{"https://notexample.com", "https://evil-example.com"} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Origin", evil)
		r.ServeHTTP(w, req)
		if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("evil origin %q got Allow-Origin %q, want empty (suffix bypass)", evil, got)
		}
		if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "" {
			t.Errorf("evil origin %q got Allow-Credentials %q, want empty", evil, got)
		}
	}

	// 真实子域仍应通过。
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "https://app.example.com")
	r.ServeHTTP(w, req)
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Errorf("real subdomain got Allow-Origin %q, want echoed", got)
	}
}

// 回归 C7a：通配符不匹配 apex 自身（example.com 不由 *.example.com 覆盖，需显式配置）。
func TestCORSWildcardDoesNotMatchApex(t *testing.T) {
	r := setupTestRouter()
	r.Use(middleware.CORSWithConfig(&config.CORSConfig{
		AllowedOrigins: []string{"*.example.com"},
	}))
	r.GET("/test", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "https://example.com")
	r.ServeHTTP(w, req)
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("apex origin matched by wildcard got %q, want empty", got)
	}
}

// 回归 C7b：开发态兜底仅对 localhost 回显，不回显任意 Origin（防凭据型反射）。
// 旧实现 cfg.IsDevelopment() && origin != "" 无条件回显任意 Origin。
func TestCORSDevModeRejectsArbitraryOrigin(t *testing.T) {
	// 注入开发态全局配置（无 CORS 白名单 → 走开发态兜底分支）。
	old := config.Get()
	config.Set(&config.Config{
		App:    config.AppConfig{Env: "development"},
		Server: config.ServerConfig{Mode: "development"},
	})
	t.Cleanup(func() {
		if old != nil {
			config.Set(old)
		} else {
			config.Set(&config.Config{})
		}
	})

	r := setupTestRouter()
	r.Use(middleware.CORS())
	r.GET("/test", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })

	// 非 localhost 的任意 Origin 不应被回显。
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "https://evil.com")
	r.ServeHTTP(w, req)
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("arbitrary origin echoed in dev mode: %q, want empty", got)
	}

	// localhost 仍应被回显（开发态正常用法）。
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/test", nil)
	req2.Header.Set("Origin", "http://localhost:3000")
	r.ServeHTTP(w2, req2)
	if got := w2.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
		t.Errorf("localhost origin in dev mode got %q, want echoed", got)
	}
}

// 回归 C7 收尾：未匹配 origin 不发 Allow-Methods/Headers（收敛信息泄露）。
// 旧实现无论 origin 是否匹配都无条件发送，向未授权 origin 暴露 API 允许的方法/头清单。
func TestCORSUnmatchedOriginNoMethodHeaders(t *testing.T) {
	r := setupTestRouter()
	r.Use(middleware.CORSWithConfig(&config.CORSConfig{
		AllowedOrigins: []string{"https://example.com"},
	}))
	r.GET("/test", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })

	// 未匹配 origin：不应发任何 CORS 头（含 Allow-Methods/Headers）。
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "https://evil.com")
	r.ServeHTTP(w, req)
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("unmatched origin got Allow-Origin %q, want empty", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Methods"); got != "" {
		t.Errorf("unmatched origin got Allow-Methods %q, want empty (info leak)", got)
	}
	if got := w.Header().Get("Access-Control-Allow-Headers"); got != "" {
		t.Errorf("unmatched origin got Allow-Headers %q, want empty (info leak)", got)
	}
	if got := w.Header().Get("Access-Control-Expose-Headers"); got != "" {
		t.Errorf("unmatched origin got Expose-Headers %q, want empty", got)
	}
	if got := w.Header().Get("Access-Control-Max-Age"); got != "" {
		t.Errorf("unmatched origin got Max-Age %q, want empty", got)
	}

	// 匹配 origin：正常发 Allow-Methods/Headers。
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/test", nil)
	req2.Header.Set("Origin", "https://example.com")
	r.ServeHTTP(w2, req2)
	if got := w2.Header().Get("Access-Control-Allow-Origin"); got != "https://example.com" {
		t.Errorf("matched origin got Allow-Origin %q, want echoed", got)
	}
	if got := w2.Header().Get("Access-Control-Allow-Methods"); got == "" {
		t.Error("matched origin should have Allow-Methods set")
	}
	if got := w2.Header().Get("Access-Control-Allow-Headers"); got == "" {
		t.Error("matched origin should have Allow-Headers set")
	}
}

// ===== RateLimit Tests =====

func TestRateLimit(t *testing.T) {
	r := setupTestRouter()
	// 使用自定义限流器
	limiter := middleware.NewRateLimiter(10, time.Minute) // 每分钟10次
	defer limiter.Stop()
	r.Use(middleware.RateLimit(limiter))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// 正常请求应该成功
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	// 单次请求应该成功
	if w.Code != 200 {
		t.Errorf("RateLimit status = %d, want 200", w.Code)
	}
}

func TestRateLimiterAllow(t *testing.T) {
	limiter := middleware.NewRateLimiter(3, time.Minute) // 每分钟3次
	defer limiter.Stop()

	// 前3次允许
	for i := 0; i < 3; i++ {
		if !limiter.Allow("192.168.1.1") {
			t.Errorf("Request %d should be allowed", i+1)
		}
	}

	// 第4次拒绝
	if limiter.Allow("192.168.1.1") {
		t.Error("Request 4 should be denied")
	}

	// 不同 IP 应该独立计数
	if !limiter.Allow("192.168.1.2") {
		t.Error("Different IP should be allowed")
	}
}

func TestCustomRateLimit(t *testing.T) {
	r := setupTestRouter()
	r.Use(middleware.CustomRateLimit(5, time.Minute))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("CustomRateLimit status = %d", w.Code)
	}
}

// ===== H4b 回归：CustomRateLimit goroutine 泄漏 =====

// goroutineCount 返回当前 goroutine 数（经短暂 GC + 让出以稳定读数）。
func goroutineCount() int {
	runtime.GC()
	// 给 cleanup goroutine 退出的时间窗口一点余量。
	for i := 0; i < 20; i++ {
		n := runtime.NumGoroutine()
		_ = n
		runtime.Gosched()
	}
	return runtime.NumGoroutine()
}

// 回归 H4b：CustomRateLimit 创建的限流器登记入表，
// StopRateLimiters 停止其 cleanup goroutine，无泄漏。
// 修复前 CustomRateLimit 创建的 limiter 无句柄，StopRateLimiters 不感知 → cleanup goroutine 永久泄漏。
func TestCustomRateLimitNoGoroutineLeak(t *testing.T) {
	// 先清空全局状态（其他测试可能残留）。
	middleware.StopRateLimiters()

	before := goroutineCount()

	// 创建多个自定义限流器（每个启动一个 cleanup goroutine）。
	const n = 5
	r := setupTestRouter()
	for i := 0; i < n; i++ {
		r.Use(middleware.CustomRateLimit(100, time.Minute))
	}
	r.GET("/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	// 触发一次请求使中间件生效（limiter 已在构造时创建）。
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/test", nil))
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	created := goroutineCount()
	// 创建 n 个限流器后 goroutine 数应明显增加（至少 n 个 cleanup goroutine）。
	if created < before+n {
		t.Errorf("after creating %d custom limiters: goroutines = %d, before = %d, want >= before+%d", n, created, before, n)
	}

	// StopRateLimiters 须停止登记的自定义限流器 → cleanup goroutine 退出。
	middleware.StopRateLimiters()

	// 等待 cleanup goroutine 退出（Stop 内部 wg.Wait 已保证，但 NumGoroutine 读数有调度延迟）。
	deadline := time.Now().Add(2 * time.Second)
	var after int
	for time.Now().Before(deadline) {
		after = goroutineCount()
		if after <= before+2 { // 允许少量调度噪声
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if after > before+2 {
		t.Errorf("after StopRateLimiters: goroutines = %d, before = %d, expected custom cleanup goroutines released (H4b leak)", after, before)
	}
}

// 回归 H4b：InitRateLimiters 重新初始化时也停止旧的自定义限流器（防 re-init 泄漏）。
func TestCustomRateLimitReinitStopsOldCustoms(t *testing.T) {
	middleware.StopRateLimiters()

	// 基线：InitRateLimiters 只建 3 个命名限流器（3 个 cleanup goroutine），无自定义。
	middleware.InitRateLimiters()
	baseline := goroutineCount()

	// 创建 2 个自定义限流器（修复后登记入表）。
	_ = middleware.CustomRateLimit(100, time.Minute)
	_ = middleware.CustomRateLimit(100, time.Minute)

	// InitRateLimiters 重建命名限流器时也应停止已登记的自定义限流器。
	// 修复后：自定义 2 个被停止，仅剩 3 个命名 cleanup goroutine → goroutine 数 ≈ baseline。
	// 修复前（不登记）：2 个自定义 cleanup goroutine 泄漏 → goroutine 数 ≈ baseline+2。
	middleware.InitRateLimiters()

	deadline := time.Now().Add(2 * time.Second)
	var after int
	for time.Now().Before(deadline) {
		after = goroutineCount()
		if after <= baseline+1 { // 允许少量调度噪声
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if after > baseline+1 {
		t.Errorf("after InitRateLimiters: goroutines = %d, baseline = %d, expected old custom limiters stopped (H4b re-init leak)", after, baseline)
	}

	middleware.StopRateLimiters()
}

// ===== H4a 回归：限流窗口语义 =====

// fakeClock 可控时钟，供 RateLimiter 测试注入 nowFunc（避免真实 Sleep flaky）。
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Now()}
}

func (fc *fakeClock) Now() time.Time {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	return fc.now
}

func (fc *fakeClock) Advance(d time.Duration) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	fc.now = fc.now.Add(d)
}

// 回归 H4a：稳态客户端跨窗口持续低于 rate，不被误限。
// rate=10/100ms。每窗口 8 次（< rate），分散在窗口内。
// 旧实现放行每次更新 windowStart，致重置分支永不成立、count 跨窗口累加，
// 第 2 个窗口内 count 累加超过 10 被误限——尽管每窗口请求数（8）低于 rate。
// 修复后 windowStart 仅窗口起点设置，跨窗口重置、稳态低于 rate 永不被限。
func TestRateLimiterSteadyClientNotBlocked(t *testing.T) {
	clock := newFakeClock()
	limiter := middleware.NewRateLimiter(10, 100*time.Millisecond)
	limiter.SetNowFunc(clock.Now)
	defer limiter.Stop()

	// 3 个窗口，每窗口 8 次（< rate=10），每次间隔 12ms（8×12=96ms < 100ms 在窗口内）。
	// 窗口间靠最后一次 Advance 推进到 >100ms 触发跨窗口。
	for w := 0; w < 3; w++ {
		for i := 0; i < 8; i++ {
			if !limiter.Allow("1.2.3.4") {
				t.Fatalf("window %d request %d should be allowed (steady below rate, H4a: count must reset per window)", w+1, i+1)
			}
			clock.Advance(12 * time.Millisecond) // 窗口内推进
		}
		// 8×12=96ms 已过，再推进 8ms 到 104ms > 100ms，进入下一窗口。
		clock.Advance(8 * time.Millisecond)
	}
}

// 回归 H4a：窗口过后 count 重置，可再次放行（固定窗口语义）。
// 旧实现 lastSeen 每次放行更新，窗口过期分支对持续客户端永不触发。
func TestRateLimiterWindowReset(t *testing.T) {
	clock := newFakeClock()
	limiter := middleware.NewRateLimiter(3, time.Minute)
	limiter.SetNowFunc(clock.Now)
	defer limiter.Stop()

	for i := 0; i < 3; i++ {
		if !limiter.Allow("1.2.3.4") {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}
	if limiter.Allow("1.2.3.4") {
		t.Error("request 4 should be denied (rate exceeded)")
	}
	clock.Advance(time.Minute + time.Second)
	if !limiter.Allow("1.2.3.4") {
		t.Error("request after window should be allowed (window reset)")
	}
}

// 回归 H4a：超限被拦（基本限流仍生效，修复不是放宽到无限）。
func TestRateLimiterBlocksOverRate(t *testing.T) {
	limiter := middleware.NewRateLimiter(3, time.Minute)
	defer limiter.Stop()

	for i := 0; i < 3; i++ {
		if !limiter.Allow("1.2.3.4") {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}
	if limiter.Allow("1.2.3.4") {
		t.Error("request 4 should be denied")
	}
}

// 回归 H4a：窗口内突发（瞬时集中）达 rate 后拒绝——固定窗口允许突发但封顶 rate。
func TestRateLimiterBurstCappedAtRate(t *testing.T) {
	clock := newFakeClock()
	limiter := middleware.NewRateLimiter(5, time.Minute)
	limiter.SetNowFunc(clock.Now)
	defer limiter.Stop()

	for i := 0; i < 5; i++ {
		if !limiter.Allow("1.2.3.4") {
			t.Errorf("burst request %d should be allowed", i+1)
		}
	}
	if limiter.Allow("1.2.3.4") {
		t.Error("request 6 in same window should be denied")
	}
}

func assertPanic(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("%s did not panic", name)
		}
	}()
	fn()
}

func TestRateLimiterRejectsInvalidConfig(t *testing.T) {
	assertPanic(t, "memory limiter zero rate", func() {
		_ = middleware.NewRateLimiter(0, time.Minute)
	})
	assertPanic(t, "memory limiter zero window", func() {
		_ = middleware.NewRateLimiter(1, 0)
	})
	assertPanic(t, "redis limiter zero rate", func() {
		_ = middleware.NewRedisRateLimiter("bad", 0, time.Minute)
	})
	assertPanic(t, "redis fail-closed limiter zero window", func() {
		_ = middleware.NewRedisRateLimiterFailClosed("bad", 1, 0)
	})
}

func TestRateLimiterStopIdempotent(t *testing.T) {
	limiter := middleware.NewRateLimiter(1, time.Minute)
	limiter.Stop()
	limiter.Stop()
}

func TestRateLimitNilLimiterFailsClosed(t *testing.T) {
	r := setupTestRouter()
	r.Use(middleware.RateLimit(nil))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/test", nil))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("RateLimit(nil) status = %d, want 503; body=%s", w.Code, w.Body.String())
	}
	if got := respCode(t, w); got != response.CodeServiceUnavailable {
		t.Fatalf("RateLimit(nil) code = %d, want %d", got, response.CodeServiceUnavailable)
	}
}

func TestLoginRateLimit(t *testing.T) {
	middleware.InitRateLimiters()
	defer middleware.StopRateLimiters()

	r := setupTestRouter()
	r.Use(middleware.LoginRateLimit())
	r.GET("/test", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("LoginRateLimit status = %d", w.Code)
	}
}

func TestAPIRateLimit(t *testing.T) {
	middleware.InitRateLimiters()
	defer middleware.StopRateLimiters()

	r := setupTestRouter()
	r.Use(middleware.APIRateLimit())
	r.GET("/test", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("APIRateLimit status = %d", w.Code)
	}
}

func TestUploadRateLimit(t *testing.T) {
	middleware.InitRateLimiters()
	defer middleware.StopRateLimiters()

	r := setupTestRouter()
	r.Use(middleware.UploadRateLimit())
	r.GET("/test", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("UploadRateLimit status = %d", w.Code)
	}
}

// ===== RedisRateLimiter Tests =====

func TestRedisRateLimiter(t *testing.T) {
	limiter := middleware.NewRedisRateLimiter("test_limit", 10, time.Minute)

	// Without Redis, should always allow
	ctx := context.Background()
	allowed, err := limiter.Allow(ctx, "192.168.1.1")
	if err != nil {
		t.Errorf("RedisRateLimiter error: %v", err)
	}
	if !allowed {
		t.Error("RedisRateLimiter should allow without Redis")
	}
}

func TestRedisRateLimitMiddleware(t *testing.T) {
	r := setupTestRouter()
	r.Use(middleware.RedisRateLimit("test_api", 100))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	// Without Redis, should pass
	if w.Code != 200 {
		t.Errorf("RedisRateLimit status = %d", w.Code)
	}
}

func TestLoginRedisRateLimit(t *testing.T) {
	// H4c: LoginRedisRateLimit 改 fail-closed——无 Redis 时拒绝（503），
	// 防爆破场景下 Redis 故障不能静默放行（原 fail-open 致限流失效）。
	// 测试环境无 Redis，故预期 503。
	prev := database.SetTestRedisClient(nil)
	defer func() { database.SetTestRedisClient(prev) }()

	r := setupTestRouter()
	r.Use(middleware.LoginRedisRateLimit())
	r.GET("/test", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("LoginRedisRateLimit (no Redis, fail-closed) status = %d, want 503", w.Code)
	}
}

func TestAPIRedisRateLimit(t *testing.T) {
	r := setupTestRouter()
	r.Use(middleware.APIRedisRateLimit())
	r.GET("/test", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("APIRedisRateLimit status = %d", w.Code)
	}
}

func TestCustomRedisRateLimit(t *testing.T) {
	r := setupTestRouter()
	r.Use(middleware.CustomRedisRateLimit("custom", 50, time.Minute))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("CustomRedisRateLimit status = %d", w.Code)
	}
}

func TestRedisRateLimitWithIdentifierNilFuncUsesClientIP(t *testing.T) {
	setupMiddlewareMiniRedis(t)

	r := setupTestRouter()
	r.Use(middleware.RedisRateLimitWithIdentifier("nil_identifier", 1, nil))
	r.GET("/test", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	w1 := httptest.NewRecorder()
	req1 := httptest.NewRequest("GET", "/test", nil)
	req1.RemoteAddr = "192.0.2.10:12345"
	r.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want 200; body=%s", w1.Code, w1.Body.String())
	}

	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/test", nil)
	req2.RemoteAddr = "192.0.2.10:12345"
	r.ServeHTTP(w2, req2)
	if got := respCode(t, w2); got != response.CodeRateLimit {
		t.Fatalf("second request code = %d, want %d; status=%d body=%s", got, response.CodeRateLimit, w2.Code, w2.Body.String())
	}
}

func TestRedisRateLimiterGetCount(t *testing.T) {
	limiter := middleware.NewRedisRateLimiter("test_count", 10, time.Minute)

	ctx := context.Background()
	count, err := limiter.GetCount(ctx, "192.168.1.1")
	if err != nil {
		t.Errorf("GetCount error: %v", err)
	}
	// Without Redis, count should be 0
	if count != 0 {
		t.Errorf("GetCount = %d, want 0 without Redis", count)
	}
}

func TestRedisRateLimiterReset(t *testing.T) {
	limiter := middleware.NewRedisRateLimiter("test_reset", 10, time.Minute)

	ctx := context.Background()
	err := limiter.Reset(ctx, "192.168.1.1")
	if err != nil {
		t.Errorf("Reset error: %v", err)
	}
}

// ===== H4c 回归：RedisRateLimiter fail-open/fail-closed + 裸断言 =====

// setupMiddlewareMiniRedis 启动 miniredis 并注入 database 内部 redisClient，返回 mr 与清理。
func setupMiddlewareMiniRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	old := database.SetTestRedisClient(client)
	t.Cleanup(func() { database.SetTestRedisClient(old) })
	return mr
}

// 回归 H4c-1a：无 Redis 时 fail-open 放行（兼容旧行为）。
func TestRedisRateLimiterFailOpenNoRedis(t *testing.T) {
	prev := database.SetTestRedisClient(nil)
	defer func() { database.SetTestRedisClient(prev) }()

	limiter := middleware.NewRedisRateLimiter("test", 10, time.Minute)
	allowed, err := limiter.Allow(context.Background(), "1.2.3.4")
	if err != nil {
		t.Errorf("fail-open no-redis err = %v, want nil", err)
	}
	if !allowed {
		t.Error("fail-open no-redis should allow (兼容旧行为)")
	}
}

// 回归 H4c-1b：无 Redis 时 fail-closed 拒绝 + 返 ErrRedisRateLimiterUnavailable。
// 修复前无此选项；fail-closed 是 H4c 新增的安全语义。
func TestRedisRateLimiterFailClosedNoRedis(t *testing.T) {
	prev := database.SetTestRedisClient(nil)
	defer func() { database.SetTestRedisClient(prev) }()

	limiter := middleware.NewRedisRateLimiterFailClosed("test", 10, time.Minute)
	allowed, err := limiter.Allow(context.Background(), "1.2.3.4")
	if allowed {
		t.Error("fail-closed no-redis should deny")
	}
	if !errors.Is(err, middleware.ErrRedisRateLimiterUnavailable) {
		t.Errorf("fail-closed no-redis err = %v, want ErrRedisRateLimiterUnavailable", err)
	}
}

// 回归 H4c-1c：Redis 故障（关闭 miniredis）时 fail-open 放行、fail-closed 拒绝。
// 这是 H4c 核心——登录防爆破场景下 fail-closed 防限流静默失效。
func TestRedisRateLimiterFailClosedOnRedisError(t *testing.T) {
	mr := setupMiddlewareMiniRedis(t)

	// fail-open：正常时放行。
	openLimiter := middleware.NewRedisRateLimiter("test_open", 10, time.Minute)
	allowed, err := openLimiter.Allow(context.Background(), "1.2.3.4")
	if err != nil {
		t.Fatalf("fail-open normal err = %v", err)
	}
	if !allowed {
		t.Error("fail-open normal should allow")
	}

	// fail-closed：正常时放行。
	closedLimiter := middleware.NewRedisRateLimiterFailClosed("test_closed", 10, time.Minute)
	allowed, err = closedLimiter.Allow(context.Background(), "1.2.3.4")
	if err != nil {
		t.Fatalf("fail-closed normal err = %v", err)
	}
	if !allowed {
		t.Error("fail-closed normal should allow")
	}

	// 关闭 miniredis 模拟 Redis 故障。
	mr.Close()

	// fail-open：故障时放行（兼容旧行为）。
	allowed, _ = openLimiter.Allow(context.Background(), "1.2.3.4")
	if !allowed {
		t.Error("fail-open on redis error should allow (兼容旧行为)")
	}

	// fail-closed：故障时拒绝（H4c 安全语义）。
	allowed, err = closedLimiter.Allow(context.Background(), "1.2.3.4")
	if allowed {
		t.Error("fail-closed on redis error should deny (H4c: 防限流静默失效)")
	}
	if err == nil {
		t.Error("fail-closed on redis error should return non-nil err")
	}
}

// 回归 H4c-1d：fail-closed 中间件在 Redis 故障时返 503（区别于真实超限的 429）。
func TestRedisRateLimitFailClosedMiddlewareReturns503(t *testing.T) {
	prev := database.SetTestRedisClient(nil)
	defer func() { database.SetTestRedisClient(prev) }()

	r := setupTestRouter()
	r.Use(middleware.RedisRateLimitFailClosed("login_limit", 10))
	r.GET("/login", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/login", nil))

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("fail-closed middleware (no redis) status = %d, want 503", w.Code)
	}
	var body response.Response
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Code != response.CodeServiceUnavailable {
		t.Errorf("fail-closed middleware code = %d, want %d", body.Code, response.CodeServiceUnavailable)
	}
}

// 回归 H4c-1e：fail-open 中间件在 Redis 故障时放行（兼容旧行为）。
func TestRedisRateLimitFailOpenMiddlewareAllowsOnError(t *testing.T) {
	prev := database.SetTestRedisClient(nil)
	defer func() { database.SetTestRedisClient(prev) }()

	r := setupTestRouter()
	r.Use(middleware.RedisRateLimit("api_limit", 100))
	r.GET("/api", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api", nil))

	if w.Code != 200 {
		t.Errorf("fail-open middleware (no redis) status = %d, want 200", w.Code)
	}
}

// 回归 H4c-2：真实 Redis 闭环——超限被拦（429），未超限放行。
// 用 miniredis 验证滑动窗口 Lua 脚本与 comma-ok 断言路径在真实 Redis 下正常。
func TestRedisRateLimiterRealRedisLimitCycle(t *testing.T) {
	setupMiddlewareMiniRedis(t)

	limiter := middleware.NewRedisRateLimiter("cycle_limit", 3, time.Minute)
	ctx := context.Background()

	// 前 3 次放行。
	for i := 0; i < 3; i++ {
		allowed, err := limiter.Allow(ctx, "1.2.3.4")
		if err != nil {
			t.Fatalf("request %d err = %v", i, err)
		}
		if !allowed {
			t.Errorf("request %d should be allowed", i)
		}
	}
	// 第 4 次拒绝（超限）。
	allowed, err := limiter.Allow(ctx, "1.2.3.4")
	if err != nil {
		t.Fatalf("over-limit err = %v", err)
	}
	if allowed {
		t.Error("request 4 should be denied (over limit)")
	}
}

// 回归 H4c-2：comma-ok 断言防护——Redis 返回非 int64 时不 panic 而返错。
// miniredis 的 slidingWindowLua 恒返 int64，无法经 Allow 触发断言失败路径；
// 此处直接用返回字符串的 Lua 脚本验证 result.(int64) 的 comma-ok 行为，
// 并对照证明裸断言会 panic（H4c-2 缺陷根因）。
func TestRedisRateLimiterCommaOkAssertion(t *testing.T) {
	setupMiddlewareMiniRedis(t)
	ctx := context.Background()

	// miniredis 执行返回字符串的脚本——模拟 Redis 返回非 int64 结果。
	res, err := database.GetRedis().Eval(ctx, `return 'not-an-int'`, nil).Result()
	if err != nil {
		t.Fatalf("eval err = %v", err)
	}

	// 对照：裸断言在非 int64 时 panic。
	var barePanicked bool
	func() {
		defer func() {
			if r := recover(); r != nil {
				barePanicked = true
			}
		}()
		_ = res.(int64) // 旧实现行：裸断言
	}()
	if !barePanicked {
		t.Error("bare assertion res.(int64) should panic on non-int64 (H4c-2 缺陷根因)")
	}

	// 修复：comma-ok 不 panic，返 ok=false。
	if _, ok := res.(int64); ok {
		t.Error("comma-ok should return ok=false for non-int64 result")
	}
}

// 回归 H4c-2：SetFailClosed 切换策略——fail-open 限流器切 fail-closed 后无 Redis 拒绝。
func TestRedisRateLimiterSetFailClosed(t *testing.T) {
	prev := database.SetTestRedisClient(nil)
	defer func() { database.SetTestRedisClient(prev) }()

	limiter := middleware.NewRedisRateLimiter("test", 10, time.Minute)
	// 初始 fail-open：放行。
	if allowed, _ := limiter.Allow(context.Background(), "1.2.3.4"); !allowed {
		t.Error("initial fail-open should allow")
	}
	// 切换 fail-closed：拒绝。
	limiter.SetFailClosed(true)
	if allowed, err := limiter.Allow(context.Background(), "1.2.3.4"); allowed {
		t.Error("after SetFailClosed(true) should deny")
	} else if !errors.Is(err, middleware.ErrRedisRateLimiterUnavailable) {
		t.Errorf("after SetFailClosed err = %v, want ErrRedisRateLimiterUnavailable", err)
	}
}
