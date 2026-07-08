package database_test

import (
	"context"
	"sync"
	"testing"

	"github.com/EthanCodeCraft/xlgo-core/database"
)

func TestCloseRedisWithoutInit(t *testing.T) {
	if err := database.CloseRedis(); err != nil {
		t.Fatalf("CloseRedis without init should not error: %v", err)
	}
	if database.GetRedis() != nil {
		t.Fatal("expected Redis client nil")
	}
}

func TestHealthCheckRedisWithoutInit(t *testing.T) {
	if err := database.HealthCheckRedis(context.Background()); err == nil {
		t.Fatal("expected health check error without Redis init")
	}
}

func TestRedisNilConfigReturnsError(t *testing.T) {
	prev := database.SetTestRedisClient(nil)
	t.Cleanup(func() { database.SetTestRedisClient(prev) })

	if err := database.NewRedisManager().Init(nil); err == nil {
		t.Fatal("RedisManager.Init(nil) 应返回错误")
	}
	if err := database.InitRedis(nil); err == nil {
		t.Fatal("包级 InitRedis(nil) 应返回错误")
	}
	if err := database.HealthCheckRedis(nil); err == nil {
		t.Fatal("未初始化 Redis 时 HealthCheckRedis(nil) 应返回错误")
	}
}

// TestDefaultRedisConcurrentSetAndGet C-1/H-4 回归：并发 SetDefaultRedisManager
// 与 GetRedis/CloseRedis/HealthCheckRedis facade 读取不应触发数据竞争。
// 修复前 DefaultRedis 是裸 *Redis.Pointer，SetDefaultRedisManager 无锁写、
// facade 无锁读，-race 必采。修复后 atomic.Pointer 保护。
func TestDefaultRedisConcurrentSetAndGet(t *testing.T) {
	// 保存并恢复默认 manager，避免污染其他测试。
	orig := database.NewRedisManager()
	database.SetDefaultRedisManager(orig)
	// 不设 client，GetRedis 返回 nil；本用例只验证读写无竞态。

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			database.SetDefaultRedisManager(database.NewRedisManager())
		}()
		go func() {
			defer wg.Done()
			_ = database.GetRedis()
			_ = database.CloseRedis()
			_ = database.HealthCheckRedis(context.Background())
		}()
	}
	wg.Wait()
}
