package jwt

import (
	"sync"
	"testing"
	"time"

	"github.com/EthanCodeCraft/xlgo-core/config"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// c9cSetupConfig 注入测试用 JWT 配置（内部测试无法访问 jwt_test 的 setupTestConfig）。
func c9cSetupConfig(t *testing.T) {
	t.Helper()
	config.Set(&config.Config{
		JWT: config.JWTConfig{
			Secret: "test-secret-key-1234567890123456789012", // ≥32 字节
			Expire: time.Hour,
		},
	})
}

// TestC9cConcurrentSetDefaultAndRead 验证 SetDefaultJWTManager（写）与包级函数
// （读 currentBlacklist → currentManager → defaultManager.Load）并发无数据竞争（C9c）。
// 修复前 tokenBlacklist 为裸包级指针，SetDefaultJWTManager 裸写、ParseToken/IsTokenRevoked/
// InvalidateTokenByID 裸读，-race 必采到指针读写竞争。
func TestC9cConcurrentSetDefaultAndRead(t *testing.T) {
	c9cSetupConfig(t)
	t.Cleanup(func() { SetDefaultJWTManager(NewJWTManager()) }) // 还原无 Redis 基线

	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	// 预生成一个合法 token 供读者 ParseToken（JTI 随机，不会被下方 InvalidateTokenByID 的固定 jti 命中）
	token, err := GenerateToken(1, "u", "admin", "super_admin")
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// 写者：在"注入 Redis"与"无 Redis"Manager 间原子置换（模拟启动期/测试期 SetDefaultJWTManager）
	wg.Add(1)
	go func() {
		defer wg.Done()
		withRedis := NewJWTManagerWithRedis(client)
		withoutRedis := NewJWTManager()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if i%2 == 0 {
				SetDefaultJWTManager(withRedis)
			} else {
				SetDefaultJWTManager(withoutRedis)
			}
		}
	}()

	// 读者：并发调用经 currentBlacklist() 的包级函数 + currentManager
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_, _ = ParseToken(token)
				_ = IsTokenRevoked("some-jti")
				_ = InvalidateTokenByID("some-jti", time.Now().Add(time.Hour))
				_ = currentManager()
			}
		}()
	}

	// 让竞争窗口运行一段时间，确保 -race 能采到（若有未加锁读）
	time.Sleep(50 * time.Millisecond)
	close(stop)
	wg.Wait()
}

// TestC9cCurrentManagerReflectsSwap 验证 SetDefaultJWTManager 原子置换后，
// 包级函数经 currentManager 读到的是新 Manager 的 blacklist（C9c 一致性）。
func TestC9cCurrentManagerReflectsSwap(t *testing.T) {
	c9cSetupConfig(t)
	t.Cleanup(func() { SetDefaultJWTManager(NewJWTManager()) })

	// 基线：无 Redis，InvalidateTokenByID 返 ErrBlacklistUnavailable
	if err := InvalidateTokenByID("jti-1", time.Now().Add(time.Hour)); !errorIsBlacklistUnavailable(err) {
		t.Fatalf("baseline without Redis: want ErrBlacklistUnavailable, got %v", err)
	}

	// 置换为注入 Redis 的 Manager
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	SetDefaultJWTManager(NewJWTManagerWithRedis(client))

	// 置换后包级函数应读到新 blacklist（Redis 可用），InvalidateTokenByID 成功
	if err := InvalidateTokenByID("jti-2", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("after swap to Redis Manager: InvalidateTokenByID err = %v, want nil", err)
	}

	// currentManager 应等于刚 Store 的 Manager
	m := currentManager()
	if m == nil {
		t.Fatal("currentManager should not be nil")
	}
}

// TestC9cDefaultJWTAliasConsistent 验证 GetDefaultJWT 与 defaultManager 真实存储一致。
func TestC9cDefaultJWTAliasConsistent(t *testing.T) {
	t.Cleanup(func() { SetDefaultJWTManager(NewJWTManager()) })

	// init 后 GetDefaultJWT 与 currentManager 应指向同一实例
	if GetDefaultJWT() != currentManager() {
		t.Fatal("GetDefaultJWT should equal currentManager after init")
	}

	// SetDefaultJWTManager 后同步
	m := NewJWTManagerWithRedis(nil)
	SetDefaultJWTManager(m)
	if GetDefaultJWT() != m {
		t.Fatal("GetDefaultJWT should sync after SetDefaultJWTManager")
	}
	if currentManager() != m {
		t.Fatal("currentManager should reflect SetDefaultJWTManager")
	}
}

// errorIsBlacklistUnavailable 避免内部测试依赖 errors.Is 包装判断。
func errorIsBlacklistUnavailable(err error) bool {
	return err != nil && err.Error() == ErrBlacklistUnavailable.Error()
}
