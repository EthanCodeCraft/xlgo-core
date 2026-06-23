package database

import (
	"context"
	"fmt"
	"sync"

	"github.com/EthanCodeCraft/xlgo-core/config"
	"github.com/EthanCodeCraft/xlgo-core/logger"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// RedisClient 全局 Redis 客户端（兼容 facade，由 RedisManager.Init 同步维护）。
// 保留供存量代码直接访问；新代码建议用 GetRedis() 或持有 *RedisManager 实例。
var RedisClient *redis.Client

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
		Addr:     cfg.Redis.Addr(),
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
	})

	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		client.Close()
		return fmt.Errorf("Redis 连接失败: %w", err)
	}

	m.cfg = cfg
	m.client = client
	RedisClient = client // 同步兼容 facade
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
	RedisClient = nil
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

// GetRedis 获取 Redis 客户端
func GetRedis() *redis.Client {
	return DefaultRedis.Client()
}
