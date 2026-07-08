package trace

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/contrib/propagators/b3"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// Config 链路追踪配置
type Config struct {
	// ServiceName 服务名称
	ServiceName string
	// ServiceVersion 服务版本
	ServiceVersion string
	// Environment 运行环境
	Environment string
	// ExporterType 导出器类型: "otlp-http", "otlp-grpc", "stdout"
	ExporterType string
	// Endpoint OTLP 导出器地址
	Endpoint string
	// Insecure 是否使用明文（无 TLS）连接 collector。
	// 默认 false（TLS）；对 localhost:4318 等明文 collector 需显式置 true（C13c）。
	Insecure bool
	// SampleRatio 采样比例 (0.0-1.0)
	SampleRatio float64
	// Enabled 是否启用
	Enabled bool
	// Propagator 传播器类型: "w3c", "b3", "jaeger"
	Propagator string
}

// DefaultConfig 默认配置
var DefaultConfig = Config{
	ServiceName:    "xlgo-service",
	ServiceVersion: "1.0.0", // 应用自身版本（非框架版本 xlgo.Version）；建议业务侧覆盖为实际应用版本
	Environment:    "development",
	ExporterType:   "otlp-http",
	Endpoint:       "localhost:4318",
	SampleRatio:    1.0,
	Enabled:        false,
	Propagator:     "w3c",
}

// tracerProviderPtr 全局 TracerProvider（atomic，C13a）。
// Init 之前/Close 之后均为 Noop/NeverSample，保证任何时刻 Load 非 nil，请求期不 panic。
var tracerProviderPtr atomic.Pointer[sdktrace.TracerProvider]

// tracerPtr 全局 Tracer（atomic，C13a）。Init 之前为 Noop，调用安全。
var tracerPtr atomic.Pointer[trace.Tracer]

const defaultOperationTimeout = 5 * time.Second

var operationTimeoutNanos atomic.Int64

func init() {
	// 初始化为 Noop，保证包级函数在任何时刻（未 Init / 已 Close）Load 均非 nil（C13a）。
	// P1 #19：用 noop.NewTracerProvider()（新 OTel API），替代已弃用的 trace.NewNoopTracerProvider()。
	noopProvider := noop.NewTracerProvider()
	noopTracer := noopProvider.Tracer("xlgo")
	tp := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.NeverSample()))
	tracerProviderPtr.Store(tp)
	tracerPtr.Store(&noopTracer)
	operationTimeoutNanos.Store(int64(defaultOperationTimeout))
}

// getTracer 返回全局 Tracer 的 atomic 快照（永不 nil）。
func getTracer() trace.Tracer {
	return *tracerPtr.Load()
}

// TracerProvider 全局 TracerProvider（导出供高级用法；返回当前快照）。
func TracerProvider() *sdktrace.TracerProvider {
	return tracerProviderPtr.Load()
}

// Init 初始化链路追踪
func Init(cfg Config) error {
	if math.IsNaN(cfg.SampleRatio) || cfg.SampleRatio < 0 || cfg.SampleRatio > 1 {
		return fmt.Errorf("trace SampleRatio must be between 0.0 and 1.0: %v", cfg.SampleRatio)
	}

	if !cfg.Enabled {
		// 设置 Noop Tracer（P1 #19：noop 包替代弃用 API）
		noopProvider := noop.NewTracerProvider()
		otel.SetTracerProvider(noopProvider)
		noopTracer := noopProvider.Tracer(cfg.ServiceName)
		tracerPtr.Store(&noopTracer)
		// M-64 修复：Swap 出旧 provider（可能持有 exporter）并 Shutdown，
		// 避免禁用 trace 后旧 exporter 后台 goroutine/连接持续占用。
		fallback := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.NeverSample()))
		if old := tracerProviderPtr.Swap(fallback); old != nil {
			_ = shutdownProviderWithTimeout(context.Background(), old)
		}
		return nil
	}

	// 创建资源
	// 注意：不传 semconv.SchemaURL 与 resource.Default() 混用——
	// resource.Default() 在不同 OTel 版本可能使用与 semconv v1.24.0 不同的 schema URL，
	// resource.Merge 对冲突的 SchemaURL 直接报错。这里用空 schema URL 的属性集合并，
	// 避免版本漂移导致的初始化失败。
	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			"",
			semconv.ServiceName(cfg.ServiceName),
			semconv.ServiceVersion(cfg.ServiceVersion),
			attribute.String("environment", cfg.Environment),
		),
	)
	if err != nil {
		return err
	}

	// 创建导出器
	exporter, err := createExporter(cfg)
	if err != nil {
		return err
	}

	// 创建 TracerProvider
	newProvider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.TraceIDRatioBased(cfg.SampleRatio)),
	)

	// 设置传播器（非法类型返错，C13e 不再静默回落）
	prop, err := createPropagator(cfg.Propagator)
	if err != nil {
		// M-65 修复：传播器非法时回滚已创建的 provider。此时尚未 otel.SetTracerProvider，
		// otel 全局仍指向旧 provider，无需恢复——仅 Shutdown 新 provider 避免泄漏。
		_ = shutdownProviderWithTimeout(context.Background(), newProvider)
		return err
	}

	// 全部成功后再切换全局状态（M-65：避免失败后 otel 指向已 Shutdown 的 provider）。
	otel.SetTracerProvider(newProvider)
	otel.SetTextMapPropagator(prop)

	// 原子替换：先建新 provider，成功后再 Store，并关闭旧 provider（若持有 exporter）。
	oldProvider := tracerProviderPtr.Swap(newProvider)
	tracer := newProvider.Tracer(cfg.ServiceName)
	tracerPtr.Store(&tracer)
	if oldProvider != nil {
		_ = shutdownProviderWithTimeout(context.Background(), oldProvider)
	}

	return nil
}

