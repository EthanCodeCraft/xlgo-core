package logger_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EthanCodeCraft/xlgo-core/config"
	"github.com/EthanCodeCraft/xlgo-core/logger"
)

// 注意：logger 需要先初始化才能调用 Info/Debug/Warn/Error
// 这里只测试 Field 函数，不测试日志输出

func TestAPILog(t *testing.T) {
	_ = logger.APILog()
	// 未初始化时为 nop logger，调用安全
}

func TestDBLog(t *testing.T) {
	_ = logger.DBLog()
	// 未初始化时为 nop logger，调用安全
}

func TestFieldString(t *testing.T) {
	field := logger.Field.String("key", "value")
	if field.Key != "key" {
		t.Error("Field.String key failed")
	}
}

func TestFieldInt(t *testing.T) {
	field := logger.Field.Int("count", 123)
	if field.Key != "count" {
		t.Error("Field.Int key failed")
	}
}

func TestFieldInt64(t *testing.T) {
	field := logger.Field.Int64("id", 1234567890)
	if field.Key != "id" {
		t.Error("Field.Int64 key failed")
	}
}

func TestFieldBool(t *testing.T) {
	field := logger.Field.Bool("enabled", true)
	if field.Key != "enabled" {
		t.Error("Field.Bool key failed")
	}
}

func TestFieldUint(t *testing.T) {
	field := logger.Field.Uint("uint_val", 100)
	if field.Key != "uint_val" {
		t.Error("Field.Uint key failed")
	}
}

func TestFieldFloat64(t *testing.T) {
	field := logger.Field.Float64("price", 99.99)
	if field.Key != "price" {
		t.Error("Field.Float64 key failed")
	}
}

func TestFieldError(t *testing.T) {
	_ = logger.Field.Error(nil)
	// zap.Field 是结构体，仅验证函数调用正常
}

// initWithTempDir 使用临时目录初始化 logger，返回日志目录与读取文件内容的辅助函数。
// 验证修复 #3 之后，三个 logger 各自只写自己的文件，互不串扰。
//
// 测试退出时通过 t.Cleanup 调用 logger.Close 释放文件句柄，
// 避免 Windows 上 t.TempDir 因句柄占用而清理失败。
func initWithTempDir(t *testing.T) (dir string, read func(name string) string) {
	t.Helper()
	dir = t.TempDir()
	cfg := &config.Config{
		Server: config.ServerConfig{Mode: "production"}, // 走 InfoLevel，避免 Debug 噪音
		Log: config.LogConfig{
			Dir:        dir,
			MaxSize:    10,
			MaxBackups: 1,
			MaxAge:     1,
			Compress:   false,
		},
	}
	if err := logger.Init(cfg); err != nil {
		t.Fatalf("logger.Init failed: %v", err)
	}
	t.Cleanup(func() { _ = logger.Close() })

	read = func(name string) string {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				return ""
			}
			t.Fatalf("read %s: %v", path, err)
		}
		return string(data)
	}
	return dir, read
}

// TestLoggerNoCrossWriting 验证 Logger / APILog / DBLog 不会重复写入彼此的日志文件。
//
// 这是 v1.0.3 修复的核心：旧实现 Logger = Tee(apiCore, dbCore, consoleCore)，
// 导致 logger.Info(...) 同时写到 api.log + database.log + console 三份。
func TestLoggerNoCrossWriting(t *testing.T) {
	_, read := initWithTempDir(t)

	// 三个标记字符串足够独特，分别由三个 logger 写入
	const (
		appMark = "MARKER_APP_LOG_ONLY_xyz"
		apiMark = "MARKER_API_LOG_ONLY_xyz"
		dbMark  = "MARKER_DB_LOG_ONLY_xyz"
	)

	logger.Info(appMark)
	logger.APILog().Info(apiMark)
	logger.DBLog().Info(dbMark)

	if err := logger.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	app := read("app.log")
	api := read("api.log")
	db := read("database.log")

	// 各自的日志文件必须包含自己的标记
	if !strings.Contains(app, appMark) {
		t.Errorf("app.log missing %q, got: %s", appMark, app)
	}
	if !strings.Contains(api, apiMark) {
		t.Errorf("api.log missing %q, got: %s", apiMark, api)
	}
	if !strings.Contains(db, dbMark) {
		t.Errorf("database.log missing %q, got: %s", dbMark, db)
	}

	// 关键：禁止串写
	if strings.Contains(api, appMark) {
		t.Errorf("api.log should NOT contain %q (cross-writing bug regressed)", appMark)
	}
	if strings.Contains(db, appMark) {
		t.Errorf("database.log should NOT contain %q (cross-writing bug regressed)", appMark)
	}
	if strings.Contains(app, apiMark) {
		t.Errorf("app.log should NOT contain %q", apiMark)
	}
	if strings.Contains(db, apiMark) {
		t.Errorf("database.log should NOT contain %q", apiMark)
	}
	if strings.Contains(app, dbMark) {
		t.Errorf("app.log should NOT contain %q", dbMark)
	}
	if strings.Contains(api, dbMark) {
		t.Errorf("api.log should NOT contain %q", dbMark)
	}
}

// TestLoggerInitNilConfig 验证 Init 对 nil 配置返回错误，不再 panic。
func TestLoggerInitNilConfig(t *testing.T) {
	if err := logger.Init(nil); err == nil {
		t.Error("Init(nil) should return error, got nil")
	}
}

// TestLoggerSyncBeforeInit 验证未初始化时 Sync 不 panic（globals 仍为 Nop）。
func TestLoggerSyncBeforeInit(t *testing.T) {
	// Nop logger 的 Sync 永远返回 nil
	if err := logger.Sync(); err != nil {
		t.Errorf("Sync on nop logger should be nil, got %v", err)
	}
}