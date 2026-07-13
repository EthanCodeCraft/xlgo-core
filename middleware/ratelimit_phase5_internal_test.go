package middleware

import (
	"testing"
	"time"

	"github.com/EthanCodeCraft/xlgo-core/database"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// TestRedisRateLimiterInjectedClientPriority 固化 Phase 5（jwt 模型）：
// RedisRateLimiter.redisClient() 注入优先、全局兜底。
func TestRedisRateLimiterInjectedClientPriority(t *testing.T) {
	mrG := miniredis.RunT(t)
	clientG := redis.NewClient(&redis.Options{Addr: mrG.Addr()})
	t.Cleanup(func() { _ = clientG.Close() })
	origGlobal := database.SetTestRedisClient(clientG)
	t.Cleanup(func() { database.SetTestRedisClient(origGlobal) })

	mrA := miniredis.RunT(t)
	clientA := redis.NewClient(&redis.Options{Addr: mrA.Addr()})
	t.Cleanup(func() { _ = clientA.Close() })

	// 注入优先
	rl := NewRedisRateLimiter("test", 10, time.Minute, WithRedisClient(clientA))
	if got := rl.redisClient(); got != clientA {
		t.Fatalf("injected redisClient() = %v, want clientA", got)
	}

	// 未注入 -> 全局兜底
	rl2 := NewRedisRateLimiter("test", 10, time.Minute)
	if got := rl2.redisClient(); got != clientG {
		t.Fatalf("fallback redisClient() = %v, want clientG (global)", got)
	}
}

// TestSwapDefaultRateLimitRegistryReturnsOld 固化 Phase 5：Swap 返回被替换的旧 Registry。
func TestSwapDefaultRateLimitRegistryReturnsOld(t *testing.T) {
	orig := defaultRateLimitRegistry.Load()
	defer SwapDefaultRateLimitRegistry(orig)

	first := NewRateLimitRegistry()
	if prev := SwapDefaultRateLimitRegistry(first); prev != orig {
		t.Fatalf("Swap returned %v, want orig", prev)
	}
	second := NewRateLimitRegistry()
	if returned := SwapDefaultRateLimitRegistry(second); returned != first {
		t.Fatalf("Swap returned %v, want first", returned)
	}
	if defaultRateLimitRegistry.Load() != second {
		t.Fatal("Swap did not install second as default")
	}
	if got := SwapDefaultRateLimitRegistry(nil); got != second {
		t.Fatalf("Swap(nil) = %v, want second (current default)", got)
	}
}

// TestRateLimitRegistryStopIdempotent 固化 Phase 5：Stop 幂等，重复调用不 panic。
func TestRateLimitRegistryStopIdempotent(t *testing.T) {
	r := NewRateLimitRegistry()
	_ = r.loginLimiterInstance()
	_ = r.apiLimiterInstance()
	_ = r.uploadLimiterInstance()
	r.registerCustom(NewRateLimiter(5, time.Minute))
	r.Stop()
	r.Stop() // 幂等
}

// TestRateLimitRegistryIsolation 固化 Phase 5：两个 Registry 实例的 limiter 互相独立。
func TestRateLimitRegistryIsolation(t *testing.T) {
	rA := NewRateLimitRegistry()
	rB := NewRateLimitRegistry()
	defer rA.Stop()
	defer rB.Stop()

	la := rA.loginLimiterInstance()
	lb := rB.loginLimiterInstance()
	if la == lb {
		t.Fatal("two registries returned the same loginLimiter instance (not isolated)")
	}

	// rA 用尽 10 次；rB 不受影响
	for i := 0; i < 10; i++ {
		if !la.Allow("1.1.1.1") {
			t.Fatalf("rA request %d should be allowed", i+1)
		}
	}
	// rA 第 11 次应被拒绝（count=10 >= rate=10）
	if la.Allow("1.1.1.1") {
		t.Fatal("rA 11th should be rejected (count reached limit)")
	}
	// rB 的 loginLimiter 独立计数，1.1.1.1 仍可放行
	if !lb.Allow("1.1.1.1") {
		t.Fatal("rB loginLimiter should be independent of rA (still allowed)")
	}}
