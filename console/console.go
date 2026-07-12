package console

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// writeMu 串行化所有 Console 的写出序列（P1 #7）。
// 用包级锁而非 per-Console 锁：多个 Console 实例可能共享同一底层 writer（如 stdout），
// per-Console 锁无法阻止跨实例交错；且 Windows printColor 的"设色→写→复位"三步必须整体
// 原子，否则并发彩色输出会串色。控制台输出非热路径，单一全局写锁的串行化开销可忽略。
var writeMu sync.Mutex

// Level 日志级别
type Level int32

const (
	// LevelDebug 调试级别（最低）。
	LevelDebug Level = iota
	// LevelInfo 普通信息。
	LevelInfo
	// LevelSuccess 成功信息。
	LevelSuccess
	// LevelWarn 警告。
	LevelWarn
	// LevelError 错误（最高常规级别）。
	LevelError

	// LevelSilent 完全静默：所有调用都不输出
	LevelSilent Level = 127
)

// String 返回级别名称
func (l Level) String() string {
	if c, ok := colors[l]; ok {
		return c.Name
	}
	if l == LevelSilent {
		return "Silent"
	}
	return "Unknown"
}

// Color 颜色定义
type Color struct {
	Code string
	Name string
}

var colors = map[Level]Color{
	LevelDebug:   {Code: "0;36", Name: "Debug"},   // 青色
	LevelInfo:    {Code: "0;37", Name: "Info"},    // 白色
	LevelSuccess: {Code: "0;92", Name: "Success"}, // 亮绿色
	LevelWarn:    {Code: "1;93", Name: "Warn"},    // 亮黄色
	LevelError:   {Code: "1;31", Name: "Error"},   // 亮红色
}

// Console 控制台打印器。
//
// console 包定位：开发期彩色 stdout 工具，跟 fmt.Println 同级。
// 不写文件、不感知运行环境、不做任何隐式 level 切换——
// 所有 level 行为都由调用方显式控制（SetLevel / WithLevel）。
//
// 业务可观测信息（用户登录、订单状态变更等"上线后必须保留的事件"）
// 请使用 logger 包；console 仅用于开发期肉眼调试。
type Console struct {
	output   io.Writer
	isColor  bool
	showTime bool
	showCall bool
	timeFmt  string
	skipCall int

	// level 通过 atomic 访问，支持运行期热切换且并发安全。
	// 用 int32 存储 Level，0 = LevelDebug，与零值默认对齐。
	level atomic.Int32
}

// Option 配置选项
type Option func(*Console)

// WithOutput 设置输出目标
func WithOutput(w io.Writer) Option {
	return func(c *Console) {
		c.output = w
	}
}

// WithColor 设置是否启用颜色
func WithColor(enable bool) Option {
	return func(c *Console) {
		c.isColor = enable
	}
}

// WithTime 设置是否显示时间
func WithTime(show bool) Option {
	return func(c *Console) {
		c.showTime = show
	}
}

// WithCaller 设置是否显示调用位置。
// skip 可选，默认 2（直接调用方）；自封装一层时传 3。
func WithCaller(show bool, skip ...int) Option {
	return func(c *Console) {
		c.showCall = show
		if len(skip) > 0 && skip[0] > 0 {
			c.skipCall = skip[0]
		}
	}
}

// WithTimeFormat 设置时间格式
func WithTimeFormat(fmt string) Option {
	return func(c *Console) {
		c.timeFmt = fmt
	}
}

// WithLevel 设置最低输出级别。低于该级别的调用会被静默丢弃。
//
// 例：WithLevel(LevelWarn) 只输出 Warn 与 Error；
//
//	WithLevel(LevelSilent) 完全静默，常用于压测或上线观察期临时关闭调试输出。
func WithLevel(l Level) Option {
	return func(c *Console) {
		c.level.Store(int32(l))
	}
}

