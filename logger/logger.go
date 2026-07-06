package logger

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/EthanCodeCraft/xlgo-core/config"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

var (
	// 内部 atomic 存储——四个 logger 的真实源（H7 修复）。
	// 所有包级函数（Info/Debug/.../APILog/DBLog/Sync）经 atomic Load 读取，
	// 使请求 goroutine 不与 Init/Close 重新装配全局变量竞争。
	// 原实现用 m.mu（实例锁）保护包级全局变量，锁与被保护对象作用域错配，
	// 读侧无锁裸读 → 热重载 re-Init/Close 与请求日志存在数据竞争。
	loggerPtr atomic.Pointer[zap.Logger]
	sugarPtr  atomic.Pointer[zap.SugaredLogger]
	apiLogPtr atomic.Pointer[zap.Logger]
	dbLogPtr  atomic.Pointer[zap.Logger]

	// defaultLoggerPtr 是包级 facade 使用的默认 manager 原子快照。
	// 保留导出的 DefaultLogger *LogManager 作为兼容变量；SetDefaultLogManager
	// 只替换这个内部指针，避免 facade 裸读/裸写导出变量。
	defaultLoggerPtr    atomic.Pointer[LogManager]
	globalLogGeneration uint64

	// globalMu 串行化包级 logger/writer 的发布与关闭。
	// 不能仅依赖 LogManager.mu：多个 LogManager 实例并发 Init/Close 时，
	// 实例锁无法保护 Logger/fileWriters 这类包级状态。
	globalMu sync.Mutex

	// Logger 全局通用日志实例（兼容别名）。Init 之前为 Nop，调用安全。
	//
	// Deprecated: 此导出变量由 Init/Close 在 globalMu 下同步维护，但直接读它在 re-Init/Close
	// 期间非并发安全（L-E）。请改用包级函数 Info/Debug/Warn/Error/...（经 atomic 读取，
	// 并发安全）。保留仅为向后兼容，未来大版本可能移除。
	Logger = zap.NewNop()

	// fileWriters 持有所有 lumberjack 实例引用，
	// Close() 调用时显式释放文件句柄。
	// 必要性：lumberjack 不依赖 GC 关闭文件，进程长跑或测试场景下
	// 不显式关闭会持有句柄导致 Windows 上无法删除日志目录。
	// 仅在 globalMu 下访问（Init/Close），无读侧竞争。
	fileWriters []*lumberjack.Logger
)

func init() {
	// 初始化 atomic 存储为 Nop，保证包级函数在任何时刻 Load 均非 nil。
	nop := zap.NewNop()
	loggerPtr.Store(nop)
	sugarPtr.Store(nop.Sugar())
	apiLogPtr.Store(nop)
	dbLogPtr.Store(nop)
	defaultLoggerPtr.Store(DefaultLogger)
}

// currentLogger 返回通用 logger 的 atomic 快照（永不 nil）。
func currentLogger() *zap.Logger {
	if l := loggerPtr.Load(); l != nil {
		return l
	}
	return zap.NewNop() // 防御：init 后不可达
}

// currentSugar 返回 sugared logger 的 atomic 快照（永不 nil）。
func currentSugar() *zap.SugaredLogger {
	if s := sugarPtr.Load(); s != nil {
		return s
	}
	return zap.NewNop().Sugar() // 防御：init 后不可达
}

// currentAPILog 返回 API logger 的 atomic 快照（永不 nil）。
func currentAPILog() *zap.Logger {
	if l := apiLogPtr.Load(); l != nil {
		return l
	}
	return zap.NewNop()
}

// currentDBLog 返回 DB logger 的 atomic 快照（永不 nil）。
func currentDBLog() *zap.Logger {
	if l := dbLogPtr.Load(); l != nil {
		return l
	}
	return zap.NewNop()
}

