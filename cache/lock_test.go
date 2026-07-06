package cache_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/EthanCodeCraft/xlgo-core/cache"
)

func TestLockToken(t *testing.T) {
	token := &cache.LockToken{
		Key:   "test_lock",
		Token: "abc123",
	}

	if token.Key != "test_lock" {
		t.Error("LockToken Key failed")
	}
	if token.Token != "abc123" {
		t.Error("LockToken Token failed")
	}
}

func TestLockErrors(t *testing.T) {
	ctx := context.Background()

	// Test ErrLockNotHeld - 当 Redis 未初始化时，先检查 nil token
	err := cache.Unlock(ctx, nil)
	// 当 Redis 未初始化时，会返回 ErrRedisNotReady
	if err != cache.ErrLockNotHeld && err != cache.ErrRedisNotReady {
		t.Errorf("Unlock with nil token should return ErrLockNotHeld or ErrRedisNotReady, got %v", err)
	}

	// Test IsLocked without Redis — M-E：应返回 (false, ErrRedisNotReady)，
	// 让调用方可区分"Redis 不可用"与"锁未占用"（原 (false,nil) 与"未占用"不可区分）。
	locked, err := cache.IsLocked(ctx, "test_key")
	if !errors.Is(err, cache.ErrRedisNotReady) {
		t.Errorf("IsLocked without Redis should return ErrRedisNotReady, got %v", err)
	}
	if locked {
		t.Error("IsLocked should return false without Redis")
	}

	// Test GetLockTTL without Redis — M-E：应返回 (0, ErrRedisNotReady)。
	ttl, err := cache.GetLockTTL(ctx, "test_key")
	if !errors.Is(err, cache.ErrRedisNotReady) {
		t.Errorf("GetLockTTL without Redis should return ErrRedisNotReady, got %v", err)
	}
	if ttl != 0 {
		t.Error("GetLockTTL should return 0 without Redis")
	}

	if err := cache.UnlockByKey(ctx, "test_key"); !errors.Is(err, cache.ErrRedisNotReady) {
		t.Errorf("UnlockByKey without Redis should return ErrRedisNotReady, got %v", err)
	}
}

func TestIncrDecr(t *testing.T) {
	ctx := context.Background()

	// Test Incr without Redis
	n, err := cache.Incr(ctx, "counter")
	if err != nil {
		t.Errorf("Incr error: %v", err)
	}
	if n != 0 {
		t.Error("Incr should return 0 without Redis")
	}

	// Test IncrBy
	n, err = cache.IncrBy(ctx, "counter", 10)
	if err != nil {
		t.Errorf("IncrBy error: %v", err)
	}
	if n != 0 {
		t.Error("IncrBy should return 0 without Redis")
	}

	// Test Decr
	n, err = cache.Decr(ctx, "counter")
	if err != nil {
		t.Errorf("Decr error: %v", err)
	}
	if n != 0 {
		t.Error("Decr should return 0 without Redis")
	}
}

func TestSetExpire(t *testing.T) {
	ctx := context.Background()

	ok, err := cache.SetExpire(ctx, "test_key", time.Minute)
	if err != nil {
		t.Errorf("SetExpire error: %v", err)
	}
	if ok {
		t.Error("SetExpire should return false without Redis")
	}
}

func TestGetRawSetRaw(t *testing.T) {
	ctx := context.Background()

	// Test SetRaw
	err := cache.SetRaw(ctx, "test_key", "test_value", time.Minute)
	if err != nil {
		t.Errorf("SetRaw error: %v", err)
	}

	// Test GetRaw
	val, err := cache.GetRaw(ctx, "test_key")
	if err != nil {
		t.Errorf("GetRaw error: %v", err)
	}
	if val != "" {
		t.Error("GetRaw should return empty string without Redis")
	}
}

func TestKFunctions(t *testing.T) {
	// Test various K functions
	key := cache.K("user:1")
	if key == "" {
		t.Error("K should not return empty string")
	}

	tempKey := cache.KTemp("token")
	if tempKey == "" {
		t.Error("KTemp should not return empty string")
	}

	permKey := cache.KPerm("config")
	if permKey == "" {
		t.Error("KPerm should not return empty string")
	}

	lockKey := cache.KLock("order:123")
	if lockKey == "" {
		t.Error("KLock should not return empty string")
	}

	counterKey := cache.KCounter("visit")
	if counterKey == "" {
		t.Error("KCounter should not return empty string")
	}

	sessionKey := cache.KSession("sid")
	if sessionKey == "" {
		t.Error("KSession should not return empty string")
	}
}
