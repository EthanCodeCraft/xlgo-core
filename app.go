package xlgo

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/EthanCodeCraft/xlgo-core/cache"
	"github.com/EthanCodeCraft/xlgo-core/config"
	"github.com/EthanCodeCraft/xlgo-core/database"
	"github.com/EthanCodeCraft/xlgo-core/logger"
	"github.com/EthanCodeCraft/xlgo-core/middleware"
	"github.com/EthanCodeCraft/xlgo-core/response"
	"github.com/EthanCodeCraft/xlgo-core/router"
	"github.com/EthanCodeCraft/xlgo-core/storage"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Version 框架版本号。发版时只改这一处，避免版本字面量散落各处。
// CLI（xlgo version）、脚手架生成的 go.mod 等均引用此常量。
const Version = "1.1.1"

// HealthCheckFunc 健康检查函数
type HealthCheckFunc func(context.Context) error

// Migrator 数据库迁移函数
type Migrator func(*gorm.DB) error

// Hook 生命周期钩子。各回调在 App 生命周期的对应阶段被调用：
//   - OnInit:  Init() 内组件初始化完成后、路由注册前
//   - OnStart: StartServer() 监听端口前
//   - OnReady: 端口就绪后（已开始接受连接）
//   - OnStop:  Shutdown() 开头，关 HTTP 之前
//
// OnInit/OnStart/OnStop 返回 error 会中断流程并向上返回。
type Hook struct {
	Name    string
	OnInit  func(*App) error
	OnStart func(*App) error
	OnReady func(*App)
	OnStop  func(*App) error
}

type staticRoute struct {
	relativePath string
	root         string
}

// App 应用实例
type App struct {
	config        *config.Config
	configPath    string
	configManager *config.Manager
	router        *gin.Engine
	registry      *router.Registry
	server        *http.Server

	enableLogger       bool
	enableMySQL        bool
	enableRedis        bool
	enableStorage      bool
	enableHealth       bool
	enableSwagger      bool
	enableAutoMigrate  bool
	enableLiveness     bool
	enableReadiness    bool
	enableMetrics      bool
	metricsPath        string

	staticRoutes []staticRoute
	migrators    []Migrator
	healthChecks []router.HealthCheck
	hooks        []Hook
	initialized  bool

	// 请求级超时（#19），<=0 表示不启用
	requestTimeout time.Duration

	// in-flight goroutine 管理（#22）
	rootCtx context.Context    // 根 ctx，App.Go 启动的 goroutine 共享
	cancel  context.CancelFunc // Shutdown 时 cancel，通知后台任务退出
	wg      sync.WaitGroup     // 跟踪 App.Go 启动的 goroutine
}

// Option 应用选项
type Option func(*App)

// WithConfigPath 设置配置文件路径
func WithConfigPath(path string) Option {
	return func(a *App) {
		a.configPath = path
		a.configManager = config.NewManager(path)
	}
}

// WithConfig 设置配置对象
func WithConfig(cfg *config.Config) Option {
	return func(a *App) {
		a.config = cfg
		config.Set(cfg)
	}
}

// WithLogger 启用日志
func WithLogger() Option {
	return func(a *App) { a.enableLogger = true }
}

// WithMySQL 启用 MySQL
func WithMySQL() Option {
	return func(a *App) { a.enableMySQL = true }
}

// WithRedis 启用 Redis
func WithRedis() Option {
	return func(a *App) { a.enableRedis = true }
}

// WithStorage 启用文件存储
func WithStorage() Option {
	return func(a *App) { a.enableStorage = true }
}

// WithHealthRoutes 启用健康检查路由
func WithHealthRoutes() Option {
	return func(a *App) { a.enableHealth = true }
}

// WithSwaggerRoutes 启用 Swagger 路由
func WithSwaggerRoutes() Option {
	return func(a *App) { a.enableSwagger = true }
}

// WithDefaultRoutes 启用默认路由（健康检查、Swagger）
func WithDefaultRoutes() Option {
	return func(a *App) {
		a.enableHealth = true
		a.enableSwagger = true
	}
}

// WithLivenessRoute 启用存活性探针路由 GET /livez（#17）。
// 永不依赖外部，始终 200，供 K8s livenessProbe。
func WithLivenessRoute() Option {
	return func(a *App) { a.enableLiveness = true }
}

