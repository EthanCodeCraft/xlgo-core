package xlgo_test

import (
	"context"
	"errors"
	"net"
	"strconv"
	"testing"
	"time"

	xlgo "github.com/EthanCodeCraft/xlgo-core"
	"github.com/EthanCodeCraft/xlgo-core/cache"
	"github.com/EthanCodeCraft/xlgo-core/config"
	"github.com/alicebob/miniredis/v2"
)

func splitAddr(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort %q: %v", addr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("Atoi port %q: %v", portStr, err)
	}
	if host == "" {
		host = "127.0.0.1"
	}
	return host, port
}

// TestAppCacheMultiAppRedisIsolation 固化 Phase 4 核心契约：单进程多 App 时，每个 App 的
// 缓存走自己的 redisManager.Client()，互不串越。修复前 cache 硬编码 database.GetRedis()
// （全局），App B swap 后 App A 的缓存会读到 App B 的 redis。
//
// 用两个 miniredis 实例分别作 App A/B 的 Redis 后端，断言各自 cache 只写自己的实例、
// 互读对方的 key 为 miss。appA.Cache()/appB.Cache() 绕过全局 facade 直取 App 实例。
func TestAppCacheMultiAppRedisIsolation(t *testing.T) {
	mrA := miniredis.RunT(t)
	mrB := miniredis.RunT(t)
	hostA, portA := splitAddr(t, mrA.Addr())
	hostB, portB := splitAddr(t, mrB.Addr())

	cfgA := testConfig(18120)
	cfgA.Redis = config.RedisConfig{Host: hostA, Port: portA}
	cfgB := testConfig(18121)
	cfgB.Redis = config.RedisConfig{Host: hostB, Port: portB}

	appA := xlgo.New(xlgo.WithConfig(cfgA), xlgo.WithRedis())
	if err := appA.Init(); err != nil {
		t.Fatalf("appA Init: %v", err)
	}
	defer appA.Shutdown()
	appB := xlgo.New(xlgo.WithConfig(cfgB), xlgo.WithRedis())
	if err := appB.Init(); err != nil {
		t.Fatalf("appB Init: %v", err)
	}
	defer appB.Shutdown()

	if appA.Cache() == nil || appB.Cache() == nil {
		t.Fatal("app.Cache() = nil after Init with WithRedis")
	}

	ctx := context.Background()
	// A 写 keyA -> 仅落 miniredisA
	if err := appA.Cache().Set(ctx, "keyA", "valA", time.Minute); err != nil {
		t.Fatalf("appA cache Set: %v", err)
	}
	if !mrA.Exists("keyA") {
		t.Error("keyA not in miniredisA (appA redis)")
	}
	if mrB.Exists("keyA") {
		t.Error("keyA leaked into miniredisB (appB redis) -- cache not isolated")
	}

	// B 写 keyB -> 仅落 miniredisB
	if err := appB.Cache().Set(ctx, "keyB", "valB", time.Minute); err != nil {
		t.Fatalf("appB cache Set: %v", err)
	}
	if !mrB.Exists("keyB") {
		t.Error("keyB not in miniredisB (appB redis)")
	}
	if mrA.Exists("keyB") {
		t.Error("keyB leaked into miniredisA (appA redis) -- cache not isolated")
	}

	// 互读对方 key -> miss（各自走自己的 redis）
	var got string
	if ok, err := appA.Cache().Get(ctx, "keyB", &got); ok || err != nil {
		t.Errorf("appA cache should miss keyB (different redis): ok=%v err=%v", ok, err)
	}
	if ok, err := appB.Cache().Get(ctx, "keyA", &got); ok || err != nil {
		t.Errorf("appB cache should miss keyA (different redis): ok=%v err=%v", ok, err)
	}

	// 各自读自己的 key -> hit
	if ok, err := appA.Cache().Get(ctx, "keyA", &got); err != nil || !ok || got != "valA" {
		t.Errorf("appA cache Get keyA = %v %q err=%v (want true valA)", ok, got, err)
	}
	if ok, err := appB.Cache().Get(ctx, "keyB", &got); err != nil || !ok || got != "valB" {
		t.Errorf("appB cache Get keyB = %v %q err=%v (want true valB)", ok, got, err)
	}
}

// TestAppCacheRollbackOnInitFailure 固化 Phase 4 回滚：Init 失败后全局默认 CacheManager
// 必须恢复为 Init 前的实例。
func TestAppCacheRollbackOnInitFailure(t *testing.T) {
	preInit := cache.NewCacheManager()
	saved := cache.SwapDefaultCacheManager(preInit) // saved = init 默认；preInit 现为全局
	defer cache.SwapDefaultCacheManager(saved)

	mr := miniredis.RunT(t)
	host, port := splitAddr(t, mr.Addr())
	cfg := testConfig(18122)
	cfg.Redis = config.RedisConfig{Host: host, Port: port}

	app := xlgo.New(
		xlgo.WithConfig(cfg),
		xlgo.WithRedis(),
		xlgo.WithHook(xlgo.Hook{Name: "fail", OnInit: func(*xlgo.App) error { return errors.New("boom") }}),
	)
	if err := app.Init(); err == nil {
		_ = app.Shutdown()
		t.Fatal("expected Init to fail via OnInit hook")
	}

	if cache.GetDefaultCache() != preInit {
		t.Fatal("rollback did not restore preInit cache manager as global default")
	}
}
