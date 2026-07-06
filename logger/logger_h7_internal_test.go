package logger

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EthanCodeCraft/xlgo-core/config"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// tmpCfg 构造一个指向临时目录的日志配置，用于 Init。
func tmpCfg(dir string) *config.Config {
	return &config.Config{
		Server: config.ServerConfig{Mode: "production"},
		Log: config.LogConfig{
			Dir:        dir,
			MaxSize:    1,
			MaxBackups: 1,
			MaxAge:     1,
			Compress:   false,
		},
	}
}

// TestH7ConcurrentInitCloseAndRead 验证请求 goroutine 的包级日志读路径
// 与 Init/Close 重新装配全局 logger 无数据竞争（-race）。
//
// H7 根因：原实现 Info/Error/APILog/DBLog/Sync 裸读包级 Logger/sugar/apiLog/dbLog，
// 而 Init/Close 在 m.mu 下写这些变量——实例锁保护全局变量、读侧无锁 → re-Init/Close
// 与请求日志竞争。修复后读路径经 atomic.Pointer Load。
//
// 红/绿验证：将 currentLogger/currentSugar/currentAPILog/currentDBLog 临时改为
// 裸读 Logger/sugar/apiLog/dbLog（或等价地恢复包级裸读），-race 必复现 DATA RACE；
// 恢复 atomic 后绿。
func TestH7ConcurrentInitCloseAndRead(t *testing.T) {
	dir := t.TempDir()
	cfg := tmpCfg(dir)

	// 确保默认 LogManager 起点干净。
	_ = GetDefaultLogManager().Close()
	t.Cleanup(func() { _ = GetDefaultLogManager().Close() })

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// 写者：循环 Init/Close（重新装配全局 logger）。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = GetDefaultLogManager().Init(cfg)
			_ = GetDefaultLogManager().Close()
		}
	}()

	// 读者：并发调用读路径（包级函数 + atomic 快照读取）。
	// 仅读取 logger 指针——这正是被竞态的访问；不调用 .Info/.Errorf 等写方法，
	// 避免对已被 re-Init 关闭的旧 writer 触发 lumberjack 重新打开同一文件，
	// 导致测试 TempDir 清理时句柄仍占用（框架不支持 re-Init 与在途写入并发，
	// 该约束非 H7 范围）。
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
				// 这些调用内部经 atomic Load 读取包级 logger 指针。
				_ = currentLogger()
				_ = currentSugar()
				_ = currentAPILog()
				_ = currentDBLog()
				_ = APILog()
				_ = DBLog()
			}
		}()
	}

	// 运行窗口足以让 race detector 采到任何竞争。
	time.Sleep(120 * time.Millisecond)
	close(stop)
	wg.Wait()

	// 收尾到 Nop，避免后续测试持有临时目录句柄。
	_ = GetDefaultLogManager().Close()
}

// TestH7CurrentLoggerReflectsInitAndClose 验证 atomic 快照随 Init/Close 正确切换：
// Init 后 currentLogger 写入文件；Close 后回到 Nop（不写文件）。
func TestH7CurrentLoggerReflectsInitAndClose(t *testing.T) {
	dir := t.TempDir()
	cfg := tmpCfg(dir)

	_ = GetDefaultLogManager().Close()
	t.Cleanup(func() { _ = GetDefaultLogManager().Close() })

	// Init 前：Nop，调用安全且不写文件。
	before := currentLogger()
	if before == nil {
		t.Fatal("currentLogger nil before Init")
	}

	if err := GetDefaultLogManager().Init(cfg); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Init 后：currentLogger 与 Logger 兼容别名一致，且为非 Nop 实例。
	after := currentLogger()
	if after == nil {
		t.Fatal("currentLogger nil after Init")
	}
	if after != Logger {
		t.Error("after Init, currentLogger() != Logger (compat alias out of sync)")
	}

	// 写一条日志并 flush，验证落到文件（说明拿到的是真实 logger，非 Nop）。
	const mark = "H7_INIT_REFLECT_xyz"
	after.Info(mark)
	_ = GetDefaultLogManager().Sync()

	data, err := readFile(t, filepath.Join(dir, "app.log"))
	if err != nil {
		t.Fatalf("read app.log: %v", err)
	}
	if !strings.Contains(data, mark) {
		t.Errorf("app.log missing mark %q after Init (got Nop?)", mark)
	}

	// Close 后：currentLogger 回到 Nop（与 Init 前实例不同，但同为 Nop 行为）。
	if err := GetDefaultLogManager().Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	closed := currentLogger()
	if closed == nil {
		t.Fatal("currentLogger nil after Close")
	}
	// Nop logger 写不报错也不落盘；验证 Close 后再写不新增 mark。
	closed.Info("H7_SHOULD_NOT_APPEAR_xyz")
	_ = GetDefaultLogManager().Sync()
	data2, _ := readFile(t, filepath.Join(dir, "app.log"))
	if strings.Contains(data2, "H7_SHOULD_NOT_APPEAR_xyz") {
		t.Error("wrote to file after Close (expected Nop)")
	}
}

