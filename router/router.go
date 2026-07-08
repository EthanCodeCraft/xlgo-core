package router

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

// HealthCheck 健康检查项
type HealthCheck struct {
	Name     string
	Check    func(context.Context) error
	Disabled bool
	Timeout  time.Duration
}

// DefaultHealthCheckTimeout bounds each dependency check so /health and /readyz
// do not hang behind a stuck database/client call.
const DefaultHealthCheckTimeout = 2 * time.Second

type healthCheckRunner struct {
	check   HealthCheck
	running chan struct{}
}

func newHealthCheckRunners(checks []HealthCheck) []healthCheckRunner {
	runners := make([]healthCheckRunner, len(checks))
	for i, check := range checks {
		runners[i] = healthCheckRunner{
			check:   check,
			running: make(chan struct{}, 1),
		}
	}
	return runners
}

func healthCheckTimeout(check HealthCheck) time.Duration {
	if check.Timeout > 0 {
		return check.Timeout
	}
	return DefaultHealthCheckTimeout
}

func healthCheckStatusFromContext(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	return "error"
}

func (r healthCheckRunner) run(ctx context.Context) (string, error) {
	check := r.check
	checkCtx, cancel := context.WithTimeout(ctx, healthCheckTimeout(check))
	defer cancel()

	select {
	case r.running <- struct{}{}:
	case <-checkCtx.Done():
		return healthCheckStatusFromContext(checkCtx.Err()), checkCtx.Err()
	}

	errCh := make(chan error, 1)
	go func() {
		defer func() {
			<-r.running
		}()
		defer func() {
			if rec := recover(); rec != nil {
				errCh <- fmt.Errorf("health check %q panic recovered: %v\n%s", check.Name, rec, debug.Stack())
			}
		}()
		errCh <- check.Check(checkCtx)
	}()

	select {
	case err := <-errCh:
		if err == nil {
			return "ok", nil
		}
		return healthCheckStatusFromContext(err), err
	case <-checkCtx.Done():
		return healthCheckStatusFromContext(checkCtx.Err()), checkCtx.Err()
	}
}

// registerGETOnce 幂等注册 GET 路由（H8d 收尾：消除 defaultModule 与 Register* 系列
// 并存时 Gin 重复路由 panic 的 footgun）。若 (GET, path) 已存在则静默跳过——首次注册胜出。
//
// 实现分两条路径：
//   - *gin.Engine：经 Routes() 精确预检（method+path），命中即跳过；未命中则直接注册，
//     不吞 panic——真正不同的路由冲突仍按 gin 原语义 panic，不被掩盖。
//   - *gin.RouterGroup（gin 未暴露其 engine，无法预检）：用 recover 兜底，仅吞 gin 的
//     重复路由 panic（"already registered" / "conflicts with existing wildcard"，
//     两者覆盖 gin 对重复注册的两类 panic），其余 panic 原样抛出。defaultModule 的
//     /swagger/*any 与 /health 重复注册即走此路径被吞。最坏情况（gin 改动文本）退化为
//     当前行为（panic），不引入新风险。
//
// 注册期单线程调用，无并发问题。
func registerGETOnce(r gin.IRoutes, path string, h gin.HandlerFunc) {
	if eng, ok := r.(*gin.Engine); ok {
		for _, ri := range eng.Routes() {
			if ri.Method == http.MethodGet && ri.Path == path {
				return
			}
		}
		eng.GET(path, h)
		return
	}
	defer func() {
		if rec := recover(); rec != nil {
			msg := fmt.Sprint(rec)
			if strings.Contains(msg, "already registered") ||
				strings.Contains(msg, "conflicts with existing wildcard") {
				return // 重复路由，静默跳过
			}
			panic(rec) // 非重复路由 panic，原样抛出
		}
	}()
	r.GET(path, h)
}

