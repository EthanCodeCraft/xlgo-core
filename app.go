package xlgo

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/EthanCodeCraft/xlgo-core/cache"
	"github.com/EthanCodeCraft/xlgo-core/config"
	"github.com/EthanCodeCraft/xlgo-core/cron"
	"github.com/EthanCodeCraft/xlgo-core/database"
	"github.com/EthanCodeCraft/xlgo-core/logger"
	"github.com/EthanCodeCraft/xlgo-core/middleware"
	"github.com/EthanCodeCraft/xlgo-core/response"
	"github.com/EthanCodeCraft/xlgo-core/router"
	"github.com/EthanCodeCraft/xlgo-core/storage"
	"github.com/EthanCodeCraft/xlgo-core/trace"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// Version 框架版本号。发版时只改这一处，避免版本字面量散落各处。
// CLI（xlgo version）、脚手架生成的 go.mod 等均引用此常量。
const Version = "1.4.0"

// HealthCheckFunc 健康检查函数
type HealthCheckFunc func(context.Context) error

// Migrator 数据库迁移函数
type Migrator func(*gorm.DB) error

// Hook 生命周期钩子。各回调在 App 生命周期的对应阶段被调用：
//   - OnInit:  Init() 内组件初始化完成后、路由注册前
//   - OnStart: StartServer() 监听端口前
//   - OnReady: 端口就绪后（已开始接受连接）
//   - OnStop:  Shutdown() 开头，关 HTTP 之前，受 server.shutdown_timeout 约束
//
// OnInit/OnStart/OnReady/OnStop 返回 error 会中断流程并向上返回。
// A2 修复：OnReady 改为返回 error，失败时触发 shutdown。
type Hook struct {
	Name    string
	OnInit  func(*App) error
	OnStart func(*App) error
	OnReady func(*App) error
	OnStop  func(*App) error
}

type staticRoute struct {
	relativePath string
	root         string
}

// cronTask 收集 WithCronTask 注册的任务，doInit 创建 App 调度器后批量注册（照
// WithMigrator/WithHealthCheck 的"Option 收集、doInit 应用"模式）。
type cronTask struct {
	name     string
	schedule cron.Schedule
	handler  cron.TaskHandler
}

// appState 是 App 生命周期状态机（M1）。状态受 lifecycleMu 保护，单调推进：
//
//	stateCreated → stateInitializing → stateInitialized → stateStopping → stateStopped
//	                     ↓ (doInit 失败)
//	               stateStopping → stateStopped（failAfterInit 回滚）
//
// Go() 仅在 state < stateStopping 时放行 wg.Add；Init() 在 state >= stateStopping 时拒绝。
type appState int32

const (
	stateCreated appState = iota
	stateInitializing
	stateInitialized
	stateStopping
	stateStopped
)

// ErrAppClosed 表示 App 已 Shutdown 或 Init 失败已回滚，拒绝再 Init（M1）。
var ErrAppClosed = errors.New("xlgo: app already shut down")

// App 应用实例
type App struct {
	config        *config.Config
	configPath    string
	configManager *config.Manager
	router        *gin.Engine
	registry      *router.Registry
	server        *http.Server

	enableLogger      bool
	enableMySQL       bool
	enableRedis       bool
	enableStorage     bool
	enableHealth      bool
	enableSwagger     bool
	enableAutoMigrate bool
	enableLiveness    bool
	enableReadiness   bool
	enableMetrics     bool
	enableCron        bool
	enableTrace       bool
	metricsPath       string

	staticRoutes []staticRoute
	migrators    []Migrator
	healthChecks []router.HealthCheck
	hooks        []Hook

	// 生命周期状态机（M1：取代裸 initOnce，解决 Init/Shutdown 并发改资源、
	// Init-after-Shutdown、App.Go 与 wg.Wait 的 WaitGroup 契约三类缺陷）。
	// state 受 lifecycleMu 保护；initMu 串行化 doInit/doShutdown 长段，避免并发改资源。
	// Go() 持 lifecycleMu.RLock 协调 wg.Add 与 Shutdown 的 wg.Wait（Add happens-before Wait）。
	state       appState
	lifecycleMu sync.RWMutex
	initMu      sync.Mutex
	initErr     error

	// shutdownOnce 确保 doShutdown 单次执行；shutdownErr 缓存结果供并发调用者共享。
	shutdownOnce sync.Once
	shutdownErr  error

	initializedLogger bool
	initializedMySQL  bool
	initializedRedis  bool
	initializedCron   bool
	initializedTrace  bool
	initializedStorage bool
	initializedCache  bool

	loggerManager  *logger.LogManager
	loggerSnapshot *logger.DefaultSnapshot
	dbManager      *database.Manager
	previousDB     *database.Manager
	redisManager   *database.RedisManager
	previousRedis  *database.RedisManager
	storageManager  *storage.StorageManager
	previousStorage *storage.StorageManager
	scheduler        *cron.Scheduler
	previousScheduler *cron.Scheduler
	cronTasks        []cronTask
	cacheManager     *cache.CacheManager
	previousCache    *cache.CacheManager
	rateLimitRegistry     *middleware.RateLimitRegistry
	previousRateLimitRegistry *middleware.RateLimitRegistry

	// 请求级超时（#19），<=0 表示不启用
	requestTimeout time.Duration

	// in-flight goroutine 管理（#22）
	rootCtx context.Context    // 根 ctx，App.Go 启动的 goroutine 共享
	cancel  context.CancelFunc // Shutdown 时 cancel，通知后台任务退出
	wg      sync.WaitGroup     // 跟踪 App.Go 启动的 goroutine
}

