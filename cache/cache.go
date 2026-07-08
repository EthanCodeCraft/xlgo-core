package cache

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"github.com/EthanCodeCraft/xlgo-core/database"
	"github.com/EthanCodeCraft/xlgo-core/logger"
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

// CacheExistChecker is implemented by cache backends that can distinguish a
// missing key from a backend failure.
type CacheExistChecker interface {
	ExistsE(ctx context.Context, key string) (bool, error)
}

// redisCache Redis 缓存实现。
//
// 不在构造时快照 redis.Client（M12 修复：原 NewRedisCache 构造时取 database.GetRedis()，
// 若在 database.InitRedis 之前构造则永久 nil、即使后续 Redis 就绪也是 no-op）。
// 改为每次操作实时取 database.GetRedis()，使"先构造后 Init Redis"的顺序也能正确工作。
type redisCache struct{}

// client 返回当前 Redis 客户端（实时取，未初始化则 nil）。
func (c *redisCache) client() *redis.Client {
	return database.GetRedis()
}

// NewRedisCache 创建 Redis 缓存实例
func NewRedisCache() CacheService {
	return &redisCache{}
}

// Get 获取缓存值
func (c *redisCache) Get(ctx context.Context, key string, dest any) bool {
	cli := c.client()
	if cli == nil {
		return false
	}

	val, err := cli.Get(ctx, key).Result()
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
	cli := c.client()
	if cli == nil {
		return ErrRedisNotReady
	}

	data, err := json.Marshal(value)
	if err != nil {
		logger.Warn("缓存序列化失败", zap.String("key", key), zap.Error(err))
		return err
	}

	if err := cli.Set(ctx, key, data, ttl).Err(); err != nil {
		logger.Warn("缓存设置失败", zap.String("key", key), zap.Error(err))
		return err
	}

	return nil
}

// Delete 删除缓存
func (c *redisCache) Delete(ctx context.Context, key string) error {
	cli := c.client()
	if cli == nil {
		return ErrRedisNotReady
	}

	if err := cli.Del(ctx, key).Err(); err != nil {
		logger.Warn("缓存删除失败", zap.String("key", key), zap.Error(err))
		return err
	}

	return nil
}

// DeleteByPattern 按模式删除缓存（使用 SCAN 避免阻塞 Redis）
func (c *redisCache) DeleteByPattern(ctx context.Context, pattern string) error {
	cli := c.client()
	if cli == nil {
		return ErrRedisNotReady
	}

	var cursor uint64
	var deleted int

	for {
		// 使用 SCAN 命令迭代查找匹配的键
		keys, nextCursor, err := cli.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			logger.Warn("缓存键扫描失败", zap.String("pattern", pattern), zap.Error(err))
			return err
		}

		// 删除找到的键
		if len(keys) > 0 {
			if err := cli.Del(ctx, keys...).Err(); err != nil {
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
	ok, err := c.ExistsE(ctx, key)
	if err != nil {
		logger.Warn("缓存存在性检查失败", zap.String("key", key), zap.Error(err))
		return false
	}
	return ok
}

// ExistsE checks whether key exists and returns Redis/backend errors to callers
// that need to distinguish a missing key from a cache outage.
func (c *redisCache) ExistsE(ctx context.Context, key string) (bool, error) {
	cli := c.client()
	if cli == nil {
		return false, ErrRedisNotReady
	}

	n, err := cli.Exists(ctx, key).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// CacheManager 缓存管理器（#10）。照 database.Manager 模式：
// 实例化 + DefaultCache 全局默认 + 包级 facade 代理，支持测试注入 mock 实现。
type CacheManager struct {
	mu  sync.Mutex
	svc CacheService
}

// defaultCachePtr 全局默认缓存管理器（CK3 修复：atomic.Pointer 替代裸指针）。
var defaultCachePtr atomic.Pointer[CacheManager]

func init() {
	defaultCachePtr.Store(NewCacheManager())
}

// NewCacheManager 创建缓存管理器实例。
func NewCacheManager() *CacheManager { return &CacheManager{} }

// GetDefaultCache 返回全局默认缓存管理器（并发安全，CK3 修复）。
func GetDefaultCache() *CacheManager {
	return defaultCachePtr.Load()
}

// SetDefaultCacheManager 提升指定 CacheManager 为全局默认（atomic 置换，并发安全）。
func SetDefaultCacheManager(m *CacheManager) {
	if m != nil {
		defaultCachePtr.Store(m)
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

// --- 包级 facade（代理到 GetDefaultCache()，CK3 修复） ---

// Init 初始化全局缓存实例
func Init() {
	GetDefaultCache().Init()
}

// GetCache 获取全局缓存实例
func GetCache() CacheService {
	return GetDefaultCache().Get()
}

// ExistsE checks whether key exists and returns backend errors. It complements
// the legacy bool-only CacheService.Exists method without changing that public
// interface for downstream custom cache implementations.
func ExistsE(ctx context.Context, key string) (bool, error) {
	svc := GetCache()
	if svc == nil {
		return false, ErrRedisNotReady
	}
	if checker, ok := svc.(CacheExistChecker); ok {
		return checker.ExistsE(ctx, key)
	}
	return svc.Exists(ctx, key), nil
}
