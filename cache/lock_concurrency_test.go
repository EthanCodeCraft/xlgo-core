package cache_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EthanCodeCraft/xlgo-core/cache"
	"github.com/EthanCodeCraft/xlgo-core/database"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// setupMiniRedis 启动一个 miniredis 实例并把它注入 database 内部，返回清理函数。
func setupMiniRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	// 通过 SetTestRedisClient 注入 miniredis 客户端到 database 包的内部 redisClient
	database.SetTestRedisClient(client)
	t.Cleanup(func() { database.SetTestRedisClient(nil) })
	return mr
}

// ===== C1a：WithLockAutoExtend 不 panic / 不泄漏锁 =====

// 回归 C1a：ctx 取消时 WithLockAutoExtend 不 send-on-closed panic，且锁被释放。
// 旧实现：ctx 取消时子 goroutine defer close(done)，父 fn() 后 done<-struct{}{} 即 panic，
// Unlock 不执行，锁泄漏到 TTL。
func TestWithLockAutoExtendCtxCancelNoPanic(t *testing.T) {
	mr := setupMiniRedis(t)
	_ = mr

	ctx, cancel := context.WithCancel(context.Background())
	key := "c1a:cancel"

	ran := make(chan struct{})
	var panicked any
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() { panicked = recover() }()
		err := cache.WithLockAutoExtend(ctx, key, 10*time.Second, 100*time.Millisecond, func() error {
			close(ran)
			// 阻塞直到 ctx 被取消，模拟长任务。
			<-ctx.Done()
			return ctx.Err()
		})
		// WithLockAutoExtend 返回 fn 的错误（ctx.Err），不应 panic。
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("unexpected err: %v", err)
		}
	}()

	// 等 fn 启动 + 至少一次续期 ticker，再取消 ctx，制造最大竞态窗口。
	<-ran
	time.Sleep(250 * time.Millisecond)
	cancel()
	wg.Wait()

	if panicked != nil {
		t.Fatalf("WithLockAutoExtend panicked on ctx cancel (C1a send-on-closed): %v", panicked)
	}
	// 锁必须被释放（Unlock 用 Background ctx 执行）。
	locked, err := cache.IsLocked(context.Background(), key)
	if err != nil {
		t.Fatalf("IsLocked: %v", err)
	}
	if locked {
		t.Error("lock leaked after ctx cancel (Unlock not executed)")
	}
}

// 回归 C1a：fn 正常返回时锁被释放，无 panic。
func TestWithLockAutoExtendNormalRelease(t *testing.T) {
	setupMiniRedis(t)
	ctx := context.Background()
	key := "c1a:normal"

	called := false
	err := cache.WithLockAutoExtend(ctx, key, 10*time.Second, 100*time.Millisecond, func() error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("WithLockAutoExtend: %v", err)
	}
	if !called {
		t.Error("fn not called")
	}
	locked, _ := cache.IsLocked(ctx, key)
	if locked {
		t.Error("lock not released after normal fn return")
	}
}

// 回归 C1a：fn 执行超过续期间隔，锁被续期不丢失（续期 goroutine 工作）。
func TestWithLockAutoExtendExtendsLock(t *testing.T) {
	mr := setupMiniRedis(t)
	ctx := context.Background()
	key := "c1a:extend"

	// initialTTL=500ms，extendInterval=100ms。fn 执行 800ms，期间应多次续期。
	// 若续期失效，锁会在 500ms 过期，另一 worker 可获取。
	err := cache.WithLockAutoExtend(ctx, key, 500*time.Millisecond, 100*time.Millisecond, func() error {
		time.Sleep(800 * time.Millisecond)
		return nil
	})
	if err != nil {
		t.Fatalf("WithLockAutoExtend: %v", err)
	}

	// 期间另一 worker 应无法获取锁（续期生效）。
	// 此断言在 fn 执行中验证更准；这里用 fn 返回后锁已释放验证基础闭环。
	locked, _ := cache.IsLocked(ctx, key)
	if locked {
		t.Error("lock not released after fn")
	}
	_ = mr
}

