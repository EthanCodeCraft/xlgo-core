package jwt

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/EthanCodeCraft/xlgo-core/config"
	"github.com/EthanCodeCraft/xlgo-core/database"
	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"
)

// Claims JWT 声明
type Claims struct {
	UserID   uint   `json:"user_id"`
	Username string `json:"username"`
	Role     string `json:"role"`      // admin 或 staff
	UserType string `json:"user_type"` // super_admin, admin, staff
	JTI      string `json:"jti"`       // JWT ID（唯一标识，用于黑名单）
	jwt.RegisteredClaims
}

var (
	//ErrTokenExpired 令牌已过期
	ErrTokenExpired = errors.New("令牌已过期")
	//ErrTokenInvalid 令牌无效
	ErrTokenInvalid = errors.New("令牌无效")
	//ErrTokenMalformed 令牌格式错误
	ErrTokenMalformed = errors.New("令牌格式错误")
	//ErrTokenNotValidYet 令牌尚未生效
	ErrTokenNotValidYet = errors.New("令牌尚未生效")
	//ErrTokenRevoked 令牌已被撤销
	ErrTokenRevoked = errors.New("令牌已被撤销")
)

// generateJTI 生成唯一的 JWT ID
func generateJTI() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("生成 JTI 失败: %w", err)
	}
	return base64.URLEncoding.EncodeToString(bytes), nil
}

// TokenBlacklist Token 黑名单管理（使用 JTI 优化）。
// client 为 nil 时回退到 database.GetRedis()，兼容存量未注入场景。
type TokenBlacklist struct {
	client *redis.Client
}

// NewTokenBlacklist 创建黑名单实例，client 可为 nil（懒取全局 Redis）。
func NewTokenBlacklist(client *redis.Client) *TokenBlacklist {
	return &TokenBlacklist{client: client}
}

func (tb *TokenBlacklist) redisClient() *redis.Client {
	if tb != nil && tb.client != nil {
		return tb.client
	}
	return database.GetRedis()
}

// Add 将 Token 的 JTI 加入黑名单
// 参数: jti JWT ID，expiry Token 过期时间
func (tb *TokenBlacklist) Add(jti string, expiry time.Time) error {
	client := tb.redisClient()
	if client == nil {
		// Redis 未启用，跳过黑名单
		return nil
	}

	ctx := context.Background()
	ttl := time.Until(expiry)
	if ttl <= 0 {
		// Token 已过期，无需加入黑名单
		return nil
	}

	// 使用 JTI 作为键名（约24字节），而非完整 Token（数百字节）
	key := fmt.Sprintf("jwt_bl:%s", jti)
	return client.Set(ctx, key, "1", ttl).Err()
}

// IsBlacklisted 检查 JTI 是否在黑名单中
func (tb *TokenBlacklist) IsBlacklisted(jti string) bool {
	client := tb.redisClient()
	if client == nil {
		// Redis 未启用，不检查黑名单
		return false
	}

	ctx := context.Background()
	key := fmt.Sprintf("jwt_bl:%s", jti)
	return client.Exists(ctx, key).Val() > 0
}

// Manager JWT 管理器（#10）。持有独立的 TokenBlacklist，
// 支持多实例（如区分 user-token 与 refresh-token 黑名单）。
type Manager struct {
	mu        sync.Mutex
	blacklist *TokenBlacklist
}

// DefaultJWT 默认 JWT 管理器，包级 facade 代理到它的 blacklist。
var DefaultJWT = NewJWTManager()

// NewJWTManager 创建 JWT 管理器实例（blacklist 懒取全局 Redis）。
func NewJWTManager() *Manager {
	return &Manager{blacklist: NewTokenBlacklist(nil)}
}

// NewJWTManagerWithRedis 创建 JWT 管理器并注入指定 Redis 客户端（用于多 Redis/测试隔离）。
func NewJWTManagerWithRedis(client *redis.Client) *Manager {
	return &Manager{blacklist: NewTokenBlacklist(client)}
}

// SetDefaultJWTManager 提升指定 Manager 为全局默认。
func SetDefaultJWTManager(m *Manager) {
	if m != nil {
		DefaultJWT = m
		tokenBlacklist = m.blacklist
	}
}

// Blacklist 返回 Manager 持有的黑名单实例。
func (m *Manager) Blacklist() *TokenBlacklist {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.blacklist
}

// 全局黑名单实例（指向 DefaultJWT 的 blacklist，兼容存量包级函数）
var tokenBlacklist = DefaultJWT.blacklist

// GenerateToken 生成 JWT Token
func GenerateToken(userID uint, username, role, userType string) (string, error) {
	cfg := config.Get()

	// 生成唯一的 JWT ID
	jti, err := generateJTI()
	if err != nil {
		return "", err
	}

	claims := Claims{
		UserID:   userID,
		Username: username,
		Role:     role,
		UserType: userType,
		JTI:      jti,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(cfg.JWT.Expire)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			NotBefore: jwt.NewNumericDate(time.Now()),
			Issuer:    issuerOrDefault(cfg.JWT.Issuer),
			ID:        jti, // 同时设置到 RegisteredClaims.ID
		},
	}

	token := jwt.NewWithClaims(signingMethod(cfg.JWT.Algorithm), claims)
	return token.SignedString([]byte(cfg.JWT.Secret))
}