// runHealthChecks 执行所有检查项，返回总体状态、HTTP code 与逐项结果。
// 无检查项时视为健康（用于 /livez 与无依赖场景）。
func runHealthChecks(ctx context.Context, runners []healthCheckRunner) (string, int, map[string]string) {
	if len(runners) == 0 {
		return "ok", http.StatusOK, nil
	}
	status := "ok"
	code := http.StatusOK
	result := make(map[string]string, len(runners))
	for _, runner := range runners {
		check := runner.check
		if check.Name == "" {
			continue
		}
		if check.Disabled || check.Check == nil {
			result[check.Name] = "disabled"
			continue
		}
		checkStatus, err := runner.run(ctx)
		result[check.Name] = checkStatus
		if err != nil {
			status = "error"
			code = http.StatusServiceUnavailable
			continue
		}
	}
	return status, code, result
}

// healthHandler 返回统一的 /health 风格响应（H8d 收敛）。
// 无检查项时视为健康（200 + {"status":"ok"}）；有检查项时任一失败返回 503
// 并附逐项结果。RegisterHealthRoute / RegisterReadinessRoute / defaultModule
// 均委托此实现，避免三个 /health 行为与响应体不一致。
func healthHandler(checks []HealthCheck) gin.HandlerFunc {
	runners := newHealthCheckRunners(checks)
	return func(c *gin.Context) {
		status, code, result := runHealthChecks(c.Request.Context(), runners)
		if result == nil {
			c.JSON(http.StatusOK, gin.H{"status": status})
			return
		}
		c.JSON(code, gin.H{"status": status, "checks": result})
	}
}

// RegisterHealthRoute 注册健康检查路由（兼容端点，等价于 readiness）。
// 幂等：若 /health 已注册则跳过（首次注册胜出），避免与 defaultModule 等并存时重复路由 panic。
func RegisterHealthRoute(r *gin.Engine, checks ...HealthCheck) {
	registerGETOnce(r, "/health", healthHandler(checks))
}