// TestH7AtomicPointersNonNil 验证 init 后四个 atomic 快照永不 nil，
// 且 APILog/DBLog 返回与内部 atomic 一致的实例。
func TestH7AtomicPointersNonNil(t *testing.T) {
	if loggerPtr.Load() == nil {
		t.Error("loggerPtr nil after init")
	}
	if sugarPtr.Load() == nil {
		t.Error("sugarPtr nil after init")
	}
	if apiLogPtr.Load() == nil {
		t.Error("apiLogPtr nil after init")
	}
	if dbLogPtr.Load() == nil {
		t.Error("dbLogPtr nil after init")
	}
	if APILog() != apiLogPtr.Load() {
		t.Error("APILog() != apiLogPtr")
	}
	if DBLog() != dbLogPtr.Load() {
		t.Error("DBLog() != dbLogPtr")
	}
	if currentLogger() != loggerPtr.Load() {
		t.Error("currentLogger() != loggerPtr")
	}
}

// TestH7DefaultLoggerCompatibility 锁定 M3 的兼容边界：
// DefaultLogger 仍保持 *LogManager 类型，旧代码可继续直接调用其方法；
// 包级 facade 的默认 manager 替换则必须走 SetDefaultLogManager 的 atomic 快照。
func TestH7DefaultLoggerCompatibility(t *testing.T) {
	var _ *LogManager = DefaultLogger

	old := GetDefaultLogManager()
	t.Cleanup(func() { SetDefaultLogManager(old) })

	next := NewLogManager()
	SetDefaultLogManager(next)
	if got := GetDefaultLogManager(); got != next {
		t.Fatalf("GetDefaultLogManager() = %p, want %p", got, next)
	}
}

// TestM3StaleManagerCloseDoesNotCloseCurrentLogger 回归：旧 manager 的 Close
// 不得关闭另一个 manager 后续发布的全局 logger/writer。
func TestM3StaleManagerCloseDoesNotCloseCurrentLogger(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	m1 := NewLogManager()
	m2 := NewLogManager()
	t.Cleanup(func() {
		_ = m1.Close()
		_ = m2.Close()
		SetDefaultLogManager(NewLogManager())
	})

	if err := m1.Init(tmpCfg(dir1)); err != nil {
		t.Fatalf("m1.Init: %v", err)
	}
	if err := m2.Init(tmpCfg(dir2)); err != nil {
		t.Fatalf("m2.Init: %v", err)
	}
	if err := m1.Close(); err != nil {
		t.Fatalf("stale m1.Close: %v", err)
	}

	const mark = "M3_CURRENT_LOGGER_SURVIVES_STALE_CLOSE"
	currentLogger().Info(mark)
	_ = syncLoggers(currentLogger())
	data, err := readFile(t, filepath.Join(dir2, "app.log"))
	if err != nil {
		t.Fatalf("read current app.log: %v", err)
	}
	if !strings.Contains(data, mark) {
		t.Fatal("stale manager Close closed the current logger")
	}
}

// TestH7DurationFieldFix 验证 H7b：Field.Duration 签名改为
// func(key string, value time.Duration) zap.Field，且 key 不再被丢弃。
//
// 旧实现 func(key string, value interface{}) 在 case zap.Field 分支 return v
// 丢弃 key，签名与实现矛盾。修复后直接委托 zap.Duration(key, value)。
func TestH7DurationFieldFix(t *testing.T) {
	f := Field.Duration("elapsed", 5*time.Second)
	if f.Key != "elapsed" {
		t.Errorf("Duration key lost: got %q, want %q (H7b regression)", f.Key, "elapsed")
	}
	// zap.Duration 用 zapcore.DurationType 编码，Integer 字段承载纳秒。
	if f.Integer != int64(5*time.Second) {
		t.Errorf("Duration value mismatch: got %d, want %d", f.Integer, int64(5*time.Second))
	}

	// 零值与其他字段不受影响。
	f2 := Field.Duration("d0", 0)
	if f2.Key != "d0" {
		t.Errorf("Duration zero key lost: %q", f2.Key)
	}
}

// TestH7DurationFieldSignature 编译期锁定 Field.Duration 的签名为
// func(string, time.Duration) zap.Field（H7b 修复的核心：类型安全签名）。
//
// 旧签名为 func(string, interface{}) zap.Field，其 case zap.Field 分支 return v
// 丢弃传入的 key。若回退旧签名，下方赋值将因参数类型不匹配（interface{} ≠ time.Duration）
// 编译失败——即"修复前红、修复后绿"由编译器强制保证。
func TestH7DurationFieldSignature(t *testing.T) {
	var _ func(string, time.Duration) zap.Field = Field.Duration
}

