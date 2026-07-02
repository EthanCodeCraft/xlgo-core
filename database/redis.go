package database

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/EthanCodeCraft/xlgo-core/config"
	"github.com/EthanCodeCraft/xlgo-core/logger"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// redisClient 内部 Redis 客户端引用（由 RedisManager.Init/Close 同步维护）。
//
// 已废弃对外暴露：所有外部代码请使用 GetRedis() 或持有 *RedisManager 实例。
// 测试如需注入 mock Redis，请用 SetDefaultRedisManager 替换 DefaultRedis。
//
// 仅包内 Init/Close 在持有 m.mu 时写入；外部直接读取存在竞态风险。
var redisClient *redis.Client

// SetTestRedisClient 供测试注入 miniredis 等 mock 客户端。
// 返回旧客户端引用以便测试清理时恢复。生产代码严禁调用。
// 注意：仅在测试环境单 goroutine 使用，不持有锁保护。
func SetTestRedisClient(c *redis.Client) *redis.Client {
	old := redisClient
	redisClient = c
	return old
}

// RedisManager Redis 连接管理器（#10）。照 database.Manager 模式：
// 实例化 + DefaultRedis 全局默认 + 包级 facade 代理，支持多实例与测试注入。
type RedisManager struct {
	mu     sync.Mutex
	cfg    *config.Config
	client *redis.Client
}

// DefaultRedis 默认 Redis 管理器，包级 facade 代理到它。
var DefaultRedis = NewRedisManager()

// NewRedisManager 创建 Redis 管理器实例。
func NewRedisManager() *RedisManager { return &RedisManager{} }

// SetDefaultRedisManager 提升指定 RedisManager 为全局默认，后续包级 facade 走它。
// 用于多实例场景或测试注入 mock。
func SetDefaultRedisManager(m *RedisManager) {
	if m != nil {
		DefaultRedis = m
	}
}

// Init 初始化 Redis 连接并 ping 验证。
func (m *RedisManager) Init(cfg *config.Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	client := redis.NewClient(&redis.Options{
		Addr:         cfg.Redis.Addr(),
		Password:     cfg.Redis.Password,
		DB:           cfg.Redis.DB,
		DialTimeout:  5 * time.Second, // D7 修复：连接超时
		ReadTimeout:  3 * time.Second, // D7 修复：读超时
		WriteTimeout: 3 * time.Second, // D7 修复：写超时
	})

	pingCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		client.Close()
		return fmt.Errorf("Redis 连接失败: %w", err)
	}

	m.cfg = cfg
	m.client = client
	redisClient = client // 内部同步
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
	redisClient = nil
	return err
}

// Client 返回当前 Redis 客户端（未初始化返回 nil）。
func (m *RedisManager) Client() *redis.Client {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.client
}

// HealthCheck Redis 健康检查。
func (m *RedisManager) HealthCheck(ctx context.Context) error {
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
	return DefaultRedis.Init(cfg)
}

// CloseRedis 关闭 Redis 连接
func CloseRedis() error {
	return DefaultRedis.Close()
}

// HealthCheckRedis Redis 健康检查
func HealthCheckRedis(ctx context.Context) error {
	return DefaultRedis.HealthCheck(ctx)
}

// GetRedis 获取 Redis 客户端。
// 优先返回 DefaultRedis 实例化的客户端；若未初始化则回退到内部客户端（测试注入路径）。
func GetRedis() *redis.Client {
	if c := DefaultRedis.Client(); c != nil {
		return c
	}
	return redisClient
}
