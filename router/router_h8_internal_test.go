package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/EthanCodeCraft/xlgo-core/middleware"
	"github.com/gin-gonic/gin"
)

// TestApplyIdempotent_H8b 复现 H8b：修复前二次 Apply 会重复 engine.Use 并触发
// Gin 重复路由 panic；修复后二次 Apply 直接返回，无 panic、无重复中间件。
func TestApplyIdempotent_H8b(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	r := NewRegistry(engine)

	runs := 0
	r.Use(func(c *gin.Context) { runs++; c.Next() })
	r.RegisterModuleFunc("test", func(g *gin.RouterGroup) {
		g.GET("/h8b", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })
	})

	r.Apply()
	r.Apply() // 二次 Apply 必须无 panic
	r.Apply() // 三次同样

	if !r.applied {
		t.Fatal("applied flag should be true after Apply")
	}

	// 中间件只应被装入一次：请求一次，runs 应为 1（若重复装入则 >1）。
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/h8b", nil))
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if runs != 1 {
		t.Fatalf("global middleware ran %d times, want 1 (Apply not idempotent)", runs)
	}
}

// TestMetricsMiddlewareFirstInApply_H8c 验证 metrics 中间件经 SetMetricsMiddleware
// 在 Apply 内装入，覆盖所有经注册中心注册的路由，且不依赖 RegisterMetricsRoute
// 的调用顺序。修复前 RegisterMetricsRoute 用 r.Use，先注册的路由不被采集。
func TestMetricsMiddlewareFirstInApply_H8c(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	r := NewRegistry(engine)

	// 用一个计数中间件模拟 metrics 采集。
	hits := 0
	r.SetMetricsMiddleware(func(c *gin.Context) {
		hits++
		c.Next()
	})

	r.RegisterModuleFunc("biz", func(g *gin.RouterGroup) {
		g.GET("/biz", func(c *gin.Context) { c.JSON(200, gin.H{}) })
	})
	// 注意：不调用 RegisterMetricsRoute，仅靠 SetMetricsMiddleware + Apply 装入。
	r.Apply()

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/biz", nil))
	if w.Code != 200 {
		t.Fatalf("biz status = %d, want 200", w.Code)
	}
	if hits != 1 {
		t.Fatalf("metrics middleware hits = %d, want 1 (route not instrumented)", hits)
	}
}

// TestMetricsMiddlewareNilSkipped_H8c：未设置 metrics 中间件时 Apply 不应装入空壳。
func TestMetricsMiddlewareNilSkipped_H8c(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	r := NewRegistry(engine)
	if r.metricsMiddleware != nil {
		t.Fatal("default metricsMiddleware should be nil")
	}
	r.Apply() // 不应 panic
}

// TestEnsureRegistryPanicsBeforeInit_H8a 复现 H8a：修复前 Init 之前调全局 helper
// 触发 nil 解引用 panic；修复后为带明确信息的 panic。
func TestEnsureRegistryPanicsBeforeInit_H8a(t *testing.T) {
	// 保存并清空全局注册中心，测试后恢复，避免污染其它测试。
	prev := globalRegistry.Load()
	globalRegistry.Store(nil)
	t.Cleanup(func() { globalRegistry.Store(prev) })

	var got any
	func() {
		defer func() { got = recover() }()
		Apply()
	}()
	if got == nil {
		t.Fatal("Apply before Init should panic, got nil")
	}
	msg, ok := got.(string)
	if !ok {
		t.Fatalf("panic value should be string, got %T: %v", got, got)
	}
	if !strings.Contains(msg, "router.Init") {
		t.Fatalf("panic message should mention router.Init, got %q", msg)
	}
}

// TestGlobalRegistryAtomicConcurrent_H8a：并发 Init/GetRegistry 不触发 data race
// （atomic.Pointer 保护）。须配合 -race 运行。
func TestGlobalRegistryAtomicConcurrent_H8a(t *testing.T) {
	prev := globalRegistry.Load()
	t.Cleanup(func() { globalRegistry.Store(prev) })

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); Init(gin.New()) }()
		go func() { defer wg.Done(); _ = GetRegistry() }()
	}
	wg.Wait()
}

