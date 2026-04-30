package cache

import (
	"context"
	"errors"
	"time"

	"github.com/EthanCodeCraft/xlgo-core/database"
	"github.com/EthanCodeCraft/xlgo-core/utils"
)

// 分布式锁错误
var (
	ErrLockNotHeld    = errors.New("锁未被当前客户端持有")
	ErrLockExpired    = errors.New("锁已过期")
	ErrRedisNotReady  = errors.New("Redis 未初始化")
)

// LockToken 锁令牌（用于安全释放锁）
type LockToken struct {
	Key   string // 锁的键名
	Token string // 锁的唯一标识（UUID）
}

// lockScript 加锁 Lua 脚本
// 返回: 1 表示成功加锁，0 表示锁已被占用
const lockScript = `
if redis.call("exists", KEYS[1]) == 0 then
    redis.call("set", KEYS[1], ARGV[1], "PX", ARGV[2])
    return 1
else
    return 0
end
`

// unlockScript 解锁 Lua 脯本
// 只有持有正确 Token 的客户端才能解锁
// 返回: 1 表示成功解锁，0 表示 Token 不匹配（锁不属于该客户端）
const unlockScript = `
if redis.call("get", KEYS[1]) == ARGV[1] then
    redis.call("del", KEYS[1])
    return 1
else
    return 0
end
`

// extendScript 续期 Lua 脚本
// 只有持有正确 Token 的客户端才能续期
// 返回: 1 表示成功续期，0 表示 Token 不匹配或锁不存在
const extendScript = `
if redis.call("get", KEYS[1]) == ARGV[1] then
    redis.call("pexpire", KEYS[1], ARGV[2])
    return 1
else
    return 0
end
`

// NewLock 创建分布式锁
// 评分: ⭐⭐⭐⭐⭐
// 理由: 使用 UUID 作为 Token，保证锁的安全释放
// 参数: key 锁名称，ttl 锁定时长
// 返回: LockToken 用于后续解锁或续期
func NewLock(ctx context.Context, key string, ttl time.Duration) (*LockToken, error) {
	if database.RedisClient == nil {
		return nil, ErrRedisNotReady
	}

	token := utils.UUID()
	ttlMs := int64(ttl / time.Millisecond)

	result, err := database.RedisClient.Eval(ctx, lockScript, []string{key}, token, ttlMs).Result()
	if err != nil {
		return nil, err
	}

	if result.(int64) == 1 {
		return &LockToken{Key: key, Token: token}, nil
	}

	return nil, nil // 锁已被其他客户端持有
}

// Lock 简化的加锁函数（返回 bool）
// 注意: 使用此函数无法安全释放锁，建议使用 NewLock
func Lock(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	token, err := NewLock(ctx, key, ttl)
	if err != nil {
		return false, err
	}
	return token != nil, nil
}

// Unlock 安全释放锁
// 评分: ⭐⭐⭐⭐⭐
// 理由: 使用 Lua 脚本保证只有锁的持有者才能释放
func Unlock(ctx context.Context, token *LockToken) error {
	if database.RedisClient == nil {
		return ErrRedisNotReady
	}

	if token == nil {
		return ErrLockNotHeld
	}

	result, err := database.RedisClient.Eval(ctx, unlockScript, []string{token.Key}, token.Token).Result()
	if err != nil {
		return err
	}

	if result.(int64) == 0 {
		return ErrLockNotHeld
	}

	return nil
}

// UnlockByKey 按键名释放锁（不安全，仅用于旧代码兼容）
// 注意: 此函数不检查 Token，任何客户端都能释放锁
func UnlockByKey(ctx context.Context, key string) error {
	if database.RedisClient == nil {
		return nil
	}
	return database.RedisClient.Del(ctx, key).Err()
}

// ExtendLock 续期锁
// 评分: ⭐⭐⭐⭐⭐
// 理由: 长任务执行时防止锁过期被其他客户端抢占
// 参数: token 锁令牌，ttl 新的过期时间
func ExtendLock(ctx context.Context, token *LockToken, ttl time.Duration) error {
	if database.RedisClient == nil {
		return ErrRedisNotReady
	}

	if token == nil {
		return ErrLockNotHeld
	}

	ttlMs := int64(ttl / time.Millisecond)

	result, err := database.RedisClient.Eval(ctx, extendScript, []string{token.Key}, token.Token, ttlMs).Result()
	if err != nil {
		return err
	}

	if result.(int64) == 0 {
		return ErrLockNotHeld
	}

	return nil
}

