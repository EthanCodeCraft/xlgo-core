package database

import (
	"context"
	cryptorand "crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/EthanCodeCraft/xlgo-core/config"
	"github.com/EthanCodeCraft/xlgo-core/logger"

	"go.uber.org/zap"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

type dbModeContextKey struct{}

// txContextKey 携带外层事务的 *gorm.DB，使 repository 等上层在调用时能 join 到外层事务
// （H6c：外层 ctx 事务无法 join）。由 WithTx 注入、TxFromContext 读取。
type txContextKey struct{}

const (
	dbModeMaster  = "master"
	dbModeReplica = "replica"
)

func normalizeContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// ReplicaPicker 从库选择策略
type ReplicaPicker interface {
	Pick(replicas []*gorm.DB) *gorm.DB
}

// RoundRobinPicker 轮询选择从库
type RoundRobinPicker struct {
	mu      sync.Mutex
	counter int
}

// Pick 轮询选择一个从库
func (p *RoundRobinPicker) Pick(replicas []*gorm.DB) *gorm.DB {
	if len(replicas) == 0 {
		return nil
	}
	p.mu.Lock()
	idx := p.counter % len(replicas)
	p.counter = (idx + 1) % len(replicas)
	p.mu.Unlock()
	return replicas[idx]
}

// RandomPicker 随机选择从库。
//
// D2 注释：rand.Intn 自 Go 1.20 起（go.dev/issue/54899）内部使用 per-goroutine
// 随机源，并发安全无需额外同步。本模块 go.mod 声明 go 1.25.0，满足此要求。
type RandomPicker struct{}

// Pick 随机选择一个从库
func (p *RandomPicker) Pick(replicas []*gorm.DB) *gorm.DB {
	if len(replicas) == 0 {
		return nil
	}
	n, err := cryptorand.Int(cryptorand.Reader, big.NewInt(int64(len(replicas))))
	if err != nil {
		return replicas[0]
	}
	return replicas[int(n.Int64())]
}

// Manager 数据库管理器，持有主库与从库连接实例
type Manager struct {
	cfg      *config.Config
	master   *gorm.DB
	replicas []*gorm.DB
	picker   ReplicaPicker
	mu       sync.Mutex
	// opMu 串行化 InitDB / InitDBWithReplicas / Close 这类资源生命周期操作。
	// 避免关闭已开始后初始化又发布新连接，或初始化发布后被并发 Close 置空。
	opMu sync.Mutex

	// #21 健康自愈
	healthy          atomic.Bool   // 主库是否健康
	replicaHealthy   []atomic.Bool // 每个从库的健康标记，索引与 replicas 对齐
	probeFailures    int           // 主库连续探活失败次数
	probeMu          sync.Mutex    // 保护 probeFailures
	replicaHealthSet bool          // replicaHealthy 是否已按 replicas 长度初始化
}

// NewManager 创建数据库管理器
func NewManager(cfg *config.Config) *Manager {
	return &Manager{cfg: cfg, picker: &RandomPicker{}}
}

// getCfg 在锁内读取 m.cfg（P1 #11：消除与 InitDB 写 m.cfg 的数据竞争）。
func (m *Manager) getCfg() *config.Config {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cfg
}

// setCfg 在锁内写入 m.cfg（P1 #11）。
func (m *Manager) setCfg(cfg *config.Config) {
	m.mu.Lock()
	m.cfg = cfg
	m.mu.Unlock()
}

// SetPicker 设置从库选择策略
func (m *Manager) SetPicker(p ReplicaPicker) {
	if p == nil {
		return
	}
	m.mu.Lock()
	m.picker = p
	m.mu.Unlock()
}

// Picker 返回当前从库选择策略
func (m *Manager) Picker() ReplicaPicker {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.picker
}

// Master 返回主库实例。
// 经 m.mu 锁保护读取，避免与 InitDB/Close 的写竞争返回已关闭/nil 池（C11d）。
func (m *Manager) Master() *gorm.DB {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.master
}

// Replicas 返回所有从库实例的拷贝。
// 经 m.mu 锁保护读取并返回拷贝，避免调用方持活切片与 InitDBWithReplicas/Close 重置竞争（C11d）。
func (m *Manager) Replicas() []*gorm.DB {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.replicas == nil {
		return nil
	}
	out := make([]*gorm.DB, len(m.replicas))
	copy(out, m.replicas)
	return out
}

// Replica 按策略选择一个从库；无从库时返回主库。
// #21：启用探活后，自动过滤不健康的从库；全不健康时回退到全部从库（仍可服务）。
// 全程持 m.mu 锁，避免 replicas/master 与重建路径写竞争（C11d）。
func (m *Manager) Replica() *gorm.DB {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.replicas) == 0 {
		return m.master
	}
	pool := m.replicas
	// 启用探活且至少有一个健康标记时，仅从健康从库中选取
	if m.replicaHealthSet {
		var healthy []*gorm.DB
		for i, r := range m.replicas {
			if i < len(m.replicaHealthy) && m.replicaHealthy[i].Load() {
				healthy = append(healthy, r)
			}
		}
		if len(healthy) > 0 {
			pool = healthy
		}
		// healthy 为空时回退到全部 replicas，避免读流量完全中断
	}

	if m.picker != nil {
		if db := m.picker.Pick(pool); db != nil {
			return db
		}
	}
	return pool[0]
}

