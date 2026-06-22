package trace

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
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

// TracerProvider 全局 TracerProvider
var tracerProvider *sdktrace.TracerProvider

// Tracer 全局 Tracer
var tracer trace.Tracer

// Init 初始化链路追踪
func Init(cfg Config) error {
	if !cfg.Enabled {
		// 设置 Noop Tracer
		otel.SetTracerProvider(trace.NewNoopTracerProvider())
		tracer = otel.Tracer(cfg.ServiceName)
		return nil
	}

	// 创建资源
	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
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
	tracerProvider = sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.TraceIDRatioBased(cfg.SampleRatio)),
	)

	// 设置全局 TracerProvider
	otel.SetTracerProvider(tracerProvider)

	// 设置传播器
	propagator := createPropagator(cfg.Propagator)
	otel.SetTextMapPropagator(propagator)

	// 创建 Tracer
	tracer = otel.Tracer(cfg.ServiceName)

	return nil
}

// createExporter 创建导出器
func createExporter(cfg Config) (sdktrace.SpanExporter, error) {
	switch cfg.ExporterType {
	case "otlp-http":
		client := otlptracehttp.NewClient(
			otlptracehttp.WithEndpoint(cfg.Endpoint),
		)
		return otlptrace.New(context.Background(), client)
	case "otlp-grpc":
		client := otlptracegrpc.NewClient(
			otlptracegrpc.WithEndpoint(cfg.Endpoint),
		)
		return otlptrace.New(context.Background(), client)
	default:
		return nil, nil
	}
}

// createPropagator 创建传播器
func createPropagator(propagatorType string) propagation.TextMapPropagator {
	switch propagatorType {
	case "w3c":
		return propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		)
	default:
		return propagation.TraceContext{}
	}
}

// Close 关闭链路追踪
func Close(ctx context.Context) error {
	if tracerProvider != nil {
		return tracerProvider.Shutdown(ctx)
	}
	return nil
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

		ctx, span := tracer.Start(ctx, spanName,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				semconv.HTTPRequestMethodKey.String(c.Request.Method),
				semconv.URLPathKey.String(c.Request.URL.Path),
				semconv.HTTPRouteKey.String(c.FullPath()),
				attribute.String("http.user_agent", c.Request.UserAgent()),
				attribute.String("http.host", c.Request.Host),
			),
		)

		// 将 context 存入 Gin Context
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
		} else {
			span.SetStatus(codes.Ok, "")
		}

		// 结束 Span
		span.End()
	}
}

// GetContext 从 Gin Context 获取 OpenTelemetry Context
func GetContext(c *gin.Context) context.Context {
	if ctx, exists := c.Get("otel_ctx"); exists {
		return ctx.(context.Context)
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
	return tracer.Start(ctx, name,
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(attrs...),
	)
}

// StartSpanFromContext 从 Context 创建 Span
func StartSpanFromContext(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return tracer.Start(ctx, name,
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
	return tracer
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