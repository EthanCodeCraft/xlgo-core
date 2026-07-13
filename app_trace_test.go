package xlgo_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	xlgo "github.com/EthanCodeCraft/xlgo-core"
	"github.com/EthanCodeCraft/xlgo-core/trace"
	"github.com/gin-gonic/gin"
)

// TestWithTraceInitErrorPropagated 固化 Phase 1 契约：WithTrace 启用后 doInit 必须调
// trace.Init 并把错误向上传播。用非法 ExporterType 触发 trace.Init 失败（config.Validate
// 只校验 SampleRatio，故 bogus exporter 能过 Validate、在 trace.Init 失败，证明是 App 主动调 Init）。
func TestWithTraceInitErrorPropagated(t *testing.T) {
	cfg := testConfig(18090)
	cfg.Trace.Enabled = true
	cfg.Trace.ExporterType = "bogus-exporter"

	app := xlgo.New(xlgo.WithConfig(cfg), xlgo.WithTrace())
	err := app.Init()
	if err == nil {
		_ = app.Shutdown()
		t.Fatal("expected Init to fail with bogus trace exporter, got nil")
	}
	// "初始化 trace 失败" 包裹 trace.Init 的 "不支持的导出器类型"
	msg := err.Error()
	if !strings.Contains(msg, "trace") && !strings.Contains(msg, "导出器") {
		t.Fatalf("expected trace init error, got %v", err)
	}
}

// TestWithTraceNoopMiddlewareDoesNotBreakRequest 固化：WithTrace + Enabled=false（默认）
// 时 trace.Init 安装 Noop tracer，trace.Middleware 装入链后不应破坏正常请求。
func TestWithTraceNoopMiddlewareDoesNotBreakRequest(t *testing.T) {
	cfg := testConfig(18091)
	// Trace.Enabled 保持默认 false -> noop

	app := xlgo.New(xlgo.WithConfig(cfg), xlgo.WithTrace())
	if err := app.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer app.Shutdown()

	// Init 后（middleware 链已装入）注册路由，确保该路由经过 trace 中间件。
	app.GetRouter().GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	app.GetRouter().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("trace noop middleware broke request: got status %d, body %s", w.Code, w.Body.String())
	}
}

// TestWithTraceShutdownReleasesExporterGoroutine 固化 Phase 1 生命周期闭环：
// WithTrace + Enabled=true + stdout exporter，Init 后 BatchSpanProcessor 后台 goroutine
// 在运行；Shutdown 必须调 trace.Close 让其退出，否则 goroutine 泄漏（H-14 修了 Close 幂等，
// 但 App 此前从未调 Close -- 本测试断言 App.Shutdown 确实调了）。
//
// 不发任何请求 -> stdout exporter 不输出 -> 无测试输出污染；仅观测 goroutine 计数。
func TestWithTraceShutdownReleasesExporterGoroutine(t *testing.T) {
	cfg := testConfig(18092)
	cfg.Trace.Enabled = true
	cfg.Trace.ExporterType = "stdout"

	app := xlgo.New(xlgo.WithConfig(cfg), xlgo.WithTrace())

	baseline := runtime.NumGoroutine()
	if err := app.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	afterInit := runtime.NumGoroutine()
	if afterInit <= baseline {
		t.Logf("note: afterInit=%d baseline=%d (batch goroutine delta not detected; test may be noisy)", afterInit, baseline)
	}

	if err := app.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	// trace.Close 的 provider.Shutdown 异步停止 batch goroutine，轮询等待回归 baseline。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= baseline {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if after := runtime.NumGoroutine(); after > baseline {
		t.Errorf("trace.Close 未释放 exporter goroutine（App.Shutdown 疑未调 trace.Close）: baseline=%d after=%d", baseline, after)
	}
}

// TestWithTraceEnabledStdoutInitOK 固化：WithTrace + Enabled=true + stdout 是合法组合，
// trace.Init 成功，App.Init 成功；二次 trace.Close 幂等（H-14）。
func TestWithTraceEnabledStdoutInitOK(t *testing.T) {
	cfg := testConfig(18093)
	cfg.Trace.Enabled = true
	cfg.Trace.ExporterType = "stdout"

	app := xlgo.New(xlgo.WithConfig(cfg), xlgo.WithTrace())
	if err := app.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := app.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	// Shutdown 已调 trace.Close；再调一次必须幂等不报错（H-14 契约）
	if err := trace.Close(context.Background()); err != nil {
		t.Fatalf("trace.Close after Shutdown should be idempotent: %v", err)
	}
}
