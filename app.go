package xlgo

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/EthanCodeCraft/xlgo-core/config"
	"github.com/EthanCodeCraft/xlgo-core/database"
	"github.com/EthanCodeCraft/xlgo-core/logger"
	"github.com/EthanCodeCraft/xlgo-core/middleware"
	"github.com/EthanCodeCraft/xlgo-core/router"
	"github.com/EthanCodeCraft/xlgo-core/storage"
	"github.com/EthanCodeCraft/xlgo-core/wire"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Version 框架版本号。发版时只改这一处，避免版本字面量散落各处。
// CLI（xlgo version）、脚手架生成的 go.mod 等均引用此常量。
const Version = "1.0.3"

// HealthCheckFunc 健康检查函数
type HealthCheckFunc func(context.Context) error

// Migrator 数据库迁移函数
type Migrator func(*gorm.DB) error

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
	enableWire         bool
	enableHealth       bool
	enableSwagger      bool
	enableAutoMigrate  bool

	staticRoutes []staticRoute
	migrators    []Migrator
	healthChecks []router.HealthCheck
	initialized  bool
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

// WithWire 启用服务容器
func WithWire() Option {
	return func(a *App) { a.enableWire = true }
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

// WithoutWire 关闭服务容器
func WithoutWire() Option {
	return func(a *App) { a.enableWire = false }
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
		a.enableWire = true
		a.enableHealth = true
		a.enableSwagger = true
		a.enableAutoMigrate = true
		a.staticRoutes = append(a.staticRoutes, staticRoute{relativePath: "/public", root: "./public"})
	}
}

// New 创建新应用
func New(opts ...Option) *App {
	app := &App{}
	app.router = gin.New()
	app.registry = router.NewRegistry(app.router)

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
		a.healthChecks = append(a.healthChecks, router.HealthCheck{Name: "mysql", Check: func(context.Context) error {
			status := database.HealthCheck()
			if !status["master"] {
				return errors.New("mysql master unavailable")
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

	if a.enableWire {
		wire.InitServices()
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

	a.router.Use(gin.Recovery())
	a.router.Use(middleware.Recover())

	for _, staticRoute := range a.staticRoutes {
		a.router.Static(staticRoute.relativePath, staticRoute.root)
	}

	if a.enableSwagger {
		router.RegisterSwaggerRoutes(a.router)
	}
	if a.enableHealth {
		router.RegisterHealthRoute(a.router, a.healthChecks...)
	}

	a.registry.Apply()
	a.initialized = true
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
	port := a.config.Server.Port

	a.server = &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      a.router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		logger.Infof("服务器启动，监听端口 %d", port)
		if err := a.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var errs []error
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

// StartServerWithPort 使用指定端口启动服务器（简化版本）
// 注意: 此函数会阻塞，需要自行处理信号
func StartServerWithPort(r *gin.Engine, port int) error {
	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	logger.Infof("服务器启动，监听端口 %d", port)
	return server.ListenAndServe()
}

// GracefulShutdown 优雅关闭辅助函数
func GracefulShutdown(server *http.Server, timeout time.Duration, cleanupFuncs ...func()) error {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(quit)

	<-quit
	logger.Info("收到关闭信号，开始优雅关闭...")

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var errs []error
	if err := server.Shutdown(ctx); err != nil {
		logger.Warnf("服务器关闭超时: %v", err)
		errs = append(errs, err)
		if closeErr := server.Close(); closeErr != nil {
			errs = append(errs, closeErr)
		}
	}

	for _, cleanup := range cleanupFuncs {
		if cleanup != nil {
			cleanup()
		}
	}

	logger.Info("应用已优雅关闭")
	return errors.Join(errs...)
}