// IsHealthy 返回主库当前健康状态（#21）。供 readiness/health 探针联动。
func (m *Manager) IsHealthy() bool {
	return m.healthy.Load()
}

// initReplicaHealth 按 replicas 数量初始化健康标记（全部为健康）。
// 已初始化（replicaHealthSet=true）时早返回；重建从库前须先 resetReplicaHealth 重置，
// 否则健康切片长度与新 replicas 错位（C11a）。
func (m *Manager) initReplicaHealth() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.replicaHealthSet {
		return
	}
	m.replicaHealthy = make([]atomic.Bool, len(m.replicas))
	for i := range m.replicaHealthy {
		m.replicaHealthy[i].Store(true)
	}
	m.replicaHealthSet = true
}

// ensureReplicaHealthLocked 按当前 replicas 重建健康标记。调用方须持有 m.mu。
func (m *Manager) ensureReplicaHealthLocked() {
	m.replicaHealthy = make([]atomic.Bool, len(m.replicas))
	for i := range m.replicaHealthy {
		m.replicaHealthy[i].Store(true)
	}
	m.replicaHealthSet = true
}

// resetReplicaHealth 清空从库健康标记，使下次 initReplicaHealth 按新 replicas 长度重建。
// 重建从库（InitDBWithReplicas）/Close 前必须调用，避免健康切片与新 replicas 长度错位（C11a）。
// 调用方须持有 m.mu。
func (m *Manager) resetReplicaHealth() {
	m.replicaHealthy = nil
	m.replicaHealthSet = false
}

// StartProbing 启动主库与从库的健康探活后台循环（#21）。
// 阻塞调用方，应通过 App.Go 在独立 goroutine 运行；ctx 取消时退出。
// 周期 ping 主库，连续失败达阈值后标记不健康（IsHealthy=false）；
// 同时 ping 各从库，失败则从读流量剔除，恢复后自动重新纳入。
func (m *Manager) StartProbing(ctx context.Context) {
	ctx = normalizeContext(ctx)
	m.initReplicaHealth()

	cfg := m.getCfg() // P1 #11：锁内快照，避免与 InitDB 写 m.cfg 竞态
	interval := 30 * time.Second
	if cfg != nil && cfg.Database.HealthCheckInterval > 0 {
		interval = cfg.Database.HealthCheckInterval
	}
	threshold := 3
	if cfg != nil && cfg.Database.HealthCheckFailureThreshold > 0 {
		threshold = cfg.Database.HealthCheckFailureThreshold
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.probeOnce(ctx, threshold)
		}
	}
}

