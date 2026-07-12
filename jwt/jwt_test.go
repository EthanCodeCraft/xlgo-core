package jwt_test

import (
	"errors"
	"testing"
	"time"

	"github.com/EthanCodeCraft/xlgo-core/config"
	"github.com/EthanCodeCraft/xlgo-core/jwt"
	"github.com/alicebob/miniredis/v2"
	gojwt "github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"
)

func setupTestConfig() {
	// 设置测试配置
	cfg := &config.Config{
		JWT: config.JWTConfig{
			Secret: "test-secret-key-1234567890123456789012", // ≥32 字节
			Expire: time.Hour,                                // 1小时
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
	setupMiniRedis(t)

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
			Secret: "different-secret-key-12345678901234567890",
			Expire: 3600,
		},
	}
	if err := config.Set(cfg); err != nil {
		t.Fatalf("Set wrong secret config: %v", err)
	}

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

	// 无 Redis 时 RefreshToken 必须 fail-closed（C9b 修复：旧 token 撤销失败不签发新 token）
	_, err := jwt.RefreshToken(token)
	if !errors.Is(err, jwt.ErrBlacklistUnavailable) {
		t.Errorf("RefreshToken without Redis should fail with ErrBlacklistUnavailable, got %v", err)
	}
}

// setupMiniRedis 启动 miniredis 并注入到 jwt 包级 tokenBlacklist（经 SetDefaultJWTManager）。
// 测试结束 cleanup 还原为无 Redis 的默认 Manager，避免污染后续测试（-shuffle 下尤其关键）。
func setupMiniRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() {
		_ = client.Close()
		// 还原包级 tokenBlacklist 为无 Redis 默认 Manager，避免残留指向已关闭 miniredis 的 client。
		jwt.SetDefaultJWTManager(jwt.NewJWTManager())
	})
	// 用注入 Redis 的 Manager 替换默认，使包级 tokenBlacklist 指向 miniredis。
	jwt.SetDefaultJWTManager(jwt.NewJWTManagerWithRedis(client))
	return mr
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

	// 无 Redis 时，Add 应返回 ErrBlacklistUnavailable（C9a 修复：fail-closed）
	err := tb.Add("test-token", time.Now().Add(time.Hour))
	if !errors.Is(err, jwt.ErrBlacklistUnavailable) {
		t.Errorf("TokenBlacklist.Add without Redis should return ErrBlacklistUnavailable, got %v", err)
	}

	// 无 Redis 时，IsBlacklisted 返回 (false, ErrBlacklistUnavailable)（fail-closed：错误上抛）
	revoked, err := tb.IsBlacklisted("test-token")
	if revoked || !errors.Is(err, jwt.ErrBlacklistUnavailable) {
		t.Errorf("TokenBlacklist.IsBlacklisted without Redis = (%v, %v), want (false, ErrBlacklistUnavailable)", revoked, err)
	}
}

