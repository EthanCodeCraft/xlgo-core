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

func TestM10ExistsReturnsBackendErrors(t *testing.T) {
	setupM10MiniRedis(t)
	c := &redisCache{}
	ctx := context.Background()

	exists, err := c.Exists(ctx, "missing")
	if err != nil {
		t.Fatalf("Exists missing err = %v", err)
	}
	if exists {
		t.Fatal("Exists missing = true, want false")
	}

	if err := c.Set(ctx, "present", "value", time.Minute); err != nil {
		t.Fatalf("Set present: %v", err)
	}
	exists, err = c.Exists(ctx, "present")
	if err != nil || !exists {
		t.Fatalf("Exists present = %v, err=%v; want true,nil", exists, err)
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if exists, err = c.Exists(canceled, "present"); err == nil || exists {
		t.Fatalf("Exists canceled = %v, err=%v; want false,error", exists, err)
	}
}

func TestM10GetReturnsBackendAndDecodeErrors(t *testing.T) {
	setupM10MiniRedis(t)
	c := &redisCache{}
	ctx := context.Background()

	var got string
	ok, err := c.Get(ctx, "missing", &got)
	if err != nil || ok {
		t.Fatalf("Get missing = %v, err=%v; want false,nil", ok, err)
	}

	if err := c.Set(ctx, "present", "value", time.Minute); err != nil {
		t.Fatalf("Set present: %v", err)
	}
	ok, err = c.Get(ctx, "present", &got)
	if err != nil || !ok || got != "value" {
		t.Fatalf("Get present = %v, %q, err=%v; want true,value,nil", ok, got, err)
	}

	if err := c.client().Set(ctx, "bad-json", "{", time.Minute).Err(); err != nil {
		t.Fatalf("Set bad-json: %v", err)
	}
	ok, err = c.Get(ctx, "bad-json", &got)
	if err == nil || ok {
		t.Fatalf("Get bad-json = %v, err=%v; want false,error", ok, err)
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	ok, err = c.Get(canceled, "present", &got)
	if err == nil || ok {
		t.Fatalf("Get canceled = %v, err=%v; want false,error", ok, err)
	}
}

func TestM10PackageExistsReturnsBackendErrors(t *testing.T) {
	setupM10MiniRedis(t)
	orig := GetDefaultCache()
	t.Cleanup(func() { SetDefaultCacheManager(orig) })
	SetDefaultCacheManager(NewCacheManager())
	Init()

	ctx := context.Background()
	if err := GetCache().Set(ctx, "present", "value", time.Minute); err != nil {
		t.Fatalf("Set through facade: %v", err)
	}
	exists, err := Exists(ctx, "present")
	if err != nil || !exists {
		t.Fatalf("facade Exists = %v, err=%v; want true,nil", exists, err)
	}
}

func TestM10PackageGetReturnsBackendErrors(t *testing.T) {
	setupM10MiniRedis(t)
	orig := GetDefaultCache()
	t.Cleanup(func() { SetDefaultCacheManager(orig) })
	SetDefaultCacheManager(NewCacheManager())
	Init()

	ctx := context.Background()
	if err := GetCache().Set(ctx, "present", "value", time.Minute); err != nil {
		t.Fatalf("Set through facade: %v", err)
	}
	var got string
	ok, err := Get(ctx, "present", &got)
	if err != nil || !ok || got != "value" {
		t.Fatalf("facade Get = %v, %q, err=%v; want true,value,nil", ok, got, err)
	}
}

// TestM10CustomCacheServiceCompile asserts a user-provided CacheService
// implementation compiles against the (bool, error) contract. This is a
// compile-time guard: if the interface changes again, this stops building.
type customCacheSvc struct{}

func (customCacheSvc) Get(ctx context.Context, key string, dest any) (bool, error) { return false, nil }
func (customCacheSvc) Set(ctx context.Context, key string, value any, ttl time.Duration) error {
	return nil
}
func (customCacheSvc) Delete(ctx context.Context, key string) error  { return nil }
func (customCacheSvc) DeleteByPattern(ctx context.Context, p string) error { return nil }
func (customCacheSvc) Exists(ctx context.Context, key string) (bool, error) { return false, nil }

func TestM10CustomCacheServiceCompiles(t *testing.T) {
	var _ CacheService = customCacheSvc{}
}

// TestM10RedisNotReadyOnGetExists asserts the Redis-not-ready error is
// surfaced (not swallowed) when no backend is configured.
func TestM10RedisNotReadyOnGetExists(t *testing.T) {
	database.SetTestRedisClient(nil)
	t.Cleanup(func() { database.SetTestRedisClient(nil) })
	c := &redisCache{}
	ctx := context.Background()

	if _, err := c.Get(ctx, "k", new(string)); !errors.Is(err, ErrRedisNotReady) {
		t.Fatalf("Get without Redis err = %v, want ErrRedisNotReady", err)
	}
	if _, err := c.Exists(ctx, "k"); !errors.Is(err, ErrRedisNotReady) {
		t.Fatalf("Exists without Redis err = %v, want ErrRedisNotReady", err)
	}
}