// probeOnce 执行一轮主库+从库探活并更新健康标记。
func (m *Manager) probeOnce(ctx context.Context, threshold int) {
	// 主库
	if err := m.HealthCheck(ctx); err != nil {
		m.probeMu.Lock()
		m.probeFailures++
		if m.probeFailures >= threshold {
			if m.healthy.Load() {
				logger.Warnf("数据库主库连续探活失败 %d 次，标记为不健康: %v", m.probeFailures, err)
			}
			m.healthy.Store(false)
		}
		m.probeMu.Unlock()
	} else {
		m.probeMu.Lock()
		if m.probeFailures >= threshold && !m.healthy.Load() {
			logger.Info("数据库主库探活恢复，重新标记为健康")
		}
		m.probeFailures = 0
		m.probeMu.Unlock()
		m.healthy.Store(true)
	}

	// 从库
	m.mu.Lock()
	replicas := make([]*gorm.DB, len(m.replicas))
	copy(replicas, m.replicas)
	if len(replicas) > 0 && !m.replicaHealthSet {
		m.ensureReplicaHealthLocked()
	}
	healthSet := m.replicaHealthSet
	replicaHealthy := m.replicaHealthy // 快照切片头，避免与 resetReplicaHealth 写竞争
	m.mu.Unlock()
	if !healthSet {
		return
	}
	for i, r := range replicas {
		if r == nil {
			continue
		}
		sqlDB, err := r.DB()
		if err != nil {
			if i < len(replicaHealthy) {
				replicaHealthy[i].Store(false)
			}
			continue
		}
		if err := sqlDB.PingContext(ctx); err != nil {
			if i < len(replicaHealthy) && replicaHealthy[i].Load() {
				logger.Warnf("数据库从库 #%d 探活失败，暂时剔除读流量: %v", i, err)
			}
			if i < len(replicaHealthy) {
				replicaHealthy[i].Store(false)
			}
		} else {
			if i < len(replicaHealthy) {
				replicaHealthy[i].Store(true)
			}
		}
	}
}

// FromContext 根据上下文选择数据库
func (m *Manager) FromContext(ctx context.Context) *gorm.DB {
	ctx = normalizeContext(ctx)
	mode, ok := ctx.Value(dbModeContextKey{}).(string)
	if !ok {
		return m.Replica()
	}
	switch mode {
	case dbModeMaster:
		return m.Master()
	case dbModeReplica:
		return m.Replica()
	default:
		return m.Replica()
	}
}

// Open 打开主库连接
func (m *Manager) Open(ctx context.Context) error {
	cfg := m.getCfg() // P1 #11：锁内读取，避免与 InitDB 写竞态
	if cfg == nil {
		return errors.New("数据库配置未设置")
	}
	return m.InitDB(ctx, cfg)
}

// OpenWithReplicas 打开主库与从库连接
func (m *Manager) OpenWithReplicas(ctx context.Context, replicaDSNs []string) error {
	cfg := m.getCfg() // P1 #11：锁内读取
	if cfg == nil {
		return errors.New("数据库配置未设置")
	}
	return m.InitDBWithReplicas(ctx, cfg, replicaDSNs)
}

