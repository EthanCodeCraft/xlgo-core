package database

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// startHungRedisListener 启动一个接受 TCP 连接但不读写任何数据的 listener，
// 模拟"挂起 Redis"（连接建立但不响应 RESP，区别于宕机的 connection-refused）。
// 返回 listener 地址；t.Cleanup 关闭 listener。
func startHungRedisListener(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("hung redis listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return // listener 关闭
			}
			// 故意不读写、不关闭，持住连接模拟挂起。连接由 t.Cleanup 的 ln.Close 间接清理。
			_ = conn
		}
	}()
	return ln.Addr().String()
}

// TestRedisHealthCheckBoundsHungRedis_Hdb1 回归 H-db-1 边界（Redis 侧）：
// RedisManager.HealthCheck(Background) 对挂起 Redis（连接活但不响应）应受
// redis client 的 ReadTimeout(3s，redis.go D7) 约束有界返回错误，不无限阻塞。
// ctx 无 deadline 时由 client ReadTimeout 兜底；ctx 自带更短 deadline 时优先尊重 ctx。
// 修复前/缺 ReadTimeout 时 Ping(Background) 会无限阻塞。
func TestRedisHealthCheckBoundsHungRedis_Hdb1(t *testing.T) {
	addr := startHungRedisListener(t)
	cfg := redisTestConfig(t, addr)

	m := NewRedisManager()
	if err := m.Init(cfg); err == nil {
		// Init 的 Ping 有 5s ctx，挂起 Redis 下应在 ~3s（ReadTimeout）失败返回错误。
		t.Fatalf("挂起 Redis 的 Init 应返回错误，got nil")
	}
	// Init 失败后 m.client 未被安装（nil），HealthCheck 返 "Redis 未初始化"。
	// 为测 HealthCheck 自身的 ping 超时约束，需 client 已安装但指向挂起 Redis。
	// 用 setClientForTest 注入一个指向挂起 Redis 的 client。
	client := newRedisClientForTest(addr)
	old := m.setClientForTest(client)
	t.Cleanup(func() {
		if old != nil {
			_ = old.Close()
		}
		_ = client.Close()
	})

	// HealthCheck(Background)：挂起 Redis 下 client.Ping 受 ReadTimeout 3s 约束有界返回。
	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- m.HealthCheck(context.Background()) }()
	select {
	case err := <-done:
		elapsed := time.Since(start)
		if err == nil {
			t.Fatalf("挂起 Redis 的 HealthCheck 应返回错误，got nil")
		}
		// 应在 ReadTimeout(3s) 附近返回，给 6s 上限（含连接/重试余量）。
		if elapsed > 6*time.Second {
			t.Fatalf("HealthCheck 耗时 %v 超过 6s 上限（应受 ReadTimeout 3s 约束）", elapsed)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("HealthCheck 在挂起 Redis 上无限阻塞（ReadTimeout 未约束 Ping）")
	}
}

// newRedisClientForTest 构造指向 addr 的 redis.Client（复用 Init 的 client 配置，
// 含 DialTimeout 5s / ReadTimeout 3s / WriteTimeout 3s）。
func newRedisClientForTest(addr string) *redis.Client {
	return newRedisClient(addr, "", 0)
}
