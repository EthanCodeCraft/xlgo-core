package logger_test

import (
	"testing"

	"github.com/EthanCodeCraft/xlgo-core/logger"
)

// 注意：logger 需要先初始化才能调用 Info/Debug/Warn/Error
// 这里只测试 Field 函数，不测试日志输出

func TestAPILog(t *testing.T) {
	_ = logger.APILog()
	// 未初始化时为 nil，这是预期行为
}

func TestDBLog(t *testing.T) {
	_ = logger.DBLog()
	// 未初始化时为 nil，这是预期行为
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