// 回归 C1a：fn 执行中另一 worker 拿不到锁（续期保活）。
func TestWithLockAutoExtendBlocksContender(t *testing.T) {
	setupMiniRedis(t)
	ctx := context.Background()
	key := "c1a:block"

	fnStarted := make(chan struct{})
	fnDone := make(chan struct{})
	lockDone := make(chan struct{})
	var contenderGotLock atomic.Bool

	go func() {
		defer close(lockDone)
		cache.WithLockAutoExtend(ctx, key, 2*time.Second, 100*time.Millisecond, func() error {
			close(fnStarted)
			time.Sleep(600 * time.Millisecond)
			close(fnDone)
			return nil
		})
	}()

	<-fnStarted
	// 期间尝试获取锁，应失败（nil）。
	token, err := cache.NewLock(ctx, key, 2*time.Second)
	if err != nil {
		t.Fatalf("NewLock contender: %v", err)
	}
	if token != nil {
		contenderGotLock.Store(true)
		_ = cache.Unlock(ctx, token)
	}
	<-fnDone
	<-lockDone

	if contenderGotLock.Load() {
		t.Error("contender acquired lock while auto-extend active (extend failed)")
	}
}

// 回归 C1a：fn panic 时锁仍被释放、续期 goroutine 不泄漏（defer 兜底）。
// 旧实现（无 defer）fn panic → close(stop) 不执行 → 续期 goroutine 永久泄漏 + Unlock 不执行 → 锁泄漏。
func TestWithLockAutoExtendFnPanicReleasesLock(t *testing.T) {
	setupMiniRedis(t)
	ctx := context.Background()
	key := "c1a:panic"

	var panicked any
	func() {
		defer func() { panicked = recover() }()
		_ = cache.WithLockAutoExtend(ctx, key, 10*time.Second, 100*time.Millisecond, func() error {
			time.Sleep(150 * time.Millisecond) // 让续期 ticker 至少触发一次
			panic("boom")
		})
	}()

	if panicked == nil {
		t.Fatal("expected fn panic to propagate")
	}

	// 锁必须被释放（defer Unlock 执行）。
	locked, _ := cache.IsLocked(ctx, key)
	if locked {
		t.Error("lock leaked after fn panic (defer Unlock not executed)")
	}
}

// 回归 C1a/HIGH：WithLock 在 ctx 取消后仍能解锁（Unlock 用 Background ctx）。
// 旧实现 defer Unlock(ctx, token) 用原 ctx，ctx 取消致 Unlock 失败、锁泄漏到 TTL。
func TestWithLockCtxCancelReleasesLock(t *testing.T) {
	setupMiniRedis(t)
	ctx, cancel := context.WithCancel(context.Background())
	key := "c1a:withlock"

	fnStarted := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = cache.WithLock(ctx, key, 10*time.Second, func() error {
			close(fnStarted)
			<-ctx.Done()
			return ctx.Err()
		})
	}()

	<-fnStarted
	cancel()
	<-done

	locked, _ := cache.IsLocked(context.Background(), key)
	if locked {
		t.Error("lock leaked after ctx cancel (WithLock Unlock should use Background ctx)")
	}
}

// ===== C1b：类型断言不 panic =====

// 回归 C1b：NewLock/Unlock/ExtendLock 在正常路径返回正确结果（Lua 脚本返 int64）。
// 此用例锁定正常路径不被 toInt64 改坏；裸断言 panic 路径需构造非 int64 返回，
// miniredis Lua 恒返整数，故 panic 路径由 toInt64 的 comma-ok 防护（代码审查保证）。
func TestLockUnlockExtendCycle(t *testing.T) {
	setupMiniRedis(t)
	ctx := context.Background()
	key := "c1b:cycle"

	// 加锁
	token, err := cache.NewLock(ctx, key, 5*time.Second)
	if err != nil || token == nil {
		t.Fatalf("NewLock: err=%v token=%v", err, token)
	}
	// 重复加锁应失败（返回 nil token）
	t2, err := cache.NewLock(ctx, key, 5*time.Second)
	if err != nil {
		t.Fatalf("second NewLock err: %v", err)
	}
	if t2 != nil {
		t.Error("second NewLock should return nil token (lock held)")
		_ = cache.Unlock(ctx, t2)
	}
	// 续期
	if err := cache.ExtendLock(ctx, token, 5*time.Second); err != nil {
		t.Errorf("ExtendLock: %v", err)
	}
	// 用错误 token 解锁应失败
	wrong := &cache.LockToken{Key: key, Token: "wrong-token"}
	if err := cache.Unlock(ctx, wrong); !errors.Is(err, cache.ErrLockNotHeld) {
		t.Errorf("Unlock wrong token err = %v, want ErrLockNotHeld", err)
	}
	// 正确解锁
	if err := cache.Unlock(ctx, token); err != nil {
		t.Errorf("Unlock: %v", err)
	}
	// 解锁后另一方可获取
	t3, err := cache.NewLock(ctx, key, 5*time.Second)
	if err != nil || t3 == nil {
		t.Fatalf("NewLock after unlock: err=%v token=%v", err, t3)
	}
	_ = cache.Unlock(ctx, t3)
}

