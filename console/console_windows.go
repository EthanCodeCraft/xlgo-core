//go:build windows

package console

import (
	"fmt"
	"syscall"
	"unsafe"
)

var kernel32 = syscall.NewLazyDLL("kernel32.dll")

// 颜色映射：ANSI -> Windows控制台颜色
// 0 黑色, 1 蓝色, 2 绿色, 3 青色, 4 红色, 5 紫色, 6 黄色, 7 淡灰色
// 8 灰色, 9 亮蓝色, 10 亮绿色, 11 亮青色, 12 亮红色, 13 亮紫色, 14 亮黄色, 15 白色
var colorMap = map[string]uintptr{
	"0;36": 11, // 青色 -> 亮青色
	"0;37": 15, // 白色 -> 白色
	"0;92": 10, // 亮绿色
	"1;93": 14, // 亮黄色
	"1;31": 12, // 亮红色
}

// printColor 彩色打印
func (c *Console) printColor(code, msg string) {
	color := colorMap[code]
	if color == 0 {
		color = 7 // 默认淡灰色
	}

	proc := kernel32.NewProc("SetConsoleTextAttribute")
	_, _, _ = proc.Call(uintptr(syscall.Stdout), color)
	fmt.Fprintln(c.output, msg)
	_, _, _ = proc.Call(uintptr(syscall.Stdout), 7) // 恢复默认颜色
}

// EnableVirtualTerminal 启用虚拟终端支持（Windows 10+）
func EnableVirtualTerminal() error {
	// 尝试启用 ANSI 转义序列支持
	proc := kernel32.NewProc("GetConsoleMode")
	var mode uint32
	_, _, err := proc.Call(uintptr(syscall.Stdout), uintptr(unsafe.Pointer(&mode)))
	if err != syscall.Errno(0) {
		return err
	}

	mode |= 0x0004 // ENABLE_VIRTUAL_TERMINAL_PROCESSING
	proc = kernel32.NewProc("SetConsoleMode")
	_, _, err = proc.Call(uintptr(syscall.Stdout), uintptr(mode))
	return err
}
