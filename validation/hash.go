package validation

import (
	"golang.org/x/crypto/bcrypt"
)

// defaultCost is the framework default bcrypt cost.
const defaultCost = 12

func normalizeHashCost(cost int) int {
	if cost < bcrypt.MinCost {
		return bcrypt.MinCost
	}
	if cost > bcrypt.MaxCost {
		return bcrypt.MaxCost
	}
	return cost
}

func normalizeUpgradeTargetCost(cost int) int {
	if cost <= 0 || cost > bcrypt.MaxCost {
		return defaultCost
	}
	return normalizeHashCost(cost)
}

// HashPassword hashes a plaintext password with the framework default cost.
func HashPassword(password string) (string, error) {
	return HashPasswordWithCost(password, defaultCost)
}

// HashPasswordWithCost hashes a plaintext password with a bounded bcrypt cost.
// Costs below bcrypt.MinCost are raised to bcrypt.MinCost, and costs above
// bcrypt.MaxCost are lowered to bcrypt.MaxCost.
func HashPasswordWithCost(password string, cost int) (string, error) {
	cost = normalizeHashCost(cost)

	bytes, err := bcrypt.GenerateFromPassword([]byte(password), cost)
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

// CheckPassword verifies whether hashedPassword matches the plaintext password.
func CheckPassword(hashedPassword, password string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(password))
	return err == nil
}

// CheckPasswordAndUpgrade verifies a password and rehashes it when the stored
// cost is lower than the normalized target cost.
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

// GetPasswordCost returns the bcrypt cost encoded in hashedPassword.
func GetPasswordCost(hashedPassword string) (int, error) {
	return bcrypt.Cost([]byte(hashedPassword))
}
