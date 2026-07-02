package config_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EthanCodeCraft/xlgo-core/config"
)

// writeConfig 写入临时配置文件并返回路径。
func writeConfig(t *testing.T, name, content string) string {
	t.Helper()
	dir := filepath.Join(os.TempDir(), "xlgo_c10_test")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return p
}

func validConfigYAML(port int) string {
	return "app:\n  name: c10\n  env: dev\nserver:\n  port: " + itoa(port) + "\n"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// TestSetDefaultManagerConcurrent 并发置换默认 Manager 与并发读取，
// 必须经 -race 无竞争（C10a）。
func TestSetDefaultManagerConcurrent(t *testing.T) {
	// 准备若干可加载的 Manager
	paths := make([]string, 4)
	for i := range paths {
		paths[i] = writeConfig(t, "c10_concurrent_"+itoa(i)+".yaml", validConfigYAML(9000+i))
	}
	defer func() {
		for _, p := range paths {
			os.Remove(p)
		}
	}()

	var wg sync.WaitGroup
	stop := make(chan struct{})
	// 写者：并发 SetDefaultManager
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				m := config.NewManager(paths[idx])
				_, _ = m.Load()
				config.SetDefaultManager(m)
			}
		}(i)
	}
	// 读者：并发 Get / GetViper / GetString
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = config.Get()
				_ = config.GetViper()
				_ = config.GetString("server.port")
			}
		}()
	}
	// 跑足够长以让 -race 采到
	time.Sleep(150 * time.Millisecond)
	close(stop)
	wg.Wait()

	// 还原全局状态，避免污染其他测试
	config.SetDefaultManager(nil)
}

// TestLoadReturnsDefensiveCopy Load 返回的配置与 Get() 内部指针独立，
// 修改返回值不污染全局（C10c）。
func TestLoadReturnsDefensiveCopy(t *testing.T) {
	p := writeConfig(t, "c10_defensive.yaml", validConfigYAML(8081))
	defer os.Remove(p)

	config.Set(nil)
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Port != 8081 {
		t.Fatalf("port = %d, want 8081", cfg.Server.Port)
	}

	// 调用方修改返回值
	cfg.Server.Port = 1
	cfg.App.Name = "mutated"

	// 全局读取不受影响
	got := config.Get()
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.Server.Port != 8081 {
		t.Errorf("global port polluted = %d, want 8081 (C10c)", got.Server.Port)
	}
	if got.App.Name == "mutated" {
		t.Errorf("global app name polluted (C10c)")
	}

	config.SetDefaultManager(nil)
}

