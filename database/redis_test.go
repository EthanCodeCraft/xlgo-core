package database_test

import (
	"context"
	"testing"

	"github.com/EthanCodeCraft/xlgo-core/database"
)

func TestCloseRedisWithoutInit(t *testing.T) {
	if err := database.CloseRedis(); err != nil {
		t.Fatalf("CloseRedis without init should not error: %v", err)
	}
	if database.GetRedis() != nil {
		t.Fatal("expected Redis client nil")
	}
}

func TestHealthCheckRedisWithoutInit(t *testing.T) {
	if err := database.HealthCheckRedis(context.Background()); err == nil {
		t.Fatal("expected health check error without Redis init")
	}
}