// closeDB 关闭 gorm.DB 底层连接池。nil 或未初始化（无 ConnPool）时返回 nil，不 panic。
// 用于重建/关闭路径释放旧池，避免直接覆盖致泄漏（C11b/C11c）。
func closeDB(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

func warnCloseDB(db *gorm.DB, context string) {
	if err := closeDB(db); err != nil {
		logger.Warnf("%s: %v", context, err)
	}
}

// Close 关闭主库与全部从库连接，并重置从库健康状态。
// 字段置空在锁内完成（保证新读取得到 nil），实际关闭在锁外执行避免持锁阻塞。
func (m *Manager) Close() error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.mu.Lock()
	master := m.master
	replicas := m.replicas
	m.master = nil
	m.replicas = nil
	m.resetReplicaHealth()
	m.healthy.Store(false)
	m.mu.Unlock()

	var errs []error
	if err := closeDB(master); err != nil {
		errs = append(errs, err)
	}
	for _, replica := range replicas {
		if err := closeDB(replica); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// HealthCheck 健康检查，主库不可达时返回错误
func (m *Manager) HealthCheck(ctx context.Context) error {
	ctx = normalizeContext(ctx)
	m.mu.Lock()
	db := m.master
	m.mu.Unlock()
	if db == nil {
		return errors.New("数据库主库未初始化")
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	return sqlDB.PingContext(ctx)
}

// DefaultManager 默认数据库管理器，包级 facade 代理到它。
//
// 主线A 修复：改用 atomic.Pointer 保护读写，消除原裸指针（外部直接
// `database.DefaultManager = ...` 赋值与请求 goroutine 经 facade 读取）之间的数据竞争。
// 与 config.defaultManager / database.DefaultRedis / storage.DefaultStorage /
// cache.defaultCachePtr / jwt.defaultManager 对齐——框架内包级可变全局一律 atomic.Pointer。
//
// 类型由 *Manager 变更为 atomic.Pointer[Manager]（breaking）：下游若直接调用
// DefaultManager.Init/Master 等方法需改用 InitDB/GetDB 等 facade，或
// DefaultManager.Load().Init(...)，或经 GetDefaultManager() 取实例后再调方法。
var DefaultManager atomic.Pointer[Manager]

func init() {
	DefaultManager.Store(NewManager(nil))
}

// SwapDefaultManager 将指定 Manager 置为全局默认，并返回被替换的旧 Manager。
// 旧 Manager 不会被关闭，供 App 初始化这类需要失败回滚的生命周期流程暂存。
// nil 被忽略，以防 facade Load 到 nil panic。
func SwapDefaultManager(m *Manager) *Manager {
	if m == nil {
		return DefaultManager.Load()
	}
	return DefaultManager.Swap(m)
}

// SetDefaultManager 提升指定 Manager 为全局默认，并关闭被替换的旧 Manager。
// 用于多实例场景或测试注入。nil 被忽略以防 facade Load 到 nil panic。
// 若调用方需要保留旧 Manager 用于回滚，请使用 SwapDefaultManager。
func SetDefaultManager(m *Manager) {
	if m == nil {
		return
	}
	old := SwapDefaultManager(m)
	if old != nil && old != m {
		if err := old.Close(); err != nil {
			logger.Warnf("关闭被替换的旧数据库 manager 失败: %v", err)
		}
	}
}

// GetDefaultManager 返回全局默认 Manager（atomic 读取，并发安全）。
// 替代直接读 DefaultManager 包级变量（类型已改为 atomic.Pointer，直接读得到的是 atomic
// 值而非 *Manager）。需直接持有 Manager 调用其方法时用本函数或 DefaultManager.Load()。
func GetDefaultManager() *Manager {
	return DefaultManager.Load()
}

// InitDB 初始化数据库连接（带重试机制），驱动由配置决定。
// ctx 控制 Ping 与重试等待；调用方取消 ctx 时初始化会尽快返回。
func (m *Manager) InitDB(ctx context.Context, cfg *config.Config) error {
	ctx = normalizeContext(ctx)
	m.opMu.Lock()
	defer m.opMu.Unlock()
	return m.initDB(ctx, cfg)
}

func (m *Manager) initDB(ctx context.Context, cfg *config.Config) error {
	if cfg == nil {
		return errors.New("数据库配置未设置")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("数据库初始化已取消: %w", err)
	}
	m.setCfg(cfg) // P1 #11：锁内写入，避免与 StartProbing/Open 读竞态

	// GORM 日志配置
	var gormLogLevel gormlogger.LogLevel
	if cfg.IsDevelopment() {
		gormLogLevel = gormlogger.Info
	} else {
		gormLogLevel = gormlogger.Warn
	}

	gormConfig := &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormLogLevel),
	}

	// M-config-2：MySQL 启用 TLS 且配置自定义 CA 时，注册命名 TLS 配置，使 DSN 中 tls=<name> 生效。
	// 失败 fail-fast 返回错误，绝不静默回退明文连接。非 MySQL / 未配 CA 为 no-op。
	if err := ensureMySQLTLSRegistered(cfg); err != nil {
		return err
	}

	// 重试配置
	maxRetries := 5
	retryDelay := time.Second

	var lastErr error
	for i := range maxRetries {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("数据库初始化已取消: %w", err)
		}
		// 连接主库：先打开到局部变量，仅 Ping 成功后才安装为 m.master，
		// 避免 Ping 失败时下轮覆盖 m.master 泄漏旧池（C11b）。
		db, err := gorm.Open(Dialector(cfg), gormConfig)
		if err != nil {
			lastErr = err
			// 不可恢复的错误（认证失败、未知数据库、DSN 非法等）直接返回，不必重试
			if !isTransientDBError(err) {
				return fmt.Errorf("数据库连接失败（不可恢复）: %w", err)
			}
		} else {
			sqlDB, err := db.DB()
			if err != nil {
				lastErr = err
				warnCloseDB(db, "关闭刚打开的数据库连接池失败") // C11b: 关闭刚打开的池，避免下轮泄漏
			} else {
				sqlDB.SetMaxIdleConns(cfg.Database.MaxIdleConns)
				sqlDB.SetMaxOpenConns(cfg.Database.MaxOpenConns)
				sqlDB.SetConnMaxLifetime(time.Hour)
				if cfg.Database.ConnMaxIdleTime > 0 {
					sqlDB.SetConnMaxIdleTime(cfg.Database.ConnMaxIdleTime)
				}

				if err := sqlDB.PingContext(ctx); err == nil {
					// 成功：安装为新主库，关闭旧主库池（重建路径覆盖前先释放旧资源，C11b）
					m.mu.Lock()
					old := m.master
					m.master = db
					m.mu.Unlock()
					m.healthy.Store(true) // Ping 通过才标记健康（#21）
					warnCloseDB(old, "关闭旧数据库主库连接池失败")
					logger.Info("数据库主库连接成功",
						zap.String("driver", driverDescription(cfg.Database.Driver)),
						zap.String("host", cfg.Database.Host),
						zap.Int("port", cfg.Database.Port))
					return nil
				} else {
					// Ping 失败（如服务端暂时不可达）视作可重试
					lastErr = err
					warnCloseDB(db, "关闭 Ping 失败的数据库连接池失败") // C11b: 关闭刚打开的池，避免下轮覆盖泄漏
				}
			}
		}

		logger.Warnf("数据库连接失败，第 %d/%d 次重试: %v", i+1, maxRetries, lastErr)
		if i == maxRetries-1 {
			break
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("数据库初始化已取消: %w", ctx.Err())
		case <-time.After(retryDelay):
		}
		retryDelay *= 2
		if retryDelay > 30*time.Second {
			retryDelay = 30 * time.Second
		}
	}

	return fmt.Errorf("数据库连接失败（重试 %d 次）: %w", maxRetries, lastErr)
}

