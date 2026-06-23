package database

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
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

const (
	dbModeMaster  = "master"
	dbModeReplica = "replica"
)

// ReplicaPicker 从库选择策略
type ReplicaPicker interface {
	Pick(replicas []*gorm.DB) *gorm.DB
}

// RoundRobinPicker 轮询选择从库
type RoundRobinPicker struct {
	counter uint64
}

// Pick 轮询选择一个从库
func (p *RoundRobinPicker) Pick(replicas []*gorm.DB) *gorm.DB {
	if len(replicas) == 0 {
		return nil
	}
	n := atomic.AddUint64(&p.counter, 1)
	return replicas[int(n-1)%len(replicas)]
}

// RandomPicker 随机选择从库
type RandomPicker struct{}

// Pick 随机选择一个从库
func (p *RandomPicker) Pick(replicas []*gorm.DB) *gorm.DB {
	if len(replicas) == 0 {
		return nil
	}
	return replicas[rand.Intn(len(replicas))]
}

// Manager 数据库管理器，持有主库与从库连接实例
type Manager struct {
	cfg      *config.Config
	master   *gorm.DB
	replicas []*gorm.DB
	picker   ReplicaPicker
	mu       sync.Mutex

	// #21 健康自愈
	healthy          atomic.Bool       // 主库是否健康
	replicaHealthy   []atomic.Bool     // 每个从库的健康标记，索引与 replicas 对齐
	probeFailures    int               // 主库连续探活失败次数
	probeMu          sync.Mutex        // 保护 probeFailures
	replicaHealthSet bool              // replicaHealthy 是否已按 replicas 长度初始化
}

