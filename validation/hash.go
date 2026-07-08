package validation

import (
	"golang.org/x/crypto/bcrypt"
)

// defaultCost 是框架默认 bcrypt cost。
const defaultCost = 12

// normalizeHashCost 将 bcrypt cost 归一化到 bcrypt 支持范围内。
func normalizeHashCost(cost int) int {
	if cost < bcrypt.MinCost {
		return bcrypt.MinCost
	}
	if cost > bcrypt.MaxCost {
		return bcrypt.MaxCost
	}
	return cost
}

// normalizeUpgradeTargetCost 归一化升级目标 cost，异常值回退到框架默认值。
func normalizeUpgradeTargetCost(cost int) int {
	if cost <= 0 || cost > bcrypt.MaxCost {
		return defaultCost
	}
	return normalizeHashCost(cost)
}

// HashPassword 使用框架默认 cost 哈希明文密码。
func HashPassword(password string) (string, error) {
	return HashPasswordWithCost(password, defaultCost)
}

// HashPasswordWithCost 使用指定 cost 哈希明文密码。
//
// 低于 bcrypt.MinCost 的 cost 会提升到 bcrypt.MinCost，高于 bcrypt.MaxCost
// 的 cost 会降到 bcrypt.MaxCost。
func HashPasswordWithCost(password string, cost int) (string, error) {
	cost = normalizeHashCost(cost)

	bytes, err := bcrypt.GenerateFromPassword([]byte(password), cost)
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

// CheckPassword 校验 hashedPassword 是否匹配明文密码。
func CheckPassword(hashedPassword, password string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(password))
	return err == nil
}

// CheckPasswordAndUpgrade 校验密码，并在存量 hash 的 cost 低于归一化后的目标 cost 时重新哈希。
func CheckPasswordAndUpgrade(hashedPassword, password string, targetCost int) (match bool, needUpgrade bool, newHash string, err error) {
	targetCost = normalizeUpgradeTargetCost(targetCost)

	err = bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(password))
	if err != nil {
		return false, false, "", err
	}

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

// GetPasswordCost 返回 hashedPassword 中编码的 bcrypt cost。
func GetPasswordCost(hashedPassword string) (int, error) {
	return bcrypt.Cost([]byte(hashedPassword))
}