// isTransientDBError 判断数据库连接错误是否值得重试。
// 认证失败、未知数据库、非法 DSN/驱动等属于配置类错误，重试无意义，直接返回更友好。
// D5 修复：覆盖 MySQL 和 PostgreSQL 常见非瞬态错误。
func isTransientDBError(err error) bool {
	if err == nil {
		return true
	}
	msg := err.Error()
	nonTransient := []string{
		// MySQL
		"Access denied",         // 认证失败（用户名/密码错误）
		"authentication plugin", // 认证插件不支持
		"Unknown database",      // 目标库不存在
		"invalid DSN",           // DSN 语法错误
		"unknown driver",        // 驱动未注册
		"unsupported driver",    // 驱动不支持
		// PostgreSQL（D5 修复；P1 #12：移除过宽的独立 "database" 子串——它会把
		// "the database system is starting up" 等瞬态错误误判为非瞬态而放弃重试。
		// PG 目标库不存在的消息形如 database "x" does not exist，用 "does not exist" 精确匹配）。
		"password authentication failed", // pg 密码错误
		"does not exist",                 // pg 目标库/角色不存在
		"no pg_hba.conf entry",           // pg_hba.conf 拒绝
	}
	for _, sub := range nonTransient {
		if strings.Contains(msg, sub) {
			return false
		}
	}
	return true
}

