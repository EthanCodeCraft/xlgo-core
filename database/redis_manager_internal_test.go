package database

import (
	"context"
	"net"
	"strconv"
	"testing"

	"github.com/EthanCodeCraft/xlgo-core/config"
	"github.com/alicebob/miniredis/v2"
)

func redisTestConfig(t *testing.T, addr string) *config.Config {
	t.Helper()
	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split redis addr: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("parse redis port: %v", err)
	}
	return &config.Config{Redis: config.RedisConfig{Host: host, Port: port}}
}

func TestRedisManagerInitClosesPreviousClient(t *testing.T) {
	mr1 := miniredis.RunT(t)
	mr2 := miniredis.RunT(t)

	m := NewRedisManager()
	if err := m.Init(redisTestConfig(t, mr1.Addr())); err != nil {
		t.Fatalf("first Init: %v", err)
	}
	first := m.Client()
	if first == nil {
		t.Fatal("first client is nil")
	}

	if err := m.Init(redisTestConfig(t, mr2.Addr())); err != nil {
		t.Fatalf("second Init: %v", err)
	}
	if err := first.Ping(context.Background()).Err(); err == nil {
		t.Fatal("first client still usable after second Init; old Redis pool was not closed")
	}
}
