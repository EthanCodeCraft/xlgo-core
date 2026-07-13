package xlgo_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	xlgo "github.com/EthanCodeCraft/xlgo-core"
	"github.com/gin-gonic/gin"
)

// TestAppRateLimitPerAppCountingIsolation 固化 #1 修复：app.RateLimitRegistry().LoginRateLimit()
// 返回的中间件捕获 App 自己的 Registry，多 App 下 per-App 计数隔离。修复前包级 LoginRateLimit()
// 解析全局默认（多 App 仅最后 Init 的 App），App A 用满额度后 App B 首请求即被拒。
//
// 验证：App A 用满 10/10 后第 11 次 429；App B 同 IP 仍可 10 次 200、第 11 次 429（独立计数）。
func TestAppRateLimitPerAppCountingIsolation(t *testing.T) {
	// REST 模式：限流映射 HTTP 429
	cfgA := testConfig(18140)
	cfgA.Server.ResponseMode = "rest"
	cfgB := testConfig(18141)
	cfgB.Server.ResponseMode = "rest"

	appA := xlgo.New(xlgo.WithConfig(cfgA))
	if appA.RateLimitRegistry() == nil {
		t.Fatal("appA.RateLimitRegistry() = nil before Init (should be pre-created in New)")
	}
	if err := appA.Init(); err != nil {
		t.Fatalf("appA Init: %v", err)
	}
	defer appA.Shutdown()
	// app-bound 中间件：捕获 appA 的 Registry，不查全局默认
	appA.GetRouter().GET("/login", appA.RateLimitRegistry().LoginRateLimit(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	appB := xlgo.New(xlgo.WithConfig(cfgB))
	if err := appB.Init(); err != nil {
		t.Fatalf("appB Init: %v", err)
	}
	defer appB.Shutdown()
	appB.GetRouter().GET("/login", appB.RateLimitRegistry().LoginRateLimit(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := func(h http.Handler) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/login", nil)
		r.RemoteAddr = "192.0.2.7:1234" // 两 App 用同一 IP
		h.ServeHTTP(w, r)
		return w
	}

	// App A 用满 10 次（200），第 11 次 429
	for i := 0; i < 10; i++ {
		if w := req(appA.GetRouter()); w.Code != http.StatusOK {
			t.Fatalf("appA request %d: got %d, want 200", i+1, w.Code)
		}
	}
	if w := req(appA.GetRouter()); w.Code != http.StatusTooManyRequests {
		t.Fatalf("appA 11th: got %d, want 429 (limit reached)", w.Code)
	}

	// App B 同 IP：应独立计数，10 次 200、第 11 次 429。
	// 包级 facade 下 B 会共享 A 的 loginLimiter（A 已用满）-> 首请求即 429，本测试即暴露该回归。
	for i := 0; i < 10; i++ {
		if w := req(appB.GetRouter()); w.Code != http.StatusOK {
			t.Fatalf("appB request %d: got %d, want 200 (per-App isolation: B should not share A's count)", i+1, w.Code)
		}
	}
	if w := req(appB.GetRouter()); w.Code != http.StatusTooManyRequests {
		t.Fatalf("appB 11th: got %d, want 429 (B independent limit reached)", w.Code)
	}
}

// TestAppRateLimitRegistryCustomNoLeakOnRollback 固化 #2 修复路径：app-bound CustomRateLimit
// 的 limiter 登记在 App 的 Registry 上，Init 失败回滚时由 registry.Stop() 收口，不泄漏。
// 验证：注册 CustomRateLimit 任务后 Init 失败，rollback 调 registry.Stop（customLimiters 被清）。
func TestAppRateLimitRegistryCustomNoLeakOnRollback(t *testing.T) {
	cfg := testConfig(18142)
	app := xlgo.New(xlgo.WithConfig(cfg),
		xlgo.WithHook(xlgo.Hook{Name: "fail", OnInit: func(*xlgo.App) error { return errors.New("boom") }}),
	)
	// app-bound CustomRateLimit（首请求才创建 limiter，但 Init 会失败故不会触发请求；
	// 此用例固化 app.RateLimitRegistry() 在 pre-Init 可用、Init 失败回滚不 panic）
	app.GetRouter().GET("/c", app.RateLimitRegistry().CustomRateLimit(5, time.Minute), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	if err := app.Init(); err == nil {
		_ = app.Shutdown()
		t.Fatal("expected Init to fail via OnInit hook")
	}
	// Init 失败后 rollbackReplacedResources 已 Stop App 的 Registry 并把 a.rateLimitRegistry 置 nil，
	// 故 app.RateLimitRegistry() 返回 nil（rollback 已收口，无泄漏、无 panic）。
	if app.RateLimitRegistry() != nil {
		t.Fatal("a.rateLimitRegistry should be nil after Init-failure rollback")
	}
}