// replicaMaxOpenConns 计算从库连接池 MaxOpenConns（H-A 修复）。
// masterMax>0 时取 max(1, masterMax/2)（从库适当减少，但绝不截断为 0）；
// masterMax<=0（未配置/无限）时返回 0，与主库"无限"语义一致。
func replicaMaxOpenConns(masterMax int) int {
	if masterMax <= 0 {
		return 0
	}
	half := masterMax / 2
	if half < 1 {
		half = 1
	}
	return half
}

// InitDBWithReplicas 初始化数据库主从连接，驱动由配置决定
// replicaDSNs: 从库连接字符串列表（需与主库驱动匹配）
func (m *Manager) InitDBWithReplicas(ctx context.Context, cfg *config.Config, replicaDSNs []string) error {
	ctx = normalizeContext(ctx)
	m.opMu.Lock()
	defer m.opMu.Unlock()
	if cfg == nil {
		return errors.New("数据库配置未设置")
	}
	// 先初始化主库
	if err := m.initDB(ctx, cfg); err != nil {
		return err
	}

	// C11c: 重建从库前关闭旧从库池；C11a: 重置健康状态，使下次 initReplicaHealth 按新 replicas 长度重建
	m.mu.Lock()
	oldReplicas := m.replicas
	m.replicas = nil
	m.resetReplicaHealth()
	m.mu.Unlock()
	for _, r := range oldReplicas {
		warnCloseDB(r, "关闭旧数据库从库连接池失败")
	}

	// 初始化从库
	if len(replicaDSNs) > 0 {
		var gormLogLevel gormlogger.LogLevel
		if cfg.IsDevelopment() {
			gormLogLevel = gormlogger.Info
		} else {
			gormLogLevel = gormlogger.Warn
		}

		gormConfig := &gorm.Config{
			Logger: gormlogger.Default.LogMode(gormLogLevel),
		}

		// 先构建到局部切片，全部成功后再安装，避免部分构建期间外部读到中间态
		var newReplicas []*gorm.DB
		for i, dsn := range replicaDSNs {
			if err := ctx.Err(); err != nil {
				for _, r := range newReplicas {
					warnCloseDB(r, "关闭已打开的数据库从库连接池失败")
				}
				return fmt.Errorf("数据库从库初始化已取消: %w", err)
			}
			replicaDB, err := gorm.Open(dialectorForDSN(cfg.Database.Driver, dsn), gormConfig)
			if err != nil {
				logger.Warnf("数据库从库 %d 连接失败: %v", i+1, err)
				continue
			}

			sqlDB, err := replicaDB.DB()
			if err != nil {
				logger.Warnf("数据库从库 %d 获取连接池失败: %v", i+1, err)
				warnCloseDB(replicaDB, "关闭刚打开的数据库从库连接池失败") // C11c: 关闭刚打开的池避免泄漏
				continue
			}

			sqlDB.SetMaxIdleConns(cfg.Database.MaxIdleConns)
			// H-A 修复：从库 MaxOpenConns 适当减少，但须避免截断为 0。
			// database/sql 中 SetMaxOpenConns(0) 表示"无限制"——原实现 MaxOpenConns/2 在
			// 配置为 1 时得 0，反而让从库连接池无上限（与"减少"意图相反、高并发下打爆 DB）；
			// 配置为 0（未配置/无限）时 0/2=0 恰好"无限"，与主库一致，保持语义。
			// 现规则：MaxOpenConns>0 时取 max(1, /2)；<=0 时从库亦无限（0），与主库对齐。
			sqlDB.SetMaxOpenConns(replicaMaxOpenConns(cfg.Database.MaxOpenConns))
			sqlDB.SetConnMaxLifetime(time.Hour)

			if err := sqlDB.PingContext(ctx); err != nil {
				logger.Warnf("数据库从库 %d Ping 失败: %v", i+1, err)
				warnCloseDB(replicaDB, "关闭 Ping 失败的数据库从库连接池失败") // C11c: 关闭刚打开的池避免泄漏
				continue
			}

			newReplicas = append(newReplicas, replicaDB)
			logger.Info("数据库从库连接成功", zap.Int("index", i+1))
		}

		m.mu.Lock()
		m.replicas = newReplicas
		m.ensureReplicaHealthLocked()
		m.mu.Unlock()
	}

	return nil
}

