package cache

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/EthanCodeCraft/xlgo-core/logger"

	"github.com/EthanCodeCraft/xlgo-core/database"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// CacheService 缓存服务接口
type CacheService interface {
	// Get 获取缓存值，如果存在则反序列化到 dest 并返回 true
	Get(ctx context.Context, key string, dest any) bool
	// Set 设置缓存值
	Set(ctx context.Context, key string, value any, ttl time.Duration) error
	// Delete 删除缓存
	Delete(ctx context.Context, key string) error
	// DeleteByPattern 按模式删除缓存
	DeleteByPattern(ctx context.Context, pattern string) error
	// Exists 检查缓存是否存在
	Exists(ctx context.Context, key string) bool
}

// redisCache Redis 缓存实现
type redisCache struct {
	client *redis.Client
}

// NewRedisCache 创建 Redis 缓存实例
func NewRedisCache() CacheService {
	return &redisCache{
		client: database.GetRedis(),
	}
}

// Get 获取缓存值
func (c *redisCache) Get(ctx context.Context, key string, dest any) bool {
	if c.client == nil {
		return false
	}

	val, err := c.client.Get(ctx, key).Result()
	if err != nil {
		if err != redis.Nil {
			logger.Warn("缓存获取失败", zap.String("key", key), zap.Error(err))
		}
		return false
	}

	if err := json.Unmarshal([]byte(val), dest); err != nil {
		logger.Warn("缓存反序列化失败", zap.String("key", key), zap.Error(err))
		return false
	}

	return true
}

// Set 设置缓存值
func (c *redisCache) Set(ctx context.Context, key string, value any, ttl time.Duration) error {
	if c.client == nil {
		return nil // Redis 未启用，跳过缓存
	}

	data, err := json.Marshal(value)
	if err != nil {
		logger.Warn("缓存序列化失败", zap.String("key", key), zap.Error(err))
		return err
	}

	if err := c.client.Set(ctx, key, data, ttl).Err(); err != nil {
		logger.Warn("缓存设置失败", zap.String("key", key), zap.Error(err))
		return err
	}

	return nil
}

// Delete 删除缓存
func (c *redisCache) Delete(ctx context.Context, key string) error {
	if c.client == nil {
		return nil
	}

	if err := c.client.Del(ctx, key).Err(); err != nil {
		logger.Warn("缓存删除失败", zap.String("key", key), zap.Error(err))
		return err
	}

	return nil
}

// DeleteByPattern 按模式删除缓存（使用 SCAN 避免阻塞 Redis）
func (c *redisCache) DeleteByPattern(ctx context.Context, pattern string) error {
	if c.client == nil {
		return nil
	}

	var cursor uint64
	var deleted int

	for {
		// 使用 SCAN 命令迭代查找匹配的键
		keys, nextCursor, err := c.client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			logger.Warn("缓存键扫描失败", zap.String("pattern", pattern), zap.Error(err))
			return err
		}

		// 删除找到的键
		if len(keys) > 0 {
			if err := c.client.Del(ctx, keys...).Err(); err != nil {
				logger.Warn("缓存批量删除失败", zap.Strings("keys", keys), zap.Error(err))
				return err
			}
			deleted += len(keys)
		}

		// 更新游标
		cursor = nextCursor

		// 游标为 0 表示遍历完成
		if cursor == 0 {
			break
		}
	}

	if deleted > 0 {
		logger.Debug("缓存批量删除完成", zap.String("pattern", pattern), zap.Int("count", deleted))
	}

	return nil
}

// Exists 检查缓存是否存在
func (c *redisCache) Exists(ctx context.Context, key string) bool {
	if c.client == nil {
		return false
	}

	return c.client.Exists(ctx, key).Val() > 0
}

// CacheManager 缓存管理器（#10）。照 database.Manager 模式：
// 实例化 + DefaultCache 全局默认 + 包级 facade 代理，支持测试注入 mock 实现。
type CacheManager struct {
	mu  sync.Mutex
	svc CacheService
}

// DefaultCache 默认缓存管理器，包级 facade 代理到它。
var DefaultCache = NewCacheManager()

// NewCacheManager 创建缓存管理器实例。
func NewCacheManager() *CacheManager { return &CacheManager{} }

// SetDefaultCacheManager 提升指定 CacheManager 为全局默认。
func SetDefaultCacheManager(m *CacheManager) {
	if m != nil {
		DefaultCache = m
	}
}

// Init 初始化缓存服务（基于 DefaultRedis 的客户端）。
func (m *CacheManager) Init() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.svc = NewRedisCache()
}

// Set 设置缓存服务实现（用于注入 mock 或自定义实现）。
func (m *CacheManager) Set(svc CacheService) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.svc = svc
}

// Get 返回缓存服务（未初始化时延迟初始化）。
func (m *CacheManager) Get() CacheService {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.svc == nil {
		m.svc = NewRedisCache()
	}
	return m.svc
}

// --- 包级 facade（代理到 DefaultCache，兼容存量） ---

// Init 初始化全局缓存实例
func Init() {
	DefaultCache.Init()
}

// GetCache 获取全局缓存实例
func GetCache() CacheService {
	return DefaultCache.Get()
}