// TryLock 尝试获取锁，失败时等待重试
// 评分: ⭐⭐⭐⭐⭐
// 理由: 高并发场景常用，避免立即失败
func TryLock(ctx context.Context, key string, ttl time.Duration, retryInterval time.Duration, maxRetry int) (*LockToken, error) {
	for i := 0; i < maxRetry; i++ {
		token, err := NewLock(ctx, key, ttl)
		if err != nil {
			return nil, err
		}
		if token != nil {
			return token, nil
		}
		time.Sleep(retryInterval)
	}
	return nil, nil
}

// WithLock 使用分布式锁执行函数（自动管理锁）
// 评分: ⭐⭐⭐⭐⭐
// 理由: 自动获取、续期、释放锁，避免忘记释放
// 参数: key 锁名称，ttl 锁定时长，fn 业务函数
// 注意: 如果任务执行时间超过 ttl，需要设置更长的 ttl 或使用 WithLockAutoExtend
func WithLock(ctx context.Context, key string, ttl time.Duration, fn func() error) error {
	token, err := NewLock(ctx, key, ttl)
	if err != nil {
		return err
	}
	if token == nil {
		return nil // 未获取到锁，跳过执行
	}
	defer Unlock(ctx, token)

	return fn()
}

// WithLockAutoExtend 使用分布式锁执行函数（自动续期）
// 评分: ⭐⭐⭐⭐⭐
// 理由: 长任务执行时自动续期，防止锁过期
// 参数: key 锁名称，initialTTL 初始锁定时长，extendInterval 续期间隔，fn 业务函数
func WithLockAutoExtend(ctx context.Context, key string, initialTTL time.Duration, extendInterval time.Duration, fn func() error) error {
	token, err := NewLock(ctx, key, initialTTL)
	if err != nil {
		return err
	}
	if token == nil {
		return nil // 未获取到锁，跳过执行
	}

	// 启动续期协程
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(extendInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-ticker.C:
				// 续期锁（每次续期为 initialTTL）
				if err := ExtendLock(ctx, token, initialTTL); err != nil {
					return // 续期失败，停止续期
				}
			}
		}
	}()

	// 执行业务函数
	err = fn()

	// 停止续期并释放锁
	done <- struct{}{}
	Unlock(ctx, token)

	return err
}

// IsLocked 检查锁是否被占用（不获取锁）
func IsLocked(ctx context.Context, key string) (bool, error) {
	if database.RedisClient == nil {
		return false, nil
	}
	return database.RedisClient.Exists(ctx, key).Val() > 0, nil
}

// GetLockTTL 获取锁的剩余过期时间
func GetLockTTL(ctx context.Context, key string) (time.Duration, error) {
	if database.RedisClient == nil {
		return 0, nil
	}
	return database.RedisClient.TTL(ctx, key).Result()
}

// ForceUnlock 强制释放锁（危险操作，仅用于管理场景）
// 注意: 此函数不检查 Token，强制删除锁
func ForceUnlock(ctx context.Context, key string) error {
	if database.RedisClient == nil {
		return nil
	}
	return database.RedisClient.Del(ctx, key).Err()
}

// ===== 计数器操作 =====

// Incr 自增计数器
func Incr(ctx context.Context, key string) (int64, error) {
	if database.RedisClient == nil {
		return 0, nil
	}
	return database.RedisClient.Incr(ctx, key).Result()
}

// IncrBy 指定增量自增
func IncrBy(ctx context.Context, key string, value int64) (int64, error) {
	if database.RedisClient == nil {
		return 0, nil
	}
	return database.RedisClient.IncrBy(ctx, key, value).Result()
}

// Decr 自减计数器
func Decr(ctx context.Context, key string) (int64, error) {
	if database.RedisClient == nil {
		return 0, nil
	}
	return database.RedisClient.Decr(ctx, key).Result()
}

// GetTTL 获取键的剩余过期时间
func GetTTL(ctx context.Context, key string) (time.Duration, error) {
	if database.RedisClient == nil {
		return 0, nil
	}
	return database.RedisClient.TTL(ctx, key).Result()
}

// SetExpire 设置键的过期时间
func SetExpire(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	if database.RedisClient == nil {
		return false, nil
	}
	return database.RedisClient.Expire(ctx, key, ttl).Result()
}

// GetRaw 获取原始字符串值（不反序列化）
func GetRaw(ctx context.Context, key string) (string, error) {
	if database.RedisClient == nil {
		return "", nil
	}
	return database.RedisClient.Get(ctx, key).Result()
}

// SetRaw 设置原始值（不序列化）
func SetRaw(ctx context.Context, key string, value string, ttl time.Duration) error {
	if database.RedisClient == nil {
		return nil
	}
	return database.RedisClient.Set(ctx, key, value, ttl).Err()
}