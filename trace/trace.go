package trace

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sync"
	"sync/atomic"

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
// Init 之前/Close 之后均为 Noop，保证任何时刻 Load 非 nil，请求期不 panic。
var tracerProviderPtr atomic.Pointer[sdktrace.TracerProvider]

// tracerPtr 全局 Tracer（atomic，C13a）。Init 之前为 Noop，调用安全。
var tracerPtr atomic.Pointer[trace.Tracer]

// closeOnce 保证 Close 只真正 Shutdown 一次（M18），重复调用安全返回 nil。
var closeOnce sync.Once
var closeErr error

func init() {
	// 初始化为 Noop，保证包级函数在任何时刻（未 Init / 已 Close）Load 均非 nil（C13a）。
	noopProvider := trace.NewNoopTracerProvider()
	noopTracer := noopProvider.Tracer("xlgo")
	tp := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.NeverSample()))
	tracerProviderPtr.Store(tp)
	tracerPtr.Store(&noopTracer)
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
	if !cfg.Enabled {
		// 设置 Noop Tracer
		noopProvider := trace.NewNoopTracerProvider()
		otel.SetTracerProvider(noopProvider)
		noopTracer := noopProvider.Tracer(cfg.ServiceName)
		tracerPtr.Store(&noopTracer)
		// 不替换 tracerProviderPtr（保留兜底 NeverSample provider），无 exporter 需关闭。
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

	// 设置全局 TracerProvider
	otel.SetTracerProvider(newProvider)

	// 设置传播器（非法类型返错，C13e 不再静默回落）
	prop, err := createPropagator(cfg.Propagator)
	if err != nil {
		// 传播器非法：回滚已创建的 provider，避免泄漏。
		_ = newProvider.Shutdown(context.Background())
		return err
	}
	otel.SetTextMapPropagator(prop)

	// 原子替换：先建新 provider，成功后再 Store，并关闭旧 provider（若持有 exporter）。
	oldProvider := tracerProviderPtr.Swap(newProvider)
	tracer := newProvider.Tracer(cfg.ServiceName)
	tracerPtr.Store(&tracer)
	if oldProvider != nil {
		_ = oldProvider.Shutdown(context.Background())
	}

	return nil
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
		return otlptrace.New(context.Background(), client)
	case "otlp-grpc":
		opts := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(cfg.Endpoint)}
		if cfg.Insecure {
			opts = append(opts, otlptracegrpc.WithInsecure()) // C13c
		}
		client := otlptracegrpc.NewClient(opts...)
		return otlptrace.New(context.Background(), client)
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

// Close 关闭链路追踪（幂等，M18：sync.Once 保证只 Shutdown 一次，重复调用安全）。
func Close(ctx context.Context) error {
	closeOnce.Do(func() {
		// 取出当前 provider 并 Shutdown；Store 回兜底 NeverSample provider，
		// 防 Close 后再用已关闭 provider（C13a）。
		tp := tracerProviderPtr.Load()
		fallback := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.NeverSample()))
		tracerProviderPtr.Store(fallback)
		noopTracer := trace.NewNoopTracerProvider().Tracer("xlgo")
		tracerPtr.Store(&noopTracer)
		if tp != nil {
			closeErr = tp.Shutdown(ctx)
		}
	})
	return closeErr
}

// Middleware Gin 中间件
func Middleware(serviceName string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 从请求中提取 TraceContext
		ctx := otel.GetTextMapPropagator().Extract(c.Request.Context(), propagation.HeaderCarrier(c.Request.Header))

		// 创建 Span
		spanName := c.Request.Method + " " + c.FullPath()
		if spanName == "" {
			spanName = c.Request.Method + " " + c.Request.URL.Path
		}

		ctx, span := getTracer().Start(ctx, spanName,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				semconv.HTTPRequestMethodKey.String(c.Request.Method),
				semconv.URLPathKey.String(c.Request.URL.Path),
				semconv.HTTPRouteKey.String(c.FullPath()),
				attribute.String("http.user_agent", c.Request.UserAgent()),
				attribute.String("http.host", c.Request.Host),
			),
		)

		// C13d：更新 c.Request，使下游 c.Request.Context() 含 span；
		// 同时保留 c.Set("otel_ctx", ctx) 兼容既有 GetContext 用法。
		c.Request = c.Request.WithContext(ctx)
		c.Set("otel_ctx", ctx)

		// 将 TraceID 添加到响应头
		traceID := span.SpanContext().TraceID().String()
		c.Header("X-Trace-ID", traceID)

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

		// 结束 Span
		span.End()
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
	ctx := GetContext(c)
	span := trace.SpanFromContext(ctx)
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}

// RecordErrorToSpan 记录错误到指定 Span
func RecordErrorToSpan(span trace.Span, err error) {
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