// GenerateTokenWithCustomExpiry 生成带自定义过期时间的 Token
func GenerateTokenWithCustomExpiry(userID uint, username, role, userType string, expireSeconds int) (string, error) {
	cfg := config.Get()

	jti, err := generateJTI()
	if err != nil {
		return "", err
	}

	claims := Claims{
		UserID:   userID,
		Username: username,
		Role:     role,
		UserType: userType,
		JTI:      jti,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Duration(expireSeconds) * time.Second)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			NotBefore: jwt.NewNumericDate(time.Now()),
			Issuer:    issuerOrDefault(cfg.JWT.Issuer),
			ID:        jti,
		},
	}

	token := jwt.NewWithClaims(signingMethod(cfg.JWT.Algorithm), claims)
	return token.SignedString([]byte(cfg.JWT.Secret))
}

// issuerOrDefault 返回配置的 issuer，未配置时回退 "xlgo"。
func issuerOrDefault(issuer string) string {
	if issuer == "" {
		return "xlgo"
	}
	return issuer
}

// signingMethod 根据 algorithm 配置返回 HMAC 签名方法。
// 目前支持 HS256(默认)/HS384/HS512；其它值回退 HS256。
// RS256 等非对称算法需扩展密钥类型，暂不支持。
func signingMethod(algorithm string) jwt.SigningMethod {
	switch strings.ToUpper(algorithm) {
	case "HS384":
		return jwt.SigningMethodHS384
	case "HS512":
		return jwt.SigningMethodHS512
	default:
		return jwt.SigningMethodHS256
	}
}

// ParseToken 解析 JWT Token
func ParseToken(tokenString string) (*Claims, error) {
	cfg := config.Get()

	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (any, error) {
		return []byte(cfg.JWT.Secret), nil
	})

	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrTokenExpired
		}
		if errors.Is(err, jwt.ErrTokenMalformed) {
			return nil, ErrTokenMalformed
		}
		if errors.Is(err, jwt.ErrTokenNotValidYet) {
			return nil, ErrTokenNotValidYet
		}
		return nil, ErrTokenInvalid
	}

	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		// 使用 JTI 检查黑名单（更高效）
		if claims.JTI != "" && tokenBlacklist.IsBlacklisted(claims.JTI) {
			return nil, ErrTokenRevoked
		}
		return claims, nil
	}

	return nil, ErrTokenInvalid
}

// InvalidateToken 使 Token 失效（加入黑名单）
func InvalidateToken(tokenString string) error {
	cfg := config.Get()

	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (any, error) {
		return []byte(cfg.JWT.Secret), nil
	})

	if err != nil {
		// Token 无效或已过期，无需加入黑名单
		return nil
	}

	if claims, ok := token.Claims.(*Claims); ok {
		if claims.JTI != "" && claims.ExpiresAt != nil {
			return tokenBlacklist.Add(claims.JTI, claims.ExpiresAt.Time)
		}
	}

	return nil
}

// InvalidateTokenByID 直接通过 JTI 使 Token 失效
// 参数: jti JWT ID，expiry 过期时间
func InvalidateTokenByID(jti string, expiry time.Time) error {
	return tokenBlacklist.Add(jti, expiry)
}

// RefreshToken 刷新 Token
func RefreshToken(tokenString string) (string, error) {
	claims, err := ParseToken(tokenString)
	if err != nil {
		return "", err
	}

	// 将旧 Token 加入黑名单
	if claims.JTI != "" && claims.ExpiresAt != nil {
		tokenBlacklist.Add(claims.JTI, claims.ExpiresAt.Time)
	}

	return GenerateToken(claims.UserID, claims.Username, claims.Role, claims.UserType)
}

// GetJTI 从 Token 中提取 JTI（不验证签名）
// 用于需要在验证前获取 JTI 的场景
func GetJTI(tokenString string) (string, error) {
	token, _, err := jwt.NewParser().ParseUnverified(tokenString, &Claims{})
	if err != nil {
		return "", err
	}

	if claims, ok := token.Claims.(*Claims); ok {
		return claims.JTI, nil
	}

	return "", ErrTokenInvalid
}

// IsTokenRevoked 检查 Token 是否被撤销（通过 JTI）
func IsTokenRevoked(jti string) bool {
	return tokenBlacklist.IsBlacklisted(jti)
}

// GetClaimsFromToken 获取 Token 的 Claims（不验证过期）
// 用于获取已过期 Token 的信息
func GetClaimsFromToken(tokenString string) (*Claims, error) {
	cfg := config.Get()

	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (any, error) {
		return []byte(cfg.JWT.Secret), nil
	}, jwt.WithoutClaimsValidation())

	if err != nil {
		return nil, err
	}

	if claims, ok := token.Claims.(*Claims); ok {
		return claims, nil
	}

	return nil, ErrTokenInvalid
}