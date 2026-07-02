//go:build windows

package console

import (
	"fmt"
	"os"
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

// printColor 彩色打印。
//
// 修复 M17：原实现对 syscall.Stdout 设置颜色、却把文本写到 c.output——
// 当 c.output 非 stdout（如 WithOutput 指向文件）时，颜色落到错误句柄、文本落文件，
// 二者分裂。现按 c.output 实际类型取句柄：*os.File 用其 Fd()，否则放弃着色只写文本。
func (c *Console) printColor(code, msg string) {
	color := colorMap[code]
	if color == 0 {
		color = 7 // 默认淡灰色
	}

	handle, ok := consoleHandle(c.output)
	if !ok {
		// 非 *os.File（如 bytes.Buffer/文件），无法设置控制台属性，退化为纯文本。
		fmt.Fprintln(c.output, msg)
		return
	}

	proc := kernel32.NewProc("SetConsoleTextAttribute")
	_, _, _ = proc.Call(handle, color)
	fmt.Fprintln(c.output, msg)
	_, _, _ = proc.Call(handle, 7) // 恢复默认颜色
}

// consoleHandle 从 io.Writer 取 Windows 控制台句柄；非 *os.File 返回 (0, false)。
func consoleHandle(w interface{ Write([]byte) (int, error) }) (uintptr, bool) {
	f, ok := w.(*os.File)
	if !ok {
		return 0, false
	}
	fd := f.Fd()
	switch fd {
	case uintptr(syscall.Stdout), uintptr(syscall.Stderr), uintptr(syscall.Stdin):
		return fd, true
	default:
		// 重定向到文件/管道的 *os.File，SetConsoleTextAttribute 无意义，退化为纯文本。
		return 0, false
	}
}

// （M17：原 EnableVirtualTerminal 为死代码且从未被调用，已移除。
// Windows 10+ 默认支持 ANSI/VT 着色；需要 VT 的调用方应自行调 kernel32。）

// 保留 unsafe 引用以备将来 VT 扩展；当前 printColor 不再需要，故显式抑制未用告警。
var _ = unsafe.Sizeof(uintptr(0))