// LogManager 日志管理器（#10）。照 database.Manager 模式：
// 实例化 + DefaultLogger 全局默认 + 包级 facade 代理。
// 包级 Logger/sugar/apiLog/dbLog 由 Init 同步维护，下游 8 个包零改动。
type LogManager struct {
	mu  sync.Mutex
	cfg *config.Config
	// level 是所有 core 共享的 AtomicLevel，支持运行期 SetLevel 热切换（M19）。
	// Init 前为 nil，SetLevel 守卫之。
	level      zap.AtomicLevel
	generation uint64
}

// DefaultSnapshot 是默认 logger 全局状态的 opaque 快照。
// App 初始化用它实现“先安装新 logger，后续失败可恢复旧 logger”的事务边界。
type DefaultSnapshot struct {
	manager    *LogManager
	logger     *zap.Logger
	sugar      *zap.SugaredLogger
	apiLog     *zap.Logger
	dbLog      *zap.Logger
	writers    []*lumberjack.Logger
	generation uint64
}

// DefaultLogger 默认日志管理器，包级 facade 代理到它。
//
// Deprecated: 直接替换该导出变量（logger.DefaultLogger = m）不会更新包级 facade
// 使用的 atomic 快照，也不是并发安全的替换方式。请用 SetDefaultLogManager(m)。
// 直接调用 DefaultLogger.Init/Close/SetLevel/GetLevel 仍保留兼容，并受 LogManager
// 自身锁保护。
var DefaultLogger = NewLogManager()

// NewLogManager 创建日志管理器实例。
func NewLogManager() *LogManager { return &LogManager{} }

// GetDefaultLogManager 返回全局默认 LogManager（atomic 读取，并发安全）。
func GetDefaultLogManager() *LogManager {
	if m := defaultLoggerPtr.Load(); m != nil {
		return m
	}
	if DefaultLogger != nil && defaultLoggerPtr.CompareAndSwap(nil, DefaultLogger) {
		return DefaultLogger
	}
	m := NewLogManager()
	if defaultLoggerPtr.CompareAndSwap(nil, m) {
		return m
	}
	return defaultLoggerPtr.Load()
}

// SetLevel 运行期热切换日志级别（M19）。需先 Init；未 Init 时返回 false。
// 影响所有 core（app/api/db/console）——它们共享同一 AtomicLevel。
func (m *LogManager) SetLevel(l zapcore.Level) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.level == (zap.AtomicLevel{}) {
		return false
	}
	m.level.SetLevel(l)
	return true
}

// GetLevel 返回当前日志级别（未 Init 返回 InfoLevel）。
func (m *LogManager) GetLevel() zapcore.Level {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.level == (zap.AtomicLevel{}) {
		return zapcore.InfoLevel
	}
	return m.level.Level()
}

// SetLevel 包级 facade：运行期热切换默认日志级别（M19）。未 Init 时无操作返回 false。
func SetLevel(l zapcore.Level) bool { return GetDefaultLogManager().SetLevel(l) }

// GetLevel 包级 facade：返回默认日志当前级别。
func GetLevel() zapcore.Level { return GetDefaultLogManager().GetLevel() }

// SetDefaultLogManager 提升指定 LogManager 为全局默认（atomic.Store，并发安全）。
func SetDefaultLogManager(m *LogManager) {
	if m != nil {
		defaultLoggerPtr.Store(m)
	}
}

// SnapshotDefault 返回默认 logger 全局状态快照。
// 快照仅用于 App 初始化回滚；调用方成功提交后必须 CloseDefaultSnapshot 释放旧 writers。
func SnapshotDefault() *DefaultSnapshot {
	globalMu.Lock()
	defer globalMu.Unlock()
	return &DefaultSnapshot{
		manager:    GetDefaultLogManager(),
		logger:     currentLogger(),
		sugar:      currentSugar(),
		apiLog:     currentAPILog(),
		dbLog:      currentDBLog(),
		writers:    append([]*lumberjack.Logger(nil), fileWriters...),
		generation: globalLogGeneration,
	}
}