// NewManager 创建数据库管理器
func NewManager(cfg *config.Config) *Manager {
	return &Manager{cfg: cfg, picker: &RandomPicker{}}
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

// Master 返回主库实例
func (m *Manager) Master() *gorm.DB {
	return m.master
}

// Replicas 返回所有从库实例
func (m *Manager) Replicas() []*gorm.DB {
	return m.replicas
}

// Replica 按策略选择一个从库；无从库时返回主库。
// #21：启用探活后，自动过滤不健康的从库；全不健康时回退到全部从库（仍可服务）。
func (m *Manager) Replica() *gorm.DB {
	if len(m.replicas) == 0 {
		return m.master
	}
	m.mu.Lock()
	defer m.mu.Unlock()

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

// StartProbing 启动主库与从库的健康探活后台循环（#21）。
// 阻塞调用方，应通过 App.Go 在独立 goroutine 运行；ctx 取消时退出。
// 周期 ping 主库，连续失败达阈值后标记不健康（IsHealthy=false）；
// 同时 ping 各从库，失败则从读流量剔除，恢复后自动重新纳入。
func (m *Manager) StartProbing(ctx context.Context) {
	m.initReplicaHealth()

	interval := 30 * time.Second
	if m.cfg != nil && m.cfg.Database.HealthCheckInterval > 0 {
		interval = m.cfg.Database.HealthCheckInterval
	}
	threshold := 3
	if m.cfg != nil && m.cfg.Database.HealthCheckFailureThreshold > 0 {
		threshold = m.cfg.Database.HealthCheckFailureThreshold
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
	healthSet := m.replicaHealthSet
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
			if i < len(m.replicaHealthy) {
				m.replicaHealthy[i].Store(false)
			}
			continue
		}
		if err := sqlDB.PingContext(ctx); err != nil {
			if i < len(m.replicaHealthy) && m.replicaHealthy[i].Load() {
				logger.Warnf("数据库从库 #%d 探活失败，暂时剔除读流量: %v", i, err)
			}
			if i < len(m.replicaHealthy) {
				m.replicaHealthy[i].Store(false)
			}
		} else {
			if i < len(m.replicaHealthy) {
				m.replicaHealthy[i].Store(true)
			}
		}
	}
}

// FromContext 根据上下文选择数据库
func (m *Manager) FromContext(ctx context.Context) *gorm.DB {
	mode, ok := ctx.Value(dbModeContextKey{}).(string)
	if !ok {
		return m.Replica()
	}
	switch mode {
	case dbModeMaster:
		return m.master
	case dbModeReplica:
		return m.Replica()
	default:
		return m.Replica()
	}
}

// Open 打开主库连接
func (m *Manager) Open(ctx context.Context) error {
	if m.cfg == nil {
		return errors.New("数据库配置未设置")
	}
	return m.InitDB(m.cfg)
}

// OpenWithReplicas 打开主库与从库连接
func (m *Manager) OpenWithReplicas(ctx context.Context, replicaDSNs []string) error {
	if m.cfg == nil {
		return errors.New("数据库配置未设置")
	}
	return m.InitDBWithReplicas(m.cfg, replicaDSNs)
}

// Close 关闭主库与全部从库连接
func (m *Manager) Close() error {
	var errs []error

	if m.master != nil {
		sqlDB, err := m.master.DB()
		if err != nil {
			errs = append(errs, err)
		} else if err := sqlDB.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	for _, replica := range m.replicas {
		if replica == nil {
			continue
		}
		sqlDB, err := replica.DB()
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if err := sqlDB.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	m.master = nil
	m.replicas = nil

	return errors.Join(errs...)
}

// HealthCheck 健康检查，主库不可达时返回错误
func (m *Manager) HealthCheck(ctx context.Context) error {
	if m.master == nil {
		return errors.New("database master not initialized")
	}
	sqlDB, err := m.master.DB()
	if err != nil {
		return err
	}
	return sqlDB.PingContext(ctx)
}

// DefaultManager 默认数据库管理器
var DefaultManager = &Manager{picker: &RandomPicker{}}

// InitDB 初始化数据库连接（带重试机制），驱动由配置决定
func (m *Manager) InitDB(cfg *config.Config) error {
	var err error
	m.cfg = cfg

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

	// 重试配置
	maxRetries := 5
	retryDelay := time.Second

	var lastErr error
	for i := range maxRetries {
		// 连接主库
		m.master, err = gorm.Open(Dialector(cfg), gormConfig)
		if err == nil {
			sqlDB, err := m.master.DB()
			if err == nil {
				sqlDB.SetMaxIdleConns(cfg.Database.MaxIdleConns)
				sqlDB.SetMaxOpenConns(cfg.Database.MaxOpenConns)
				sqlDB.SetConnMaxLifetime(time.Hour)
				if cfg.Database.ConnMaxIdleTime > 0 {
					sqlDB.SetConnMaxIdleTime(cfg.Database.ConnMaxIdleTime)
				}
				m.healthy.Store(true) // 主库连通即标记健康（#21）

				if err := sqlDB.Ping(); err == nil {
					logger.Info("数据库主库连接成功",
						zap.String("driver", driverDescription(cfg.Database.Driver)),
						zap.String("host", cfg.Database.Host),
						zap.Int("port", cfg.Database.Port))
					return nil
				} else {
					// Ping 失败（如服务端暂时不可达）视作可重试
					lastErr = err
				}
			} else {
				lastErr = err
			}
		} else {
			lastErr = err
			// 不可恢复的错误（认证失败、未知数据库、DSN 非法等）直接返回，不必重试
			if !isTransientDBError(err) {
				return fmt.Errorf("数据库连接失败（不可恢复）: %w", err)
			}
		}

		logger.Warnf("数据库连接失败，第 %d/%d 次重试: %v", i+1, maxRetries, lastErr)
		time.Sleep(retryDelay)
		retryDelay *= 2
		if retryDelay > 30*time.Second {
			retryDelay = 30 * time.Second
		}
	}

	return fmt.Errorf("数据库连接失败（重试 %d 次）: %w", maxRetries, lastErr)
}

// isTransientDBError 判断数据库连接错误是否值得重试。
// 认证失败、未知数据库、非法 DSN/驱动等属于配置类错误，重试无意义，直接返回更友好。
func isTransientDBError(err error) bool {
	if err == nil {
		return true
	}
	msg := err.Error()
	nonTransient := []string{
		"Access denied",         // MySQL 认证失败（用户名/密码错误）
		"authentication plugin", // MySQL 认证插件不支持
		"Unknown database",      // MySQL 目标库不存在
		"invalid DSN",           // DSN 语法错误
		"unknown driver",        // 驱动未注册
		"unsupported driver",    // 驱动不支持
	}
	for _, sub := range nonTransient {
		if strings.Contains(msg, sub) {
			return false
		}
	}
	return true
}

// InitDBWithReplicas 初始化数据库主从连接，驱动由配置决定
// replicaDSNs: 从库连接字符串列表（需与主库驱动匹配）
func (m *Manager) InitDBWithReplicas(cfg *config.Config, replicaDSNs []string) error {
	// 先初始化主库
	if err := m.InitDB(cfg); err != nil {
		return err
	}

	m.replicas = nil

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

		for i, dsn := range replicaDSNs {
			replicaDB, err := gorm.Open(dialectorForDSN(cfg.Database.Driver, dsn), gormConfig)
			if err != nil {
				logger.Warnf("数据库从库 %d 连接失败: %v", i+1, err)
				continue
			}

			sqlDB, err := replicaDB.DB()
			if err != nil {
				logger.Warnf("数据库从库 %d 获取连接池失败: %v", i+1, err)
				continue
			}

			sqlDB.SetMaxIdleConns(cfg.Database.MaxIdleConns)
			sqlDB.SetMaxOpenConns(cfg.Database.MaxOpenConns / 2) // 从库连接数可适当减少
			sqlDB.SetConnMaxLifetime(time.Hour)

			if err := sqlDB.Ping(); err != nil {
				logger.Warnf("数据库从库 %d Ping 失败: %v", i+1, err)
				continue
			}

			m.replicas = append(m.replicas, replicaDB)
			logger.Info("数据库从库连接成功", zap.Int("index", i+1))
		}
	}

	return nil
}

// InitDB 初始化数据库连接（带重试机制），驱动由配置决定
func InitDB(cfg *config.Config) error {
	return DefaultManager.InitDB(cfg)
}

// InitDBWithReplicas 初始化数据库主从连接，驱动由配置决定
func InitDBWithReplicas(cfg *config.Config, replicaDSNs []string) error {
	return DefaultManager.InitDBWithReplicas(cfg, replicaDSNs)
}

// GetReadDB 获取读库实例（按策略选择从库）
func GetReadDB() *gorm.DB {
	return DefaultManager.Replica()
}

// GetWriteDB 获取写库实例（主库）
func GetWriteDB() *gorm.DB {
	return DefaultManager.Master()
}

// GetDB 获取数据库实例（默认主库，兼容旧代码）
func GetDB() *gorm.DB {
	return DefaultManager.Master()
}

// GetReplicas 获取所有从库实例
func GetReplicas() []*gorm.DB {
	return DefaultManager.Replicas()
}

// SetReplicaPicker 设置默认管理器的从库选择策略
func SetReplicaPicker(p ReplicaPicker) {
	DefaultManager.SetPicker(p)
}

// UseMaster 强制使用主库（用于事务或需要实时数据的场景）
func UseMaster(ctx context.Context) context.Context {
	return context.WithValue(ctx, dbModeContextKey{}, dbModeMaster)
}

// UseReplica 强制使用从库（用于报表查询等场景）
func UseReplica(ctx context.Context) context.Context {
	return context.WithValue(ctx, dbModeContextKey{}, dbModeReplica)
}

// GetDBFromContext 根据上下文选择数据库
func GetDBFromContext(ctx context.Context) *gorm.DB {
	return DefaultManager.FromContext(ctx)
}

// AutoMigrate 自动迁移数据库表结构（由应用通过 WithMigrator/WithModels 注册）
func AutoMigrate() error {
	logger.Info("数据库表结构迁移完成")
	return nil
}

// Close 关闭主库连接（兼容旧代码，从库连接请使用 CloseAll）
func Close() error {
	if DefaultManager.master == nil {
		return nil
	}
	sqlDB, err := DefaultManager.master.DB()
	if err != nil {
		return err
	}
	err = sqlDB.Close()
	DefaultManager.master = nil
	return err
}

// CloseAll 关闭所有数据库连接（包括从库）
func CloseAll() error {
	return DefaultManager.Close()
}

// Transaction 事务操作（自动使用主库）
func Transaction(fn func(tx *gorm.DB) error) error {
	if DefaultManager.master == nil {
		return errors.New("数据库未初始化")
	}
	return DefaultManager.master.Transaction(fn)
}

// TransactionWithContext 带上下文的事务操作
func TransactionWithContext(ctx context.Context, fn func(tx *gorm.DB) error) error {
	if DefaultManager.master == nil {
		return errors.New("数据库未初始化")
	}
	return DefaultManager.master.WithContext(ctx).Transaction(fn)
}

// ReadQuery 读查询（自动路由到从库）
func ReadQuery(ctx context.Context, model any, query string, args ...any) error {
	db := GetDBFromContext(ctx)
	if db == nil {
		return errors.New("数据库未初始化")
	}
	return db.WithContext(ctx).Where(query, args...).Find(model).Error
}

// WriteQuery 写查询（强制使用主库）
func WriteQuery(ctx context.Context, model any, query string, args ...any) error {
	if DefaultManager.master == nil {
		return errors.New("数据库未初始化")
	}
	return DefaultManager.master.WithContext(ctx).Where(query, args...).Find(model).Error
}

// HealthCheck 健康检查
func HealthCheck() map[string]bool {
	result := make(map[string]bool)

	// 检查主库
	if DefaultManager.master != nil {
		sqlDB, err := DefaultManager.master.DB()
		if err == nil && sqlDB.Ping() == nil {
			result["master"] = true
		} else {
			result["master"] = false
		}
	} else {
		result["master"] = false
	}

	// 检查从库
	for i, replica := range DefaultManager.replicas {
		if replica != nil {
			sqlDB, err := replica.DB()
			if err == nil && sqlDB.Ping() == nil {
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
	return DefaultManager.IsHealthy()
}

// StartDBProbing 启动主库/从库探活后台循环（#21）。
// 阻塞，应通过 App.Go 在独立 goroutine 运行；ctx 取消时退出。
func StartDBProbing(ctx context.Context) {
	DefaultManager.StartProbing(ctx)
}
