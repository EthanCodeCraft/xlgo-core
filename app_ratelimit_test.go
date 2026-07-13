package xlgo_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	xlgo "github.com/EthanCodeCraft/xlgo-core"
	"github.com/EthanCodeCraft/xlgo-core/middleware"
	"github.com/gin-gonic/gin"
)

// TestAppRateLimitMultiAppShutdownIsolation 固化 Phase 5 核心契约：单进程多 App 时，
// App A 的 Shutdown 不得重置/停止 App B 的限流器。修复前 App.Shutdown 调全局
// middleware.StopRateLimiters，把全局 loginLimiter 置 nil，App B 下次请求会懒创建
// 新 limiter（计数清零）--稳态客户端借 App A 的 Shutdown 窗口绕过限流。
//
// 验证：App B 的 /login 已用 10/10 次后，App A.Shutdown，第 11 次仍应 429（计数保留）。
// 修复前第 11 次会 200（计数被重置）。
func TestAppRateLimitMultiAppShutdownIsolation(t *testing.T) {
	saved := middleware.SwapDefaultRateLimitRegistry(middleware.NewRateLimitRegistry())
	defer middleware.SwapDefaultRateLimitRegistry(saved)

	// 用 REST 响应模式：限流映射 HTTP 429（business 模式下是 200+业务码 429，不便断言）
	cfgA := testConfig(18130)
	cfgA.Server.ResponseMode = "rest"
	cfgB := testConfig(18131)
	cfgB.Server.ResponseMode = "rest"

	appA := xlgo.New(xlgo.WithConfig(cfgA))
	if err := appA.Init(); err != nil {
		t.Fatalf("appA Init: %v", err)
	}
	appA.GetRouter().GET("/login", middleware.LoginRateLimit(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	appB := xlgo.New(xlgo.WithConfig(cfgB))
	if err := appB.Init(); err != nil {
		t.Fatalf("appB Init: %v", err)
	}
	defer appB.Shutdown()
	appB.GetRouter().GET("/login", middleware.LoginRateLimit(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	// 10 次请求 App B 的 /login（限流 10/min，同一 IP）-> 全 200
	for i := 0; i < 10; i++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/login", nil)
		req.RemoteAddr = "192.0.2.1:1234"
		appB.GetRouter().ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("request %d: got %d, want 200 (under limit)", i+1, w.Code)
		}
	}

	// 第 11 次（不经 A.Shutdown）应 429 -- 先确认限流器确实计数到上限（排除 IP/计数 bug）
	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/login", nil)
		req.RemoteAddr = "192.0.2.1:1234"
		appB.GetRouter().ServeHTTP(w, req)
		if w.Code != http.StatusTooManyRequests {
			t.Fatalf("sanity: 11th without shutdown got %d, want 429 (limiter not counting?)", w.Code)
		}
	}

	// App A Shutdown -- 不得重置/停止 App B 的 login limiter
	if err := appA.Shutdown(); err != nil {
		t.Fatalf("appA Shutdown: %v", err)
	}

	// 第 12 次请求 App B 的 /login -> 应仍 429（计数保留在 10，registryB 未被 A.Shutdown 重置）。
	// 修复前 A.Shutdown 调 StopRateLimiters 把全局 loginLimiter 置 nil，下次请求懒创建新 limiter（计数清零）-> 200。
	{
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/login", nil)
		req.RemoteAddr = "192.0.2.1:1234"
		appB.GetRouter().ServeHTTP(w, req)
		if w.Code != http.StatusTooManyRequests {
			t.Errorf("12th request after appA Shutdown: got %d, want 429 (limiter count should be preserved, not reset by appA.Shutdown)", w.Code)
		}
	}
}

// TestAppRateLimitRollbackOnInitFailure 固化 Phase 5 回滚：Init 失败后全局默认 Registry
// 必须恢复为 Init 前的实例。
func TestAppRateLimitRollbackOnInitFailure(t *testing.T) {
	preInit := middleware.NewRateLimitRegistry()
	saved := middleware.SwapDefaultRateLimitRegistry(preInit)
	defer middleware.SwapDefaultRateLimitRegistry(saved)

	app := xlgo.New(
		xlgo.WithConfig(testConfig(18132)),
		xlgo.WithHook(xlgo.Hook{Name: "fail", OnInit: func(*xlgo.App) error { return errors.New("boom") }}),
	)
	if err := app.Init(); err == nil {
		_ = app.Shutdown()
		t.Fatal("expected Init to fail via OnInit hook")
	}

	if middleware.GetDefaultRateLimitRegistry() != preInit {
		t.Fatal("rollback did not restore preInit rate limit registry as global default")
	}
}