// readFile 读取文件内容，文件不存在时返回空串（不报错）。
func readFile(t *testing.T, path string) (string, error) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(b), nil
}

// TestSetLevelHotSwap_M19：Init 后可运行期热切换日志级别（M19）。
func TestSetLevelHotSwap_M19(t *testing.T) {
	dir := t.TempDir()
	cfg := tmpCfg(dir)
	_ = GetDefaultLogManager().Close()
	t.Cleanup(func() { _ = GetDefaultLogManager().Close() })

	if err := GetDefaultLogManager().Init(cfg); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Init 后（tmpCfg 为 production 模式，默认 InfoLevel）可热切换到 ErrorLevel。
	before := GetDefaultLogManager().GetLevel()
	if before != zapcore.InfoLevel {
		t.Errorf("default level = %v, want InfoLevel (production)", before)
	}
	if ok := GetDefaultLogManager().SetLevel(zapcore.ErrorLevel); !ok {
		t.Fatal("SetLevel after Init should return true")
	}
	if got := GetDefaultLogManager().GetLevel(); got != zapcore.ErrorLevel {
		t.Errorf("after SetLevel, level = %v, want ErrorLevel", got)
	}

	// 包级 facade 同步。
	SetLevel(zapcore.WarnLevel)
	if got := GetLevel(); got != zapcore.WarnLevel {
		t.Errorf("package GetLevel = %v, want WarnLevel", got)
	}
}

// TestM3ConcurrentInitSetLevelAndClose 验证 Init/SetLevel/GetLevel/Close
// 共享同一个 manager 临界区，不再出现 m.level 锁外写与锁内读写竞争（-race）。
func TestM3ConcurrentInitSetLevelAndClose(t *testing.T) {
	dir := t.TempDir()
	cfg := tmpCfg(dir)
	m := NewLogManager()
	SetDefaultLogManager(m)
	t.Cleanup(func() {
		_ = m.Close()
		SetDefaultLogManager(NewLogManager())
	})

	stop := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = m.Init(cfg)
			_ = m.Close()
		}
	}()

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			levels := []zapcore.Level{
				zapcore.DebugLevel,
				zapcore.InfoLevel,
				zapcore.WarnLevel,
				zapcore.ErrorLevel,
			}
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = m.SetLevel(levels[i%len(levels)])
				_ = m.GetLevel()
			}
		}(i)
	}

	time.Sleep(120 * time.Millisecond)
	close(stop)
	wg.Wait()
}

// TestM3ConcurrentSetDefaultAndFacades 验证默认 LogManager 经 atomic.Pointer
// 读写，SetDefaultLogManager 与包级 facade 并发时无裸全局指针竞态（-race）。
func TestM3ConcurrentSetDefaultAndFacades(t *testing.T) {
	t.Cleanup(func() {
		_ = Close()
		SetDefaultLogManager(NewLogManager())
	})

	stop := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			SetDefaultLogManager(NewLogManager())
		}
	}()

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
				_ = GetLevel()
				_ = SetLevel(zapcore.WarnLevel)
				_ = Sync()
				_ = APILog()
				_ = DBLog()
			}
		}()
	}

	time.Sleep(120 * time.Millisecond)
	close(stop)
	wg.Wait()
}

// TestM3ConcurrentManagersInitClose 验证不同 LogManager 实例并发 Init/Close 时，
// 包级 Logger/fileWriters 的发布与关闭由 globalMu 串行化，不再依赖实例锁保护包级状态。
func TestM3ConcurrentManagersInitClose(t *testing.T) {
	dir := t.TempDir()
	cfg := tmpCfg(dir)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	managers := make([]*LogManager, 3)

	for i := 0; i < 3; i++ {
		managers[i] = NewLogManager()
		wg.Add(1)
		go func(m *LogManager) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				SetDefaultLogManager(m)
				_ = m.Init(cfg)
				_ = m.Close()
			}
		}(managers[i])
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = currentLogger()
			_ = currentSugar()
			_ = APILog()
			_ = DBLog()
		}
	}()

	time.Sleep(120 * time.Millisecond)
	close(stop)
	wg.Wait()
	for _, m := range managers {
		_ = m.Close()
	}
	_ = Close()
	SetDefaultLogManager(NewLogManager())
}

// TestM3InitRejectsInvalidLogConfig 验证 logger.Init 对明显非法日志配置失败，
// 避免空目录意外把 app.log 写到进程工作目录。
func TestM3InitRejectsInvalidLogConfig(t *testing.T) {
	if err := NewLogManager().Init(&config.Config{}); err == nil {
		t.Fatal("Init with empty log dir should fail")
	}

	cfg := tmpCfg(t.TempDir())
	cfg.Log.MaxAge = -1
	if err := NewLogManager().Init(cfg); err == nil {
		t.Fatal("Init with negative MaxAge should fail")
	}
}
