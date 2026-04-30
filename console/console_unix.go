//go:build linux || darwin

package console

import (
	"fmt"
)

// printColor 彩色打印（使用ANSI转义序列）
func (c *Console) printColor(code, msg string) {
	// \033[ 是ANSI转义序列起始
	// %sm 是颜色代码
	// \033[0m 是重置颜色
	fmt.Fprintf(c.output, "\033[%sm%s\033[0m\n", code, msg)
}