// New 创建控制台打印器
func New(opts ...Option) *Console {
	c := &Console{
		output:   os.Stdout,
		isColor:  true,
		showTime: true,
		showCall: true,
		timeFmt:  "15:04:05.000",
		skipCall: 2,
	}
	// 默认 LevelDebug：开发期所有级别都打印。生产期请显式 SetLevel/WithLevel。
	c.level.Store(int32(LevelDebug))
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// SetLevel 运行期切换最低输出级别。并发安全。
func (c *Console) SetLevel(l Level) {
	c.level.Store(int32(l))
}

// Level 返回当前最低输出级别
func (c *Console) Level() Level {
	return Level(c.level.Load())
}

// Default 默认控制台
var Default = New()

// SetLevel 设置默认控制台的最低输出级别。并发安全。
//
// 典型用法（在 main 中根据 cfg 显式切换）：
//
//	if cfg.IsProduction() {
//	    console.SetLevel(console.LevelWarn) // 生产期只看 Warn / Error
//	}
//
// 框架不会自动根据环境模式切换，选择权完全在调用方。
func SetLevel(l Level) {
	Default.SetLevel(l)
}

// GetLevel 返回默认控制台当前最低输出级别。
// （命名加 Get 前缀是因为 Level 已被类型占用，Go 不允许同名函数。）
func GetLevel() Level {
	return Default.Level()
}

// print 内部打印函数
func (c *Console) print(level Level, s ...any) {
	// 级别过滤：低于阈值或调用方显式 LevelSilent 时直接返回，零开销
	if level < c.Level() {
		return
	}

	var sb strings.Builder

	// 时间
	if c.showTime {
		sb.WriteString(time.Now().Format(c.timeFmt))
		sb.WriteString(" ")
	}

	// 级别名称
	color := colors[level]
	sb.WriteString("[")
	sb.WriteString(color.Name)
	sb.WriteString("] ")

	// 调用位置
	if c.showCall {
		sb.WriteString(c.getCaller())
		sb.WriteString(" ")
	}

	// 内容
	sb.WriteString(fmt.Sprint(s...))

	// 输出。P1 #7：全局写锁串行化整个写序列，避免并发交错输出；
	// Windows 下 printColor 的"设色→写→复位"三步也被此锁整体保护，防止串色。
	writeMu.Lock()
	if c.isColor {
		c.printColor(color.Code, sb.String())
	} else {
		fmt.Fprintln(c.output, sb.String())
	}
	writeMu.Unlock()
}

// getCaller 获取调用位置
func (c *Console) getCaller() string {
	_, file, line, ok := runtime.Caller(c.skipCall)
	if !ok {
		return ""
	}
	// 只取文件名（兼容 / 与 \ 分隔）
	if idx := strings.LastIndexAny(file, "/\\"); idx >= 0 {
		file = file[idx+1:]
	}
	return fmt.Sprintf("%s:%d", file, line)
}

// Debug 打印调试信息（青色）
func (c *Console) Debug(s ...any) {
	c.print(LevelDebug, s...)
}

// Info 打印普通信息（白色）
func (c *Console) Info(s ...any) {
	c.print(LevelInfo, s...)
}

// Success 打印成功信息（绿色）
func (c *Console) Success(s ...any) {
	c.print(LevelSuccess, s...)
}

// Warn 打印警告信息（黄色）
func (c *Console) Warn(s ...any) {
	c.print(LevelWarn, s...)
}

// Error 打印错误信息（红色）
func (c *Console) Error(s ...any) {
	c.print(LevelError, s...)
}

// ===== 包级别便捷函数 =====

// Debug 使用默认控制台打印调试信息
func Debug(s ...any) {
	Default.print(LevelDebug, s...)
}

// Info 使用默认控制台打印普通信息
func Info(s ...any) {
	Default.print(LevelInfo, s...)
}

// Success 使用默认控制台打印成功信息
func Success(s ...any) {
	Default.print(LevelSuccess, s...)
}

// Warn 使用默认控制台打印警告信息
func Warn(s ...any) {
	Default.print(LevelWarn, s...)
}

// Error 使用默认控制台打印错误信息
func Error(s ...any) {
	Default.print(LevelError, s...)
}