// WithReadinessRoute 启用就绪性探针路由 GET /readyz（#17）。
// 复用 healthChecks 检查依赖，失败返回 503，供 K8s readinessProbe。
func WithReadinessRoute() Option {
	return func(a *App) { a.enableReadiness = true }
}

// WithMetricsRoute 启用 Prometheus 指标端点与采集中间件（#18）。
// path 默认 /metrics，传入可自定义。
func WithMetricsRoute(path ...string) Option {
	return func(a *App) {
		a.enableMetrics = true
		if len(path) > 0 && path[0] != "" {
			a.metricsPath = path[0]
		}
	}
}

// WithoutLogger 关闭日志。
//
// Without* 系列的定位：xlgo.New() 默认是轻量应用（组件全关），故 Without*
// 主要用于 NewFullStack / RunFullStack 启用全部组件后，排除个别不需要的项。
// 例如：xlgo.NewFullStack(xlgo.WithoutSwaggerRoutes()) 全组件但关 Swagger。
func WithoutLogger() Option {
	return func(a *App) { a.enableLogger = false }
}

// WithoutMySQL 关闭 MySQL
func WithoutMySQL() Option {
	return func(a *App) { a.enableMySQL = false }
}

// WithoutRedis 关闭 Redis
func WithoutRedis() Option {
	return func(a *App) { a.enableRedis = false }
}

// WithoutStorage 关闭文件存储
func WithoutStorage() Option {
	return func(a *App) { a.enableStorage = false }
}

// WithoutWire 已移除（wire 包在 v1.1.0 删除）。保留空函数仅为编译兼容，
// 调用无副作用。后续版本将删除。
func WithoutWire() Option {
	return func(a *App) {}
}



// WithAutoMigrate 启用数据库迁移（需配合 WithMigrator/WithModels 注册迁移逻辑）
func WithAutoMigrate() Option {
	return func(a *App) { a.enableAutoMigrate = true }
}

// WithoutAutoMigrate 关闭数据库迁移（即使已通过 WithMigrator/WithModels 注册）
func WithoutAutoMigrate() Option {
	return func(a *App) { a.enableAutoMigrate = false }
}

// WithoutHealthRoutes 关闭健康检查路由
func WithoutHealthRoutes() Option {
	return func(a *App) { a.enableHealth = false }
}

// WithoutSwaggerRoutes 关闭 Swagger 路由
func WithoutSwaggerRoutes() Option {
	return func(a *App) { a.enableSwagger = false }
}

// WithoutDefaultRoutes 关闭默认路由（健康检查、Swagger）
func WithoutDefaultRoutes() Option {
	return func(a *App) {
		a.enableHealth = false
		a.enableSwagger = false
	}
}

// WithStatic 注册静态文件服务
func WithStatic(relativePath, root string) Option {
	return func(a *App) {
		a.staticRoutes = append(a.staticRoutes, staticRoute{relativePath: relativePath, root: root})
	}
}

// WithPublicStatic 注册默认 public 静态文件服务
func WithPublicStatic() Option {
	return WithStatic("/public", "./public")
}

// WithHealthCheck 注册健康检查项
func WithHealthCheck(name string, check HealthCheckFunc) Option {
	return func(a *App) {
		a.healthChecks = append(a.healthChecks, router.HealthCheck{Name: name, Check: check})
	}
}

// WithMigrator 注册数据库迁移函数（自动启用 AutoMigrate）
func WithMigrator(m Migrator) Option {
	return func(a *App) {
		if m != nil {
			a.migrators = append(a.migrators, m)
			a.enableAutoMigrate = true
		}
	}
}

// WithHook 注册生命周期钩子（#12）。可多次调用注册多个，按注册顺序触发。
// 详见 Hook 类型注释。
func WithHook(h Hook) Option {
	return func(a *App) {
		a.hooks = append(a.hooks, h)
	}
}

// WithRequestTimeout 设置请求级超时（#19）。下游 GORM/Redis 走
// c.Request.Context() 即可级联取消。d <= 0 不启用。
func WithRequestTimeout(d time.Duration) Option {
	return func(a *App) { a.requestTimeout = d }
}

