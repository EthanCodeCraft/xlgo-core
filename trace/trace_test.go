package trace

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// initTracerSnapshot 捕获包 init() 后、任何测试改动前的全局 tracer 快照。
// 用于锁定 init() 的 Noop 兜底不变式（C13a），避免 resetGlobal/Init/Close
// 在其他测试中重置全局后掩盖 init() 路径的回归。
var initTracerSnapshot oteltrace.Tracer

func TestMain(m *testing.M) {
	// init() 已运行；此处立即快照 getTracer()（永不 nil 的 Noop 兜底）。
	initTracerSnapshot = getTracer()
	code := m.Run()
	os.Exit(code)
}

// resetGlobal 恢复 trace 包级全局到 Noop 兜底状态，避免测试间污染。
func resetGlobal(t *testing.T) {
	t.Helper()
	noopProvider := oteltrace.NewNoopTracerProvider()
	noopTracer := noopProvider.Tracer("xlgo")
	tp := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.NeverSample()))
	tracerProviderPtr.Store(tp)
	tracerPtr.Store(&noopTracer)
	otel.SetTracerProvider(noopProvider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{}))
}

// TestC13aInitNoopInvariant 锁定 init() 的 Noop 兜底不变式：
// 包加载后（任何 Init/Close/resetGlobal 之前）getTracer() 必须非 nil。
//
// 变异 init() 去掉 Noop Store 后，initTracerSnapshot（在 TestMain 即 init 后捕获）
// 将为 nil → 此测试红。resetGlobal 等后续改动不影响此快照。
func TestC13aInitNoopInvariant(t *testing.T) {
	if initTracerSnapshot == nil {
		t.Fatal("init() did not store a Noop tracer: getTracer() was nil at package load (C13a)")
	}
}

// ============================================================
// C13a：未 Init 即用 → nil tracer panic
// ============================================================

// TestC13aNoInitNoPanic 验证未 Init 时包级函数不 panic（Noop 兜底）。
//
// 修复前：包级 tracer 为 nil，Middleware/StartSpanFromContext/GetTracer 裸用 → panic。
// 修复后：init() Store Noop，getTracer() 永不 nil。
func TestC13aNoInitNoPanic(t *testing.T) {
	// 强制重置到"未 Init"的 Noop 兜底状态。
	resetGlobal(t)

	// GetTracer 非 nil。
	if tr := GetTracer(); tr == nil {
		t.Fatal("GetTracer() nil before Init (C13a)")
	}

	// StartSpanFromContext 不 panic。
	ctx, span := StartSpanFromContext(context.Background(), "test-span")
	defer span.End()
	if span == nil {
		t.Fatal("StartSpanFromContext returned nil span")
	}
	_ = ctx

	// Middleware 不 panic：走一次请求。
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Middleware("test-svc"))
	r.GET("/p", func(c *gin.Context) {
		// 下游用 c.Request.Context() 取 span（C13d 闭环）。
		s := oteltrace.SpanFromContext(c.Request.Context())
		c.String(http.StatusOK, s.SpanContext().TraceID().String())
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/p", nil)
	r.ServeHTTP(w, req)

	// Noop tracer 的 TraceID 为空（Noop 不记录），但绝不应 panic。
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (C13a should not panic)", w.Code)
	}
}

