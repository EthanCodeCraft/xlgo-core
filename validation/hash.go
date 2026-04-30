package validation

import (
	"golang.org/x/crypto/bcrypt"
)

// 默认加密成本（bcrypt 推荐值）
const defaultCost = 12

// HashPassword 对密码进行加密
func HashPassword(password string) (string, error) {
	return HashPasswordWithCost(password, defaultCost)
}

// HashPasswordWithCost 使用指定成本对密码进行加密
// cost 范围: 4-31，值越大越安全但越慢
func HashPasswordWithCost(password string, cost int) (string, error) {
	if cost < bcrypt.MinCost {
		cost = bcrypt.MinCost
	}
	if cost > bcrypt.MaxCost {
		cost = bcrypt.MaxCost
	}

	bytes, err := bcrypt.GenerateFromPassword([]byte(password), cost)
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

// CheckPassword 验证密码是否匹配
// hashedPassword 是加密后的密码，password 是明文密码
func CheckPassword(hashedPassword, password string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(password))
	return err == nil
}

// CheckPasswordAndUpgrade 验证密码并在需要时升级加密成本
// 返回：是否匹配、是否需要升级、升级后的密码、错误
func CheckPasswordAndUpgrade(hashedPassword, password string, targetCost int) (match bool, needUpgrade bool, newHash string, err error) {
	// 验证密码
	err = bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(password))
	if err != nil {
		return false, false, "", err
	}

	// 检查是否需要升级
	cost, err := bcrypt.Cost([]byte(hashedPassword))
	if err != nil {
		return true, false, "", nil
	}

	if cost < targetCost {
		newHash, err = HashPasswordWithCost(password, targetCost)
		if err != nil {
			return true, false, "", err
		}
		return true, true, newHash, nil
	}

	return true, false, "", nil
}

// GetPasswordCost 获取加密密码的成本
func GetPasswordCost(hashedPassword string) (int, error) {
	return bcrypt.Cost([]byte(hashedPassword))
}
