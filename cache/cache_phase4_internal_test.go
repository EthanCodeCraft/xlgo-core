package cache

import (
	"testing"

	"github.com/EthanCodeCraft/xlgo-core/database"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// TestRedisCacheInjectedClientPriority 固化 Phase 4 核心契约（jwt 模型）：
// redisCache.client() 注入优先、全局兜底。注入的 client 即使与 database.GetRedis()
// 不同也必须用注入的--这是多 App 缓存隔离的基础。
func TestRedisCacheInjectedClientPriority(t *testing.T) {
	// 全局 redis = clientG
	mrG := miniredis.RunT(t)
	clientG := redis.NewClient(&redis.Options{Addr: mrG.Addr()})
	t.Cleanup(func() { _ = clientG.Close() })
	origGlobal := database.SetTestRedisClient(clientG)
	t.Cleanup(func() { database.SetTestRedisClient(origGlobal) })

	// 注入 clientA（与全局 clientG 不同实例）
	mrA := miniredis.RunT(t)
	clientA := redis.NewClient(&redis.Options{Addr: mrA.Addr()})
	t.Cleanup(func() { _ = clientA.Close() })

	// 注入优先
	injected := &redisCache{injectedClient: clientA}
	if got := injected.client(); got != clientA {
		t.Fatalf("injected redisCache.client() = %v, want clientA (注入优先)", got)
	}

	// 未注入 -> 回退全局 clientG
	fallback := &redisCache{}
	if got := fallback.client(); got != clientG {
		t.Fatalf("fallback redisCache.client() = %v, want clientG (全局兜底)", got)
	}

	// NewRedisCacheWithRedis 构造的实例持注入 client
	svc := NewRedisCacheWithRedis(clientA)
	rc, ok := svc.(*redisCache)
	if !ok {
		t.Fatalf("NewRedisCacheWithRedis returned %T, want *redisCache", svc)
	}
	if rc.client() != clientA {
		t.Fatalf("NewRedisCacheWithRedis client = %v, want clientA", rc.client())
	}
}

// TestSwapDefaultCacheManagerReturnsOld 固化 Phase 4：Swap 返回被替换的旧 Manager 且不关闭。
func TestSwapDefaultCacheManagerReturnsOld(t *testing.T) {
	orig := defaultCachePtr.Load()
	defer SwapDefaultCacheManager(orig)

	first := NewCacheManager()
	if prev := SwapDefaultCacheManager(first); prev != orig {
		t.Fatalf("Swap returned %v, want orig", prev)
	}
	second := NewCacheManager()
	if returned := SwapDefaultCacheManager(second); returned != first {
		t.Fatalf("Swap returned %v, want first", returned)
	}
	if defaultCachePtr.Load() != second {
		t.Fatal("Swap did not install second as default")
	}
	if got := SwapDefaultCacheManager(nil); got != second {
		t.Fatalf("Swap(nil) = %v, want second (current default)", got)
	}
}
