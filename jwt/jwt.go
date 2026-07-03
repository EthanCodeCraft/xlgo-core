package jwt

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/EthanCodeCraft/xlgo-core/config"
	"github.com/EthanCodeCraft/xlgo-core/database"
	"github.com/EthanCodeCraft/xlgo-core/logger"
	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
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
	// ErrBlacklistUnavailable Redis 未初始化或不可用，黑名单功能失效（C9a 修复）。
	// Add 返回此错误使调用方（RefreshToken/InvalidateToken）感知黑名单不可用并 fail-closed，
	// 避免无 Redis 时静默成功致撤销/刷新失效、新旧 token 双有效。
	// IsBlacklisted 在无 Redis 时仍返回 false（验证侧 fail-open 是无 Redis 部署的固有局限，
	// 文档约束：安全敏感场景必须启用 Redis）。
	ErrBlacklistUnavailable = errors.New("token 黑名单不可用：Redis 未初始化")
	// ErrEmptySecret JWT 密钥为空（P0 修复）。空 secret 意味着以零长度 HMAC 密钥签发/校验，
	// 任何以 "" 签名的 token 都会通过——签发与校验一律 fail-closed 拒绝，杜绝该空密钥绕过。
	ErrEmptySecret = errors.New("jwt.secret 未配置：拒绝签发/校验（防空密钥导致任意 token 通过）")
	// ErrUnsupportedAlgorithm 配置了不支持的签名算法（P0 修复）。
	// 本实现仅支持 HMAC 族（HS256/HS384/HS512）；RS256 等非对称算法暂不支持，
	// 不再静默回退 HS256——避免用户误以为在用非对称算法、实则 HMAC，并助长算法混淆攻击。
	ErrUnsupportedAlgorithm = errors.New("jwt: 不支持的签名算法（仅支持 HS256/HS384/HS512）")
)

// validMethods 允许的签名算法名（HMAC 族）。ParseWithClaims 传 jwt.WithValidMethods 固定算法，
// 防算法混淆（alg confusion，P0）：拒绝 alg=none 及非 HMAC 算法——否则若部署配置为非对称算法，
// 攻击者可用公钥作为 HMAC 密钥伪造 token 通过校验。
var validMethods = []string{"HS256", "HS384", "HS512"}

// secretKey 返回 HMAC 密钥字节；cfg 为 nil 或密钥为空时 fail-closed 返回 ErrEmptySecret（P0）。
func secretKey(cfg *config.Config) ([]byte, error) {
	if cfg == nil || cfg.JWT.Secret == "" {
		return nil, ErrEmptySecret
	}
	return []byte(cfg.JWT.Secret), nil
}

// hmacKeyfunc 构造校验签名方法为 HMAC 族并返回密钥的 jwt.Keyfunc（P0：防算法混淆 + 空密钥）。
// 双重防护：① 断言 token.Method 为 *jwt.SigningMethodHMAC，拒绝非 HMAC（含 none/RS/ES）；
// ② 经 secretKey 拒绝空密钥。配合 ParseWithClaims 的 jwt.WithValidMethods(validMethods) 使用。
func hmacKeyfunc(cfg *config.Config) jwt.Keyfunc {
	return func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("%w: 期望 HMAC，实际 alg=%v", ErrUnsupportedAlgorithm, token.Header["alg"])
		}
		return secretKey(cfg)
	}
}

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

// blacklistOpTimeout 黑名单 Redis 操作的上下文超时（M-A 修复）。
// Redis 客户端已配 ReadTimeout/WriteTimeout=3s（redis.go D7），但鉴权热路径（ParseToken
// 每次调 IsBlacklisted）需更紧边界——显式 ctx 超时把鉴权阻塞上限收敛到 1s，避免 Redis
// 挂起时每个鉴权请求被长时间拖住。健康 Redis 下 Exists/SET 为亚毫秒级，1s 余量充足。
// 注：ctx 超时只约束命令往返，不影响 Set 的服务端 TTL（ttl 可远大于 1s）。
const blacklistOpTimeout = 1 * time.Second

// blacklistCtx 创建带超时的 context 用于黑名单 Redis 操作（M-A）。
func blacklistCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), blacklistOpTimeout)
}

