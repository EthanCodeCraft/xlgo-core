package cache

import (
	"sync"
	"testing"
)

// TestGlobalKeyBuilderConcurrentInitGet_M13：并发 InitKeyBuilder/GetKeyBuilder/K 不触发 data race
// 且不 nil-panic（M13：sync.Once + RWMutex 保护全局构建器）。须配合 -race 运行。
func TestGlobalKeyBuilderConcurrentInitGet_M13(t *testing.T) {
	// 预置一个已知前缀，避免依赖 config.Get。
	InitKeyBuilder("race_site")
	t.Cleanup(func() { globalKBMu.Lock(); globalKeyBuilder = nil; globalKBMu.Unlock() })

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(3)
		go func() { defer wg.Done(); InitKeyBuilder("race_site") }()
		go func() { defer wg.Done(); _ = GetKeyBuilder() }()
		go func() { defer wg.Done(); _ = K("user:1") }()
	}
	wg.Wait()

	// 最终 GetKeyBuilder 非 nil，K 返回带前缀键。
	if GetKeyBuilder() == nil {
		t.Fatal("GetKeyBuilder nil after concurrent init")
	}
	if got := K("user:1"); got == "user:1" {
		t.Errorf("K returned unprefixed key %q (prefix not applied)", got)
	}
}
