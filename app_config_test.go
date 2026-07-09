package xlgo_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	xlgo "github.com/EthanCodeCraft/xlgo-core"
	"github.com/EthanCodeCraft/xlgo-core/config"
)

// TestAppShutdownStopsConfigWatcher_Mconfig3 固化 M-config-3：App.Shutdown 须停止其
// configManager 的热重载 watcher，避免关闭后遗留监听 goroutine（违反 C7 生命周期契约）。
//
// Init 后 resolveConfig 把 a.configManager 提升为全局默认，故经包级 config.StartWatcher
// 在其上启动 watcher（等价于用户对 configManager 调 LoadWithWatch/StartWatcher）；
// Shutdown 后断言 watchLoop goroutine 已退出。
func TestAppShutdownStopsConfigWatcher_Mconfig3(t *testing.T) {
	dir := filepath.Join(os.TempDir(), "xlgo_app_mconfig3")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	defer os.RemoveAll(dir)
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("app:\n  name: m3\n  env: dev\nserver:\n  port: 18080\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	app := xlgo.New(xlgo.WithConfigPath(cfgPath))
	if err := app.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// 经全局默认（=a.configManager）启动 watcher，模拟用户开启热重载
	if err := config.StartWatcher(); err != nil {
		t.Fatalf("StartWatcher: %v", err)
	}
	defer config.SetDefaultManager(nil)

	time.Sleep(120 * time.Millisecond) // 等 watchLoop 就绪
	before := runtime.NumGoroutine()

	if err := app.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	// watchLoop goroutine 应随 Shutdown 退出
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() < before {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if after := runtime.NumGoroutine(); after >= before {
		t.Errorf("App.Shutdown 未停止 config watcher（M-config-3）: before=%d after=%d", before, after)
	}
}