// Option 应用选项
type Option func(*App)

// WithConfigPath 设置配置文件路径。
//
// M-config-5 全局可见性提示：WithConfigPath 在 Init 时会把 App 持有的 configManager 提升为
// 全局默认（config.SetDefaultManager），故 config.Get / config.GetString 等包级便捷函数可读到
// 该配置。这与 WithConfig 形成不对称：WithConfig 仅保留在 App 实例内，不污染全局，包级
// config.Get() 取不到注入配置。下游若混用 a.config 与 config.GetString，两种注入方式行为不同--
// 需要 config.Get/GetString 的下游请用 WithConfigPath（或自行 config.SetDefaultManager）。
func WithConfigPath(path string) Option {
	return func(a *App) {
		a.configPath = path
		a.configManager = config.NewManager(path)
	}
}

// WithConfig 设置配置对象。
// 配置会在传入时深拷贝成 App 私有快照，并在 Init 时 Validate；调用方后续修改 cfg
// 不会污染 App 内部配置。不再调用 config.Set(cfg)，配置仅保留在 App 实例内，不污染全局状态。
//
// M-config-5：因此 config.Get / config.GetString 等包级函数取不到此注入配置（默认空 manager）。
// 依赖 config.Get() 获取注入配置的下游代码请改用 WithConfigPath（会提升为全局默认）。
func WithConfig(cfg *config.Config) Option {
	return func(a *App) {
		a.config = cfg.Clone()
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

// WithCron 将 cron 调度器纳入 App 生命周期（Phase 3 实例化）。
// 启用后：Init 时创建 App 专属 Scheduler、注册 WithCronTask 收集的任务、提升为全局默认、
// Start 启动调度循环；Shutdown 在剩余预算内 StopWithTimeout 等待在跑任务退出。
//
// 任务注册：优先用 WithCronTask(...)（或 Init 后 app.Scheduler().AddTask(...)）注册到 App 实例。
// 包级 cron.AddTask(...) 仍可用--它代理到当前全局默认（App swap 后即 App 的调度器）--但
// 在 App.Init 之前调用会注册到 init 默认调度器、被 App swap 丢弃，故 pre-Init 注册请用 WithCronTask。
func WithCron() Option {
	return func(a *App) { a.enableCron = true }
}

// WithCronTask 注册一个定时任务到 App 的调度器（Phase 3）。蕴含启用 cron（照 WithMigrator
// 自动启用 AutoMigrate 的模式），故无需同时 WithCron。任务在 doInit 创建调度器后注册。
//
// name/schedule/handler 的校验由 cron.Scheduler.AddTask 负责（nil schedule/handler panic，
// 非法 cron 字段 panic）。动态输入请先用 cron.ParseCronStrict 处理 error。
func WithCronTask(name string, schedule cron.Schedule, handler cron.TaskHandler) Option {
	return func(a *App) {
		a.cronTasks = append(a.cronTasks, cronTask{name: name, schedule: schedule, handler: handler})
		a.enableCron = true
	}
}

// WithTrace 将链路追踪（OpenTelemetry）纳入 App 生命周期（Phase 1）。
//
// 启用后：Init 时按 config.Trace 调 trace.Init（安装 TracerProvider/Propagator）并把
// trace.Middleware 装入全局中间件链；Shutdown 时调 trace.Close 刷出 exporter 并释放
// 后台 goroutine/连接（H-14 后 Close 幂等，但 App 此前从未调它--本 Option 补上生命周期闭环）。
//
// 实际是否导出 span 由 config.Trace.Enabled 控制：Enabled=false（默认）时 trace.Init
// 安装 Noop tracer，Middleware 不 panic、不导出。故"WithTrace + trace.enabled=true + endpoint"
// 才真正出链路；仅 WithTrace 而未配 enabled 为 Noop（安全）。
//
// 不做 per-App 实例隔离：OTel 的 TracerProvider/Propagator 是进程级全局单例
// （otel.SetTracerProvider 全局生效），多 App 进程共享同一 OTel 状态，与 OTel 设计一致。
func WithTrace() Option {
	return func(a *App) { a.enableTrace = true }
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
	// Phase 5：限流器 Registry 在 New 时预创建，使 app.RateLimitRegistry() 在 Init 前即可用于
	// 路由装配（app-bound 中间件捕获此实例，请求时走 App 自己的 Registry 而非全局默认，
	// 实现多 App per-App 计数隔离）。doInit 时 swap 为全局默认供包级 facade 代理。
	app.rateLimitRegistry = middleware.NewRateLimitRegistry()

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

// Init 初始化应用，不启动 HTTP 监听（M1：状态机 + initMu 保证单次执行与并发安全）。
// 多次调用：已成功则返回缓存结果；已 Shutdown/Init 失败则返回 ErrAppClosed。
//
// initMu 串行化 doInit 与 doShutdown（避免并发改资源）；lifecycleMu 仅短暂持有以翻状态，
// 不阻塞 Go()（doInit 内 a.Go 只抢 lifecycleMu.RLock）。
//
// 注意：生命周期 hook（OnInit/OnStart/OnReady/OnStop）不允许重入调用 Init/Shutdown/Run——
// initMu 非重入，hook 内调用会自锁死锁（M1 文档约束）。
func (a *App) Init() error {
	a.initMu.Lock()
	defer a.initMu.Unlock()

	a.lifecycleMu.Lock()
	switch a.state {
	case stateCreated:
		a.state = stateInitializing
		a.lifecycleMu.Unlock()
	case stateInitialized:
		err := a.initErr
		a.lifecycleMu.Unlock()
		return err
	case stateStopping, stateStopped:
		a.lifecycleMu.Unlock()
		return ErrAppClosed
	default: // stateInitializing — initMu 串行下不可达，防御性返回
		err := a.initErr
		a.lifecycleMu.Unlock()
		return err
	}

	a.initErr = a.doInit()
	if a.initErr != nil {
		a.failAfterInit()
		return a.initErr
	}

	a.lifecycleMu.Lock()
	a.state = stateInitialized
	a.lifecycleMu.Unlock()
	return nil
}

// failAfterInit 在 doInit 失败时执行资源回滚（M1）。
// 顺序：标记 stateStopping（阻新 Go）→ cancel rootCtx → wg.Wait（让 a.Go 启动的探活等
// goroutine 先退出，10s 超时）→ stopCron(5s) → rollbackReplacedResources。
// 先停 goroutine 再回滚资源，避免“回滚 DB 时探活 goroutine 仍在用”的竞态。
//
// 回滚走 rollbackReplacedResources（恢复 Init 前默认 manager 再关新建 manager），
// 而非 closeResources（直接关闭，仅供 doShutdown 复用）。Init 失败需恢复默认 manager
// 而非直接关，故两者不复用同一路径。cron 停止预算固定 5s，由本路径单独停止。
// 回滚错误与原 initErr 合并上抛，不吞。
func (a *App) failAfterInit() {
	a.lifecycleMu.Lock()
	a.state = stateStopping
	a.lifecycleMu.Unlock()

	// H-2：cancel rootCtx 让 a.Go 启动的后台 goroutine 退出，不泄漏。
	if a.cancel != nil {
		a.cancel()
	}
	waitDone := make(chan struct{})
	go func() { a.wg.Wait(); close(waitDone) }()
	select {
	case <-waitDone:
	case <-time.After(10 * time.Second):
		logger.Warnf("Init 失败后等待后台 goroutine 退出超时（10s），可能存在未响应 ctx.Done 的 goroutine")
	}

	// M1：回滚 doInit 已初始化的资源。cron 显式 5s 超时不卡死回滚；
	// logger/db/redis 恢复 Init 前默认 manager，再关闭本 App 新建的 manager。
	if err := a.stopCron(5 * time.Second); err != nil {
		a.initErr = errors.Join(a.initErr, err)
	}
	// trace 不是 swap 资源（OTel 全局），不走 rollbackReplacedResources；Init 失败时单独 Close。
	if a.initializedTrace {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := trace.Close(closeCtx); err != nil {
			a.initErr = errors.Join(a.initErr, fmt.Errorf("trace 关闭失败: %w", err))
		}
		closeCancel()
		a.initializedTrace = false
	}
	if rbErr := a.rollbackReplacedResources(); rbErr != nil {
		a.initErr = errors.Join(a.initErr, fmt.Errorf("回滚已初始化资源失败: %w", rbErr))
	}

	a.lifecycleMu.Lock()
	a.state = stateStopped
	a.lifecycleMu.Unlock()
}

// closeResources 关闭 db→redis→logger（M1）。各均为幂等 no-op（未 init 时安全），
// 仅供 doShutdown 复用。cron 因停止预算随调用方不同（Init 失败 5s / Shutdown 剩余
// 预算），由各调用方单独停止，不在此处。返回聚合错误。
// 注意：Init 失败的回滚不经本函数，而走 failAfterInit 的 rollbackReplacedResources
// （需恢复 Init 前默认 manager，而非直接关闭）。
func (a *App) closeResources() error {
	var errs []error
	if a.initializedMySQL {
		if a.dbManager != nil {
			if err := a.dbManager.Close(); err != nil {
				errs = append(errs, err)
			}
		} else if err := database.CloseAll(); err != nil {
			errs = append(errs, err)
		}
		a.initializedMySQL = false
		a.dbManager = nil
	}
	if a.initializedRedis {
		if a.redisManager != nil {
			if err := a.redisManager.Close(); err != nil {
				errs = append(errs, err)
			}
		} else if err := database.CloseRedis(); err != nil {
			errs = append(errs, err)
		}
		a.initializedRedis = false
		a.redisManager = nil
	}
	if a.initializedStorage {
		// StorageManager.Close 当前为 no-op（oss.Client 无 Close），但闭合 Init/Close 生命周期，
		// 为未来驱动预留收口点。与 db/redis/logger manager 对齐。
		if a.storageManager != nil {
			if err := a.storageManager.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		a.initializedStorage = false
		a.storageManager = nil
	}
	if a.initializedLogger {
		if a.loggerManager != nil {
			if err := a.loggerManager.Close(); err != nil {
				errs = append(errs, err)
			}
		} else if err := logger.Close(); err != nil {
			errs = append(errs, err)
		}
		a.initializedLogger = false
		a.loggerManager = nil
	}
	// M-config-3：停止 App 持有的 configManager 热重载 watcher（若已启用），避免 App 关闭后
	// 遗留监听 goroutine。默认流程（resolveConfig 用 Load 不启 watcher）为 no-op；用户对
	// configManager 调用 LoadWithWatch/StartWatcher 后由这里统一收口。StopWatcher 幂等且会等待
	// 进行中的 reload 回调结束，符合 C7 生命周期契约。configManager 可能为 nil（WithConfig 注入）。
	if a.configManager != nil {
		a.configManager.StopWatcher()
	}
	return errors.Join(errs...)
}

func (a *App) rollbackReplacedResources() error {
	var errs []error
	if a.initializedMySQL {
		if a.previousDB != nil {
			database.SwapDefaultManager(a.previousDB)
		}
		if a.dbManager != nil {
			if err := a.dbManager.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		a.dbManager = nil
		a.previousDB = nil
		a.initializedMySQL = false
	}
	if a.initializedRedis {
		if a.previousRedis != nil {
			database.SwapDefaultRedisManager(a.previousRedis)
		}
		if a.redisManager != nil {
			if err := a.redisManager.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		a.redisManager = nil
		a.previousRedis = nil
		a.initializedRedis = false
	}
	if a.initializedStorage {
		// 把全局默认 swap 回 Init 前的旧实例，并 Close 本 App 新建的 manager（与 closeResources
		// 的 Shutdown 路径对称）。当前 Close 为 no-op，但未来驱动有真实副作用时不漏关。
		if a.previousStorage != nil {
			storage.SwapDefaultStorageManager(a.previousStorage)
		}
		if a.storageManager != nil {
			if err := a.storageManager.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		a.storageManager = nil
		a.previousStorage = nil
		a.initializedStorage = false
	}
	// cron：stopCron 已在 failAfterInit 停止调度 goroutine（并把 initializedCron 置 false），
	// 此处仅把全局默认 swap 回 Init 前的旧调度器。用 a.scheduler 作守卫（stopCron 不 nil 它）。
	if a.scheduler != nil {
		if a.previousScheduler != nil {
			cron.SwapDefaultScheduler(a.previousScheduler)
		}
		a.previousScheduler = nil
		a.scheduler = nil
	}
	if a.initializedCache {
		// CacheManager 无 Close（client 由 redisManager 持有）：仅 swap 回全局默认、清理引用。
		if a.previousCache != nil {
			cache.SwapDefaultCacheManager(a.previousCache)
		}
		a.cacheManager = nil
		a.previousCache = nil
		a.initializedCache = false
	}
	if a.rateLimitRegistry != nil {
		// 停止可能已创建的 limiter（Init 期间通常无请求、无 limiter；Stop 幂等）。
		a.rateLimitRegistry.Stop()
		if a.previousRateLimitRegistry != nil {
			middleware.SwapDefaultRateLimitRegistry(a.previousRateLimitRegistry)
		}
		a.previousRateLimitRegistry = nil
		a.rateLimitRegistry = nil
	}
	if a.initializedLogger {
		if err := logger.RestoreDefaultSnapshot(a.loggerSnapshot); err != nil {
			errs = append(errs, err)
		}
		a.loggerManager = nil
		a.loggerSnapshot = nil
		a.initializedLogger = false
	}
	return errors.Join(errs...)
}

func (a *App) commitReplacedResources() {
	// Phase 4 多 App 修复：不关闭 previousDB/previousRedis。previous 是 Init 前的全局默认，
	// 多 App 下可能属于另一个 App 的活跃 manager--关闭会误杀它（B 的 commit 关掉 A 的 redis，
	// 致 A 的 cache 持有的 client 失效 "redis: client is closed"）。此 App 不 owns previous，
	// 仅清理回滚引用。单 App 下 previous 为 init() 空默认（无 client，Close 本就是 no-op），
	// 不关闭无泄漏。用户若手动 InitRedis/InitDB 后再 App.Init，旧 client 的关闭由用户负责。
	if a.previousDB != nil {
		a.previousDB = nil
	}
	if a.previousRedis != nil {
		a.previousRedis = nil
	}
	if a.previousStorage != nil {
		// StorageManager 无 Close；Init 成功后仅清理回滚引用，旧实例交由 GC。
		a.previousStorage = nil
	}
	if a.previousScheduler != nil {
		// App 调度器已 Start 并接管全局默认；旧默认（init 或上一 App 的）无 goroutine 需停
		// （init 默认从未 Start；上一 App 的已在其 Shutdown 停止）。仅清理回滚引用。
		a.previousScheduler = nil
	}
	if a.previousCache != nil {
		// CacheManager 无 Close（client 由 redisManager 持有）；仅清理回滚引用。
		a.previousCache = nil
	}
	if a.previousRateLimitRegistry != nil {
		// Registry 的 limiter 由 App.Shutdown 的 registry.Stop() 停止；此处仅清理回滚引用。
		a.previousRateLimitRegistry = nil
	}
	if a.loggerSnapshot != nil {
		// Phase 4 多 App 修复（与 previousDB/previousRedis 对齐）：不关闭 loggerSnapshot 捕获的
		// 旧 writers。多 App 下该 snapshot 可能捕获的是另一个 App 的活跃 logger writers--关闭会
		// 误杀它（B 的 commit 关掉 A 的 app.log/api.log writer，A 后续写日志失败/丢失）。此 App
		// 不 owns 旧 writers：init() 默认为 Nop（无 writer，不泄漏）；另一 App 的 writers 由其自身
		// Shutdown 关闭；用户手动配置 logger 后再 App.Init 的旧 writer 由用户负责。仅清理回滚引用。
		a.loggerSnapshot = nil
	}
}

func (a *App) stopCron(timeout time.Duration) error {
	if !a.initializedCron {
		return nil
	}
	a.initializedCron = false
	if a.scheduler == nil {
		return nil
	}
	// 停 App 专属调度器（Phase 3），不再调全局 cron.StopGlobalWithTimeout。
	if !a.scheduler.StopWithTimeout(timeout) {
		return fmt.Errorf("cron 调度器停止超时（%s）", timeout)
	}
	return nil
}

func (a *App) shutdownAfterStartFailure(startErr error) error {
	if startErr == nil {
		return nil
	}
	if shutErr := a.Shutdown(); shutErr != nil {
		return fmt.Errorf("%w (关闭错误: %v)", startErr, shutErr)
	}
	return startErr
}

func (a *App) runOnStopHook(ctx context.Context, h Hook) error {
	if h.OnStop == nil {
		return nil
	}
	done := make(chan error, 1)
	go func() {
		done <- h.OnStop(a)
	}()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("OnStop hook %q 失败: %w", h.Name, err)
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("OnStop hook %q 超时: %w", h.Name, ctx.Err())
	}
}

// doInit 执行实际初始化流程。仅在 Init 持有 initMu 并完成状态切换后调用，不可直接调用。
func (a *App) doInit() error {
	cfg, err := a.resolveConfig()
	if err != nil {
		return err
	}
	if cfg.IsProduction() {
		gin.SetMode(gin.ReleaseMode)
	}

	// trace 先于其它组件初始化：失败即早退，避免已建 DB/Redis 等资源因 trace 失败回滚。
	// OTel 进程全局，Init 安装全局 TracerProvider；Enabled=false 时为 Noop（安全）。
	if a.enableTrace {
		if err := trace.Init(buildTraceConfig(cfg)); err != nil {
			return fmt.Errorf("初始化 trace 失败: %w", err)
		}
		a.initializedTrace = true
	}

	if a.enableLogger {
		lm := logger.NewLogManager()
		snapshot := logger.SnapshotDefault()
		if err := lm.InitPreservingPrevious(cfg); err != nil {
			return fmt.Errorf("初始化日志失败: %w", err)
		}
		logger.SetDefaultLogManager(lm)
		a.loggerManager = lm
		a.loggerSnapshot = snapshot
		a.initializedLogger = true
	}

	if a.enableMySQL {
		dbm := database.NewManager(cfg)
		if err := dbm.InitDB(a.rootCtx, cfg); err != nil {
			return fmt.Errorf("初始化数据库失败: %w", err)
		}
		a.previousDB = database.SwapDefaultManager(dbm)
		a.dbManager = dbm
		a.initializedMySQL = true
		a.healthChecks = append(a.healthChecks, router.HealthCheck{Name: "mysql", Check: func(ctx context.Context) error {
			// 优先读探活缓存标记（#21），避免每次探针都同步 ping
			if !dbm.IsHealthy() {
				return errors.New("mysql 主库探活不健康")
			}
			return dbm.HealthCheck(ctx)
		}})
	}

	if a.enableRedis {
		rm := database.NewRedisManager()
		if err := rm.Init(cfg); err != nil {
			return fmt.Errorf("初始化 Redis 失败: %w", err)
		}
		a.previousRedis = database.SwapDefaultRedisManager(rm)
		a.redisManager = rm
		a.initializedRedis = true
		a.healthChecks = append(a.healthChecks, router.HealthCheck{Name: "redis", Check: rm.HealthCheck})
	}

	if a.enableStorage {
		sm := storage.NewStorageManager()
		if err := sm.Init(&cfg.Storage); err != nil {
			return fmt.Errorf("初始化存储失败: %w", err)
		}
		// 实例化 + 提升为全局默认（照 db/redis 的 Swap 模式）。StorageManager 无 Close
		// （LocalStorage/OSSStorage 无 closeable 资源），Shutdown 不关、仅 Init 失败时 swap 回退。
		a.previousStorage = storage.SwapDefaultStorageManager(sm)
		a.storageManager = sm
		a.initializedStorage = true
	}

	if a.enableRedis {
		// Redis 就绪后初始化缓存。Phase 4：注入 App 的 redisManager.Client()，使缓存走 per-App
		// Redis 而非全局 database.GetRedis()（修复多 App 跨 Redis 串越）。提升为全局默认供包级
		// facade（cache.Get 等）代理到此。CacheManager 无 Close（client 由 redisManager 持有）。
		cm := cache.NewCacheManager()
		cm.InitWithRedis(a.redisManager.Client())
		a.previousCache = cache.SwapDefaultCacheManager(cm)
		a.cacheManager = cm
		a.initializedCache = true
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
	// trace 中间件装在 Recover 之后：Recover 兜底捕获 trace 内 panic，span 覆盖业务 handler。
	// Enabled=false 时为 Noop tracer，Middleware 不导出；noop span 无 TraceID 则不写 X-Trace-ID。
	if a.enableTrace {
		// 用与 buildTraceConfig 一致的回退名（ServiceName 空时取 cfg.App.Name），避免中间件
		// span 属性与 provider resource 的 service.name 来源不一致。
		svcName := a.config.Trace.ServiceName
		if svcName == "" {
			svcName = a.config.App.Name
		}
		a.router.Use(trace.Middleware(svcName))
	}
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
		// 采集中间件交给注册中心，Apply 时作为首个全局中间件装入（H8c），
		// 覆盖所有经注册中心注册的业务路由，不依赖调用顺序。
		a.registry.SetMetricsMiddleware(middleware.Metrics())
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

	// 错误 Detail 暴露策略（P1 #15）：仅开发环境向客户端输出 Error.Detail，
	// 生产环境隐藏，避免内部错误细节（SQL/堆栈等）经 Detail 泄露给客户端。
	response.SetExposeDetail(cfg.IsDevelopment())

	// in-flight goroutine 根 ctx 在 New() 时已初始化（#22）

	// 启动主库/从库探活后台循环（#21），ctx 在 Shutdown 时取消
	if a.enableMySQL {
		a.Go(a.dbManager.StartProbing)
	}

	// 启动 App 专属 cron 调度器（Phase 3 实例化）：创建实例 -> 注册 WithCronTask 收集的任务 ->
	// 提升为全局默认（包级 cron.AddTask 代理到此）-> Start。Shutdown 时 StopWithTimeout。
	if a.enableCron {
		a.scheduler = cron.NewScheduler()
		for _, ct := range a.cronTasks {
			a.scheduler.AddTask(ct.name, ct.schedule, ct.handler)
		}
		a.previousScheduler = cron.SwapDefaultScheduler(a.scheduler)
		a.scheduler.Start()
		a.initializedCron = true
	}

	// Phase 5：把 New 时预创建的 App 专属 Registry 提升为全局默认，供包级 facade（LoginRateLimit
	// 等）代理。app-bound 中间件（app.RateLimitRegistry().LoginRateLimit()）直接捕获此实例，
	// 不查全局默认，实现多 App per-App 计数隔离。不预 Init（懒创建）；Shutdown 时 Stop。
	a.previousRateLimitRegistry = middleware.SwapDefaultRateLimitRegistry(a.rateLimitRegistry)

	// OnInit hooks：组件初始化完成后触发（#12）
	for _, h := range a.hooks {
		if h.OnInit != nil {
			if err := h.OnInit(a); err != nil {
				return fmt.Errorf("OnInit hook %q 失败: %w", h.Name, err)
			}
		}
	}
	a.commitReplacedResources()
	return nil
}

func (a *App) resolveConfig() (*config.Config, error) {
	if a.config != nil {
		cfg := a.config.Clone()
		if err := cfg.Validate(); err != nil {
			return nil, fmt.Errorf("配置校验失败: %w", err)
		}
		a.config = cfg
		return cfg, nil
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

// buildTraceConfig 把 config.TraceConfig 映射为 trace.Config，并补友好默认值：
//   - ServiceName 空时回退 cfg.App.Name（业务侧常已配 app.name，免重复填）；
//   - ExporterType 空时默认 "otlp-http"（最常用），避免 trace.Init 对空值报"不支持的导出器类型"。
//
// 不默认 Endpoint/SampleRatio/Propagator：Endpoint 空时 OTEL 客户端走自身默认（localhost:4318）；
// SampleRatio=0 是合法值（不采样），不应被默认覆盖；Propagator 空时 trace.Init 已按 w3c 处理。
// Enabled 完全透传--由 config.Trace.Enabled 决定是否真正导出，WithTrace 只管生命周期。
func buildTraceConfig(cfg *config.Config) trace.Config {
	tc := cfg.Trace
	exporter := tc.ExporterType
	if exporter == "" {
		exporter = "otlp-http"
	}
	name := tc.ServiceName
	if name == "" {
		name = cfg.App.Name
	}
	return trace.Config{
		ServiceName:    name,
		ServiceVersion: tc.ServiceVersion,
		Environment:    tc.Environment,
		ExporterType:   exporter,
		Endpoint:       tc.Endpoint,
		Insecure:       tc.Insecure,
		SampleRatio:    tc.SampleRatio,
		Enabled:        tc.Enabled,
		Propagator:     tc.Propagator,
	}
}

// Run 启动应用
func (a *App) Run() error {
	if err := a.Init(); err != nil {
		return err
	}
	return a.shutdownAfterStartFailure(a.StartServer())
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
				return a.shutdownAfterStartFailure(fmt.Errorf("OnStart hook %q 失败: %w", h.Name, err))
			}
		}
	}

	network := "tcp"
	address := a.server.Addr
	if useUnix {
		network = "unix"
		address = srvCfg.UnixSocket
	}
	ln, err := net.Listen(network, address)
	if err != nil {
		return a.shutdownAfterStartFailure(fmt.Errorf("服务器监听失败: %w", err))
	}
	if srvCfg.TLS.Enabled {
		cert, err := tls.LoadX509KeyPair(srvCfg.TLS.CertFile, srvCfg.TLS.KeyFile)
		if err != nil {
			_ = ln.Close()
			return a.shutdownAfterStartFailure(fmt.Errorf("服务器 TLS 配置无效: %w", err))
		}
		ln = tls.NewListener(ln, &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		})
	}

	serverErr := make(chan error, 1)
	serveStarted := make(chan struct{})
	go func() {
		close(serveStarted)
		if useUnix {
			logger.Infof("服务器启动，监听 unix socket %s", srvCfg.UnixSocket)
		} else if srvCfg.Host != "" {
			logger.Infof("服务器启动，监听 %s:%d", srvCfg.Host, srvCfg.Port)
		} else {
			logger.Infof("服务器启动，监听端口 %d（所有接口）", srvCfg.Port)
		}
		serveErr := a.server.Serve(ln)
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			serverErr <- serveErr
			return
		}
		serverErr <- nil
	}()
	<-serveStarted
	select {
	case err := <-serverErr:
		if err != nil {
			return a.shutdownAfterStartFailure(fmt.Errorf("服务器启动失败: %w", err))
		}
		return nil
	default:
	}

	// OnReady hooks：listener 已成功创建，Serve goroutine 已启动（M1）。
	for _, h := range a.hooks {
		if h.OnReady != nil {
			if err := h.OnReady(a); err != nil {
				logger.Errorf("OnReady hook %q 失败: %v", h.Name, err)
				// H-1 修复：失败时走 Shutdown 释放 HTTP 端口、后台 goroutine 与已初始化资源，
				// 避免监听 goroutine 永久阻塞在 Serve 致端口/goroutine 泄漏。
				// 不再向 serverErr 写入——监听 goroutine 在 Shutdown 后会写 nil 到 serverErr（cap=1），
				// 若此处也写则第二次写阻塞致 goroutine 泄漏。
				if shutErr := a.Shutdown(); shutErr != nil {
					return fmt.Errorf("OnReady hook %q 失败: %w (关闭错误: %v)", h.Name, err, shutErr)
				}
				_ = ln.Close()
				return fmt.Errorf("OnReady hook %q 失败: %w", h.Name, err)
			}
		}
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(quit)

	select {
	case err := <-serverErr:
		if err != nil {
			return a.shutdownAfterStartFailure(fmt.Errorf("服务器启动失败: %w", err))
		}
		return nil
	case sig := <-quit:
		logger.Infof("收到信号 %v，开始优雅关闭...", sig)
		return a.Shutdown()
	}
}

// Shutdown 优雅关闭应用（M1：幂等 + 与 Init 互斥）。
// shutdownOnce 保证 doShutdown 单次执行，并发调用者阻塞至首个完成后返回同一 shutdownErr。
// initMu 串行化 doShutdown 与 doInit，避免并发改资源。
//
// 注意：生命周期 hook 不允许重入调用 Init/Shutdown/Run——initMu 非重入，hook 内调用会自锁。
func (a *App) Shutdown() error {
	a.shutdownOnce.Do(func() {
		a.initMu.Lock()
		defer a.initMu.Unlock()

		a.lifecycleMu.Lock()
		if a.state >= stateStopping {
			// shutdownOnce 已保证单次，此处不可达；防御性直接返回。
			a.lifecycleMu.Unlock()
			return
		}
		wasInitialized := a.state == stateInitialized
		a.state = stateStopping
		a.lifecycleMu.Unlock()

		a.shutdownErr = a.doShutdown(wasInitialized)

		a.lifecycleMu.Lock()
		a.state = stateStopped
		a.lifecycleMu.Unlock()
	})
	return a.shutdownErr
}

// doShutdown 执行实际关闭流程。仅在 Shutdown（持 initMu）中调用。
// 顺序（M1 调整）：OnStop（仅 Init 曾成功时）→ cancel rootCtx → server.Shutdown →
// wg.Wait → cron / 限流器 / db / redis / logger。OnStop 在 shutdown 开头、关 HTTP 之前，
// 保有 cancel 前协调资源的机会；state=Stopping 已阻新 Go，无需先 cancel/wait 再跑 hook。
// OnStop 也受同一个 shutdownTimeout 预算约束，避免 hook 卡死拖住整个进程退出。
func (a *App) doShutdown(wasInitialized bool) error {
	shutdownTimeout := 30 * time.Second
	if a.config != nil {
		shutdownTimeout = a.config.Server.EffectiveShutdownTimeout()
	}
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	var errs []error

	// OnStop hooks：Shutdown 开头，关 HTTP 之前触发（#12）。仅 Init 曾成功时执行（M1）——
	// Init 失败/未 Init 时 HTTP 从未启动，OnStop 语义不适用。
	if wasInitialized {
		for _, h := range a.hooks {
			if err := a.runOnStopHook(ctx, h); err != nil {
				errs = append(errs, err)
				if ctx.Err() != nil {
					break
				}
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

	// 停止 cron 全局调度器（P1 #10），在剩余 shutdown 预算内等待在跑任务退出。
	if a.initializedCron {
		logger.Info("停止 cron 调度器...")
		remaining := time.Second
		if dl, ok := ctx.Deadline(); ok {
			if r := time.Until(dl); r > 0 {
				remaining = r
			}
		}
		if err := a.stopCron(remaining); err != nil {
			logger.Warnf("cron 调度器停止超时，可能有任务未响应 ctx 取消")
		}
	}

	// Phase 5：停 App 专属限流器 Registry（不再调全局 middleware.StopRateLimiters，避免误停其他 App）。
	if a.rateLimitRegistry != nil {
		logger.Info("停止限流器...")
		a.rateLimitRegistry.Stop()
	}

	// trace.Close 刷出 exporter 批量 span 并释放后台 goroutine/连接（H-14 后幂等）。
	// 放在 db/redis/logger 关闭之前，避免 exporter 依赖已关闭的日志/连接。
	if a.initializedTrace {
		logger.Info("关闭 trace...")
		if err := trace.Close(ctx); err != nil {
			logger.Warnf("trace 关闭失败: %v", err)
			errs = append(errs, err)
		}
	}

	// db/redis/logger 经 closeResources 统一关闭（M1）。注意：closeResources 仅供
	// Shutdown 路径使用；Init 失败的回滚走 failAfterInit 的 rollbackReplacedResources，
	// 两者不复用同一路径（Init 失败需恢复默认 manager，Shutdown 直接关闭）。
	// 先记最后一条 "应用已优雅关闭"，再 Close（关闭后写日志会 fall back 到 nop）。
	logger.Info("关闭数据库连接/Redis/日志...")
	logger.Info("应用已优雅关闭")
	if err := a.closeResources(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// Go 启动一个受 App 生命周期管理的后台 goroutine（#22）。
// fn 收到的 ctx 在 Shutdown 时被 cancel，fn 应在 ctx.Done() 时及时退出。
// Shutdown/Init 失败后调用为 no-op（M1：state>=stateStopping 拒绝 wg.Add，避免与
// Shutdown 的 wg.Wait 竞争 WaitGroup 契约——Add 由 lifecycleMu.RLock 保护 happen-before Wait）。
func (a *App) Go(fn func(ctx context.Context)) {
	if fn == nil {
		return
	}
	a.lifecycleMu.RLock()
	if a.state >= stateStopping {
		a.lifecycleMu.RUnlock()
		return
	}
	ctx := a.rootCtx
	if ctx == nil {
		ctx = context.Background()
	}
	a.wg.Add(1)
	a.lifecycleMu.RUnlock()
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

// Scheduler 返回 App 专属的 cron 调度器（Phase 3）。Init 后非 nil，用于动态注册任务
// （app.Scheduler().AddTask(...)）。未启用 cron 或 Init 前返回 nil。
func (a *App) Scheduler() *cron.Scheduler {
	return a.scheduler
}

// Cache 返回 App 专属的缓存服务（Phase 4）。Init 后非 nil，用于直接操作 App 的缓存
// （绕过全局 facade，多 App 隔离场景下确保走 App 自己的 Redis）。未启用 Redis 或 Init 前返回 nil。
func (a *App) Cache() cache.CacheService {
	if a.cacheManager == nil {
		return nil
	}
	return a.cacheManager.Get()
}

// RedisClient 返回 App 专属的 Redis 客户端（Phase 5），用于注入到需要 redis client 的
// 组件（如 middleware.NewRedisRateLimiter(..., middleware.WithRedisClient(app.RedisClient()))），
// 使其走 App 的 Redis 而非全局。未启用 Redis 或 Init 前返回 nil。
func (a *App) RedisClient() *redis.Client {
	if a.redisManager == nil {
		return nil
	}
	return a.redisManager.Client()
}

// RateLimitRegistry 返回 App 专属的限流器 Registry（Phase 5）。New 后即非 nil，用于装配
// app-bound 限流中间件（app.RateLimitRegistry().LoginRateLimit() 等）--此类中间件捕获 App
// 自己的 Registry，请求时不查全局默认，实现多 App per-App 计数隔离。
func (a *App) RateLimitRegistry() *middleware.RateLimitRegistry {
	return a.rateLimitRegistry
}
