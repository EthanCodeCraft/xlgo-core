package storage

import "testing"

// TestSwapDefaultStorageManagerPreservesReplacedManager 固化 Phase 2：
// SwapDefaultStorageManager 必须返回被替换的旧 Manager 且不关闭它（供 App Init 失败回滚暂存）。
// 照 database.SwapDefaultRedisManager 的内部测试模式。
func TestSwapDefaultStorageManagerPreservesReplacedManager(t *testing.T) {
	orig := DefaultStorage.Load()

	old := NewStorageManager()
	previous := SwapDefaultStorageManager(old)
	if previous != orig {
		t.Fatalf("SwapDefaultStorageManager 应返回被替换的旧默认（orig），got %v", previous)
	}
	if DefaultStorage.Load() != old {
		t.Fatal("SwapDefaultStorageManager 应安装新的默认 manager（old）")
	}

	next := NewStorageManager()
	returned := SwapDefaultStorageManager(next)
	if returned != old {
		t.Fatalf("SwapDefaultStorageManager 应返回上一次安装的 manager（old），got %v", returned)
	}
	if DefaultStorage.Load() != next {
		t.Fatal("SwapDefaultStorageManager 应安装新的默认 manager（next）")
	}

	// nil 被忽略，返回当前默认
	if got := SwapDefaultStorageManager(nil); got != next {
		t.Fatalf("SwapDefaultStorageManager(nil) 应返回当前默认，got %v", got)
	}

	// 恢复 init() 时的默认，避免污染其它测试
	SwapDefaultStorageManager(orig)
}