// InitDB 初始化数据库连接（带重试机制），驱动由配置决定。
func InitDB(ctx context.Context, cfg *config.Config) error {
	return DefaultManager.Load().InitDB(ctx, cfg)
}

// InitDBWithReplicas 初始化数据库主从连接，驱动由配置决定
func InitDBWithReplicas(ctx context.Context, cfg *config.Config, replicaDSNs []string) error {
	return DefaultManager.Load().InitDBWithReplicas(ctx, cfg, replicaDSNs)
}

// GetReadDB 获取读库实例（按策略选择从库）
func GetReadDB() *gorm.DB {
	return DefaultManager.Load().Replica()
}

// GetWriteDB 获取写库实例（主库）
func GetWriteDB() *gorm.DB {
	return DefaultManager.Load().Master()
}

// GetDB 获取数据库实例（默认主库，兼容旧代码）
func GetDB() *gorm.DB {
	return DefaultManager.Load().Master()
}

// GetReplicas 获取所有从库实例
func GetReplicas() []*gorm.DB {
	return DefaultManager.Load().Replicas()
}

// SetReplicaPicker 设置默认管理器的从库选择策略
func SetReplicaPicker(p ReplicaPicker) {
	DefaultManager.Load().SetPicker(p)
}

// UseMaster 强制使用主库（用于事务或需要实时数据的场景）
func UseMaster(ctx context.Context) context.Context {
	ctx = normalizeContext(ctx)
	return context.WithValue(ctx, dbModeContextKey{}, dbModeMaster)
}

// UseReplica 强制使用从库（用于报表查询等场景）
func UseReplica(ctx context.Context) context.Context {
	ctx = normalizeContext(ctx)
	return context.WithValue(ctx, dbModeContextKey{}, dbModeReplica)
}

// GetDBFromContext 根据上下文选择数据库
func GetDBFromContext(ctx context.Context) *gorm.DB {
	return DefaultManager.Load().FromContext(ctx)
}

// WithTx 将外层事务注入 ctx，使上层（如 repository.BaseRepo）在调用时能 join 到该事务
// 而非另开连接/路由到主从库（H6c）。
//
// 用法：
//
//	err := database.TransactionWithContext(ctx, func(tx *gorm.DB) error {
//	    ctx2 := database.WithTx(ctx, tx)
//	    // 传 ctx2 给 repo 方法，repo 内部会优先使用该 tx
//	    return repo.FindByID(ctx2, id) // 此查询参与外层事务
//	})
//
// 注意：tx 仅在该 ctx 的生命周期内有效；事务提交/回滚后不得再用该 ctx 携带的 tx。
func WithTx(ctx context.Context, tx *gorm.DB) context.Context {
	ctx = normalizeContext(ctx)
	if tx == nil {
		return ctx
	}
	return context.WithValue(ctx, txContextKey{}, tx)
}

// TxFromContext 取出 ctx 携带的外层事务；无则返回 nil。
func TxFromContext(ctx context.Context) *gorm.DB {
	ctx = normalizeContext(ctx)
	if tx, ok := ctx.Value(txContextKey{}).(*gorm.DB); ok {
		return tx
	}
	return nil
}

// AutoMigrate 自动迁移数据库表结构（由应用通过 WithMigrator/WithModels 注册）
func AutoMigrate() error {
	logger.Info("数据库表结构迁移完成")
	return nil
}

// Close 关闭所有数据库连接（主库与从库），等价于 CloseAll。
// 历史上仅关闭主库、遗留从库池泄漏（C11f），已修正为委托 CloseAll。
func Close() error {
	return CloseAll()
}

