package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/EthanCodeCraft/xlgo-core/response"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
)

// 回归 C6b：TTL 过期分支。直接向包级存储注入一个“已过期”的 token，
// 断言 CSRFForAPI 拒绝它（即使该 token 从未被消费过）。
// 这条分支（time.Since(issuedAt) > apiTokenTTL）无法靠单次消费用例覆盖，
// 必须单独构造过期时间戳。
func TestCSRFForAPITTLExpiry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(CSRFForAPI())
	r.POST("/action", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })

	expiredToken := "expired-test-token"
	apiTokensMu.Lock()
	// 注入一个早于 TTL 的颁发时间，模拟 token 已过期。
	apiTokens[expiredToken] = time.Now().Add(-(apiTokenTTL + time.Second))
	apiTokensMu.Unlock()
	defer func() {
		apiTokensMu.Lock()
		delete(apiTokens, expiredToken)
		apiTokensMu.Unlock()
	}()

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/action", nil)
	req.Header.Set("X-CSRF-Token", expiredToken)
	r.ServeHTTP(w, req)

	var resp response.Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response %q: %v", w.Body.String(), err)
	}
	if resp.Code == response.CodeSuccess {
		t.Errorf("expired token must be rejected, got code=%d (success)", resp.Code)
	}
}

// 回归 P1 #9：CSRF 从 JSON body 取 token 后，下游 handler 仍能读到完整请求体。
// 修复前用 ShouldBindJSON 读干 body，下游再绑定拿到 EOF；改用 ShouldBindBodyWith 缓存 body。
func TestCSRFJSONBodyPreservedForDownstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(CSRF())

	var gotData string
	r.POST("/action", func(c *gin.Context) {
		// 下游再次绑定 body，应能读到完整内容（而非被 CSRF 中间件读空）。
		var payload struct {
			Data string `json:"data"`
		}
		if err := c.ShouldBindBodyWith(&payload, binding.JSON); err != nil {
			c.JSON(500, gin.H{"err": err.Error()})
			return
		}
		gotData = payload.Data
		c.JSON(200, gin.H{"status": "ok"})
	})

	// token 仅放在 JSON body 里（不放 header/form），触发中间件读 body 的分支。
	const tok = "csrf-token-abc-1234567890"
	body := `{"_csrf":"` + tok + `","data":"hello-downstream"}`
	req := httptest.NewRequest("POST", "/action", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// cookie 携带同一 token，使 CSRF 校验通过。
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: tok})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d, body=%s", w.Code, w.Body.String())
	}
	if gotData != "hello-downstream" {
		t.Errorf("downstream read data=%q, want 'hello-downstream' (P1 #9: body must survive CSRF)", gotData)
	}
}

func TestCSRFJSONBodyTooLargeRejected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(CSRF(CSRFConfig{MaxBodyBytes: 16}))
	r.POST("/action", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })

	body := `{"_csrf":"token","data":"` + strings.Repeat("x", 64) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/action", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "token"})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body=%s", w.Code, w.Body.String())
	}
}

func TestCSRFNegativeTokenLengthFallsBack(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(CSRF(CSRFConfig{TokenLength: -1}))
	r.GET("/form", func(c *gin.Context) { c.JSON(200, gin.H{"token": GetCSRFToken(c)}) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/form", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if len(w.Result().Cookies()) == 0 {
		t.Fatal("expected csrf cookie to be set")
	}
}

func TestCSRFCookieSameSiteLax(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(CSRF())
	r.GET("/form", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/form", nil))
	for _, c := range w.Result().Cookies() {
		if c.Name == CSRFCookieName {
			if c.SameSite != http.SameSiteLaxMode {
				t.Fatalf("SameSite = %v, want Lax", c.SameSite)
			}
			return
		}
	}
	t.Fatal("csrf cookie not set")
}

func TestCSRFPartialConfigKeepsCookieDefaults(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(CSRF(CSRFConfig{TokenLength: -1}))
	r.GET("/form", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/form", nil))
	for _, c := range w.Result().Cookies() {
		if c.Name == CSRFCookieName {
			if c.SameSite != http.SameSiteLaxMode {
				t.Fatalf("SameSite = %v, want Lax for partial config", c.SameSite)
			}
			if c.MaxAge != DefaultCSRFConfig.MaxAge {
				t.Fatalf("MaxAge = %d, want %d for partial config", c.MaxAge, DefaultCSRFConfig.MaxAge)
			}
			return
		}
	}
	t.Fatal("csrf cookie not set")
}

func TestDoubleSubmitCookieSameSiteLax(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(DoubleSubmitCookie())
	r.GET("/get", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/get", nil))
	for _, c := range w.Result().Cookies() {
		if c.Name == CSRFCookieName {
			if c.SameSite != http.SameSiteLaxMode {
				t.Fatalf("SameSite = %v, want Lax", c.SameSite)
			}
			return
		}
	}
	t.Fatal("csrf cookie not set")
}

func TestCSRFWithSkipAnchorsPathBoundary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(CSRFWithSkip([]string{"/api"}))
	r.POST("/api", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	r.POST("/api/users", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	r.POST("/apix", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	for _, path := range []string{"/api", "/api/users"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, nil))
		if w.Code != http.StatusNoContent {
			t.Fatalf("%s status = %d, want skipped 204", path, w.Code)
		}
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/apix", nil))
	if w.Code == http.StatusNoContent {
		t.Fatal("/apix was skipped by /api prefix; want CSRF rejection")
	}
}

func TestGenerateAPITokenCleansExpiredTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)
	expiredToken := "expired-before-issue"
	apiTokensMu.Lock()
	apiTokens[expiredToken] = time.Now().Add(-(apiTokenTTL + time.Second))
	apiTokensMu.Unlock()
	defer func() {
		apiTokensMu.Lock()
		delete(apiTokens, expiredToken)
		apiTokensMu.Unlock()
	}()

	r := gin.New()
	r.GET("/csrf-token", GenerateAPIToken)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/csrf-token", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}

	apiTokensMu.RLock()
	_, exists := apiTokens[expiredToken]
	apiTokensMu.RUnlock()
	if exists {
		t.Fatal("GenerateAPIToken did not clean expired token")
	}
}