func TestInvalidateToken(t *testing.T) {
	setupTestConfig()

	token, _ := jwt.GenerateToken(1, "test", "admin", "admin")

	// 无 Redis 时应返回 ErrBlacklistUnavailable（C9a 修复：fail-closed，不再静默成功）
	err := jwt.InvalidateToken(token)
	if !errors.Is(err, jwt.ErrBlacklistUnavailable) {
		t.Errorf("InvalidateToken without Redis should return ErrBlacklistUnavailable, got %v", err)
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

// ===== C9b 回归：刷新令牌撤销闭环 =====

// 回归 C9b：RefreshToken 成功后，旧 token 必须被拉黑（ParseToken 返 ErrTokenRevoked），
// 新 token 可用。旧实现丢弃 Add 错误仍签发新 token，旧 token 仍有效（双有效）。
func TestRefreshTokenRevokesOldToken(t *testing.T) {
	setupTestConfig()
	setupMiniRedis(t)

	token, _ := jwt.GenerateToken(1, "testuser", "admin", "super_admin")

	// 刷新
	newToken, err := jwt.RefreshToken(token)
	if err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	if newToken == "" {
		t.Fatal("RefreshToken should return non-empty token")
	}

	// 新 token 可解析
	newClaims, err := jwt.ParseToken(newToken)
	if err != nil {
		t.Fatalf("ParseToken new token: %v", err)
	}
	if newClaims.Username != "testuser" {
		t.Error("new token claims should match original")
	}

	// 旧 token 必须已被拉黑（C9b 核心）
	_, err = jwt.ParseToken(token)
	if !errors.Is(err, jwt.ErrTokenRevoked) {
		t.Errorf("old token after refresh err = %v, want ErrTokenRevoked (C9b: old must be blacklisted)", err)
	}
}

// 回归 C9b：Redis 抖动（Add 失败）时 RefreshToken 必须 fail-closed，不签发新 token。
// 旧实现丢弃 Add 错误仍签发新 token → 旧 token 未拉黑、新旧双有效。
func TestRefreshTokenFailsOnRedisError(t *testing.T) {
	setupTestConfig()
	mr := setupMiniRedis(t)

	token, _ := jwt.GenerateToken(1, "testuser", "admin", "super_admin")

	// 模拟 Redis 抖动：关闭 miniredis，使 Add 的 Set 失败。
	mr.Close()

	_, err := jwt.RefreshToken(token)
	if err == nil {
		t.Fatal("RefreshToken should fail when Redis unavailable (C9b: must not issue new token)")
	}
	// 不应是 ErrBlacklistUnavailable（那是无 Redis 路径），而是 Add 的 Set 错误包装。
	// 关键：未签发新 token，旧 token 未被拉黑（因 Add 失败）。
}

// 回归 C9a/b：InvalidateToken 闭环——登出后 token 被拉黑、ParseToken 返 ErrTokenRevoked。
func TestInvalidateTokenRevokesToken(t *testing.T) {
	setupTestConfig()
	setupMiniRedis(t)

	token, _ := jwt.GenerateToken(1, "test", "admin", "admin")

	// 登出前可解析
	if _, err := jwt.ParseToken(token); err != nil {
		t.Fatalf("ParseToken before invalidate: %v", err)
	}

	// 登出（拉黑）
	if err := jwt.InvalidateToken(token); err != nil {
		t.Fatalf("InvalidateToken: %v", err)
	}

	// 登出后必须被拉黑
	_, err := jwt.ParseToken(token)
	if !errors.Is(err, jwt.ErrTokenRevoked) {
		t.Errorf("ParseToken after invalidate err = %v, want ErrTokenRevoked", err)
	}
}

// 回归 C9a：无 Redis 时 InvalidateTokenByID 返 ErrBlacklistUnavailable（fail-closed）。
func TestInvalidateTokenByIDNoRedis(t *testing.T) {
	setupTestConfig()
	// 不 setupMiniRedis → tokenBlacklist 指向无 Redis 的 Manager
	jwt.SetDefaultJWTManager(jwt.NewJWTManager())
	t.Cleanup(func() { jwt.SetDefaultJWTManager(jwt.NewJWTManager()) }) // 还原基线

	err := jwt.InvalidateTokenByID("some-jti", time.Now().Add(time.Hour))
	if !errors.Is(err, jwt.ErrBlacklistUnavailable) {
		t.Errorf("InvalidateTokenByID without Redis err = %v, want ErrBlacklistUnavailable", err)
	}
}

// ===== P0 回归：算法混淆 / 空密钥 / 不支持算法 =====

// 回归 P0：alg=none 的 token 必须被拒（jwt.WithValidMethods 固定 HMAC 族）。
// 这是算法混淆攻击的一种典型形态——攻击者去掉签名并将 alg 置为 none。
func TestParseTokenRejectsAlgNone(t *testing.T) {
	setupTestConfig()

	claims := jwt.Claims{UserID: 1, Username: "attacker", Role: "admin", UserType: "super_admin"}
	// 构造 alg=none 的未签名 token。
	noneToken := gojwt.NewWithClaims(gojwt.SigningMethodNone, claims)
	signed, err := noneToken.SignedString(gojwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("构造 none token 失败: %v", err)
	}

	if _, err := jwt.ParseToken(signed); err == nil {
		t.Error("ParseToken 必须拒绝 alg=none 的 token（算法混淆防护）")
	}
}

// 回归 P0：空密钥时 GenerateToken/ParseToken 必须 fail-closed 返 ErrEmptySecret，
// 杜绝以零长度 HMAC 密钥签发/校验导致任意 token 通过。
func TestEmptySecretFailsClosed(t *testing.T) {
	config.Set(&config.Config{JWT: config.JWTConfig{Secret: "", Expire: time.Hour}})
	t.Cleanup(setupTestConfig) // 还原基线，避免污染其它测试

	_, err := jwt.GenerateToken(1, "u", "admin", "admin")
	if !errors.Is(err, jwt.ErrEmptySecret) {
		t.Errorf("空密钥 GenerateToken err = %v, want ErrEmptySecret", err)
	}

	// 校验侧：任意 token 在空密钥下都不得通过。
	if _, err := jwt.ParseToken("a.b.c"); err == nil {
		t.Error("空密钥 ParseToken 不得通过任何 token")
	}
}

// 回归 P0：配置不支持的算法（如 RS256）时 GenerateToken 必须返 ErrUnsupportedAlgorithm，
// 不再静默回退 HS256。
func TestUnsupportedAlgorithmFailsClosed(t *testing.T) {
	config.Set(&config.Config{JWT: config.JWTConfig{
		Secret:    "test-secret-key-1234567890123456789012",
		Algorithm: "RS256",
		Expire:    time.Hour,
	}})
	t.Cleanup(setupTestConfig)

	_, err := jwt.GenerateToken(1, "u", "admin", "admin")
	if !errors.Is(err, jwt.ErrUnsupportedAlgorithm) {
		t.Errorf("RS256 GenerateToken err = %v, want ErrUnsupportedAlgorithm", err)
	}
}

// 回归 P0：显式支持的 HS384 仍可正常签发与校验（确认算法固定未误伤合法 HMAC 族）。
func TestSupportedAlgorithmHS384(t *testing.T) {
	config.Set(&config.Config{JWT: config.JWTConfig{
		Secret:    "test-secret-key-1234567890123456789012",
		Algorithm: "HS384",
		Expire:    time.Hour,
	}})
	t.Cleanup(setupTestConfig)
	setupMiniRedis(t)

	token, err := jwt.GenerateToken(1, "u", "admin", "admin")
	if err != nil {
		t.Fatalf("HS384 GenerateToken: %v", err)
	}
	if _, err := jwt.ParseToken(token); err != nil {
		t.Errorf("HS384 ParseToken: %v", err)
	}
}

func TestParseTokenRejectsWrongIssuer(t *testing.T) {
	config.Set(&config.Config{JWT: config.JWTConfig{
		Secret: "test-secret-key-1234567890123456789012",
		Expire: time.Hour,
		Issuer: "trusted-issuer",
	}})
	t.Cleanup(setupTestConfig)

	claims := jwt.Claims{
		UserID:   1,
		Username: "attacker",
		Role:     "admin",
		UserType: "super_admin",
		RegisteredClaims: gojwt.RegisteredClaims{
			ExpiresAt: gojwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  gojwt.NewNumericDate(time.Now()),
			NotBefore: gojwt.NewNumericDate(time.Now()),
			Issuer:    "evil-issuer",
		},
	}
	token := signTokenForTest(t, claims)

	if _, err := jwt.ParseToken(token); !errors.Is(err, jwt.ErrTokenInvalid) {
		t.Errorf("ParseToken wrong issuer err = %v, want ErrTokenInvalid", err)
	}
	if _, err := jwt.GetClaimsFromToken(token); !errors.Is(err, jwt.ErrTokenInvalid) {
		t.Errorf("GetClaimsFromToken wrong issuer err = %v, want ErrTokenInvalid", err)
	}
}

func TestRefreshTokenUsesRefreshExpire(t *testing.T) {
	config.Set(&config.Config{JWT: config.JWTConfig{
		Secret:        "test-secret-key-1234567890123456789012",
		Expire:        time.Hour,
		RefreshExpire: 4 * time.Hour,
	}})
	t.Cleanup(setupTestConfig)
	setupMiniRedis(t)

	token, err := jwt.GenerateToken(1, "testuser", "admin", "super_admin")
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	newToken, err := jwt.RefreshToken(token)
	if err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	claims, err := jwt.ParseToken(newToken)
	if err != nil {
		t.Fatalf("ParseToken refreshed token: %v", err)
	}
	ttl := time.Until(claims.ExpiresAt.Time)
	if ttl < 3*time.Hour || ttl > 4*time.Hour+time.Minute {
		t.Errorf("refreshed token ttl = %v, want about 4h", ttl)
	}
}

func TestGenerateTokenWithCustomExpiryRejectsNonPositive(t *testing.T) {
	setupTestConfig()

	for _, seconds := range []int{0, -1} {
		if _, err := jwt.GenerateTokenWithCustomExpiry(1, "u", "admin", "admin", seconds); !errors.Is(err, jwt.ErrInvalidExpiry) {
			t.Errorf("GenerateTokenWithCustomExpiry(%d) err = %v, want ErrInvalidExpiry", seconds, err)
		}
	}
}

func TestInvalidateTokenByIDRejectsEmptyJTI(t *testing.T) {
	setupTestConfig()

	if err := jwt.InvalidateTokenByID("", time.Now().Add(time.Hour)); !errors.Is(err, jwt.ErrEmptyJTI) {
		t.Errorf("InvalidateTokenByID empty err = %v, want ErrEmptyJTI", err)
	}
}

func TestRefreshTokenRejectsEmptyJTI(t *testing.T) {
	setupTestConfig()
	setupMiniRedis(t)

	token := signTokenForTest(t, jwt.Claims{
		UserID:   1,
		Username: "legacy",
		Role:     "admin",
		UserType: "admin",
		RegisteredClaims: gojwt.RegisteredClaims{
			ExpiresAt: gojwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  gojwt.NewNumericDate(time.Now()),
			NotBefore: gojwt.NewNumericDate(time.Now()),
			Issuer:    "xlgo",
		},
	})

	if _, err := jwt.RefreshToken(token); !errors.Is(err, jwt.ErrEmptyJTI) {
		t.Errorf("RefreshToken empty JTI err = %v, want ErrEmptyJTI", err)
	}
}

func TestInvalidateTokenRejectsEmptyJTI(t *testing.T) {
	setupTestConfig()
	setupMiniRedis(t)

	token := signTokenForTest(t, jwt.Claims{
		UserID:   1,
		Username: "legacy",
		Role:     "admin",
		UserType: "admin",
		RegisteredClaims: gojwt.RegisteredClaims{
			ExpiresAt: gojwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  gojwt.NewNumericDate(time.Now()),
			NotBefore: gojwt.NewNumericDate(time.Now()),
			Issuer:    "xlgo",
		},
	})

	if err := jwt.InvalidateToken(token); !errors.Is(err, jwt.ErrEmptyJTI) {
		t.Errorf("InvalidateToken empty JTI err = %v, want ErrEmptyJTI", err)
	}
}

func TestParseTokenEmptySecretReturnsSentinel(t *testing.T) {
	setupTestConfig()
	token, err := jwt.GenerateToken(1, "u", "admin", "admin")
	if err != nil {
		t.Fatalf("GenerateToken before empty secret: %v", err)
	}

	config.Set(&config.Config{JWT: config.JWTConfig{Secret: "", Expire: time.Hour}})
	t.Cleanup(setupTestConfig)

	if _, err := jwt.ParseToken(token); !errors.Is(err, jwt.ErrEmptySecret) {
		t.Fatalf("ParseToken empty secret err = %v, want ErrEmptySecret", err)
	}
}

func TestParseTokenBlacklistPolicy(t *testing.T) {
	setupTestConfig()
	jwt.SetDefaultJWTManager(jwt.NewJWTManager())
	t.Cleanup(func() { jwt.SetDefaultJWTManager(jwt.NewJWTManager()) })

	token, err := jwt.GenerateToken(1, "test", "admin", "admin")
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if _, err := jwt.ParseToken(token); !errors.Is(err, jwt.ErrBlacklistUnavailable) {
		t.Fatalf("ParseToken default fail-closed should reject without Redis, err = %v, want ErrBlacklistUnavailable", err)
	}
	if _, err := jwt.ParseTokenWithBlacklistPolicy(token, jwt.BlacklistFailOpen); err != nil {
		t.Fatalf("ParseTokenWithBlacklistPolicy fail-open should pass without Redis: %v", err)
	}
}

func TestInvalidateTokenAllowsFutureNotBeforeToken(t *testing.T) {
	setupTestConfig()
	mr := setupMiniRedis(t)

	claims := jwt.Claims{
		UserID:   1,
		Username: "external",
		Role:     "admin",
		UserType: "admin",
		JTI:      "future-jti",
		RegisteredClaims: gojwt.RegisteredClaims{
			ExpiresAt: gojwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  gojwt.NewNumericDate(time.Now()),
			NotBefore: gojwt.NewNumericDate(time.Now().Add(10 * time.Minute)),
			Issuer:    "xlgo",
			ID:        "future-jti",
		},
	}
	token := signTokenForTest(t, claims)

	if err := jwt.InvalidateToken(token); err != nil {
		t.Fatalf("InvalidateToken future nbf: %v", err)
	}
	if !mr.Exists("jwt_bl:future-jti") {
		t.Fatal("future nbf token JTI should be blacklisted")
	}
}

func signTokenForTest(t *testing.T, claims jwt.Claims) string {
	t.Helper()
	token, err := gojwt.NewWithClaims(gojwt.SigningMethodHS256, claims).SignedString([]byte("test-secret-key-1234567890123456789012"))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return token
}