func operationTimeout() time.Duration {
	timeout := time.Duration(operationTimeoutNanos.Load())
	if timeout <= 0 {
		return defaultOperationTimeout
	}
	return timeout
}

func contextWithOperationTimeout(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, operationTimeout())
}

func shutdownProviderWithTimeout(parent context.Context, provider *sdktrace.TracerProvider) error {
	if provider == nil {
		return nil
	}
	ctx, cancel := contextWithOperationTimeout(parent)
	defer cancel()
	return provider.Shutdown(ctx)
}

// createExporter 创建导出器
func createExporter(cfg Config) (sdktrace.SpanExporter, error) {
	switch cfg.ExporterType {
	case "otlp-http":
		opts := []otlptracehttp.Option{otlptracehttp.WithEndpoint(cfg.Endpoint)}
		if cfg.Insecure {
			opts = append(opts, otlptracehttp.WithInsecure()) // C13c
		}
		client := otlptracehttp.NewClient(opts...)
		ctx, cancel := contextWithOperationTimeout(context.Background())
		defer cancel()
		return otlptrace.New(ctx, client)
	case "otlp-grpc":
		opts := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(cfg.Endpoint)}
		if cfg.Insecure {
			opts = append(opts, otlptracegrpc.WithInsecure()) // C13c
		}
		client := otlptracegrpc.NewClient(opts...)
		ctx, cancel := contextWithOperationTimeout(context.Background())
		defer cancel()
		return otlptrace.New(ctx, client)
	case "stdout":
		return stdouttrace.New(
			stdouttrace.WithWriter(os.Stdout),
			stdouttrace.WithPrettyPrint(),
		)
	default:
		// C13b：未知导出器返错，不再返回 nil 喂 WithBatcher(nil)。
		return nil, fmt.Errorf("不支持的导出器类型: %s", cfg.ExporterType)
	}
}

// createPropagator 创建传播器（C13e：实现 b3，jaeger 映射 W3C，未知返错）。
func createPropagator(propagatorType string) (propagation.TextMapPropagator, error) {
	switch propagatorType {
	case "w3c", "":
		return propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		), nil
	case "b3":
		// 同时支持多头与单头注入/抽取，兼容旧 B3 客户端。
		return b3.New(b3.WithInjectEncoding(b3.B3MultipleHeader | b3.B3SingleHeader)), nil
	case "jaeger":
		// 现代 Jaeger agent 透传 W3C TraceContext；纯 Jaeger thrift 头协议需下游用 b3。
		// 不引入不稳定的 jaegerremix 模块，jaeger 视为 W3C 别名。
		return propagation.TraceContext{}, nil
	default:
		return nil, fmt.Errorf("不支持的传播器类型: %s", propagatorType)
	}
}

// Close 关闭链路追踪。
//
// H-14 修复：去掉 sync.Once。原实现 Close→Init→Close 第二次因 once 已消费而 no-op，
// 新 provider 的 exporter 后台 goroutine/连接泄漏。改为每次 Swap 出当前 provider 并
// Shutdown，Store 回兜底 NeverSample provider（C13a：Close 后再用已关闭 provider）。
// 幂等：重复 Close 时 Swap 得到的是无 exporter 的兜底 provider，Shutdown 无害。
// 并发安全：Swap 原子返回唯一指针，不会 double-Shutdown 同一 provider。
func Close(ctx context.Context) error {
	fallback := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.NeverSample()))
	old := tracerProviderPtr.Swap(fallback)
	noopTracer := noop.NewTracerProvider().Tracer("xlgo") // P1 #19：noop 包替代弃用 API
	tracerPtr.Store(&noopTracer)
	if old != nil {
		return shutdownProviderWithTimeout(ctx, old)
	}
	return nil
}

