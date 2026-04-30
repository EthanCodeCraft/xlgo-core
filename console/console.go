package console

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"time"
)

// Level 日志级别
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelSuccess
	LevelWarn
	LevelError
)

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

// Console 控制台打印器
type Console struct {
	output    io.Writer
	isColor   bool
	showTime  bool
	showCall  bool
	timeFmt   string
	skipCall  int
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

// WithCaller 设置是否显示调用位置
func WithCaller(show bool, skip int) Option {
	return func(c *Console) {
		c.showCall = show
		c.skipCall = skip
	}
}

// WithTimeFormat 设置时间格式
func WithTimeFormat(fmt string) Option {
	return func(c *Console) {
		c.timeFmt = fmt
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
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Default 默认控制台
var Default = New()

// print 内部打印函数
func (c *Console) print(level Level, s ...any) {
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

	// 输出
	if c.isColor {
		c.printColor(color.Code, sb.String())
	} else {
		fmt.Fprintln(c.output, sb.String())
	}
}

// getCaller 获取调用位置
func (c *Console) getCaller() string {
	_, file, line, ok := runtime.Caller(c.skipCall)
	if !ok {
		return ""
	}
	// 只取文件名
	idx := strings.LastIndex(file, "/")
	if idx >= 0 {
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