// RestoreDefaultSnapshot 恢复 SnapshotDefault 捕获的默认 logger 状态，并关闭当前新 writers。
func RestoreDefaultSnapshot(s *DefaultSnapshot) error {
	if s == nil {
		return nil
	}
	globalMu.Lock()
	newLogger := currentLogger()
	newAPILog := currentAPILog()
	newDBLog := currentDBLog()
	newWriters := fileWriters
	loggerPtr.Store(s.logger)
	sugarPtr.Store(s.sugar)
	apiLogPtr.Store(s.apiLog)
	dbLogPtr.Store(s.dbLog)
	Logger = s.logger
	fileWriters = append([]*lumberjack.Logger(nil), s.writers...)
	globalLogGeneration = s.generation
	globalMu.Unlock()
	if s.manager != nil {
		SetDefaultLogManager(s.manager)
	}
	return errors.Join(syncLoggers(newLogger, newAPILog, newDBLog), closeFileWriters(newWriters))
}

// CloseDefaultSnapshot 关闭 SnapshotDefault 捕获的旧 writers；用于新 logger 已提交成功后释放旧资源。
func CloseDefaultSnapshot(s *DefaultSnapshot) error {
	if s == nil {
		return nil
	}
	return errors.Join(syncLoggers(s.logger, s.apiLog, s.dbLog), closeFileWriters(s.writers))
}

// Init 初始化日志。
//
// 三个 logger 的分流策略：
//   - Logger（通用）：写 console + logs/app.log
//   - APILog()      ：写 console + logs/api.log
//   - DBLog()       ：写 console + logs/database.log
//
// 关键修复（v1.0.3）：旧实现把 apiCore 和 dbCore 都 Tee 进通用 Logger，
// 导致每条 logger.Info(...) 都会同时落到 api.log + database.log + console
// 三份，磁盘占用翻倍且分流形同虚设。新实现通用 Logger 只走独立的 app.log。
func (m *LogManager) Init(cfg *config.Config) error {
	return m.init(cfg, true)
}

// InitPreservingPrevious 初始化并发布 logger，但暂不关闭被替换的旧 writers。
// App.Init 使用该方法配合 SnapshotDefault，在后续 hook 失败时可恢复旧 logger。
func (m *LogManager) InitPreservingPrevious(cfg *config.Config) error {
	return m.init(cfg, false)
}

