package database

import (
	"testing"

	"github.com/redis/go-redis/v9"
)

func TestSetDefaultRedisManagerClosesReplacedManager(t *testing.T) {
	old := NewRedisManager()
	old.setClientForTest(redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"}))

	orig := SwapDefaultRedisManager(old)
	t.Cleanup(func() {
		current := SwapDefaultRedisManager(orig)
		if current != nil && current != orig {
			_ = current.Close()
		}
	})

	next := NewRedisManager()
	SetDefaultRedisManager(next)
	if GetDefaultRedisManager() != next {
		t.Fatal("SetDefaultRedisManager 应安装新的默认 manager")
	}
	if old.Client() != nil {
		t.Fatal("SetDefaultRedisManager 应关闭并清空被替换的旧 Redis manager")
	}
}

func TestSwapDefaultRedisManagerPreservesReplacedManager(t *testing.T) {
	old := NewRedisManager()
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"})
	old.setClientForTest(client)

	orig := SwapDefaultRedisManager(old)
	t.Cleanup(func() {
		current := SwapDefaultRedisManager(orig)
		if current != nil && current != orig {
			_ = current.Close()
		}
		_ = old.Close()
	})

	next := NewRedisManager()
	previous := SwapDefaultRedisManager(next)
	if previous != old {
		t.Fatal("SwapDefaultRedisManager 应返回被替换的旧 manager")
	}
	if GetDefaultRedisManager() != next {
		t.Fatal("SwapDefaultRedisManager 应安装新的默认 manager")
	}
	if old.Client() != client {
		t.Fatal("SwapDefaultRedisManager 不应关闭旧 Redis manager，旧资源需可用于失败回滚")
	}
}
