package middleware

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// #18 Prometheus 指标。三个标配：
//   - http_requests_total: 请求计数（按 method/route/status 维度）
//   - http_request_duration_seconds: 请求耗时直方图（按 method/route 维度）
//   - http_requests_in_flight: 当前在飞请求数

var (
	httpRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "HTTP 请求总数。",
		},
		[]string{"method", "route", "status"},
	)

	httpRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP 请求耗时（秒）。",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "route"},
	)

	httpRequestsInFlight = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "http_requests_in_flight",
			Help: "正在处理的 HTTP 请求数。",
		},
	)
)

// Metrics Prometheus 指标中间件（#18）。记录请求计数、耗时、在飞数。
// route 标签用 c.FullPath()（路由模板，如 /api/v1/users/:id），避免高基数。
func Metrics() gin.HandlerFunc {
	return func(c *gin.Context) {
		httpRequestsInFlight.Inc()
		start := time.Now()

		c.Next()

		httpRequestsInFlight.Dec()
		elapsed := time.Since(start).Seconds()
		status := strconv.Itoa(c.Writer.Status())
		route := c.FullPath()
		if route == "" {
			route = "not_found"
		}
		method := c.Request.Method

		httpRequestsTotal.WithLabelValues(method, route, status).Inc()
		httpRequestDuration.WithLabelValues(method, route).Observe(elapsed)
	}
}