// RegisterLivenessRoute 注册存活性探针（#17）。
// GET /livez 永不依赖外部，仅表示进程存活，始终返回 200。
// 供 K8s livenessProbe 使用：失败由进程崩溃体现，而非端点返回 503。
// 幂等：重复注册跳过。
func RegisterLivenessRoute(r *gin.Engine) {
	registerGETOnce(r, "/livez", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
}

// RegisterReadinessRoute 注册就绪性探针（#17）。
// GET /readyz 复用 HealthCheck 检查依赖（mysql/redis...），任一失败返回 503。
// 供 K8s readinessProbe 使用：未就绪时不接流量。
// 幂等：重复注册跳过。
func RegisterReadinessRoute(r *gin.Engine, checks ...HealthCheck) {
	registerGETOnce(r, "/readyz", healthHandler(checks))
}

// RegisterSwaggerRoutes 注册 Swagger 文档路由。幂等：重复注册跳过。
func RegisterSwaggerRoutes(r *gin.Engine) {
	registerGETOnce(r, "/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
}

// RegisterDefaultRoutes 注册框架默认路由（健康检查、Swagger）
// 用户可以选择使用或不使用这些默认路由
func RegisterDefaultRoutes(r *gin.Engine, checks ...HealthCheck) {
	RegisterSwaggerRoutes(r)
	RegisterHealthRoute(r, checks...)
}

// DefaultModule 默认路由模块（可用于 WithModules）
var DefaultModule = &defaultModule{}

type defaultModule struct{}

func (m *defaultModule) Name() string { return "default" }
func (m *defaultModule) Register(r *gin.RouterGroup) {
	// 作为模块注册时，路由在根路径。经 registerGETOnce 幂等注册（H8d 收尾）：
	// 若用户已通过 RegisterSwaggerRoutes / RegisterHealthRoute 注册过同名路由，
	// 此处静默跳过，避免 Gin 重复路由 panic。首次注册胜出。
	registerGETOnce(r, "/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
	registerGETOnce(r, "/health", healthHandler(nil))
}

// Module 路由模块接口
// 用户实现此接口来注册业务路由
type Module interface {
	// Name 模块名称（用于日志和调试）
	Name() string
	// Register 注册路由到指定组
	Register(r *gin.RouterGroup)
}

// ModuleFunc 函数式模块（简化单文件模块注册）
type ModuleFunc func(r *gin.RouterGroup)

// Register 实现 Module 接口
func (f ModuleFunc) Register(r *gin.RouterGroup) {
	f(r)
}

// Name 实现 Module 接口（函数式模块默认名称）
func (f ModuleFunc) Name() string {
	return "func-module"
}

// VersionedAPI 版本化 API 配置
type VersionedAPI struct {
	Version     string            // 版本标识，如 "v1", "v2"
	BasePath    string            // 基础路径，如 "/api/v1"
	Modules     []Module          // 该版本的模块列表
	Middlewares []gin.HandlerFunc // 该版本的公共中间件
}

// MiddlewareGroup 中间件分组
type MiddlewareGroup struct {
	Name        string
	Middlewares []gin.HandlerFunc
}

// Registry 路由注册中心
type Registry struct {
	engine            *gin.Engine
	modules           []Module
	versions          map[string]*VersionedAPI
	middlewareGroups  map[string]*MiddlewareGroup
	globalMiddlewares []gin.HandlerFunc

	// metricsMiddleware 在 Apply 时作为首个全局中间件装入（H8c），
	// 使所有经注册中心注册的路由都被采集，不依赖 RegisterMetricsRoute 的调用顺序。
	// 为 nil 时不装入。/metrics 端点本身与 /health 等基础路由直接挂在 engine 上，
	// 不经此中间件，故不被自采集。
	metricsMiddleware gin.HandlerFunc

	// applyOnce 保证 Apply 仅执行一次（H8b + P1 #13）。
	// 原实现用裸 bool 无同步，并发 Apply 会竞态并可能重复 engine.Use/重复注册致 gin panic；
	// 改用 sync.Once，二次/并发 Apply 均安全幂等。
	applyOnce sync.Once
}

// NewRegistry 创建路由注册中心
func NewRegistry(engine *gin.Engine) *Registry {
	return &Registry{
		engine:           engine,
		modules:          make([]Module, 0),
		versions:         make(map[string]*VersionedAPI),
		middlewareGroups: make(map[string]*MiddlewareGroup),
	}
}

// Use 注册全局中间件
func (r *Registry) Use(middlewares ...gin.HandlerFunc) *Registry {
	r.globalMiddlewares = append(r.globalMiddlewares, middlewares...)
	return r
}

// RegisterModule 注册模块（无版本）
func (r *Registry) RegisterModule(module Module) *Registry {
	r.modules = append(r.modules, module)
	return r
}

// RegisterModuleFunc 注册函数式模块
func (r *Registry) RegisterModuleFunc(name string, fn func(r *gin.RouterGroup)) *Registry {
	return r.RegisterModule(&namedModule{name: name, fn: fn})
}

// namedModule 命名模块包装（内部类型）
type namedModule struct {
	name string
	fn   func(r *gin.RouterGroup)
}

func (m *namedModule) Name() string                { return m.name }
func (m *namedModule) Register(r *gin.RouterGroup) { m.fn(r) }

// RegisterVersion 注册版本化 API
func (r *Registry) RegisterVersion(version *VersionedAPI) *Registry {
	r.versions[version.Version] = version
	return r
}

// RegisterMiddlewareGroup 注册中间件分组
func (r *Registry) RegisterMiddlewareGroup(group *MiddlewareGroup) *Registry {
	r.middlewareGroups[group.Name] = group
	return r
}

// GetMiddlewareGroup 获取中间件分组
func (r *Registry) GetMiddlewareGroup(name string) []gin.HandlerFunc {
	if group, ok := r.middlewareGroups[name]; ok {
		return group.Middlewares
	}
	return nil
}

// SetMetricsMiddleware 设置指标采集中间件（H8c）。Apply 时它会作为首个全局中间件
// 装入 engine，使所有经注册中心注册的路由都被采集，不再依赖注册顺序。
// 传入 nil 清除。须在 Apply 之前调用。
func (r *Registry) SetMetricsMiddleware(mw gin.HandlerFunc) {
	r.metricsMiddleware = mw
}

// Apply 应用所有路由注册。
//
// 幂等（H8b）：二次调用直接返回，避免重复 engine.Use 与 Gin 重复路由 panic。
// 装入顺序：metrics 中间件（若有）→ 用户全局中间件 → 模块/版本路由。
// metrics 置于首位保证全量业务路由被采集，且不依赖 RegisterMetricsRoute 调用顺序。
func (r *Registry) Apply() {
	r.applyOnce.Do(func() {
		// 指标采集中间件首个装入，统计所有经注册中心注册的业务路由
		if r.metricsMiddleware != nil {
			r.engine.Use(r.metricsMiddleware)
		}

		// 应用全局中间件
		r.engine.Use(r.globalMiddlewares...)

		// 注册无版本模块
		for _, module := range r.modules {
			module.Register(r.engine.Group(""))
		}

		// 注册版本化 API。P1 #13：按 version 键排序遍历，使跨版本注册顺序确定，
		// 避免 map 随机序导致重叠路径"谁先胜出"（配合 registerGETOnce）每次运行不一致。
		versionKeys := make([]string, 0, len(r.versions))
		for k := range r.versions {
			versionKeys = append(versionKeys, k)
		}
		sort.Strings(versionKeys)
		for _, k := range versionKeys {
			v := r.versions[k]
			group := r.engine.Group(v.BasePath)
			if len(v.Middlewares) > 0 {
				group.Use(v.Middlewares...)
			}
			for _, module := range v.Modules {
				module.Register(group)
			}
		}
	})
}

// ===== 全局注册中心 =====

// globalRegistry 包级全局注册中心。用 atomic.Pointer 保护读写（H8a）：
// Init 写入、各全局 helper 读取，避免裸指针与请求 goroutine 的无锁竞争。
var globalRegistry atomic.Pointer[Registry]

// Init 初始化全局注册中心。须在使用任何全局 helper（Use/RegisterModule/Apply…）之前调用。
func Init(engine *gin.Engine) *Registry {
	r := NewRegistry(engine)
	globalRegistry.Store(r)
	return r
}

// GetRegistry 获取全局注册中心，未初始化时返回 nil。
func GetRegistry() *Registry {
	return globalRegistry.Load()
}

// ensureRegistry 取全局注册中心，未初始化时以明确信息 panic（H8a）。
// 把晦涩的 nil 解引用 panic 转成可定位的初始化顺序错误。
func ensureRegistry() *Registry {
	r := globalRegistry.Load()
	if r == nil {
		panic("router: 全局注册中心未初始化，请先调用 router.Init(engine) 再使用全局 helper")
	}
	return r
}

// Use 注册全局中间件（全局方式）
func Use(middlewares ...gin.HandlerFunc) *Registry {
	return ensureRegistry().Use(middlewares...)
}

// RegisterModule 注册模块（全局方式）
func RegisterModule(module Module) *Registry {
	return ensureRegistry().RegisterModule(module)
}

// RegisterModuleFunc 注册函数式模块（全局方式）
func RegisterModuleFunc(name string, fn func(r *gin.RouterGroup)) *Registry {
	return ensureRegistry().RegisterModuleFunc(name, fn)
}

// RegisterVersion 注册版本化 API（全局方式）
func RegisterVersion(version *VersionedAPI) *Registry {
	return ensureRegistry().RegisterVersion(version)
}

// Apply 应用路由注册（全局方式）
func Apply() {
	ensureRegistry().Apply()
}

// ===== 快捷构建函数 =====

// NewVersion 创建版本化 API
func NewVersion(version, basePath string, middlewares ...gin.HandlerFunc) *VersionedAPI {
	return &VersionedAPI{
		Version:     version,
		BasePath:    basePath,
		Middlewares: middlewares,
		Modules:     make([]Module, 0),
	}
}

// AddModule 为版本添加模块
func (v *VersionedAPI) AddModule(module Module) *VersionedAPI {
	v.Modules = append(v.Modules, module)
	return v
}

// AddModuleFunc 为版本添加函数式模块
func (v *VersionedAPI) AddModuleFunc(name string, fn func(r *gin.RouterGroup)) *VersionedAPI {
	return v.AddModule(&namedModule{name: name, fn: fn})
}

// NewMiddlewareGroup 创建中间件分组
func NewMiddlewareGroup(name string, middlewares ...gin.HandlerFunc) *MiddlewareGroup {
	return &MiddlewareGroup{
		Name:        name,
		Middlewares: middlewares,
	}
}

// ===== 路由组辅助 =====

// Group 创建路由组（带中间件分组）
func Group(engine *gin.Engine, path string, middlewares ...gin.HandlerFunc) *gin.RouterGroup {
	return engine.Group(path, middlewares...)
}

// GroupWithMiddlewareGroup 使用中间件分组创建路由组。
//
// H-B 修复：改走 ensureRegistry()（与 Use/RegisterModule/Apply 等所有全局 helper 一致），
// 把"未初始化"从 nil 解引用 panic 转成可定位的明确 panic（H8a 目标）。原实现用 GetRegistry()
// 可返回 nil，nil.GetMiddlewareGroup 即 panic，错误信息晦涩。
func GroupWithMiddlewareGroup(engine *gin.Engine, path string, groupName string) *gin.RouterGroup {
	middlewares := ensureRegistry().GetMiddlewareGroup(groupName)
	return engine.Group(path, middlewares...)
}

// RESTfulRoute RESTful 路由快捷注册
type RESTfulRoute struct {
	Group *gin.RouterGroup
	Path  string
}

// NewRESTful 创建 RESTful 路由
func NewRESTful(group *gin.RouterGroup, path string) *RESTfulRoute {
	return &RESTfulRoute{Group: group, Path: path}
}

// GET 注册 GET 路由
func (r *RESTfulRoute) GET(handlers ...gin.HandlerFunc) {
	r.Group.GET(r.Path, handlers...)
}

// POST 注册 POST 路由
func (r *RESTfulRoute) POST(handlers ...gin.HandlerFunc) {
	r.Group.POST(r.Path, handlers...)
}

// PUT 注册 PUT 路由
func (r *RESTfulRoute) PUT(handlers ...gin.HandlerFunc) {
	r.Group.PUT(r.Path, handlers...)
}

// DELETE 注册 DELETE 路由
func (r *RESTfulRoute) DELETE(handlers ...gin.HandlerFunc) {
	r.Group.DELETE(r.Path, handlers...)
}

// PATCH 注册 PATCH 路由
func (r *RESTfulRoute) PATCH(handlers ...gin.HandlerFunc) {
	r.Group.PATCH(r.Path, handlers...)
}

// CRUD 注册标准 CRUD 路由
// GET /path - 列表
// GET /path/:id - 详情
// POST /path - 创建
// PUT /path/:id - 更新
// DELETE /path/:id - 删除
func (r *RESTfulRoute) CRUD(list, detail, create, update, delete gin.HandlerFunc) {
	if list != nil {
		r.Group.GET(r.Path, list)
	}
	if detail != nil {
		r.Group.GET(r.Path+"/:id", detail)
	}
	if create != nil {
		r.Group.POST(r.Path, create)
	}
	if update != nil {
		r.Group.PUT(r.Path+"/:id", update)
	}
	if delete != nil {
		r.Group.DELETE(r.Path+"/:id", delete)
	}
}
