package sse

import (
	"context"
	"errors"
	"testing"
	"time"
)

// 回归 C3a：Stream 在 ctx 取消时返回 ctx.Err，不再阻塞在 for-range ch。
// 旧实现 for range ch 无 ctx.Done 分支，ctx 取消后仍阻塞等 ch → goroutine 泄漏 + 上游 LLM 流持续。
func TestStreamStopsOnCtxCancelInternal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	w := &SSEWriter{ctx: ctx} // writer/flusher 不用（ctx.Done 先触发，不写数据）

	ch := make(chan any)
	// 不向 ch 发数据，也不关闭 ch——若 Stream 无 ctx.Done 分支将永久阻塞。
	done := make(chan error, 1)
	go func() {
		done <- w.Stream("msg", ch)
	}()

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Stream err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stream did not return after ctx cancel (C3a: no ctx.Done branch)")
	}
}


