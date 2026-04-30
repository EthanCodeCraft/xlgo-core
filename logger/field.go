package logger

import (
	"go.uber.org/zap"
)

// Field Zap 字段别名，用于简化日志字段定义
var Field = struct {
	String   func(key string, value string) zap.Field
	Int      func(key string, value int) zap.Field
	Int64    func(key string, value int64) zap.Field
	Bool     func(key string, value bool) zap.Field
	Uint     func(key string, value uint) zap.Field
	Float64  func(key string, value float64) zap.Field
	Duration func(key string, value interface{}) zap.Field
	Error    func(err error) zap.Field
}{
	String:  zap.String,
	Int:     zap.Int,
	Int64:   zap.Int64,
	Bool:    zap.Bool,
	Uint:    zap.Uint,
	Float64: zap.Float64,
	Duration: func(key string, value interface{}) zap.Field {
		switch v := value.(type) {
		case zap.Field:
			return v
		default:
			return zap.Any(key, value)
		}
	},
	Error: func(err error) zap.Field {
			return zap.Error(err)
		},
}
