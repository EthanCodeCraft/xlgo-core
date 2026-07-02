package router

import (
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// RegisterMetricsRoute 注册 Prometheus 指标暴露端点（#18，H8c 修正）。
//
// 默认路径 /metrics。传入 path 可自定义。本函数仅注册暴露端点，不再用 r.Use
// 挂采集中间件——采集中间件改由 Registry.SetMetricsMiddleware 在 Apply 内作为
// 首个全局中间件装入，使所有经注册中心注册的业务路由都被采集，且不依赖本函数
// 相对其他路由注册的调用顺序。/metrics 端点自身与 /health 等基础路由不经采集中间件。
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
	// 幂等注册（H8d 收尾）：重复调用跳过，避免重复路由 panic。
	registerGETOnce(r, p, gin.WrapH(promhttp.Handler()))
}
