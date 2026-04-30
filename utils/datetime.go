package utils

import (
	"strconv"
	"time"
)

// NowUnix 返回当前秒级时间戳
// 评分: ⭐⭐⭐⭐
// 理由: 简单封装，语义清晰，避免每次写 time.Now().Unix()
func NowUnix() int64 {
	return time.Now().Unix()
}

// NowTimestamp 返回当前毫秒时间戳
// 评分: ⭐⭐⭐⭐⭐
// 理由: API接口常用毫秒时间戳，封装后更易用
func NowTimestamp() int64 {
	return time.Now().UnixMilli()
}

// FromUnix 秒级时间戳转 time.Time
// 评分: ⭐⭐⭐⭐
// 理由: 时间戳转换常用，封装后语义清晰
func FromUnix(unix int64) time.Time {
	return time.Unix(unix, 0)
}

// FromTimestamp 毫秒时间戳转 time.Time
// 评分: ⭐⭐⭐⭐
// 理由: 毫秒时间戳转换，API交互常用
func FromTimestamp(timestamp int64) time.Time {
	return time.UnixMilli(timestamp)
}

// FormatTime 格式化时间
// 评分: ⭐⭐⭐⭐
// 理由: 常用时间格式化封装，支持自定义布局
func FormatTime(t time.Time, layout string) string {
	return t.Format(layout)
}

// ParseTime 解析时间字符串
// 评分: ⭐⭐⭐⭐
// 理由: 时间解析封装，统一错误处理
func ParseTime(timeStr, layout string) (time.Time, error) {
	return time.Parse(layout, timeStr)
}

// FormatDateTime 格式化为标准日期时间 "2006-01-02 15:04:05"
// 评分: ⭐⭐⭐⭐⭐
// 理由: 预设常用格式，避免记忆 Go 的时间布局
func FormatDateTime(t time.Time) string {
	return t.Format("2006-01-02 15:04:05")
}

// FormatDate 格式化为日期 "2006-01-02"
// 评分: ⭐⭐⭐⭐⭐
// 理由: 预设常用格式
func FormatDate(t time.Time) string {
	return t.Format("2006-01-02")
}

// FormatTimeOnly 格式化为时间 "15:04:05"
// 评分: ⭐⭐⭐⭐⭐
// 理由: 预设常用格式
func FormatTimeOnly(t time.Time) string {
	return t.Format("15:04:05")
}

// StartOfDay 返回指定时间当天的开始时间 (00:00:00)
// 评分: ⭐⭐⭐⭐⭐
// 理由: 统计报表、日期范围查询常用
func StartOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// EndOfDay 返回指定时间当天的结束时间 (23:59:59.999999999)
// 评分: ⭐⭐⭐⭐⭐
// 理由: 统计报表、日期范围查询常用
func EndOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 999999999, t.Location())
}

// StartOfWeek 返回指定时间当周的开始时间（周一为第一天）
// 评分: ⭐⭐⭐⭐
// 理由: 周报表统计常用
func StartOfWeek(t time.Time) time.Time {
	weekday := int(t.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	d := time.Duration(weekday-1) * 24 * time.Hour
	return StartOfDay(t.Add(-d))
}

// StartOfMonth 返回指定时间当月的开始时间
// 评分: ⭐⭐⭐⭐⭐
// 理由: 月度统计常用
func StartOfMonth(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
}

// EndOfMonth 返回指定时间当月的结束时间
// 评分: ⭐⭐⭐⭐⭐
// 理由: 月度统计常用
func EndOfMonth(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month()+1, 0, 23, 59, 59, 999999999, t.Location())
}

// GetDateInt 返回 yyyyMMdd 格式的日期整数
// 评分: ⭐⭐⭐
// 理由: 特定场景使用（如分区键），但返回 int 可能不够通用
func GetDateInt(t time.Time) int {
	ret, _ := strconv.Atoi(t.Format("20060102"))
	return ret
}

// ParseDateInt 将 yyyyMMdd 格式的整数转为时间
// 评分: ⭐⭐⭐
// 理由: GetDateInt 的反向操作
func ParseDateInt(date int) time.Time {
	year := date / 10000
	month := (date % 10000) / 100
	day := date % 100
	return time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.Local)
}