// Middleware Gin 中间件
func Middleware(serviceName string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 从请求中提取 TraceContext
		ctx := otel.GetTextMapPropagator().Extract(c.Request.Context(), propagation.HeaderCarrier(c.Request.Header))

		// 创建 Span。P1 #19：span 名以路由模板（FullPath）为准以保持低基数。
		// 原实现 `Method+" "+FullPath()` 后判 `== ""` 永不成立（"GET " 非空），
		// 未匹配路由的回退分支形同虚设、且用原始 URL.Path（含 ID）会导致 span 名高基数爆炸。
		// 现显式判断 FullPath 为空（未匹配路由），用固定低基数名。
		route := c.FullPath()
		var spanName string
		if route == "" {
			spanName = c.Request.Method + " [unmatched]"
		} else {
			spanName = c.Request.Method + " " + route
		}

		attrs := []attribute.KeyValue{
			semconv.HTTPRequestMethodKey.String(c.Request.Method),
			semconv.URLPathKey.String(c.Request.URL.Path),
			semconv.HTTPRouteKey.String(route),
			attribute.String("http.user_agent", c.Request.UserAgent()),
			attribute.String("http.host", c.Request.Host),
		}
		if serviceName != "" {
			attrs = append(attrs, attribute.String("service.name", serviceName))
		}

		ctx, span := getTracer().Start(ctx, spanName,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(attrs...),
		)
		// P1 #19：defer 保证下游 handler panic（在上游 recovery 捕获前）时 span 也被结束，
		// 不泄漏未结束的 span。
		defer span.End()

		// C13d：更新 c.Request，使下游 c.Request.Context() 含 span；
		// 同时保留 c.Set("otel_ctx", ctx) 兼容既有 GetContext 用法。
		c.Request = c.Request.WithContext(ctx)
		c.Set("otel_ctx", ctx)

		// 将 TraceID 添加到响应头
		if span.SpanContext().HasTraceID() {
			c.Header("X-Trace-ID", span.SpanContext().TraceID().String())
		}

		// 执行请求
		c.Next()

		// 设置 Span 状态
		status := c.Writer.Status()
		span.SetAttributes(semconv.HTTPResponseStatusCodeKey.Int(status))

		if status >= 400 {
			span.SetStatus(codes.Error, http.StatusText(status))
		}
		// 成功路径不显式设 codes.Ok（M18）：OTel 规范中 Span 状态默认 UNSET，
		// 仅在错误时设 Error；显式设 Ok 会掩盖下游子 Span 的真实错误状态。
	}
}

// GetContext 从 Gin Context 获取 OpenTelemetry Context
//
// C13：裸断言改 comma-ok，防 "otel_ctx" 被外部置为非 context 值时 panic。
func GetContext(c *gin.Context) context.Context {
	if v, exists := c.Get("otel_ctx"); exists {
		if ctx, ok := v.(context.Context); ok {
			return ctx
		}
	}
	return c.Request.Context()
}

// GetTraceID 获取当前 TraceID
func GetTraceID(c *gin.Context) string {
	ctx := GetContext(c)
	span := trace.SpanFromContext(ctx)
	if span.SpanContext().HasTraceID() {
		return span.SpanContext().TraceID().String()
	}
	return ""
}

// StartSpan 创建子 Span
func StartSpan(c *gin.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	ctx := GetContext(c)
	return getTracer().Start(ctx, name,
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(attrs...),
	)
}

// StartSpanFromContext 从 Context 创建 Span
func StartSpanFromContext(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return getTracer().Start(ctx, name,
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(attrs...),
	)
}

// RecordError 记录错误
func RecordError(c *gin.Context, err error) {
	if c == nil || err == nil {
		return
	}
	ctx := GetContext(c)
	span := trace.SpanFromContext(ctx)
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}

// RecordErrorToSpan 记录错误到指定 Span
func RecordErrorToSpan(span trace.Span, err error) {
	if span == nil || err == nil {
		return
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}

// AddAttributes 添加属性到当前 Span
func AddAttributes(c *gin.Context, attrs ...attribute.KeyValue) {
	ctx := GetContext(c)
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(attrs...)
}

// GetTracer 获取全局 Tracer
func GetTracer() trace.Tracer {
	return getTracer()
}

// SetAttribute 设置单个属性
func SetAttribute(c *gin.Context, key string, value any) {
	ctx := GetContext(c)
	span := trace.SpanFromContext(ctx)

	switch v := value.(type) {
	case string:
		span.SetAttributes(attribute.String(key, v))
	case int:
		span.SetAttributes(attribute.Int(key, v))
	case int64:
		span.SetAttributes(attribute.Int64(key, v))
	case bool:
		span.SetAttributes(attribute.Bool(key, v))
	case float64:
		span.SetAttributes(attribute.Float64(key, v))
	default:
		span.SetAttributes(attribute.String(key, fmt.Sprintf("%v", v)))
	}
}