func (m *LogManager) init(cfg *config.Config, closePrevious bool) error {
	if cfg == nil {
		return errors.New("logger: 配置为空")
	}
	if err := validateLogConfig(cfg.Log); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// 确保日志目录存在（0750：owner+group 可访问，与 storage 目录权限一致）
	if err := os.MkdirAll(cfg.Log.Dir, 0o750); err != nil {
		return err
	}

	// 日志编码器配置
	encoderConfig := zapcore.EncoderConfig{
		TimeKey:        "time",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		FunctionKey:    zapcore.OmitKey,
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.SecondsDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	// 根据运行模式设置日志级别（M19：用 AtomicLevel 支持运行期 SetLevel 热切换）
	level := zap.NewAtomicLevelAt(zapcore.DebugLevel)
	if cfg.IsProduction() {
		level = zap.NewAtomicLevelAt(zapcore.InfoLevel)
	}

	jsonEncoder := zapcore.NewJSONEncoder(encoderConfig)
	consoleEncoder := zapcore.NewConsoleEncoder(encoderConfig)
	consoleCore := zapcore.NewCore(consoleEncoder, zapcore.AddSync(os.Stdout), level)

	// 通用日志独立文件，避免与 api/db 日志重复写入
	appWriter := newRotatingWriter(cfg.Log, "app.log")
	apiWriter := newRotatingWriter(cfg.Log, "api.log")
	dbWriter := newRotatingWriter(cfg.Log, "database.log")

	appCore := zapcore.NewCore(jsonEncoder, zapcore.AddSync(appWriter), level)
	apiCore := zapcore.NewCore(jsonEncoder, zapcore.AddSync(apiWriter), level)
	dbCore := zapcore.NewCore(jsonEncoder, zapcore.AddSync(dbWriter), level)

	// 三个 logger 各写自己的文件 + console，互不 Tee
	newLogger := zap.New(
		zapcore.NewTee(appCore, consoleCore),
		zap.AddCaller(), zap.AddCallerSkip(1),
	)
	newAPILog := zap.New(
		zapcore.NewTee(apiCore, consoleCore),
		zap.AddCaller(), zap.AddCallerSkip(1),
	)
	newDBLog := zap.New(
		zapcore.NewTee(dbCore, consoleCore),
		zap.AddCaller(), zap.AddCallerSkip(1),
	)

	// 全部构造成功后再原子替换全局变量，避免半初始化状态。
	// 先发布新 logger，再关闭旧 writer，避免请求 goroutine 在替换窗口内
	// 通过包级函数拿到仍指向已关闭 writer 的旧 logger。
	// H7：四个 logger 经 atomic.Pointer Store，读侧（请求 goroutine）无锁原子 load；
	// fileWriters 仅在 globalMu 下访问。Logger 兼容别名同步维护。
	globalMu.Lock()
	oldWriters := fileWriters
	globalLogGeneration++
	m.generation = globalLogGeneration
	loggerPtr.Store(newLogger)
	sugarPtr.Store(newLogger.Sugar())
	apiLogPtr.Store(newAPILog)
	dbLogPtr.Store(newDBLog)
	Logger = newLogger
	fileWriters = []*lumberjack.Logger{appWriter, apiWriter, dbWriter}
	globalMu.Unlock()

	m.level = level
	m.cfg = cfg

	if closePrevious {
		return closeFileWriters(oldWriters)
	}
	return nil
}

// Init 包级 facade：初始化日志（代理到 DefaultLogger）。
func Init(cfg *config.Config) error {
	return GetDefaultLogManager().Init(cfg)
}

func validateLogConfig(cfg config.LogConfig) error {
	if strings.TrimSpace(cfg.Dir) == "" {
		return errors.New("logger: 日志目录为空")
	}
	if cfg.MaxSize < 0 {
		return errors.New("logger: MaxSize 不能为负数")
	}
	if cfg.MaxBackups < 0 {
		return errors.New("logger: MaxBackups 不能为负数")
	}
	if cfg.MaxAge < 0 {
		return errors.New("logger: MaxAge 不能为负数")
	}
	return nil
}

// newRotatingWriter 创建带 lumberjack 滚动归档的 writer
func newRotatingWriter(cfg config.LogConfig, filename string) *lumberjack.Logger {
	return &lumberjack.Logger{
		Filename:   filepath.Join(cfg.Dir, filename),
		MaxSize:    cfg.MaxSize,
		MaxBackups: cfg.MaxBackups,
		MaxAge:     cfg.MaxAge,
		Compress:   cfg.Compress,
	}
}

// closeFileWriters 关闭传入的 lumberjack writer，并聚合关闭错误。
func closeFileWriters(writers []*lumberjack.Logger) error {
	var errs []error
	for _, w := range writers {
		if w != nil {
			if err := w.Close(); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func syncLoggers(loggers ...*zap.Logger) error {
	var errs []error
	for _, l := range loggers {
		if l == nil {
			continue
		}
		if err := l.Sync(); err != nil && !isHarmlessSyncError(err) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Sync 同步全部 logger 缓冲到底层 writer。
//
// 注意：在 Windows / 部分 *nix 平台上对 stdout/stderr 调用 Sync 会返回
// "invalid argument" / "inappropriate ioctl for device"，属于 zap 已知行为，
// 这里把这类错误识别并忽略，只返回真实的写入失败。
func (m *LogManager) Sync() error {
	return syncLoggers(currentLogger(), currentAPILog(), currentDBLog())
}

// Sync 包级 facade：同步全部 logger 缓冲（代理到 DefaultLogger）。
func Sync() error {
	return GetDefaultLogManager().Sync()
}

// Close 关闭日志文件句柄，重置全局 logger 为 Nop。
// 通常由 App.Shutdown 在 Sync 之后调用；测试场景需要清理临时目录时也应调用。
//
// 调用后再次写日志不会 panic（fall back to nop logger），但不会写入文件。
// 如需重新启用，请再次调用 Init。
func (m *LogManager) Close() error {
	m.mu.Lock()
	globalMu.Lock()
	if m.generation == 0 || m.generation != globalLogGeneration {
		m.level = zap.AtomicLevel{}
		m.cfg = nil
		m.generation = 0
		globalMu.Unlock()
		m.mu.Unlock()
		return nil
	}
	oldLogger := currentLogger()
	oldAPILog := currentAPILog()
	oldDBLog := currentDBLog()
	oldWriters := fileWriters

	// H7：重置为 Nop 经 atomic Store，与读侧一致；Logger 兼容别名同步。
	nop := zap.NewNop()
	loggerPtr.Store(nop)
	sugarPtr.Store(nop.Sugar())
	apiLogPtr.Store(nop)
	dbLogPtr.Store(nop)
	Logger = nop
	fileWriters = nil
	globalMu.Unlock()

	m.level = zap.AtomicLevel{}
	m.cfg = nil
	m.generation = 0
	m.mu.Unlock()

	return errors.Join(
		syncLoggers(oldLogger, oldAPILog, oldDBLog),
		closeFileWriters(oldWriters),
	)
}

// Close 包级 facade：关闭日志文件句柄，重置为 Nop（代理到 DefaultLogger）。
func Close() error {
	return GetDefaultLogManager().Close()
}

// isHarmlessSyncError 识别 stdout/stderr Sync 在不同平台返回的预期错误。
// 这些错误来自 console core，对真实 writer 无影响，可安全忽略。
func isHarmlessSyncError(err error) bool {
	if err == nil {
		return true
	}
	msg := err.Error()
	for _, sub := range []string{
		"invalid argument",               // Linux stdout
		"inappropriate ioctl for device", // macOS stdout
		"bad file descriptor",            // Windows stdout
	} {
		if strings.Contains(msg, sub) {
			return true
		}
	}
	return false
}

// Debug 调试日志
func Debug(msg string, fields ...zap.Field) {
	currentLogger().Debug(msg, fields...)
}

// Info 信息日志
func Info(msg string, fields ...zap.Field) {
	currentLogger().Info(msg, fields...)
}

// Warn 警告日志
func Warn(msg string, fields ...zap.Field) {
	currentLogger().Warn(msg, fields...)
}

// Error 错误日志
func Error(msg string, fields ...zap.Field) {
	currentLogger().Error(msg, fields...)
}

// Fatal 致命错误日志（仅供应用层使用，框架内部禁止调用）
func Fatal(msg string, fields ...zap.Field) {
	currentLogger().Fatal(msg, fields...)
}

// Debugf 格式化调试日志
func Debugf(template string, args ...any) {
	currentSugar().Debugf(template, args...)
}

// Infof 格式化信息日志
func Infof(template string, args ...any) {
	currentSugar().Infof(template, args...)
}

// Warnf 格式化警告日志
func Warnf(template string, args ...any) {
	currentSugar().Warnf(template, args...)
}

// Errorf 格式化错误日志
func Errorf(template string, args ...any) {
	currentSugar().Errorf(template, args...)
}

// Fatalf 格式化致命错误日志（仅供应用层使用，框架内部禁止调用）
func Fatalf(template string, args ...any) {
	currentSugar().Fatalf(template, args...)
}

// APILog 返回 API 专用日志器（写 logs/api.log + console）
func APILog() *zap.Logger {
	return currentAPILog()
}

// DBLog 返回数据库专用日志器（写 logs/database.log + console）
func DBLog() *zap.Logger {
	return currentDBLog()
}