// Add 将 Token 的 JTI 加入黑名单
// 参数: jti JWT ID，expiry Token 过期时间
//
// 无 Redis 时返回 ErrBlacklistUnavailable（C9a 修复）：让调用方（RefreshToken/InvalidateToken）
// 感知黑名单不可用并 fail-closed，避免无 Redis 时静默成功致撤销失效。
func (tb *TokenBlacklist) Add(jti string, expiry time.Time) error {
	client := tb.redisClient()
	if client == nil {
		// Redis 未启用，黑名单不可用——fail-closed 让调用方决策。
		return ErrBlacklistUnavailable
	}

	ctx, cancel := blacklistCtx()
	defer cancel()
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

	ctx, cancel := blacklistCtx()
	defer cancel()
	key := fmt.Sprintf("jwt_bl:%s", jti)
	// M-A 修复：显式处理 Redis 错误（原 .Val() 吞错致故障被静默当"未拉黑"）。
	// 错误时保持 fail-open（返 false），与"无 Redis 部署可用"的固有局限一致，但记录告警
	// 便于运维感知 Redis 故障。安全敏感场景必须启用 Redis（见 ErrBlacklistUnavailable 注释）。
	n, err := client.Exists(ctx, key).Result()
	if err != nil {
		logger.Warn("jwt 黑名单检查失败，fail-open 放行", zap.String("jti", jti), zap.Error(err))
		return false
	}
	return n > 0
}

// Manager JWT 管理器（#10）。持有独立的 TokenBlacklist，
// 支持多实例（如区分 user-token 与 refresh-token 黑名单）。
type Manager struct {
	mu        sync.Mutex
	blacklist *TokenBlacklist
}

// defaultManager 是全局默认 JWT 管理器的真实存储，经 atomic 读写（C9c）。
var defaultManager atomic.Pointer[Manager]

func init() {
	defaultManager.Store(NewJWTManager())
}

// currentManager 返回全局默认 JWT 管理器（atomic 读取，C9c）。
// 正常情况下 init 后永不为 nil；防御性地在极罕见的 nil 情况下回退一个懒取 Redis 的实例。
func currentManager() *Manager {
	if m := defaultManager.Load(); m != nil {
		return m
	}
	m := NewJWTManager()
	defaultManager.Store(m)
	return m
}

// GetDefaultJWT 返回全局默认 JWT 管理器（并发安全，C9c/J1 修复）。
// 替代已删除的 DefaultJWT 包级变量。
func GetDefaultJWT() *Manager {
	return currentManager()
}

// currentBlacklist 返回全局默认 Manager 持有的黑名单（atomic 读取 Manager 后经 Blacklist()，C9c）。
func currentBlacklist() *TokenBlacklist {
	return currentManager().Blacklist()
}

// NewJWTManager 创建 JWT 管理器实例（blacklist 懒取全局 Redis）。
func NewJWTManager() *Manager {
	return &Manager{blacklist: NewTokenBlacklist(nil)}
}

// NewJWTManagerWithRedis 创建 JWT 管理器并注入指定 Redis 客户端（用于多 Redis/测试隔离）。
func NewJWTManagerWithRedis(client *redis.Client) *Manager {
	return &Manager{blacklist: NewTokenBlacklist(client)}
}

// SetDefaultJWTManager 提升指定 Manager 为全局默认（atomic 置换，并发安全，J1 修复）。
func SetDefaultJWTManager(m *Manager) {
	if m == nil {
		return
	}
	defaultManager.Store(m)
}

// Blacklist 返回 Manager 持有的黑名单实例。
func (m *Manager) Blacklist() *TokenBlacklist {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.blacklist
}

// GenerateToken 生成 JWT Token
func GenerateToken(userID uint, username, role, userType string) (string, error) {
	cfg := config.Get()

	// P0：先校验密钥非空与算法受支持（fail-closed）。secretKey 亦守卫 cfg==nil，
	// 通过后 cfg 保证非空，后续访问 cfg.JWT.* 安全。
	key, err := secretKey(cfg)
	if err != nil {
		return "", err
	}
	method, err := signingMethod(cfg.JWT.Algorithm)
	if err != nil {
		return "", err
	}

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

	token := jwt.NewWithClaims(method, claims)
	return token.SignedString(key)
}

