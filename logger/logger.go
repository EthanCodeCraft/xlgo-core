package logger

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/EthanCodeCraft/xlgo-core/config"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

var (
	// Logger 全局通用日志实例。Init 之前为 Nop，调用安全。
	Logger = zap.NewNop()
	sugar  = Logger.Sugar()
	apiLog = zap.NewNop()
	dbLog  = zap.NewNop()

	// fileWriters 持有所有 lumberjack 实例引用，
	// Close() 调用时显式释放文件句柄。
	// 必要性：lumberjack 不依赖 GC 关闭文件，进程长跑或测试场景下
	// 不显式关闭会持有句柄导致 Windows 上无法删除日志目录。
	fileWriters []*lumberjack.Logger
)

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
func Init(cfg *config.Config) error {
	if cfg == nil {
		return errors.New("logger: config is nil")
	}

	// 确保日志目录存在
	if err := os.MkdirAll(cfg.Log.Dir, 0o755); err != nil {
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

	// 根据运行模式设置日志级别
	level := zapcore.DebugLevel
	if cfg.IsProduction() {
		level = zapcore.InfoLevel
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
	// 同时关闭旧 writer 释放句柄（重复 Init 场景，主要服务于测试）。
	closeFileWriters()
	Logger = newLogger
	sugar = Logger.Sugar()
	apiLog = newAPILog
	dbLog = newDBLog
	fileWriters = []*lumberjack.Logger{appWriter, apiWriter, dbWriter}

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

// closeFileWriters 关闭并清空当前持有的 lumberjack writer
func closeFileWriters() {
	for _, w := range fileWriters {
		if w != nil {
			_ = w.Close()
		}
	}
	fileWriters = nil
}

// Sync 同步全部 logger 缓冲到底层 writer。
//
// 注意：在 Windows / 部分 *nix 平台上对 stdout/stderr 调用 Sync 会返回
// "invalid argument" / "inappropriate ioctl for device"，属于 zap 已知行为，
// 这里把这类错误识别并忽略，只返回真实的写入失败。
func Sync() error {
	var errs []error
	for _, l := range []*zap.Logger{Logger, apiLog, dbLog} {
		if l == nil {
			continue
		}
		if err := l.Sync(); err != nil && !isHarmlessSyncError(err) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Close 关闭日志文件句柄，重置全局 logger 为 Nop。
// 通常由 App.Shutdown 在 Sync 之后调用；测试场景需要清理临时目录时也应调用。
//
// 调用后再次写日志不会 panic（fall back to nop logger），但不会写入文件。
// 如需重新启用，请再次调用 Init。
func Close() error {
	syncErr := Sync()

	closeFileWriters()

	Logger = zap.NewNop()
	sugar = Logger.Sugar()
	apiLog = zap.NewNop()
	dbLog = zap.NewNop()

	return syncErr
}

// isHarmlessSyncError 识别 stdout/stderr Sync 在不同平台返回的预期错误。
// 这些错误来自 console core，对真实 writer 无影响，可安全忽略。
func isHarmlessSyncError(err error) bool {
	if err == nil {
		return true
	}
	msg := err.Error()
	for _, sub := range []string{
		"invalid argument",                // Linux stdout
		"inappropriate ioctl for device",  // macOS stdout
		"bad file descriptor",             // Windows stdout
	} {
		if strings.Contains(msg, sub) {
			return true
		}
	}
	return false
}

// Debug 调试日志
func Debug(msg string, fields ...zap.Field) {
	Logger.Debug(msg, fields...)
}

// Info 信息日志
func Info(msg string, fields ...zap.Field) {
	Logger.Info(msg, fields...)
}

// Warn 警告日志
func Warn(msg string, fields ...zap.Field) {
	Logger.Warn(msg, fields...)
}

// Error 错误日志
func Error(msg string, fields ...zap.Field) {
	Logger.Error(msg, fields...)
}

// Fatal 致命错误日志（仅供应用层使用，框架内部禁止调用）
func Fatal(msg string, fields ...zap.Field) {
	Logger.Fatal(msg, fields...)
}

// Debugf 格式化调试日志
func Debugf(template string, args ...any) {
	sugar.Debugf(template, args...)
}

// Infof 格式化信息日志
func Infof(template string, args ...any) {
	sugar.Infof(template, args...)
}

// Warnf 格式化警告日志
func Warnf(template string, args ...any) {
	sugar.Warnf(template, args...)
}

// Errorf 格式化错误日志
func Errorf(template string, args ...any) {
	sugar.Errorf(template, args...)
}

// Fatalf 格式化致命错误日志（仅供应用层使用，框架内部禁止调用）
func Fatalf(template string, args ...any) {
	sugar.Fatalf(template, args...)
}

// APILog 返回 API 专用日志器（写 logs/api.log + console）
func APILog() *zap.Logger {
	return apiLog
}

// DBLog 返回数据库专用日志器（写 logs/database.log + console）
func DBLog() *zap.Logger {
	return dbLog
}
