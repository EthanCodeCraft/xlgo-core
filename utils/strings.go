package utils

import (
	"unicode"
	"unicode/utf8"
)

// IsBlank 检查字符串是否为空或仅包含空白字符
func IsBlank(s string) bool {
	if s == "" {
		return true
	}
	for _, r := range s {
		if !unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

// IsNotBlank 检查字符串是否非空且不全是空白字符
func IsNotBlank(s string) bool {
	return !IsBlank(s)
}

// IsAnyBlank 检查多个字符串中是否有任意一个为空或空白
func IsAnyBlank(strs ...string) bool {
	for _, s := range strs {
		if IsBlank(s) {
			return true
		}
	}
	return false
}

// IsAllBlank 检查所有字符串是否都为空或空白
func IsAllBlank(strs ...string) bool {
	for _, s := range strs {
		if IsNotBlank(s) {
			return false
		}
	}
	return true
}

// DefaultIfBlank 如果字符串为空或空白，返回默认值
func DefaultIfBlank(s, def string) string {
	if IsBlank(s) {
		return def
	}
	return s
}

// IsEmpty 检查任意类型是否为空值
// 支持: string, []T, map[K]V, nil, 零值
func IsEmpty(v any) bool {
	if v == nil {
		return true
	}
	switch val := v.(type) {
	case string:
		return val == ""
	case []byte:
		return len(val) == 0
	default:
		return false
	}
}

// Trim 去除字符串首尾的空白字符（包括空格、制表符、换行符）
func Trim(s string) string {
	return trimFunc(s, unicode.IsSpace)
}

// trimFunc 内部修剪函数
func trimFunc(s string, f func(rune) bool) string {
	s = trimLeftFunc(s, f)
	return trimRightFunc(s, f)
}

func trimLeftFunc(s string, f func(rune) bool) string {
	for i, r := range s {
		if !f(r) {
			return s[i:]
		}
	}
	return ""
}

func trimRightFunc(s string, f func(rune) bool) string {
	for i := len(s); i > 0; {
		r, size := utf8.DecodeLastRuneInString(s[:i])
		if !f(r) {
			return s[:i]
		}
		i -= size
	}
	return ""
}

// Substr 截取子字符串（按 Unicode 字符计数）
// 参数: start 起始位置（支持负数从末尾计算），length 截取长度
func Substr(s string, start, length int) string {
	runes := []rune(s)
	lenRunes := len(runes)

	if lenRunes == 0 {
		return ""
	}

	// 处理负数起始位置
	if start < 0 {
		start = lenRunes + start
	}
	if start < 0 {
		start = 0
	}
	if start >= lenRunes {
		return ""
	}

	end := start + length
	if end > lenRunes {
		end = lenRunes
	}
	if end <= start {
		return ""
	}

	return string(runes[start:end])
}

// StrLen 获取字符串的 Unicode 字符数
func StrLen(s string) int {
	return utf8.RuneCountInString(s)
}

// EqualsIgnoreCase 不区分大小写比较字符串
func EqualsIgnoreCase(a, b string) bool {
	if a == b {
		return true
	}
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca := a[i]
		cb := b[i]
		if ca != cb {
			// 转小写比较
			if ca >= 'A' && ca <= 'Z' {
				ca += 32
			}
			if cb >= 'A' && cb <= 'Z' {
				cb += 32
			}
			if ca != cb {
				return false
			}
		}
	}
	return true
}
