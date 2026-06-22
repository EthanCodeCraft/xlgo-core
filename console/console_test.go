package console_test

import (
	"bytes"
	"strings"
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

// TestConsoleLevelFilter 验证显式 level 屏蔽：低于阈值的调用不输出。
// 这是方案 A 的核心契约——用户显式控制何时屏蔽，框架不做隐式行为。
func TestConsoleLevelFilter(t *testing.T) {
	var buf bytes.Buffer
	c := console.New(
		console.WithOutput(&buf),
		console.WithColor(false),
		console.WithTime(false),
		console.WithCaller(false),
		console.WithLevel(console.LevelWarn),
	)

	c.Debug("DEBUG_MARK")
	c.Info("INFO_MARK")
	c.Success("SUCCESS_MARK")
	c.Warn("WARN_MARK")
	c.Error("ERROR_MARK")

	out := buf.String()

	// Warn / Error 必须输出
	if !strings.Contains(out, "WARN_MARK") {
		t.Errorf("Warn should be printed at LevelWarn, got: %q", out)
	}
	if !strings.Contains(out, "ERROR_MARK") {
		t.Errorf("Error should be printed at LevelWarn, got: %q", out)
	}

	// Debug / Info / Success 必须被静默
	for _, mark := range []string{"DEBUG_MARK", "INFO_MARK", "SUCCESS_MARK"} {
		if strings.Contains(out, mark) {
			t.Errorf("%s should be filtered at LevelWarn, but found in: %q", mark, out)
		}
	}
}

// TestConsoleLevelSilent 验证 LevelSilent 完全静默所有调用。
func TestConsoleLevelSilent(t *testing.T) {
	var buf bytes.Buffer
	c := console.New(
		console.WithOutput(&buf),
		console.WithColor(false),
		console.WithLevel(console.LevelSilent),
	)

	c.Debug("D")
	c.Info("I")
	c.Success("S")
	c.Warn("W")
	c.Error("E")

	if buf.Len() != 0 {
		t.Errorf("LevelSilent should suppress all output, got %d bytes: %q", buf.Len(), buf.String())
	}
}

// TestConsoleSetLevel 验证运行期热切换 level。
func TestConsoleSetLevel(t *testing.T) {
	var buf bytes.Buffer
	c := console.New(
		console.WithOutput(&buf),
		console.WithColor(false),
		console.WithTime(false),
		console.WithCaller(false),
	)

	// 默认 LevelDebug，Debug 应输出
	c.Debug("FIRST")
	if !strings.Contains(buf.String(), "FIRST") {
		t.Errorf("Debug should print at default LevelDebug, got: %q", buf.String())
	}

	buf.Reset()

	// 切到 LevelError 后，Debug 应静默
	c.SetLevel(console.LevelError)
	if got := c.Level(); got != console.LevelError {
		t.Errorf("Level() = %v, want LevelError", got)
	}
	c.Debug("SECOND")
	if buf.Len() != 0 {
		t.Errorf("Debug should be filtered after SetLevel(LevelError), got: %q", buf.String())
	}

	// Error 仍然输出
	c.Error("THIRD")
	if !strings.Contains(buf.String(), "THIRD") {
		t.Errorf("Error should print at LevelError, got: %q", buf.String())
	}
}

// TestConsolePackageLevelAPI 验证包级 SetLevel / GetLevel 操作的是 Default 实例。
func TestConsolePackageLevelAPI(t *testing.T) {
	original := console.GetLevel()
	t.Cleanup(func() { console.SetLevel(original) })

	console.SetLevel(console.LevelWarn)
	if got := console.GetLevel(); got != console.LevelWarn {
		t.Errorf("GetLevel() = %v, want LevelWarn", got)
	}
	if got := console.Default.Level(); got != console.LevelWarn {
		t.Errorf("Default.Level() = %v, want LevelWarn (package SetLevel must affect Default)", got)
	}
}

// TestConsoleLevelString 验证 Level.String 输出可读名称（错误信息 / 日志会用到）。
func TestConsoleLevelString(t *testing.T) {
	cases := map[console.Level]string{
		console.LevelDebug:   "Debug",
		console.LevelInfo:    "Info",
		console.LevelSuccess: "Success",
		console.LevelWarn:    "Warn",
		console.LevelError:   "Error",
		console.LevelSilent:  "Silent",
	}
	for l, want := range cases {
		if got := l.String(); got != want {
			t.Errorf("Level(%d).String() = %q, want %q", l, got, want)
		}
	}
}
