package response_test

import (
	"sync"
	"testing"

	"github.com/EthanCodeCraft/xlgo-core/response"
)

// TestWithDetailDoesNotMutateOriginal H-10 回归：WithDetail 必须返回新拷贝，
// 不得 mutate 共享的预定义 Err*。修复前 WithDetail 会写 e.Detail 污染全局。
func TestWithDetailDoesNotMutateOriginal(t *testing.T) {
	if response.ErrNotFound.Detail != "" {
		t.Fatalf("预置条件：ErrNotFound.Detail 应为空，实际 %q", response.ErrNotFound.Detail)
	}

	detailed := response.ErrNotFound.WithDetail("用户 123 不存在")

	// 原共享对象不得被污染
	if response.ErrNotFound.Detail != "" {
		t.Fatalf("WithDetail 污染了共享 ErrNotFound，Detail=%q", response.ErrNotFound.Detail)
	}
	// 新拷贝携带 detail
	if detailed.Detail != "用户 123 不存在" {
		t.Fatalf("新 Error 应携带 detail，实际 %q", detailed.Detail)
	}
	if detailed.Code != response.ErrNotFound.Code || detailed.Message != response.ErrNotFound.Message {
		t.Fatalf("新 Error 应继承 Code/Message，实际 Code=%d Message=%q", detailed.Code, detailed.Message)
	}
}

// TestWithDetailConcurrentOnSharedError H-10 回归：并发在共享 Err* 上调 WithDetail
// 不应触发数据竞争（修复前 mutate 共享对象，-race 必采）。
func TestWithDetailConcurrentOnSharedError(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			e := response.ErrNotFound.WithDetail("concurrent")
			if e.Detail != "concurrent" {
				t.Errorf("Detail 应为 concurrent，实际 %q", e.Detail)
			}
		}(i)
	}
	wg.Wait()
}

// TestExposeDetailGating P1 #15：SetExposeDetail(false) 时 ToResponse 不得输出 Detail；
// true 时输出。默认（未设置）为 true，保持存量行为。
func TestExposeDetailGating(t *testing.T) {
	t.Cleanup(func() { response.SetExposeDetail(true) }) // 还原默认

	err := response.NewErrorWithDetail(response.CodeServerError, "服务器错误", "pq: relation \"users\" does not exist")

	// 暴露开启（默认/开发）：Detail 出现在 Data 中
	response.SetExposeDetail(true)
	if resp := err.ToResponse(); resp.Data == nil {
		t.Error("SetExposeDetail(true): ToResponse 应包含 detail")
	}

	// 暴露关闭（生产）：Detail 不得出现，防内部错误泄露
	response.SetExposeDetail(false)
	if resp := err.ToResponse(); resp.Data != nil {
		t.Errorf("SetExposeDetail(false): ToResponse 不得输出 detail，实际 Data=%v", resp.Data)
	}
}
