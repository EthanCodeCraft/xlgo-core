package validation

import (
	"strconv"
	"unicode"
)

// PasswordConfig 密码验证配置
type PasswordConfig struct {
	MinLength      int  // 最小长度
	MaxLength      int  // 最大长度
	RequireUpper   bool // 需要大写字母
	RequireLower   bool // 需要小写字母
	RequireDigit   bool // 需要数字
	RequireSpecial bool // 需要特殊字符
}

// DefaultPasswordConfig 默认密码配置
var DefaultPasswordConfig = PasswordConfig{
	MinLength:      8,   // 最少8位
	MaxLength:      128, // 最多128位
	RequireUpper:   true,
	RequireLower:   true,
	RequireDigit:   true,
	RequireSpecial: false,
}

// ValidatePassword 验证密码强度
// 返回：是否有效，错误信息
func ValidatePassword(password string) (bool, string) {
	return ValidatePasswordWithConfig(password, DefaultPasswordConfig)
}

// ValidatePasswordWithConfig 使用指定配置验证密码强度
func ValidatePasswordWithConfig(password string, config PasswordConfig) (bool, string) {
	length := len(password)

	// 检查最小长度
	if length < config.MinLength {
		return false, "密码长度不能少于" + strconv.Itoa(config.MinLength) + "位"
	}

	// 检查最大长度
	if length > config.MaxLength {
		return false, "密码长度不能超过" + strconv.Itoa(config.MaxLength) + "位"
	}

	var hasUpper, hasLower, hasDigit, hasSpecial bool

	for _, char := range password {
		switch {
		case unicode.IsUpper(char):
			hasUpper = true
		case unicode.IsLower(char):
			hasLower = true
		case unicode.IsDigit(char):
			hasDigit = true
		case unicode.IsPunct(char) || unicode.IsSymbol(char):
			hasSpecial = true
		}
	}

	// 检查大写字母
	if config.RequireUpper && !hasUpper {
		return false, "密码必须包含大写字母"
	}

	// 检查小写字母
	if config.RequireLower && !hasLower {
		return false, "密码必须包含小写字母"
	}

	// 检查数字
	if config.RequireDigit && !hasDigit {
		return false, "密码必须包含数字"
	}

	// 检查特殊字符
	if config.RequireSpecial && !hasSpecial {
		return false, "密码必须包含特殊字符"
	}

	return true, ""
}