// WithModels 注册 GORM 自动迁移模型（自动启用 AutoMigrate）
func WithModels(models ...any) Option {
	return WithMigrator(func(db *gorm.DB) error {
		return db.AutoMigrate(models...)
	})
}

// WithModules 注册模块
func WithModules(modules ...router.Module) Option {
	return func(a *App) {
		for _, m := range modules {
			a.registry.RegisterModule(m)
		}
	}
}

// WithVersions 注册版本化 API
func WithVersions(versions ...*router.VersionedAPI) Option {
	return func(a *App) {
		for _, v := range versions {
			a.registry.RegisterVersion(v)
		}
	}
}

// WithMiddlewares 注册全局中间件
func WithMiddlewares(middlewares ...gin.HandlerFunc) Option {
	return func(a *App) {
		a.registry.Use(middlewares...)
	}
}

// WithFullStack 启用全功能默认组件
func WithFullStack() Option {
	return func(a *App) {
		a.enableLogger = true
		a.enableMySQL = true
		a.enableRedis = true
		a.enableStorage = true
		a.enableHealth = true
		a.enableSwagger = true
		a.enableAutoMigrate = true
		// 生产就绪路由（#17/#18）
		a.enableLiveness = true
		a.enableReadiness = true
		a.enableMetrics = true
		a.staticRoutes = append(a.staticRoutes, staticRoute{relativePath: "/public", root: "./public"})
	}
}

// New 创建新应用
func New(opts ...Option) *App {
	app := &App{}
	app.router = gin.New()
	app.registry = router.NewRegistry(app.router)
	// rootCtx 生命周期与 App 一致，不依赖 Init，使 App.Go 在 Init 前也可用（#22）
	app.rootCtx, app.cancel = context.WithCancel(context.Background())

	for _, opt := range opts {
		opt(app)
	}

	return app
}

// NewFullStack 创建启用默认全功能组件的应用
func NewFullStack(opts ...Option) *App {
	all := append([]Option{WithFullStack()}, opts...)
	return New(all...)
}

// RunFullStack 创建并启动启用默认全功能组件的应用
func RunFullStack(opts ...Option) error {
	return NewFullStack(opts...).Run()
}

