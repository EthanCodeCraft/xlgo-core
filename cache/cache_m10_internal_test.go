package cache

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/EthanCodeCraft/xlgo-core/database"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func setupM10MiniRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	database.SetTestRedisClient(client)
	t.Cleanup(func() { database.SetTestRedisClient(nil) })
	return mr
}

func TestM10CacheWritesReturnRedisNotReady(t *testing.T) {
	database.SetTestRedisClient(nil)
	t.Cleanup(func() { database.SetTestRedisClient(nil) })

	c := &redisCache{}
	ctx := context.Background()

	if err := c.Set(ctx, "k", "v", time.Minute); !errors.Is(err, ErrRedisNotReady) {
		t.Fatalf("Set without Redis err = %v, want ErrRedisNotReady", err)
	}
	if err := c.Delete(ctx, "k"); !errors.Is(err, ErrRedisNotReady) {
		t.Fatalf("Delete without Redis err = %v, want ErrRedisNotReady", err)
	}
	if err := c.DeleteByPattern(ctx, "k:*"); !errors.Is(err, ErrRedisNotReady) {
		t.Fatalf("DeleteByPattern without Redis err = %v, want ErrRedisNotReady", err)
	}
}

func TestM10RawHelpersReturnRedisNotReady(t *testing.T) {
	database.SetTestRedisClient(nil)
	t.Cleanup(func() { database.SetTestRedisClient(nil) })
	ctx := context.Background()

	if _, err := Incr(ctx, "k"); !errors.Is(err, ErrRedisNotReady) {
		t.Fatalf("Incr without Redis err = %v, want ErrRedisNotReady", err)
	}
	if _, err := IncrBy(ctx, "k", 2); !errors.Is(err, ErrRedisNotReady) {
		t.Fatalf("IncrBy without Redis err = %v, want ErrRedisNotReady", err)
	}
	if _, err := Decr(ctx, "k"); !errors.Is(err, ErrRedisNotReady) {
		t.Fatalf("Decr without Redis err = %v, want ErrRedisNotReady", err)
	}
	if _, err := GetTTL(ctx, "k"); !errors.Is(err, ErrRedisNotReady) {
		t.Fatalf("GetTTL without Redis err = %v, want ErrRedisNotReady", err)
	}
	if _, err := SetExpire(ctx, "k", time.Minute); !errors.Is(err, ErrRedisNotReady) {
		t.Fatalf("SetExpire without Redis err = %v, want ErrRedisNotReady", err)
	}
	if _, err := GetRaw(ctx, "k"); !errors.Is(err, ErrRedisNotReady) {
		t.Fatalf("GetRaw without Redis err = %v, want ErrRedisNotReady", err)
	}
	if err := SetRaw(ctx, "k", "v", time.Minute); !errors.Is(err, ErrRedisNotReady) {
		t.Fatalf("SetRaw without Redis err = %v, want ErrRedisNotReady", err)
	}
}

func TestM10ExistsEReturnsBackendErrors(t *testing.T) {
	setupM10MiniRedis(t)
	c := &redisCache{}
	ctx := context.Background()

	exists, err := c.ExistsE(ctx, "missing")
	if err != nil {
		t.Fatalf("ExistsE missing err = %v", err)
	}
	if exists {
		t.Fatal("ExistsE missing = true, want false")
	}

	if err := c.Set(ctx, "present", "value", time.Minute); err != nil {
		t.Fatalf("Set present: %v", err)
	}
	exists, err = c.ExistsE(ctx, "present")
	if err != nil || !exists {
		t.Fatalf("ExistsE present = %v, err=%v; want true,nil", exists, err)
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if exists, err = c.ExistsE(canceled, "present"); err == nil || exists {
		t.Fatalf("ExistsE canceled = %v, err=%v; want false,error", exists, err)
	}
}

func TestM10PackageExistsEUsesOptionalInterface(t *testing.T) {
	setupM10MiniRedis(t)
	orig := GetDefaultCache()
	t.Cleanup(func() { SetDefaultCacheManager(orig) })
	SetDefaultCacheManager(NewCacheManager())
	Init()

	ctx := context.Background()
	if err := GetCache().Set(ctx, "present", "value", time.Minute); err != nil {
		t.Fatalf("Set through facade: %v", err)
	}
	exists, err := ExistsE(ctx, "present")
	if err != nil || !exists {
		t.Fatalf("facade ExistsE = %v, err=%v; want true,nil", exists, err)
	}
}
