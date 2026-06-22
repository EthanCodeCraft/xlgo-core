package utils

import (
	"strconv"
)

// ToInt 字符串转 int，失败返回 0
func ToInt(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// ToIntDefault 字符串转 int，失败返回默认值
func ToIntDefault(s string, def int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

// ToInt64 字符串转 int64，失败返回 0
func ToInt64(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

// ToInt64Default 字符串转 int64，失败返回默认值
func ToInt64Default(s string, def int64) int64 {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return def
	}
	return n
}

// ToUint64 字符串转 uint64，失败返回 0
func ToUint64(s string) uint64 {
	n, _ := strconv.ParseUint(s, 10, 64)
	return n
}

// ToUint64Default 字符串转 uint64，失败返回默认值
func ToUint64Default(s string, def uint64) uint64 {
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return def
	}
	return n
}

// ToFloat64 字符串转 float64，失败返回 0
func ToFloat64(s string) float64 {
	n, _ := strconv.ParseFloat(s, 64)
	return n
}

// ToFloat64Default 字符串转 float64，失败返回默认值
func ToFloat64Default(s string, def float64) float64 {
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return def
	}
	return n
}

// ToString 整数转字符串
func ToString(n int) string {
	return strconv.Itoa(n)
}

// ToString64 int64 转字符串
func ToString64(n int64) string {
	return strconv.FormatInt(n, 10)
}

// CalcPageCount 计算总页数
func CalcPageCount(total, pageSize int64) int64 {
	if total <= 0 || pageSize <= 0 {
		return 0
	}
	return (total + pageSize - 1) / pageSize
}

// CalcOffset 计算分页偏移量
func CalcOffset(page, pageSize int) int {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	return (page - 1) * pageSize
}
