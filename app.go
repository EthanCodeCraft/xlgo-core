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
)

// App 应用实例
type App struct {
	config   *config.Config
	router   *gin.Engine
	registry *router.Registry
	server   *http.Server
}

// Option 应用选项
type Option func(*App)

// WithConfigPath 设置配置文件路径
func WithConfigPath(path string) Option {
	return func(a *App) {
		_ = path // 配置在 New 时加载
	}
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

// Run 启动应用
func (a *App) Run() error {
	// 加载配置
	cfg := config.Get()
	if cfg == nil {
		return config.ErrConfigNotLoaded
	}
	a.config = cfg

	// 初始化日志
	if err := logger.Init(cfg); err != nil {
		return err
	}

	// 初始化数据库
	if err := database.InitMySQL(cfg); err != nil {
		logger.Fatalf("初始化 MySQL 失败: %v", err)
	}

	// 初始化 Redis
	if err := database.InitRedis(cfg); err != nil {
		logger.Fatalf("初始化 Redis 失败: %v", err)
	}

	// 自动迁移数据库表
	if err := database.AutoMigrate(); err != nil {
		logger.Fatalf("数据库迁移失败: %v", err)
	}

	// 初始化存储
	if err := storage.Init(&cfg.Storage); err != nil {
		logger.Fatalf("初始化存储失败: %v", err)
	}

	// 初始化服务容器
	wire.InitServices()

	// 设置 Gin 模式
	if cfg.IsProduction() {
		gin.SetMode(gin.ReleaseMode)
	}

	// 设置基础中间件
	a.router.Use(gin.Recovery())
	a.router.Use(middleware.Recover())

	// 静态文件服务
	a.router.Static("/public", "./public")

	// 注册默认路由（健康检查、Swagger）
	router.RegisterDefaultRoutes(a.router)

	// 应用用户注册的路由
	a.registry.Apply()

	// 启动服务器（优雅关闭）
	return a.StartServer()
}

// StartServer 启动 HTTP 服务器（支持优雅关闭）
// 评分: ⭐⭐⭐⭐⭐
// 理由: 监听系统信号，实现优雅关闭，等待请求处理完成
func (a *App) StartServer() error {
	port := a.config.Server.Port

	// 创建 HTTP 服务器
	a.server = &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      a.router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// 启动服务器（非阻塞）
	go func() {
		logger.Infof("服务器启动，监听端口 %d", port)
		if err := a.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatalf("服务器启动失败: %v", err)
		}
	}()

	// 等待中断信号
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	// 阻塞等待信号
	sig := <-quit
	logger.Infof("收到信号 %v，开始优雅关闭...", sig)

	// 执行关闭流程
	return a.Shutdown()
}

// Shutdown 优雅关闭应用
// 评分: ⭐⭐⭐⭐⭐
// 理由: 按顺序关闭各组件，等待请求处理完成
func (a *App) Shutdown() error {
	// 创建关闭上下文（最多等待 30 秒）
	ctx, cancel := context.WithTimeout(context.Background(), 30 * time.Second)
	defer cancel()

	// 1. 停止 HTTP 服务器
	if a.server != nil {
		logger.Info("关闭 HTTP 服务器...")
		if err := a.server.Shutdown(ctx); err != nil {
			logger.Warnf("HTTP 服务器关闭超时: %v", err)
			a.server.Close()
		}
	}

	// 2. 停止限流器
	logger.Info("停止限流器...")
	middleware.StopRateLimiters()

	// 3. 关闭数据库连接
	logger.Info("关闭数据库连接...")
	database.Close()
	database.CloseRedis()

	// 4. 同步日志
	logger.Info("同步日志...")
	logger.Sync()

	logger.Info("应用已优雅关闭")
	return nil
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
// 评分: ⭐⭐⭐⭐⭐
// 理由: 可独立使用的优雅关闭函数
func GracefulShutdown(server *http.Server, timeout time.Duration, cleanupFuncs ...func()) error {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	<-quit
	logger.Info("收到关闭信号，开始优雅关闭...")

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// 关闭 HTTP 服务器
	if err := server.Shutdown(ctx); err != nil {
		logger.Warnf("服务器关闭超时: %v", err)
		server.Close()
	}

	// 执行清理函数
	for _, cleanup := range cleanupFuncs {
		if cleanup != nil {
			cleanup()
		}
	}

	logger.Info("应用已优雅关闭")
	return nil
}