// Init 初始化应用，不启动 HTTP 监听
func (a *App) Init() error {
	if a.initialized {
		return nil
	}

	cfg, err := a.resolveConfig()
	if err != nil {
		return err
	}
	if cfg.IsProduction() {
		gin.SetMode(gin.ReleaseMode)
	}

	if a.enableLogger {
		if err := logger.Init(cfg); err != nil {
			return fmt.Errorf("初始化日志失败: %w", err)
		}
	}

	if a.enableMySQL {
		if err := database.InitDB(cfg); err != nil {
			return fmt.Errorf("初始化数据库失败: %w", err)
		}
		a.healthChecks = append(a.healthChecks, router.HealthCheck{Name: "mysql", Check: func(ctx context.Context) error {
			// 优先读探活缓存标记（#21），避免每次探针都同步 ping
			if !database.IsDBHealthy() {
				return errors.New("mysql 主库探活不健康")
			}
			status := database.HealthCheck()
			if !status["master"] {
				return errors.New("mysql 主库不可用")
			}
			return nil
		}})
	}

	if a.enableRedis {
		if err := database.InitRedis(cfg); err != nil {
			return fmt.Errorf("初始化 Redis 失败: %w", err)
		}
		a.healthChecks = append(a.healthChecks, router.HealthCheck{Name: "redis", Check: database.HealthCheckRedis})
	}

	if a.enableStorage {
		if err := storage.Init(&cfg.Storage); err != nil {
			return fmt.Errorf("初始化存储失败: %w", err)
		}
	}

	if a.enableRedis {
		// Redis 就绪后初始化缓存（cache 依赖 Redis 客户端）
		cache.Init()
	}

	if a.enableAutoMigrate && len(a.migrators) > 0 {
		if !a.enableMySQL {
			return errors.New("注册了数据库迁移但未启用 MySQL")
		}
		db := database.GetDB()
		if db == nil {
			return errors.New("MySQL 未初始化，无法执行数据库迁移")
		}
		for _, migrator := range a.migrators {
			if err := migrator(db); err != nil {
				return fmt.Errorf("数据库迁移失败: %w", err)
			}
		}
	}

	// 全局中间件链：RequestID 必须最先装入，保证后续 Recovery/日志/响应都能拿到 request_id（#24）
	a.router.Use(middleware.RequestID())
	a.router.Use(middleware.Recover())
	// 请求级超时（#19），配置后装入，下游走 c.Request.Context() 级联取消
	if a.requestTimeout > 0 {
		a.router.Use(middleware.Timeout(a.requestTimeout))
	}

	for _, staticRoute := range a.staticRoutes {
		a.router.Static(staticRoute.relativePath, staticRoute.root)
	}

	if a.enableSwagger {
		router.RegisterSwaggerRoutes(a.router)
	}
	if a.enableHealth {
		router.RegisterHealthRoute(a.router, a.healthChecks...)
	}
	if a.enableLiveness {
		router.RegisterLivenessRoute(a.router)
	}
	if a.enableReadiness {
		router.RegisterReadinessRoute(a.router, a.healthChecks...)
	}
	if a.enableMetrics {
		if a.metricsPath != "" {
			router.RegisterMetricsRoute(a.router, a.metricsPath)
		} else {
			router.RegisterMetricsRoute(a.router)
		}
	}

	a.registry.Apply()

	// 响应模式：默认 business（全 200 + 业务码），可配置 rest（按错误码映射 HTTP status）（#15）
	if mode := strings.TrimSpace(strings.ToLower(a.config.Server.ResponseMode)); mode != "" {
		switch mode {
		case "rest":
			response.SetMode(response.ModeREST)
		case "business":
			response.SetMode(response.ModeBusiness)
		}
	}

	// in-flight goroutine 根 ctx 在 New() 时已初始化（#22）

	// 启动主库/从库探活后台循环（#21），ctx 在 Shutdown 时取消
	if a.enableMySQL {
		a.Go(database.StartDBProbing)
	}

	a.initialized = true

	// OnInit hooks：组件初始化完成后触发（#12）
	for _, h := range a.hooks {
		if h.OnInit != nil {
			if err := h.OnInit(a); err != nil {
				return fmt.Errorf("OnInit hook %q 失败: %w", h.Name, err)
			}
		}
	}
	return nil
}

func (a *App) resolveConfig() (*config.Config, error) {
	if a.config != nil {
		return a.config, nil
	}
	if a.configManager == nil && a.configPath != "" {
		a.configManager = config.NewManager(a.configPath)
	}
	if a.configManager != nil {
		cfg, err := a.configManager.Load()
		if err != nil {
			return nil, err
		}
		// 让 App 持有的 manager 成为全局默认，
		// 使 config.Get / config.GetString 等便捷函数仍能取到正确配置。
		config.SetDefaultManager(a.configManager)
		a.config = cfg
		return cfg, nil
	}
	cfg := config.Get()
	if cfg == nil {
		return nil, config.ErrConfigNotLoaded
	}
	a.config = cfg
	return cfg, nil
}

// Run 启动应用
func (a *App) Run() error {
	if err := a.Init(); err != nil {
		return err
	}
	return a.StartServer()
}

