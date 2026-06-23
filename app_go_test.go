package xlgo_test

import (
	"context"
	"testing"
	"time"

	xlgo "github.com/EthanCodeCraft/xlgo-core"
)

func TestAppGoWaitsOnShutdown(t *testing.T) {
	app := xlgo.New()

	started := make(chan struct{})
	exited := make(chan struct{})
	app.Go(func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		// 模拟收尾
		time.Sleep(10 * time.Millisecond)
		close(exited)
	})

	<-started

	// Shutdown 应 cancel ctx 并等待 goroutine 退出
	done := make(chan error, 1)
	go func() { done <- app.Shutdown() }()
	select {
	case <-exited:
		// goroutine 在 cancel 后退出，符合预期
	case <-time.After(2 * time.Second):
		t.Fatal("App.Go goroutine did not exit after Shutdown cancel")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Shutdown returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown did not return")
	}
}
