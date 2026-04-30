package cache_test

import (
	"testing"

	"github.com/EthanCodeCraft/xlgo-core/cache"
)

func TestKeyBuilder(t *testing.T) {
	kb := cache.NewKeyBuilder(
		cache.WithPrefix("test_site"),
		cache.WithSeparator(":"),
		cache.WithCacheType("cache"),
	)

	// 测试 Build
	result := kb.Build("user:1")
	expected := "cache:test_site:user:1"
	if result != expected {
		t.Errorf("Build = %s, want %s", result, expected)
	}

	// 测试 BuildTemp
	tempResult := kb.BuildTemp("token")
	if tempResult != "temp:test_site:token" {
		t.Errorf("BuildTemp = %s, want temp:test_site:token", tempResult)
	}

	// 测试 BuildPerm
	permResult := kb.BuildPerm("config")
	if permResult != "perm:test_site:config" {
		t.Errorf("BuildPerm = %s, want perm:test_site:config", permResult)
	}

	// 测试 BuildLock
	lockResult := kb.BuildLock("order:123")
	if lockResult != "lock:test_site:order:123" {
		t.Errorf("BuildLock = %s, want lock:test_site:order:123", lockResult)
	}

	// 测试 BuildCounter
	counterResult := kb.BuildCounter("visit")
	if counterResult != "counter:test_site:visit" {
		t.Errorf("BuildCounter = %s, want counter:test_site:visit", counterResult)
	}

	// 测试 BuildSession
	sessionResult := kb.BuildSession("sid123")
	if sessionResult != "session:test_site:sid123" {
		t.Errorf("BuildSession = %s, want session:test_site:sid123", sessionResult)
	}
}

func TestKeyBuilderNoPrefix(t *testing.T) {
	// 无前缀的构建器
	kb := cache.NewKeyBuilder()

	result := kb.Build("user:1")
	expected := "cache:user:1"
	if result != expected {
		t.Errorf("Build without prefix = %s, want %s", result, expected)
	}
}

func TestKeyBuilderSetPrefix(t *testing.T) {
	kb := cache.NewKeyBuilder(cache.WithPrefix("site_a"))

	// 动态修改前缀
	kb.SetPrefix("site_b")

	result := kb.Build("user:1")
	if result != "cache:site_b:user:1" {
		t.Errorf("SetPrefix failed: %s", result)
	}

	// 验证链式调用
	kb2 := kb.SetPrefix("site_c")
	if kb2 != kb {
		t.Error("SetPrefix should return same builder")
	}
}

func TestKeyBuilderGetPrefix(t *testing.T) {
	kb := cache.NewKeyBuilder(cache.WithPrefix("my_site"))

	prefix := kb.GetPrefix()
	if prefix != "my_site" {
		t.Errorf("GetPrefix = %s, want my_site", prefix)
	}
}

func TestKeyBuilderBuildPattern(t *testing.T) {
	kb := cache.NewKeyBuilder(cache.WithPrefix("site_a"))

	result := kb.BuildPattern("user:*")
	if result != "cache:site_a:user:*" {
		t.Errorf("BuildPattern = %s, want cache:site_a:user:*", result)
	}
}

func TestKeyBuilderCustomSeparator(t *testing.T) {
	kb := cache.NewKeyBuilder(
		cache.WithPrefix("site"),
		cache.WithSeparator("_"),
	)

	result := kb.Build("user:1")
	if result != "cache_site_user:1" {
		t.Errorf("Custom separator = %s, want cache_site_user:1", result)
	}
}

func TestKeyBuilderCustomCacheType(t *testing.T) {
	kb := cache.NewKeyBuilder(
		cache.WithPrefix("site"),
		cache.WithCacheType("session"),
	)

	result := kb.Build("user:1")
	if result != "session:site:user:1" {
		t.Errorf("Custom cache type = %s, want session:site:user:1", result)
	}
}

func TestGlobalKeyBuilder(t *testing.T) {
	// 初始化全局构建器
	cache.InitKeyBuilder("global_site")

	// 测试 K 函数
	result := cache.K("user:1")
	if result != "cache:global_site:user:1" {
		t.Errorf("K = %s, want cache:global_site:user:1", result)
	}

	// 测试 KTemp
	temp := cache.KTemp("token")
	if temp != "temp:global_site:token" {
		t.Errorf("KTemp = %s, want temp:global_site:token", temp)
	}

	// 测试 KPerm
	perm := cache.KPerm("config")
	if perm != "perm:global_site:config" {
		t.Errorf("KPerm = %s, want perm:global_site:config", perm)
	}

	// 测试 KLock
	lock := cache.KLock("order:123")
	if lock != "lock:global_site:order:123" {
		t.Errorf("KLock = %s, want lock:global_site:order:123", lock)
	}

	// 测试 KCounter
	counter := cache.KCounter("visit")
	if counter != "counter:global_site:visit" {
		t.Errorf("KCounter = %s, want counter:global_site:visit", counter)
	}

	// 测试 KSession
	session := cache.KSession("sid")
	if session != "session:global_site:sid" {
		t.Errorf("KSession = %s, want session:global_site:sid", session)
	}
}

func TestGetKeyBuilder(t *testing.T) {
	// 获取全局构建器（如果未初始化会自动初始化）
	kb := cache.GetKeyBuilder()
	if kb == nil {
		t.Error("GetKeyBuilder should not return nil")
	}
}

func TestWithPrefixFunc(t *testing.T) {
	opt := cache.WithPrefix("test")
	kb := cache.NewKeyBuilder()
	opt(kb)

	if kb.GetPrefix() != "test" {
		t.Errorf("WithPrefix failed")
	}
}

func TestWithSeparatorFunc(t *testing.T) {
	opt := cache.WithSeparator("_")
	kb := cache.NewKeyBuilder()
	opt(kb)

	result := kb.Build("key")
	// 分隔符应该是 _
	if !contains(result, "_") {
		t.Errorf("WithSeparator failed, result: %s", result)
	}
}

func TestWithCacheTypeFunc(t *testing.T) {
	opt := cache.WithCacheType("custom")
	kb := cache.NewKeyBuilder()
	opt(kb)

	result := kb.Build("key")
	if !contains(result, "custom") {
		t.Errorf("WithCacheType failed, result: %s", result)
	}
}

func contains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}