// StartServer 启动 HTTP 服务器（支持优雅关闭）
func (a *App) StartServer() error {
	if a.config == nil {
		return config.ErrConfigNotLoaded
	}
	srvCfg := a.config.Server

	a.server = &http.Server{
		Handler:        a.router,
		ReadTimeout:    srvCfg.EffectiveReadTimeout(),
		WriteTimeout:   srvCfg.EffectiveWriteTimeout(),
		IdleTimeout:    srvCfg.EffectiveIdleTimeout(),
		MaxHeaderBytes: srvCfg.EffectiveMaxHeaderBytes(),
	}

	useUnix := strings.TrimSpace(srvCfg.UnixSocket) != ""
	if useUnix {
		a.server.Addr = srvCfg.UnixSocket
	} else {
		// Host 为空时监听所有接口（":port"）；设值时绑定指定地址（"host:port"），
		// 用于仅本机（127.0.0.1）或绑定内网网卡的场景
		a.server.Addr = fmt.Sprintf("%s:%d", srvCfg.Host, srvCfg.Port)
	}

	// OnStart hooks：监听端口前
	for _, h := range a.hooks {
		if h.OnStart != nil {
			if err := h.OnStart(a); err != nil {
				return fmt.Errorf("OnStart hook %q 失败: %w", h.Name, err)
			}
		}
	}

	serverErr := make(chan error, 1)
	go func() {
		if useUnix {
			logger.Infof("服务器启动，监听 unix socket %s", srvCfg.UnixSocket)
		} else if srvCfg.Host != "" {
			logger.Infof("服务器启动，监听 %s:%d", srvCfg.Host, srvCfg.Port)
		} else {
			logger.Infof("服务器启动，监听端口 %d（所有接口）", srvCfg.Port)
		}
		var err error
		if srvCfg.TLS.Enabled {
			err = a.server.ListenAndServeTLS(srvCfg.TLS.CertFile, srvCfg.TLS.KeyFile)
		} else {
			err = a.server.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	// OnReady hooks：端口就绪后
	for _, h := range a.hooks {
		if h.OnReady != nil {
			h.OnReady(a)
		}
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(quit)

	select {
	case err := <-serverErr:
		if err != nil {
			return fmt.Errorf("服务器启动失败: %w", err)
		}
		return nil
	case sig := <-quit:
		logger.Infof("收到信号 %v，开始优雅关闭...", sig)
		return a.Shutdown()
	}
}

// Shutdown 优雅关闭应用
func (a *App) Shutdown() error {
	shutdownTimeout := 30 * time.Second
	if a.config != nil {
		shutdownTimeout = a.config.Server.EffectiveShutdownTimeout()
	}
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	var errs []error

	// OnStop hooks：关 HTTP 之前触发（#12）
	for _, h := range a.hooks {
		if h.OnStop != nil {
			if err := h.OnStop(a); err != nil {
				errs = append(errs, fmt.Errorf("OnStop hook %q 失败: %w", h.Name, err))
			}
		}
	}

	// 取消根 ctx，通知 App.Go 启动的后台 goroutine 退出（#22）
	if a.cancel != nil {
		a.cancel()
	}

	if a.server != nil {
		logger.Info("关闭 HTTP 服务器...")
		if err := a.server.Shutdown(ctx); err != nil {
			logger.Warnf("HTTP 服务器关闭超时: %v", err)
			errs = append(errs, err)
			if closeErr := a.server.Close(); closeErr != nil {
				errs = append(errs, closeErr)
			}
		}
	}

	// 等待业务 in-flight goroutine 退出（#22），受 shutdownTimeout 约束
	waitDone := make(chan struct{})
	go func() {
		a.wg.Wait()
		close(waitDone)
	}()
	select {
	case <-waitDone:
	case <-ctx.Done():
		logger.Warnf("等待后台 goroutine 退出超时")
	}

	logger.Info("停止限流器...")
	middleware.StopRateLimiters()

	logger.Info("关闭数据库连接...")
	if err := database.CloseAll(); err != nil {
		errs = append(errs, err)
	}
	if err := database.CloseRedis(); err != nil {
		errs = append(errs, err)
	}

	logger.Info("关闭日志...")
	// 先记录最后一条 "应用已优雅关闭"，再 Close（关闭后写日志会 fall back 到 nop）
	logger.Info("应用已优雅关闭")
	if err := logger.Close(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// Go 启动一个受 App 生命周期管理的后台 goroutine（#22）。
// fn 收到的 ctx 在 Shutdown 时被 cancel，fn 应在 ctx.Done() 时及时退出。
// Shutdown 会等待所有 App.Go 启动的 goroutine 退出（带 ShutdownTimeout 超时）。
func (a *App) Go(fn func(ctx context.Context)) {
	if fn == nil {
		return
	}
	ctx := a.rootCtx
	if ctx == nil {
		ctx = context.Background()
	}
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		fn(ctx)
	}()
}

// GetRegistry 获取路由注册中心（用于动态注册）
func (a *App) GetRegistry() *router.Registry {
	return a.registry
}

// GetRouter 获取 Gin Engine（用于高级自定义）
func (a *App) GetRouter() *gin.Engine {
	return a.router
}

// GetServer 获取 HTTP Server（用于高级自定义）
func (a *App) GetServer() *http.Server {
	return a.server
}