// TestHealthHandlerConvergedSchema_H8d 验证 defaultModule / RegisterHealthRoute(无 checks)
// 与 handler 风格的 /health 同 schema：200 + {"status":"ok"}。
func TestHealthHandlerConvergedSchema_H8d(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	r := NewRegistry(engine)
	r.RegisterModule(&defaultModule{})
	r.Apply()

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))
	if w.Code != 200 {
		t.Fatalf("defaultModule /health status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"status":"ok"`) {
		t.Fatalf("defaultModule /health body = %s, want {\"status\":\"ok\"}", body)
	}
	// 不应携带 response 业务信封字段。
	if strings.Contains(body, `"code"`) || strings.Contains(body, `"data"`) {
		t.Fatalf("defaultModule /health should not use response envelope, got %s", body)
	}
}

// 编译期保证 middleware 包仍可独立使用（H8c 回归用）。
var _ gin.HandlerFunc = middleware.Metrics()

// TestDefaultModuleAndRegisterHealthRouteCoexist_H8dfootgun 复现 H8d 收尾 footgun：
// 修复前 WithDefaultRoutes()+WithModules(DefaultModule) 并存会触发 Gin 重复路由 panic；
// 修复后 registerGETOnce 使二者幂等共存，/health 与 /swagger 均可访问。
// 此测试模拟 app.go 的真实顺序：Register* 先注册（带 checks），defaultModule 经 Apply 后注册。
func TestDefaultModuleAndRegisterHealthRouteCoexist_H8dfootgun(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()

	// 先经 Register* 注册（等价 app.go Init 中 enableHealth/enableSwagger 段）
	RegisterHealthRoute(engine, HealthCheck{Name: "mysql", Check: func(context.Context) error { return nil }})
	RegisterSwaggerRoutes(engine)
	// 再经注册中心注册 DefaultModule（等价 app.go registry.Apply()）
	r := NewRegistry(engine)
	r.RegisterModule(&defaultModule{})
	r.Apply() // 修复前在此 panic: handlers are already registered for path '/health'

	// /health 仍可访问，且首次注册（带 checks）胜出——响应含 checks 字段。
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))
	if w.Code != 200 {
		t.Fatalf("/health status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"mysql":"ok"`) {
		t.Fatalf("first registration (with checks) should win, got %s", w.Body.String())
	}

	// /swagger/*any 注册存在（非 404 即说明路由已注册）
	w2 := httptest.NewRecorder()
	engine.ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/swagger/index.html", nil))
	if w2.Code == http.StatusNotFound {
		t.Fatal("/swagger/*any should be registered, got 404")
	}
}

// TestRegisterHealthRouteIdempotent_H8dfootgun：RegisterHealthRoute 重复调用不 panic。
func TestRegisterHealthRouteIdempotent_H8dfootgun(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	RegisterHealthRoute(engine, HealthCheck{Name: "a", Check: func(context.Context) error { return nil }})
	RegisterHealthRoute(engine) // 重复，不 panic
	RegisterHealthRoute(engine) // 三次，不 panic

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))
	if w.Code != 200 {
		t.Fatalf("/health status = %d, want 200", w.Code)
	}
}

// TestDefaultModuleOnly_H8dfootgun：仅用 defaultModule（不预先 Register*）时，
// /health 与 /swagger 仍正常注册（recover 兜底路径不影响首次注册）。
func TestDefaultModuleOnly_H8dfootgun(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	r := NewRegistry(engine)
	r.RegisterModule(&defaultModule{})
	r.Apply()

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))
	if w.Code != 200 {
		t.Fatalf("/health status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"status":"ok"`) {
		t.Fatalf("/health body = %s", w.Body.String())
	}
}

// TestRegisterGETOnceEngineDoesNotSwallowRealConflict_H8dfootgun：Engine 路径经 Routes()
// 精确预检，未命中即直接注册（无 recover）。真正不同的路由冲突（如 /foo/:id 已存在再注册
// /foo/*any）仍按 gin 原语义 panic，不被掩盖——证明幂等只吞"同一 path 重复"，不掩盖真实冲突。
func TestRegisterGETOnceEngineDoesNotSwallowRealConflict_H8dfootgun(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/foo/:id", func(c *gin.Context) {})

	var got any
	func() {
		defer func() { got = recover() }()
		registerGETOnce(engine, "/foo/*any", func(c *gin.Context) {}) // 与 :id 真实冲突
	}()
	if got == nil {
		t.Fatal("registerGETOnce (Engine) should panic on real (different-path) conflict, got nil")
	}
}

