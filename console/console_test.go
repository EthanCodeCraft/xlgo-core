package console_test

import (
	"testing"

	"github.com/EthanCodeCraft/xlgo-core/console"
)

func TestConsole(t *testing.T) {
	// 使用默认控制台
	console.Debug("这是一条调试信息")
	console.Info("这是一条普通信息")
	console.Success("这是一条成功信息")
	console.Warn("这是一条警告信息")
	console.Error("这是一条错误信息")

	// 创建自定义控制台
	c := console.New(
		console.WithColor(true),
		console.WithTime(true),
		console.WithCaller(true, 2),
	)
	c.Debug("自定义控制台 - Debug")
	c.Info("自定义控制台 - Info")
	c.Success("自定义控制台 - Success")
	c.Warn("自定义控制台 - Warn")
	c.Error("自定义控制台 - Error")
}