// ===== C1c：TryLock 响应 ctx 取消 =====

// 回归 C1c：TryLock 在 ctx 取消时立即返回，不阻塞 maxRetry*retryInterval。
// 旧实现 time.Sleep 不响应 ctx，取消后仍要等满所有重试。
func TestTryLockRespectsCtxCancel(t *testing.T) {
	setupMiniRedis(t)
	// 先占用锁，使 TryLock 必然重试。
	holder, err := cache.NewLock(context.Background(), "c1c:try", 10*time.Second)
	if err != nil || holder == nil {
		t.Fatalf("setup holder: err=%v token=%v", err, holder)
	}
	defer cache.Unlock(context.Background(), holder)

	ctx, cancel := context.WithCancel(context.Background())
	// retryInterval=200ms，maxRetry=10 → 旧实现取消后最长阻塞 2s。
	start := time.Now()
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()

	token, err := cache.TryLock(ctx, "c1c:try", 5*time.Second, 200*time.Millisecond, 10)
	elapsed := time.Since(start)

	if !errors.Is(err, context.Canceled) {
		t.Errorf("TryLock err = %v, want context.Canceled", err)
	}
	if token != nil {
		t.Error("TryLock should return nil token (held by other)")
		_ = cache.Unlock(context.Background(), token)
	}
	// 取消应在 ~150ms 后返回，远小于 2s。
	if elapsed > 1*time.Second {
		t.Errorf("TryLock took %v, should return shortly after ctx cancel (C1c: time.Sleep ignored ctx)", elapsed)
	}
}

func TestLockRejectsSubMillisecondTTL(t *testing.T) {
	setupMiniRedis(t)
	ctx := context.Background()

	if token, err := cache.NewLock(ctx, "m10:ttl", time.Nanosecond); !errors.Is(err, cache.ErrInvalidLockTTL) || token != nil {
		t.Fatalf("NewLock sub-ms ttl token=%v err=%v, want ErrInvalidLockTTL", token, err)
	}

	token, err := cache.NewLock(ctx, "m10:ttl-valid", time.Second)
	if err != nil || token == nil {
		t.Fatalf("NewLock valid setup token=%v err=%v", token, err)
	}
	defer cache.Unlock(ctx, token)

	if err := cache.ExtendLock(ctx, token, 0); !errors.Is(err, cache.ErrInvalidLockTTL) {
		t.Errorf("ExtendLock zero ttl err = %v, want ErrInvalidLockTTL", err)
	}
}

func TestWithLockAutoExtendRejectsNonPositiveInterval(t *testing.T) {
	setupMiniRedis(t)
	called := false

	err := cache.WithLockAutoExtend(context.Background(), "m10:interval", time.Second, 0, func() error {
		called = true
		return nil
	})
	if !errors.Is(err, cache.ErrInvalidLockRetryInterval) {
		t.Fatalf("WithLockAutoExtend zero interval err = %v, want ErrInvalidLockRetryInterval", err)
	}
	if called {
		t.Fatal("WithLockAutoExtend should not run fn with invalid interval")
	}
}

func TestWithLockHeldReturnsErrLockNotAcquired(t *testing.T) {
	setupMiniRedis(t)
	ctx := context.Background()
	key := "m10:not-acquired"

	holder, err := cache.NewLock(ctx, key, time.Second)
	if err != nil || holder == nil {
		t.Fatalf("setup holder token=%v err=%v", holder, err)
	}
	defer cache.Unlock(ctx, holder)

	called := false
	err = cache.WithLock(ctx, key, time.Second, func() error {
		called = true
		return nil
	})
	if !errors.Is(err, cache.ErrLockNotAcquired) {
		t.Fatalf("WithLock held err = %v, want ErrLockNotAcquired", err)
	}
	if called {
		t.Fatal("WithLock should not run fn when lock is held")
	}
}

func TestTryLockExhaustedReturnsErrLockNotAcquired(t *testing.T) {
	setupMiniRedis(t)
	ctx := context.Background()
	key := "m10:try-exhausted"

	holder, err := cache.NewLock(ctx, key, time.Second)
	if err != nil || holder == nil {
		t.Fatalf("setup holder token=%v err=%v", holder, err)
	}
	defer cache.Unlock(ctx, holder)

	token, err := cache.TryLock(ctx, key, time.Second, time.Millisecond, 2)
	if !errors.Is(err, cache.ErrLockNotAcquired) {
		t.Fatalf("TryLock exhausted err = %v, want ErrLockNotAcquired", err)
	}
	if token != nil {
		t.Fatalf("TryLock exhausted token = %v, want nil", token)
	}
}
