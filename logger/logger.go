package logger

import (
	"os"
	"path/filepath"

	"github.com/EthanCodeCraft/xlgo-core/config"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

var (
	// Logger 全局日志实例
	Logger *zap.Logger
	sugar  *zap.SugaredLogger
	apiLog *zap.Logger
	dbLog  *zap.Logger
)

// Init 初始化日志
func Init(cfg *config.Config) error {
	// 确保日志目录存在
	if err := os.MkdirAll(cfg.Log.Dir, 0755); err != nil {
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
	var level zapcore.Level
	if cfg.IsProduction() {
		level = zapcore.WarnLevel
	} else {
		level = zapcore.DebugLevel
	}

	// API 日志
	apiWriter := &lumberjack.Logger{
		Filename:   filepath.Join(cfg.Log.Dir, "api.log"),
		MaxSize:    cfg.Log.MaxSize,
		MaxBackups: cfg.Log.MaxBackups,
		MaxAge:     cfg.Log.MaxAge,
		Compress:   cfg.Log.Compress,
	}

	// 数据库日志
	dbWriter := &lumberjack.Logger{
		Filename:   filepath.Join(cfg.Log.Dir, "database.log"),
		MaxSize:    cfg.Log.MaxSize,
		MaxBackups: cfg.Log.MaxBackups,
		MaxAge:     cfg.Log.MaxAge,
		Compress:   cfg.Log.Compress,
	}

	// 控制台输出
	consoleEncoder := zapcore.NewConsoleEncoder(encoderConfig)
	consoleWriter := zapcore.AddSync(os.Stdout)

	// 创建核心
	apiCore := zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderConfig),
		zapcore.AddSync(apiWriter),
		level,
	)

	dbCore := zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderConfig),
		zapcore.AddSync(dbWriter),
		level,
	)

	consoleCore := zapcore.NewCore(
		consoleEncoder,
		consoleWriter,
		level,
	)

	// 合并核心
	core := zapcore.NewTee(apiCore, dbCore, consoleCore)

	// 创建日志实例
	Logger = zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1))
	sugar = Logger.Sugar()

	// 创建专用日志实例
	apiLog = zap.New(apiCore, zap.AddCaller(), zap.AddCallerSkip(1))
	dbLog = zap.New(dbCore, zap.AddCaller(), zap.AddCallerSkip(1))

	return nil
}

// Sync 同步日志
func Sync() {
	if Logger != nil {
		_ = Logger.Sync()
	}
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

// Fatal 致命错误日志
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

// Fatalf 格式化致命错误日志
func Fatalf(template string, args ...any) {
	sugar.Fatalf(template, args...)
}

// APILog API 专用日志
func APILog() *zap.Logger {
	return apiLog
}

// DBLog 数据库专用日志
func DBLog() *zap.Logger {
	return dbLog
}
