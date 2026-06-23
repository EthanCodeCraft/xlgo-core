package router

import (
	"github.com/EthanCodeCraft/xlgo-core/middleware"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// RegisterMetricsRoute 注册 Prometheus 指标暴露端点与采集中间件（#18）。
//
// 默认路径 /metrics。传入 path 可自定义。同时把 Metrics() 中间件挂到该组，
// 这样只有业务路由被统计，/metrics 自身与 /health 等基础路由不计入。
//
// 用法：
//
//	router.RegisterMetricsRoute(r)              // /metrics
//	router.RegisterMetricsRoute(r, "/metrics")  // 等价
func RegisterMetricsRoute(r *gin.Engine, path ...string) {
	p := "/metrics"
	if len(path) > 0 && path[0] != "" {
		p = path[0]
	}
	// 指标采集中间件挂在根引擎，统计所有业务请求
	r.Use(middleware.Metrics())
	r.GET(p, gin.WrapH(promhttp.Handler()))
}