// TestC13aInitDisabledNoop 验证 Init(Enabled:false) 后 Noop 安全。
func TestC13aInitDisabledNoop(t *testing.T) {
	t.Cleanup(func() { _ = Close(context.Background()); resetGlobal(t) })
	if err := Init(Config{Enabled: false, ServiceName: "svc"}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if GetTracer() == nil {
		t.Fatal("GetTracer nil after Init(Enabled:false)")
	}
	// Noop tracer Start 不 panic。
	_, span := StartSpanFromContext(context.Background(), "x")
	span.End()
}

// TestC13aCloseThenUseNoPanic 验证 Close 后再用不 panic（Store 回 Noop 兜底）。
func TestC13aCloseThenUseNoPanic(t *testing.T) {
	resetGlobal(t)
	t.Cleanup(func() { resetGlobal(t) })

	// 即便未真正 Init 出带 exporter 的 provider，Close 也应安全并把全局重置为兜底。
	if err := Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Close 后包级函数仍安全。
	if GetTracer() == nil {
		t.Fatal("GetTracer nil after Close")
	}
	_, span := StartSpanFromContext(context.Background(), "after-close")
	span.End()
}

// ============================================================
// C13b：未知导出器返 nil + stdout 未实现
// ============================================================

// TestC13bStdoutExporterWorks 验证 stdout 导出器可创建（C13b 实现）。
func TestC13bStdoutExporterWorks(t *testing.T) {
	// 用 stdouttrace 但 Init 会向 os.Stdout 输出，故直接测 createExporter。
	cfg := Config{ExporterType: "stdout"}
	exp, err := createExporter(cfg)
	if err != nil {
		t.Fatalf("createExporter(stdout): %v (C13b stdout unimplemented)", err)
	}
	if exp == nil {
		t.Fatal("createExporter(stdout) returned nil exporter (C13b)")
	}
	_ = exp.Shutdown(context.Background())
}

// TestC13bUnknownExporterReturnsError 验证未知导出器返错（修复前返 nil,nil）。
func TestC13bUnknownExporterReturnsError(t *testing.T) {
	cfg := Config{ExporterType: "xyz-unknown"}
	exp, err := createExporter(cfg)
	if err == nil {
		t.Error("createExporter(unknown) should return error (C13b), got nil")
	}
	if exp != nil {
		t.Errorf("createExporter(unknown) should return nil exporter, got %T", exp)
	}
}

// TestC13bInitUnknownExporterFails 验证 Init 未知导出器返错（不喂 nil 给 WithBatcher）。
func TestC13bInitUnknownExporterFails(t *testing.T) {
	t.Cleanup(func() { resetGlobal(t) })
	err := Init(Config{Enabled: true, ExporterType: "xyz-unknown", Propagator: "w3c"})
	if err == nil {
		t.Error("Init with unknown exporter should fail (C13b)")
	}
}

// ============================================================
// C13c：OTLP 默认 HTTPS 无 WithInsecure
// ============================================================

// TestC13cInsecureExporterCreates 验证 Insecure:true 时 otlp-http 导出器可创建（无 TLS 握手）。
// createExporter 仅构造 client，不连接；Insecure 注入不报错即验证 option 路径生效。
func TestC13cInsecureExporterCreates(t *testing.T) {
	cfg := Config{ExporterType: "otlp-http", Endpoint: "localhost:4318", Insecure: true}
	exp, err := createExporter(cfg)
	if err != nil {
		t.Fatalf("createExporter(otlp-http, Insecure): %v (C13c)", err)
	}
	_ = exp.Shutdown(context.Background())
}

// TestC13cOtlpGrpcInsecureCreates 验证 otlp-grpc Insecure 路径。
func TestC13cOtlpGrpcInsecureCreates(t *testing.T) {
	cfg := Config{ExporterType: "otlp-grpc", Endpoint: "localhost:4317", Insecure: true}
	exp, err := createExporter(cfg)
	if err != nil {
		t.Fatalf("createExporter(otlp-grpc, Insecure): %v (C13c)", err)
	}
	_ = exp.Shutdown(context.Background())
}

// ============================================================
// C13d：Middleware 不更新 c.Request
// ============================================================

// TestC13dRequestContextContainsSpan 验证 Middleware 更新 c.Request，
// 下游 c.Request.Context() 含 span（TraceID 非空且与 X-Trace-ID 一致）。
//
// 修复前：仅 c.Set("otel_ctx", ctx)，下游 c.Request.Context() 无 span。
func TestC13dRequestContextContainsSpan(t *testing.T) {
	resetGlobal(t)
	t.Cleanup(func() { _ = Close(context.Background()); resetGlobal(t) })

	// 用 stdout 导出器 + 全采样，使 span 真实生成（非 Noop）。
	if err := Init(Config{
		Enabled:       true,
		ServiceName:   "test-svc",
		ExporterType:  "stdout",
		SampleRatio:   1.0,
		Propagator:    "w3c",
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Middleware("test-svc"))

	var seenTraceID string
	r.GET("/p", func(c *gin.Context) {
		// 关键：用 c.Request.Context()（而非 trace.GetContext(c)）取 span。
		s := oteltrace.SpanFromContext(c.Request.Context())
		seenTraceID = s.SpanContext().TraceID().String()
		c.String(http.StatusOK, "ok")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/p", nil)
	r.ServeHTTP(w, req)

	if seenTraceID == "" {
		t.Fatal("c.Request.Context() has no span/TraceID (C13d: Middleware didn't update c.Request)")
	}
	headerTraceID := w.Header().Get("X-Trace-ID")
	if headerTraceID == "" {
		t.Fatal("X-Trace-ID header missing")
	}
	if seenTraceID != headerTraceID {
		t.Errorf("TraceID mismatch: downstream %q vs header %q (C13d)", seenTraceID, headerTraceID)
	}
}

// TestC13dPropagatedTraceContextExtracted 验证 Middleware 从入站 W3C 头提取父 context
// 并写入 c.Request，下游 span 继承父 TraceID。
func TestC13dPropagatedTraceContextExtracted(t *testing.T) {
	resetGlobal(t)
	t.Cleanup(func() { _ = Close(context.Background()); resetGlobal(t) })

	if err := Init(Config{
		Enabled: true, ServiceName: "test-svc", ExporterType: "stdout",
		SampleRatio: 1.0, Propagator: "w3c",
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// 构造一个父 span 并注入 W3C traceparent 头（用真实采样 provider，非 noop）。
	parentProvider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	defer parentProvider.Shutdown(context.Background())
	parentTracer := parentProvider.Tracer("test-parent")
	parentCtx, parentSpan := parentTracer.Start(context.Background(), "parent")
	defer parentSpan.End()
	parentTraceID := parentSpan.SpanContext().TraceID().String()

	carrier := propagation.HeaderCarrier{}
	otel.GetTextMapPropagator().Inject(parentCtx, carrier)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Middleware("test-svc"))
	var seen string
	r.GET("/p", func(c *gin.Context) {
		s := oteltrace.SpanFromContext(c.Request.Context())
		seen = s.SpanContext().TraceID().String()
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/p", nil)
	// 把 carrier 中的头复制到请求。
	for _, key := range carrier.Keys() {
		req.Header.Set(key, carrier.Get(key))
	}
	r.ServeHTTP(w, req)

	if seen != parentTraceID {
		t.Errorf("downstream TraceID = %q, want parent %q (C13d propagation/extract)", seen, parentTraceID)
	}
}

// ============================================================
// C13e：b3/jaeger 未实现
// ============================================================

// TestC13eB3PropagatorImplemented 验证 b3 传播器返回非 nil 的 b3 propagator（C13e 实现）。
func TestC13eB3PropagatorImplemented(t *testing.T) {
	prop, err := createPropagator("b3")
	if err != nil {
		t.Fatalf("createPropagator(b3): %v (C13e unimplemented)", err)
	}
	if prop == nil {
		t.Fatal("createPropagator(b3) returned nil (C13e)")
	}
	// 用真实采样的 provider tracer，使 span SpanContext 有效。
	tp := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	defer tp.Shutdown(context.Background())
	tracer := tp.Tracer("t")
	ctx, span := tracer.Start(context.Background(), "s")
	defer span.End()
	// b3 propagator 应识别 b3 头。注入后 b3 单头或 x-b3-traceid 至少其一非空。
	carrier := propagation.HeaderCarrier{}
	prop.Inject(ctx, carrier)
	if carrier.Get("b3") == "" && carrier.Get("x-b3-traceid") == "" {
		t.Error("b3 propagator did not inject b3 headers (C13e)")
	}
}

// TestC13eJaegerMapsToW3C 验证 jaeger 映射 W3C TraceContext（非静默 nil）。
func TestC13eJaegerMapsToW3C(t *testing.T) {
	prop, err := createPropagator("jaeger")
	if err != nil {
		t.Fatalf("createPropagator(jaeger): %v (C13e)", err)
	}
	if prop == nil {
		t.Fatal("createPropagator(jaeger) returned nil (C13e)")
	}
	// 用真实采样的 provider tracer，使 span SpanContext 有效（noop tracer 不生成 TraceID）。
	tp := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	defer tp.Shutdown(context.Background())
	tracer := tp.Tracer("t")
	ctx, span := tracer.Start(context.Background(), "s")
	defer span.End()
	// 注入 W3C traceparent 头。
	carrier := propagation.HeaderCarrier{}
	prop.Inject(ctx, carrier)
	if carrier.Get("traceparent") == "" {
		t.Error("jaeger(→W3C) propagator did not inject traceparent (C13e)")
	}
}

// TestC13eUnknownPropagatorReturnsError 验证未知传播器返错（修复前静默回落 W3C）。
func TestC13eUnknownPropagatorReturnsError(t *testing.T) {
	prop, err := createPropagator("xyz")
	if err == nil {
		t.Error("createPropagator(unknown) should return error (C13e), got nil")
	}
	if prop != nil {
		t.Errorf("createPropagator(unknown) should return nil, got %T", prop)
	}
}

// TestC13eInitUnknownPropagatorFails 验证 Init 未知传播器返错且回滚 provider。
func TestC13eInitUnknownPropagatorFails(t *testing.T) {
	t.Cleanup(func() { resetGlobal(t) })
	err := Init(Config{Enabled: true, ExporterType: "stdout", Propagator: "xyz"})
	if err == nil {
		t.Error("Init with unknown propagator should fail (C13e)")
	}
}

// TestC13eW3CDefault 验证空 propagator 默认 W3C（兼容）。
func TestC13eW3CDefault(t *testing.T) {
	prop, err := createPropagator("")
	if err != nil {
		t.Fatalf("createPropagator(''): %v", err)
	}
	if prop == nil {
		t.Fatal("createPropagator('') returned nil")
	}
}

// ============================================================
// C13 顺带：GetContext 裸断言防护
// ============================================================

// TestC13GetContextCommaOk 验证 otel_ctx 被置为非 context 值时不 panic。
func TestC13GetContextCommaOk(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Set("otel_ctx", "not-a-context") // 污染

	// 修复前：ctx.(context.Context) 裸断言 panic；修复后 comma-ok 回退 c.Request.Context()。
	ctx := GetContext(c)
	if ctx == nil {
		t.Fatal("GetContext returned nil")
	}
	// 应回退到 c.Request.Context()。
	if ctx != c.Request.Context() {
		t.Error("GetContext should fall back to c.Request.Context() on bad otel_ctx")
	}
}
