package utils

import (
	"regexp"
)

// IsPhone 检查是否为有效的中国大陆手机号
// 注意: 正则基于当前号段，新号段开放时需更新
func IsPhone(phone string) bool {
	// 1开头，第二位为3-9，共11位
	pattern := `^1[3-9]\d{9}$`
	matched, _ := regexp.MatchString(pattern, phone)
	return matched
}

// IsEmail 检查是否为有效的邮箱地址
func IsEmail(email string) bool {
	// 简单邮箱验证：xxx@xxx.xxx
	pattern := `^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`
	matched, _ := regexp.MatchString(pattern, email)
	return matched
}

// IsIPv4 检查是否为有效的 IPv4 地址
func IsIPv4(ip string) bool {
	pattern := `^(\d{1,3}\.){3}\d{1,3}$`
	matched, _ := regexp.MatchString(pattern, ip)
	if !matched {
		return false
	}
	// 验证每个段在 0-255 范围内
	parts := splitByDot(ip)
	for _, part := range parts {
		n := ToInt(part)
		if n < 0 || n > 255 {
			return false
		}
	}
	return true
}

// IsIDCard 检查是否为有效的中国身份证号（18位）
// 注意: 仅校验格式，不校验校验位
func IsIDCard(id string) bool {
	// 18位身份证：6位地区码 + 8位生日 + 3位顺序码 + 1位校验码
	pattern := `^\d{6}(19|20)\d{2}(0[1-9]|1[0-2])(0[1-9]|[12]\d|3[01])\d{3}[\dXx]$`
	matched, _ := regexp.MatchString(pattern, id)
	return matched
}

// IsChinese 检查字符串是否全部为中文字符
func IsChinese(s string) bool {
	for _, r := range s {
		if r < 0x4E00 || r > 0x9FFF {
			return false
		}
	}
	return len(s) > 0
}

// HasChinese 检查字符串是否包含中文字符
func HasChinese(s string) bool {
	for _, r := range s {
		if r >= 0x4E00 && r <= 0x9FFF {
			return true
		}
	}
	return false
}

// IsNumeric 检查字符串是否全部为数字
func IsNumeric(s string) bool {
	if len(s) == 0 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// IsAlpha 检查字符串是否全部为字母
func IsAlpha(s string) bool {
	if len(s) == 0 {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
			return false
		}
	}
	return true
}

// IsAlphanumeric 检查字符串是否全部为字母或数字
func IsAlphanumeric(s string) bool {
	if len(s) == 0 {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// 内部函数
func splitByDot(s string) []string {
	var result []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			result = append(result, s[start:i])
			start = i + 1
		}
	}
	result = append(result, s[start:])
	return result
}
