package utils

import (
	"regexp"
)

// L-B 修复：正则预编译到包级变量，避免 IsPhone/IsEmail/IsIPv4/IsIDCard 每次调用
// regexp.MatchString 重编译（高 QPS 下浪费 CPU）。零行为变化。
var (
	phoneRE  = regexp.MustCompile(`^1[3-9]\d{9}$`)
	emailRE  = regexp.MustCompile(`^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`)
	ipv4RE   = regexp.MustCompile(`^(\d{1,3}\.){3}\d{1,3}$`)
	idCardRE = regexp.MustCompile(`^\d{6}(19|20)\d{2}(0[1-9]|1[0-2])(0[1-9]|[12]\d|3[01])\d{3}[\dXx]$`)
)

// IsPhone 检查是否为有效的中国大陆手机号
// 注意: 正则基于当前号段，新号段开放时需更新
func IsPhone(phone string) bool {
	return phoneRE.MatchString(phone)
}

// IsEmail 检查是否为有效的邮箱地址
func IsEmail(email string) bool {
	return emailRE.MatchString(email)
}

// IsIPv4 检查是否为有效的 IPv4 地址
func IsIPv4(ip string) bool {
	if !ipv4RE.MatchString(ip) {
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
	return idCardRE.MatchString(id)
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
