package database

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/EthanCodeCraft/xlgo-core/config"
	"github.com/EthanCodeCraft/xlgo-core/logger"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// RedisManager Redis 连接管理器（#10）。照 database.Manager 模式：
// 实例化 + DefaultRedis 全局默认 + 包级 facade 代理，支持多实例与测试注入。
type RedisManager struct {
	mu     sync.Mutex
	cfg    *config.Config
	client *redis.Client
}

// DefaultRedis 默认 Redis 管理器，包级 facade 代理到它。
//
// C-1/H-4 修复：改用 atomic.Pointer 保护读写，消除原裸指针置换（SetDefaultRedisManager）
// 与请求 goroutine 无锁读（GetRedis 等 facade）之间的数据竞争。与 config.defaultManager
// 对齐。类型由 *RedisManager 变更为 atomic.Pointer[RedisManager]（breaking：下游若直接
// 调用 DefaultRedis.Init 等方法需改用 InitRedis 等 facade，或 DefaultRedis.Load().Init）。
var DefaultRedis atomic.Pointer[RedisManager]

func init() {
	DefaultRedis.Store(NewRedisManager())
}

// NewRedisManager 创建 Redis 管理器实例。
func NewRedisManager() *RedisManager { return &RedisManager{} }

// SwapDefaultRedisManager 将指定 RedisManager 置为全局默认，并返回被替换的旧 Manager。
// 旧 Manager 不会被关闭，供 App 初始化这类需要失败回滚的生命周期流程暂存。
// nil 被忽略，以防 facade Load 到 nil panic。
func SwapDefaultRedisManager(m *RedisManager) *RedisManager {
	if m == nil {
		return DefaultRedis.Load()
	}
	return DefaultRedis.Swap(m)
}

// SetDefaultRedisManager 提升指定 RedisManager 为全局默认，并关闭被替换的旧 Manager。
// 用于多实例场景或测试注入 mock。并发安全。若调用方需要保留旧 Manager
// 用于回滚，请使用 SwapDefaultRedisManager。
func SetDefaultRedisManager(m *RedisManager) {
	if m == nil {
		return
	}
	old := SwapDefaultRedisManager(m)
	if old != nil && old != m {
		if err := old.Close(); err != nil {
			logger.Warnf("关闭被替换的旧 Redis manager 失败: %v", err)
		}
	}
}

// GetDefaultRedisManager 返回当前默认 Redis 管理器。
func GetDefaultRedisManager() *RedisManager {
	return DefaultRedis.Load()
}

// newRedisClient 构造带 D7 超时（Dial 5s / Read 3s / Write 3s）的 redis.Client。
// 由 Init 与测试共用，确保所有 client 实例一致具备超时约束（挂起 Redis 不无限阻塞）。
func newRedisClient(addr, password string, db int) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:         addr,
		Password:     password,
		DB:           db,
		DialTimeout:  5 * time.Second, // D7 修复：连接超时
		ReadTimeout:  3 * time.Second, // D7 修复：读超时（约束 HealthCheck Ping 不被挂起 Redis 阻塞）
		WriteTimeout: 3 * time.Second, // D7 修复：写超时
	})
}

// Init 初始化 Redis 连接并 ping 验证。
func (m *RedisManager) Init(cfg *config.Config) error {
	if cfg == nil {
		return errors.New("Redis 配置未设置")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	client := newRedisClient(cfg.Redis.Addr(), cfg.Redis.Password, cfg.Redis.DB)

	pingCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		if cerr := client.Close(); cerr != nil {
			return errors.Join(fmt.Errorf("Redis 连接失败: %w", err), fmt.Errorf("Redis 关闭失败: %w", cerr))
		}
		return fmt.Errorf("Redis 连接失败: %w", err)
	}

	old := m.client
	m.cfg = cfg
	m.client = client
	if old != nil {
		if err := old.Close(); err != nil {
			logger.Warnf("关闭旧 Redis 连接失败: %v", err)
		}
	}
	logger.Info("Redis 连接成功", zap.String("addr", cfg.Redis.Addr()))
	return nil
}

// Close 关闭 Redis 连接。
func (m *RedisManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.client == nil {
		return nil
	}
	err := m.client.Close()
	m.client = nil
	return err
}

// Client 返回当前 Redis 客户端（未初始化返回 nil）。
func (m *RedisManager) Client() *redis.Client {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.client
}

// setClientForTest 仅测试用：在持锁下替换 manager 的 client，返回旧 client。
// 供 SetTestRedisClient 注入 miniredis 等 mock，消除原包级 redisClient 双源真相。
func (m *RedisManager) setClientForTest(c *redis.Client) *redis.Client {
	m.mu.Lock()
	defer m.mu.Unlock()
	old := m.client
	m.client = c
	return old
}

// HealthCheck Redis 健康检查。
func (m *RedisManager) HealthCheck(ctx context.Context) error {
	ctx = normalizeContext(ctx)
	m.mu.Lock()
	client := m.client
	m.mu.Unlock()
	if client == nil {
		return fmt.Errorf("Redis 未初始化")
	}
	return client.Ping(ctx).Err()
}

// --- 包级 facade（代理到 DefaultRedis，兼容存量） ---

// InitRedis 初始化 Redis 连接
func InitRedis(cfg *config.Config) error {
	return DefaultRedis.Load().Init(cfg)
}

// CloseRedis 关闭 Redis 连接
func CloseRedis() error {
	return DefaultRedis.Load().Close()
}

// HealthCheckRedis Redis 健康检查
func HealthCheckRedis(ctx context.Context) error {
	return DefaultRedis.Load().HealthCheck(ctx)
}

// GetRedis 获取 Redis 客户端（未初始化返回 nil）。
//
// H-4 修复：单源——仅从 DefaultRedis.Load().Client() 取，废弃原包级 redisClient
// 回退路径（其在 manager 替换后会返回 stale client，且无锁读存在竞态）。
// 测试注入的 mock client 经 SetTestRedisClient 直接设在当前 manager 上，闭环一致。
func GetRedis() *redis.Client {
	return DefaultRedis.Load().Client()
}

// SetTestRedisClient 供测试注入 miniredis 等 mock 客户端。
// 返回旧客户端引用以便测试清理时恢复。生产代码严禁调用。
//
// H-4 修复：改为在当前默认 manager 上持锁替换 client（setClientForTest），
// 不再写独立的包级 redisClient 变量，消除双源真相与无锁写竞态。
//
// 约束：操作 DefaultRedis.Load() 返回的当前默认 manager。测试应避免在调用本函数
// 的同时并发 SetDefaultRedisManager 替换默认 manager，否则注入/恢复会作用到不同
// manager 上。典型用法为 init 阶段注入、t.Cleanup 恢复，串行执行。
func SetTestRedisClient(c *redis.Client) *redis.Client {
	return DefaultRedis.Load().setClientForTest(c)
}