// CloseAll 关闭所有数据库连接（包括从库）
func CloseAll() error {
	return DefaultManager.Load().Close()
}

// Transaction 事务操作（自动使用主库）
func Transaction(fn func(tx *gorm.DB) error) error {
	db := DefaultManager.Load().Master()
	if db == nil {
		return errors.New("数据库未初始化")
	}
	return db.Transaction(fn)
}

// TransactionWithContext 带上下文的事务操作
func TransactionWithContext(ctx context.Context, fn func(tx *gorm.DB) error) error {
	ctx = normalizeContext(ctx)
	db := DefaultManager.Load().Master()
	if db == nil {
		return errors.New("数据库未初始化")
	}
	return db.WithContext(ctx).Transaction(fn)
}

// ReadQuery 读查询。遵循 ctx 中的数据库路由标记；未指定时默认走从库。
func ReadQuery(ctx context.Context, model any, query string, args ...any) error {
	ctx = normalizeContext(ctx)
	db := GetDBFromContext(ctx)
	if db == nil {
		return errors.New("数据库未初始化")
	}
	return db.WithContext(ctx).Where(query, args...).Find(model).Error
}

// WriteQuery 在主库上执行查询并扫描到 model（强制主库，绕过从库延迟）。
// 注意：命名沿用历史，实际用 .Find() 扫描结果集（读取语义），并非写操作——
// 强制主库是为了读到刚写入的最新数据（read-your-writes）。命名误导见 M11。
func WriteQuery(ctx context.Context, model any, query string, args ...any) error {
	ctx = normalizeContext(ctx)
	db := DefaultManager.Load().Master()
	if db == nil {
		return errors.New("数据库未初始化")
	}
	return db.WithContext(ctx).Where(query, args...).Find(model).Error
}

// healthCheckTimeout 健康检查单次 ping 超时，避免探针被慢/挂起的 DB 长期阻塞（M11）。
const healthCheckTimeout = 3 * time.Second

// pingWithTimeout 带超时的 ping，ctx 由调用方传入时优先尊重其 deadline。
func pingWithTimeout(sqlDB *sql.DB, parent context.Context) error {
	parent = normalizeContext(parent)
	ctx, cancel := context.WithTimeout(parent, healthCheckTimeout)
	defer cancel()
	return sqlDB.PingContext(ctx)
}

// HealthCheck 健康检查（主库 + 从库），单次 ping 限 3s 超时（M11）。
func HealthCheck() map[string]bool {
	result := make(map[string]bool)
	ctx := context.Background()
	m := DefaultManager.Load()

	// 检查主库
	if master := m.Master(); master != nil {
		sqlDB, err := master.DB()
		if err == nil && pingWithTimeout(sqlDB, ctx) == nil {
			result["master"] = true
		} else {
			result["master"] = false
		}
	} else {
		result["master"] = false
	}

	// 检查从库
	for i, replica := range m.Replicas() {
		if replica != nil {
			sqlDB, err := replica.DB()
			if err == nil && pingWithTimeout(sqlDB, ctx) == nil {
				result[fmt.Sprintf("replica_%d", i+1)] = true
			} else {
				result[fmt.Sprintf("replica_%d", i+1)] = false
			}
		} else {
			result[fmt.Sprintf("replica_%d", i+1)] = false
		}
	}

	return result
}

// IsDBHealthy 返回主库探活健康状态（#21）。
// 与 HealthCheck()（实时 ping）不同，这是后台探活维护的缓存标记，
// 供 readiness 探针快速判断是否接流量，避免每次探针都同步 ping。
func IsDBHealthy() bool {
	return DefaultManager.Load().IsHealthy()
}

// StartDBProbing 启动主库/从库探活后台循环（#21）。
// 阻塞，应通过 App.Go 在独立 goroutine 运行；ctx 取消时退出。
func StartDBProbing(ctx context.Context) {
	DefaultManager.Load().StartProbing(ctx)
}