// TestLoadDefensiveCopySliceContract 固化 C10c 的浅拷贝语义契约：
// 标量字段独立（修改不污染全局），切片字段共享底层数组（文档化为只读契约，
// 调用方不得修改切片元素）。本测试锁定该行为，防止未来误改。
func TestLoadDefensiveCopySliceContract(t *testing.T) {
	content := "app:\n  name: c10slice\nserver:\n  port: 8090\ncors:\n  allowed_origins:\n    - https://a.example.com\n    - https://b.example.com\n"
	p := writeConfig(t, "c10_slice.yaml", content)
	defer os.Remove(p)

	config.Set(nil)
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// 标量独立
	cfg.Server.Port = 1
	if got := config.Get().Server.Port; got != 8090 {
		t.Errorf("scalar polluted = %d, want 8090", got)
	}

	// 切片共享底层数组（浅拷贝局限）：修改切片元素会污染全局。
	// 这是文档化的只读契约——本断言锁定该行为，提醒调用方不得改切片元素。
	cfg.CORS.AllowedOrigins[0] = "https://mutated.example.com"
	if got := config.Get().CORS.AllowedOrigins[0]; got != "https://mutated.example.com" {
		t.Errorf("slice backing array should be shared (shallow copy), got %q", got)
	}

	config.SetDefaultManager(nil)
}
func TestReloadInvalidConfigKeepsOld(t *testing.T) {
	p := writeConfig(t, "c10_reload_bad.yaml", validConfigYAML(8082))
	defer os.Remove(p)

	m := config.NewManager(p)
	if _, err := m.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := m.Get().Server.Port; got != 8082 {
		t.Fatalf("initial port = %d, want 8082", got)
	}

	// 覆盖为非法配置（端口越界）
	if err := os.WriteFile(p, []byte("app:\n  name: bad\nserver:\n  port: 99999\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	err := m.Reload()
	if err == nil {
		t.Fatal("Reload invalid config should return error (C10b)")
	}
	if !strings.Contains(err.Error(), "server.port") {
		t.Errorf("error should mention server.port, got %v", err)
	}
	// 旧配置保留
	if got := m.Get().Server.Port; got != 8082 {
		t.Errorf("old config not preserved = %d, want 8082 (C10b)", got)
	}
}

// TestHotReloadInvalidConfigKeepsOld 文件监听路径遇非法配置保留旧配置，
// 且不触发回调；监听仍存活，后续合法变更正常生效（C10b + C10d 监听健壮性）。
func TestHotReloadInvalidConfigKeepsOld(t *testing.T) {
	p := writeConfig(t, "c10_watch_bad.yaml", validConfigYAML(8083))
	defer os.Remove(p)

	m := config.NewManager(p)
	if _, err := m.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	changes := make(chan int, 16)
	m.RegisterCallback(func(c *config.Config) {
		select {
		case changes <- c.Server.Port:
		default:
		}
	})
	if err := m.StartWatcher(); err != nil {
		t.Fatalf("StartWatcher: %v", err)
	}
	defer m.StopWatcher()

	// 1) 写入非法配置：应保留旧配置、不触发回调
	if err := os.WriteFile(p, []byte("app:\n  name: bad\nserver:\n  port: 99999\n"), 0644); err != nil {
		t.Fatalf("WriteFile invalid: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := m.Get().Server.Port; got != 8083 {
			t.Errorf("invalid config leaked into global = %d, want 8083 (C10b)", got)
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	select {
	case port := <-changes:
		t.Errorf("callback fired for invalid config with port %d (C10b)", port)
	default:
	}

	// 2) 写入合法配置：监听仍存活，应触发回调且全局更新
	if err := os.WriteFile(p, []byte(validConfigYAML(8084)), 0644); err != nil {
		t.Fatalf("WriteFile valid: %v", err)
	}
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if got := m.Get().Server.Port; got == 8084 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := m.Get().Server.Port; got != 8084 {
		t.Fatalf("watcher did not reload valid config = %d, want 8084", got)
	}
}

// TestStopWatcherReleasesGoroutine StopWatcher 后监听 goroutine 退出，无泄漏（C10d）。
func TestStopWatcherReleasesGoroutine(t *testing.T) {
	p := writeConfig(t, "c10_stop.yaml", validConfigYAML(8085))
	defer os.Remove(p)

	m := config.NewManager(p)
	if _, err := m.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := m.StartWatcher(); err != nil {
		t.Fatalf("StartWatcher: %v", err)
	}

	// 等待监听 goroutine 就绪
	time.Sleep(100 * time.Millisecond)
	before := runtime.NumGoroutine()

	m.StopWatcher()

	// 轮询确认 goroutine 退出
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() < before {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if after := runtime.NumGoroutine(); after >= before {
		t.Errorf("watcher goroutine not released: before=%d after=%d (C10d)", before, after)
	}

	// 幂等：再次 Stop 不 panic
	m.StopWatcher()
}

// TestStartWatcherIdempotent 重复 StartWatcher 不创建多个监听 goroutine（幂等）。
func TestStartWatcherIdempotent(t *testing.T) {
	p := writeConfig(t, "c10_idem.yaml", validConfigYAML(8086))
	defer os.Remove(p)

	m := config.NewManager(p)
	if _, err := m.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := m.StartWatcher(); err != nil {
		t.Fatalf("StartWatcher 1: %v", err)
	}
	time.Sleep(80 * time.Millisecond)
	once := runtime.NumGoroutine()

	if err := m.StartWatcher(); err != nil {
		t.Fatalf("StartWatcher 2: %v", err)
	}
	if err := m.StartWatcher(); err != nil {
		t.Fatalf("StartWatcher 3: %v", err)
	}
	time.Sleep(80 * time.Millisecond)
	twice := runtime.NumGoroutine()
	if twice > once {
		t.Errorf("idempotent StartWatcher leaked goroutine: once=%d twice=%d", once, twice)
	}
	m.StopWatcher()
}