// GenerateTokenWithCustomExpiry 生成带自定义过期时间的 Token
func GenerateTokenWithCustomExpiry(userID uint, username, role, userType string, expireSeconds int) (string, error) {
	cfg := config.Get()

	// P0：先校验密钥非空与算法受支持（fail-closed，见 GenerateToken）。
	key, err := secretKey(cfg)
	if err != nil {
		return "", err
	}
	method, err := signingMethod(cfg.JWT.Algorithm)
	if err != nil {
		return "", err
	}

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

	token := jwt.NewWithClaims(method, claims)
	return token.SignedString(key)
}

// issuerOrDefault 返回配置的 issuer，未配置时回退 "xlgo"。
func issuerOrDefault(issuer string) string {
	if issuer == "" {
		return "xlgo"
	}
	return issuer
}

// signingMethod 根据 algorithm 配置返回 HMAC 签名方法。
// 支持 HS256（默认，空值等价）/HS384/HS512；其它值（含 RS256 等非对称算法，暂不支持）
// 返回 ErrUnsupportedAlgorithm，不再静默回退 HS256（P0：防"配 RS256 实得 HMAC"的算法混淆隐患）。
func signingMethod(algorithm string) (jwt.SigningMethod, error) {
	switch strings.ToUpper(strings.TrimSpace(algorithm)) {
	case "", "HS256":
		return jwt.SigningMethodHS256, nil
	case "HS384":
		return jwt.SigningMethodHS384, nil
	case "HS512":
		return jwt.SigningMethodHS512, nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedAlgorithm, algorithm)
	}
}

// ParseToken 解析 JWT Token
func ParseToken(tokenString string) (*Claims, error) {
	cfg := config.Get()

	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, hmacKeyfunc(cfg), jwt.WithValidMethods(validMethods))

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
		if claims.JTI != "" && currentBlacklist().IsBlacklisted(claims.JTI) {
			return nil, ErrTokenRevoked
		}
		return claims, nil
	}

	return nil, ErrTokenInvalid
}

// InvalidateToken 使 Token 失效（加入黑名单）
func InvalidateToken(tokenString string) error {
	cfg := config.Get()

	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, hmacKeyfunc(cfg), jwt.WithValidMethods(validMethods))

	if err != nil {
		// Token 无效或已过期，无需加入黑名单
		return nil
	}

	if claims, ok := token.Claims.(*Claims); ok {
		if claims.JTI != "" && claims.ExpiresAt != nil {
			return currentBlacklist().Add(claims.JTI, claims.ExpiresAt.Time)
		}
	}

	return nil
}

// InvalidateTokenByID 直接通过 JTI 使 Token 失效
// 参数: jti JWT ID，expiry 过期时间
func InvalidateTokenByID(jti string, expiry time.Time) error {
	return currentBlacklist().Add(jti, expiry)
}

// RefreshToken 刷新 Token
//
// 安全约束（C9b 修复）：将旧 Token 加入黑名单的 Add 错误必须向上传播——若 Add 失败
// （Redis 抖动或未启用）仍签发新 token，会导致旧 token 未拉黑、新旧 token 双有效，
// 形成会话固定窗口。故 Add 失败时不签发新 token（fail-closed）。
func RefreshToken(tokenString string) (string, error) {
	claims, err := ParseToken(tokenString)
	if err != nil {
		return "", err
	}

	// 将旧 Token 加入黑名单；失败则不签发新 token（C9b：禁止吞 Add 错误）。
	if claims.JTI != "" && claims.ExpiresAt != nil {
		if err := currentBlacklist().Add(claims.JTI, claims.ExpiresAt.Time); err != nil {
			return "", fmt.Errorf("刷新令牌失败：旧令牌撤销失败: %w", err)
		}
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
	return currentBlacklist().IsBlacklisted(jti)
}

// GetClaimsFromToken 获取 Token 的 Claims（不验证过期）
// 用于获取已过期 Token 的信息
func GetClaimsFromToken(tokenString string) (*Claims, error) {
	cfg := config.Get()

	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, hmacKeyfunc(cfg),
		jwt.WithValidMethods(validMethods), jwt.WithoutClaimsValidation())

	if err != nil {
		return nil, err
	}

	if claims, ok := token.Claims.(*Claims); ok {
		return claims, nil
	}

	return nil, ErrTokenInvalid
}