package jwt_test

import (
	"testing"
	"time"

	"github.com/EthanCodeCraft/xlgo-core/config"
	"github.com/EthanCodeCraft/xlgo-core/jwt"
)

func setupTestConfig() {
	// 设置测试配置
	cfg := &config.Config{
		JWT: config.JWTConfig{
			Secret: "test-secret-key-12345",
			Expire: 3600, // 1小时
		},
	}
	config.Set(cfg)
}

func TestGenerateToken(t *testing.T) {
	setupTestConfig()

	token, err := jwt.GenerateToken(1, "testuser", "admin", "super_admin")
	if err != nil {
		t.Fatalf("GenerateToken error: %v", err)
	}

	if token == "" {
		t.Error("GenerateToken should return non-empty token")
	}

	// Token 应包含三部分（用 . 分隔）
	parts := splitToken(token)
	if len(parts) != 3 {
		t.Errorf("Token should have 3 parts, got %d", len(parts))
	}
}

func TestParseToken(t *testing.T) {
	setupTestConfig()

	// 先生成 token
	token, _ := jwt.GenerateToken(1, "testuser", "admin", "super_admin")

	// 解析 token
	claims, err := jwt.ParseToken(token)
	if err != nil {
		t.Fatalf("ParseToken error: %v", err)
	}

	if claims.UserID != 1 {
		t.Errorf("UserID = %d, want 1", claims.UserID)
	}
	if claims.Username != "testuser" {
		t.Errorf("Username = %s, want testuser", claims.Username)
	}
	if claims.Role != "admin" {
		t.Errorf("Role = %s, want admin", claims.Role)
	}
	if claims.UserType != "super_admin" {
		t.Errorf("UserType = %s, want super_admin", claims.UserType)
	}
}

func TestParseTokenInvalid(t *testing.T) {
	setupTestConfig()

	// 无效 token
	_, err := jwt.ParseToken("invalid-token")
	if err == nil {
		t.Error("ParseToken should fail with invalid token")
	}

	// 空 token
	_, err = jwt.ParseToken("")
	if err == nil {
		t.Error("ParseToken should fail with empty token")
	}
}

func TestParseTokenWrongSecret(t *testing.T) {
	setupTestConfig()

	// 用不同 secret 生成的 token
	token, _ := jwt.GenerateToken(1, "test", "admin", "admin")

	// 修改 secret
	cfg := &config.Config{
		JWT: config.JWTConfig{
			Secret: "different-secret",
			Expire: 3600,
		},
	}
	config.Set(cfg)

	// 应该解析失败
	_, err := jwt.ParseToken(token)
	if err == nil {
		t.Error("ParseToken should fail with wrong secret")
	}
}

func TestRefreshToken(t *testing.T) {
	setupTestConfig()

	// 生成 token
	token, _ := jwt.GenerateToken(1, "testuser", "admin", "super_admin")

	// 刷新 token
	newToken, err := jwt.RefreshToken(token)
	if err != nil {
		t.Fatalf("RefreshToken error: %v", err)
	}

	if newToken == "" {
		t.Error("RefreshToken should return non-empty token")
	}

	// 新 token 应可解析
	claims, err := jwt.ParseToken(newToken)
	if err != nil {
		t.Fatalf("ParseToken new token error: %v", err)
	}

	if claims.Username != "testuser" {
		t.Error("RefreshToken claims should match original")
	}
}

func TestClaimsStructure(t *testing.T) {
	claims := jwt.Claims{
		UserID:   1,
		Username: "test",
		Role:     "admin",
		UserType: "super_admin",
	}

	if claims.UserID != 1 {
		t.Error("Claims UserID failed")
	}
	if claims.Username != "test" {
		t.Error("Claims Username failed")
	}
	if claims.Role != "admin" {
		t.Error("Claims Username failed")
	}
	if claims.UserType != "super_admin" {
		t.Error("Claims Username failed")
	}
}

func TestErrorDefinitions(t *testing.T) {
	if jwt.ErrTokenExpired == nil {
		t.Error("ErrTokenExpired should be defined")
	}
	if jwt.ErrTokenInvalid == nil {
		t.Error("ErrTokenInvalid should be defined")
	}
	if jwt.ErrTokenMalformed == nil {
		t.Error("ErrTokenMalformed should be defined")
	}
	if jwt.ErrTokenNotValidYet == nil {
		t.Error("ErrTokenNotValidYet should be defined")
	}
}

func TestTokenBlacklist(t *testing.T) {
	tb := jwt.TokenBlacklist{}

	// 无 Redis 时，Add 应返回 nil
	err := tb.Add("test-token", time.Now().Add(time.Hour))
	if err != nil {
		t.Errorf("TokenBlacklist.Add without Redis should return nil, got %v", err)
	}

	// 无 Redis 时，IsBlacklisted 应返回 false
	if tb.IsBlacklisted("test-token") {
		t.Error("TokenBlacklist.IsBlacklisted without Redis should return false")
	}
}

func TestInvalidateToken(t *testing.T) {
	setupTestConfig()

	token, _ := jwt.GenerateToken(1, "test", "admin", "admin")

	// 无 Redis 时应返回 nil
	err := jwt.InvalidateToken(token)
	if err != nil {
		t.Errorf("InvalidateToken without Redis should return nil, got %v", err)
	}
}

func splitToken(token string) []string {
	count := 0
	for _, c := range token {
		if c == '.' {
			count++
		}
	}
	if count != 2 {
		return []string{}
	}

	start := 0
	result := make([]string, 0, 3)
	for i, c := range token {
		if c == '.' {
			result = append(result, token[start:i])
			start = i + 1
		}
	}
	result = append(result, token[start:])
	return result
}
