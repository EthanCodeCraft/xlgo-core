package router

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

// HealthCheck 健康检查项
type HealthCheck struct {
	Name     string
	Check    func(context.Context) error
	Disabled bool
}

// runHealthChecks 执行所有检查项，返回总体状态、HTTP code 与逐项结果。
// 无检查项时视为健康（用于 /livez 与无依赖场景）。
func runHealthChecks(ctx context.Context, checks []HealthCheck) (string, int, map[string]string) {
	if len(checks) == 0 {
		return "ok", http.StatusOK, nil
	}
	status := "ok"
	code := http.StatusOK
	result := make(map[string]string, len(checks))
	for _, check := range checks {
		if check.Name == "" {
			continue
		}
		if check.Disabled || check.Check == nil {
			result[check.Name] = "disabled"
			continue
		}
		if err := check.Check(ctx); err != nil {
			result[check.Name] = "error"
			status = "error"
			code = http.StatusServiceUnavailable
			continue
		}
		result[check.Name] = "ok"
	}
	return status, code, result
}

// RegisterHealthRoute 注册健康检查路由（兼容端点，等价于 readiness）。
func RegisterHealthRoute(r *gin.Engine, checks ...HealthCheck) {
	r.GET("/health", func(c *gin.Context) {
		status, code, result := runHealthChecks(c.Request.Context(), checks)
		if result == nil {
			c.JSON(http.StatusOK, gin.H{"status": status})
			return
		}
		c.JSON(code, gin.H{"status": status, "checks": result})
	})
}

// RegisterLivenessRoute 注册存活性探针（#17）。
// GET /livez 永不依赖外部，仅表示进程存活，始终返回 200。
// 供 K8s livenessProbe 使用：失败由进程崩溃体现，而非端点返回 503。
func RegisterLivenessRoute(r *gin.Engine) {
	r.GET("/livez", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
}

// RegisterReadinessRoute 注册就绪性探针（#17）。
// GET /readyz 复用 HealthCheck 检查依赖（mysql/redis...），任一失败返回 503。
// 供 K8s readinessProbe 使用：未就绪时不接流量。
func RegisterReadinessRoute(r *gin.Engine, checks ...HealthCheck) {
	r.GET("/readyz", func(c *gin.Context) {
		status, code, result := runHealthChecks(c.Request.Context(), checks)
		if result == nil {
			c.JSON(http.StatusOK, gin.H{"status": status})
			return
		}
		c.JSON(code, gin.H{"status": status, "checks": result})
	})
}

// RegisterSwaggerRoutes 注册 Swagger 文档路由
func RegisterSwaggerRoutes(r *gin.Engine) {
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
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
	// 作为模块注册时，路由在根路径
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
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
	engine       *gin.Engine
	modules      []Module
	versions     map[string]*VersionedAPI
	middlewareGroups map[string]*MiddlewareGroup
	globalMiddlewares []gin.HandlerFunc
}

// NewRegistry 创建路由注册中心
func NewRegistry(engine *gin.Engine) *Registry {
	return &Registry{
		engine:       engine,
		modules:      make([]Module, 0),
		versions:     make(map[string]*VersionedAPI),
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

func (m *namedModule) Name() string { return m.name }
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

// Apply 应用所有路由注册
func (r *Registry) Apply() {
	// 应用全局中间件
	r.engine.Use(r.globalMiddlewares...)

	// 注册无版本模块
	for _, module := range r.modules {
		module.Register(r.engine.Group(""))
	}

	// 注册版本化 API
	for _, v := range r.versions {
		group := r.engine.Group(v.BasePath)
		if len(v.Middlewares) > 0 {
			group.Use(v.Middlewares...)
		}
		for _, module := range v.Modules {
			module.Register(group)
		}
	}
}

// ===== 全局注册中心 =====

var globalRegistry *Registry

// Init 初始化全局注册中心
func Init(engine *gin.Engine) *Registry {
	globalRegistry = NewRegistry(engine)
	return globalRegistry
}

// GetRegistry 获取全局注册中心
func GetRegistry() *Registry {
	return globalRegistry
}

// Use 注册全局中间件（全局方式）
func Use(middlewares ...gin.HandlerFunc) *Registry {
	return globalRegistry.Use(middlewares...)
}

// RegisterModule 注册模块（全局方式）
func RegisterModule(module Module) *Registry {
	return globalRegistry.RegisterModule(module)
}

// RegisterModuleFunc 注册函数式模块（全局方式）
func RegisterModuleFunc(name string, fn func(r *gin.RouterGroup)) *Registry {
	return globalRegistry.RegisterModuleFunc(name, fn)
}

// RegisterVersion 注册版本化 API（全局方式）
func RegisterVersion(version *VersionedAPI) *Registry {
	return globalRegistry.RegisterVersion(version)
}

// Apply 应用路由注册（全局方式）
func Apply() {
	globalRegistry.Apply()
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

// GroupWithMiddlewareGroup 使用中间件分组创建路由组
func GroupWithMiddlewareGroup(engine *gin.Engine, path string, groupName string) *gin.RouterGroup {
	middlewares := GetRegistry().GetMiddlewareGroup(groupName)
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