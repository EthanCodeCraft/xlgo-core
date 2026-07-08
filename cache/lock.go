package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/EthanCodeCraft/xlgo-core/database"
	"github.com/EthanCodeCraft/xlgo-core/utils"
)

// 分布式锁错误
var (
	ErrLockNotHeld   = errors.New("锁未被当前客户端持有")
	ErrLockExpired   = errors.New("锁已过期")
	ErrRedisNotReady = errors.New("Redis 未初始化")
	// ErrLockNotAcquired 表示锁被其它客户端持有，业务函数未执行。
	ErrLockNotAcquired = errors.New("未获取到锁")
	// ErrInvalidLockTTL 表示锁 TTL 小于 Redis PX 支持的 1ms 粒度或非正。
	ErrInvalidLockTTL = errors.New("锁 TTL 必须大于等于 1ms")
	// ErrInvalidLockRetryInterval 表示重试或续期间隔非法。
	ErrInvalidLockRetryInterval = errors.New("锁重试/续期间隔必须大于 0")
	// ErrLockUnexpectedResult Lua 脚本返回了非预期的结果类型（C1b：裸类型断言防护）。
	ErrLockUnexpectedResult = errors.New("锁脚本返回非预期结果")
	// ErrLockFuncNil 表示分布式锁业务函数为空。
	ErrLockFuncNil = errors.New("锁业务函数不能为空")
)

// toInt64 将 Lua 脚本返回值安全断言为 int64（C1b：禁止裸断言 panic）。
// go-redis 对整数返回 int64，但 nil/错误响应下可能为其他类型。
func toInt64(v any) (int64, error) {
	n, ok := v.(int64)
	if !ok {
		return 0, fmt.Errorf("脚本返回类型 %T: %w", v, ErrLockUnexpectedResult)
	}
	return n, nil
}

func ttlMillis(ttl time.Duration) (int64, error) {
	if ttl < time.Millisecond {
		return 0, ErrInvalidLockTTL
	}
	return int64(ttl / time.Millisecond), nil
}

// LockToken 锁令牌（用于安全释放锁）。
//
// 安全说明（C1d 设计局限）：Token 是随机 UUID（非单调递增的 fencing token）。
// 本实现基于 Redis SET PX + Lua CAS，保证"持有者才能解锁/续期"，但**无法防 TTL 到期后的
// 双 worker 并发**：若 worker A 因 GC/网络停滞超过 TTL，锁过期后 worker B 获得锁，
// A 恢复后仍可能写过期数据。完整的 fencing token 防护需：① 用 Redis INCR 生成单调 token，
// ② 下游存储层（DB/外部服务）记录已见最大 token 并拒绝旧 token 写入。
// 框架无法单方面保证②，需下游配合，故本类型仅提供 UUID token。对 TTL 到期敏感的场景，
// 请确保 ttl >> 业务最长执行时间，或下游实现 fencing token 校验。
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
// 参数: key 锁名称，ttl 锁定时长
// 返回: LockToken 用于后续解锁或续期
func NewLock(ctx context.Context, key string, ttl time.Duration) (*LockToken, error) {
	rdb := database.GetRedis()
	if rdb == nil {
		return nil, ErrRedisNotReady
	}

	token := utils.UUID()
	ttlMs, err := ttlMillis(ttl)
	if err != nil {
		return nil, err
	}

	result, err := rdb.Eval(ctx, lockScript, []string{key}, token, ttlMs).Result()
	if err != nil {
		return nil, err
	}

	n, err := toInt64(result)
	if err != nil {
		return nil, err
	}
	if n == 1 {
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
func Unlock(ctx context.Context, token *LockToken) error {
	rdb := database.GetRedis()
	if rdb == nil {
		return ErrRedisNotReady
	}

	if token == nil {
		return ErrLockNotHeld
	}

	result, err := rdb.Eval(ctx, unlockScript, []string{token.Key}, token.Token).Result()
	if err != nil {
		return err
	}

	n, err := toInt64(result)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrLockNotHeld
	}

	return nil
}

// UnlockByKey 按键名释放锁（不安全，仅用于旧代码兼容）
// 注意: 此函数不检查 Token，任何客户端都能释放锁
func UnlockByKey(ctx context.Context, key string) error {
	rdb := database.GetRedis()
	if rdb == nil {
		return ErrRedisNotReady
	}
	return rdb.Del(ctx, key).Err()
}

// ExtendLock 续期锁
// 参数: token 锁令牌，ttl 新的过期时间
func ExtendLock(ctx context.Context, token *LockToken, ttl time.Duration) error {
	rdb := database.GetRedis()
	if rdb == nil {
		return ErrRedisNotReady
	}

	if token == nil {
		return ErrLockNotHeld
	}

	ttlMs, err := ttlMillis(ttl)
	if err != nil {
		return err
	}

	result, err := rdb.Eval(ctx, extendScript, []string{token.Key}, token.Token, ttlMs).Result()
	if err != nil {
		return err
	}

	n, err := toInt64(result)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrLockNotHeld
	}

	return nil
}

// TryLock 尝试获取锁，失败时等待重试。重试等待响应 ctx 取消（C1c 修复）。
func TryLock(ctx context.Context, key string, ttl time.Duration, retryInterval time.Duration, maxRetry int) (*LockToken, error) {
	if retryInterval <= 0 {
		return nil, ErrInvalidLockRetryInterval
	}
	for i := 0; i < maxRetry; i++ {
		token, err := NewLock(ctx, key, ttl)
		if err != nil {
			return nil, err
		}
		if token != nil {
			return token, nil
		}
		// 响应 ctx 取消，避免最长阻塞 maxRetry*retryInterval（C1c：禁止 time.Sleep 无视 ctx）。
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(retryInterval):
		}
	}
	return nil, ErrLockNotAcquired
}

// WithLock 使用分布式锁执行函数（自动管理锁）。
// 参数: key 锁名称，ttl 锁定时长，fn 业务函数
// 注意: 如果任务执行时间超过 ttl，需要设置更长的 ttl 或使用 WithLockAutoExtend。
//
// 解锁用独立 Background ctx（C1a 一致性修复）：fn 返回或 panic 后，原 ctx 可能已被
// 调用方取消，用其解锁会失败导致锁泄漏到 TTL。fn panic 时 defer 也保证解锁执行。
func WithLock(ctx context.Context, key string, ttl time.Duration, fn func(context.Context) error) error {
	if fn == nil {
		return ErrLockFuncNil
	}
	token, err := NewLock(ctx, key, ttl)
	if err != nil {
		return err
	}
	if token == nil {
		return ErrLockNotAcquired
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = Unlock(unlockCtx, token)
	}()

	return fn(ctx)
}

// WithLockAutoExtend 使用分布式锁执行函数（自动续期）。
// 参数: key 锁名称，initialTTL 初始锁定时长，extendInterval 续期间隔，fn 业务函数
//
// 并发安全说明（C1a 修复）：续期 goroutine 与父用"父关停 + 子 ack"双 channel 协调——
// 父用 close(stop) 通知子退出（close 由唯一所有者执行，安全），子用 close(finished) ack。
// 避免旧实现 done 无缓冲 + 子 defer close(done) + 父 done<-struct{}{} 的 send-on-closed panic
// （ctx 取消或 ExtendLock 失败时 done 已 closed，父再 send 即 panic，Unlock 不执行、锁泄漏到 TTL）。
// Unlock 用 context.Background() 派生超时，避免原 ctx 已取消致 Unlock 失败再泄漏。
func WithLockAutoExtend(ctx context.Context, key string, initialTTL time.Duration, extendInterval time.Duration, fn func(context.Context) error) error {
	if fn == nil {
		return ErrLockFuncNil
	}
	if extendInterval <= 0 {
		return ErrInvalidLockRetryInterval
	}
	token, err := NewLock(ctx, key, initialTTL)
	if err != nil {
		return err
	}
	if token == nil {
		return ErrLockNotAcquired
	}

	// 父关停信号（仅父 close）与子 ack 信号（仅子 close）。
	stop := make(chan struct{})
	finished := make(chan struct{})

	go func() {
		defer close(finished) // 子退出时 ack，父等待 finished
		ticker := time.NewTicker(extendInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-ticker.C:
				// 续期锁（每次续期为 initialTTL）。续期失败则停止续期，fn 应尽快结束。
				if err := ExtendLock(ctx, token, initialTTL); err != nil {
					return
				}
			}
		}
	}()

	// defer 兜底：fn panic 时也要停止续期 goroutine 并释放锁（C1a panic 路径修复）。
	// 无 defer 时 fn panic 会导致 close(stop) 不执行 → 续期 goroutine 永久泄漏，且 Unlock 不执行 → 锁泄漏到 TTL。
	defer func() {
		close(stop)
		<-finished
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = Unlock(unlockCtx, token)
	}()

	// 执行业务函数。fn 必须接收 ctx，避免请求取消后业务 loader/DB/HTTP 调用继续运行。
	err = fn(ctx)

	return err
}

// IsLocked 检查锁是否被占用（不获取锁）。
//
// M-E 修复（失败语义统一）：Redis 未初始化时返回 (false, ErrRedisNotReady) 而非 (false, nil)——
// 后者与"锁确实未被占用"不可区分，调用方可能误以为可获取锁而进入临界区（正确性 bug）。
// 调用方应 errors.Is(err, ErrRedisNotReady) 区分"Redis 不可用"与"锁未占用"。
// L-G 修复：用 .Result() 显式返回 Redis 错误（原 .Val() 吞错致故障被当"未占用"）。
//
// 契约说明：锁操作有正确性影响（无锁进入临界区 = bug），故 Redis 不可用时显式返错；
// 与 cache.Get/Set 等数据操作（性能层、best-effort 静默 no-op）区分。
func IsLocked(ctx context.Context, key string) (bool, error) {
	rdb := database.GetRedis()
	if rdb == nil {
		return false, ErrRedisNotReady
	}
	n, err := rdb.Exists(ctx, key).Result()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// GetLockTTL 获取锁的剩余过期时间。
//
// M-E 修复：Redis 未初始化时返回 (0, ErrRedisNotReady) 而非 (0, nil)，调用方可区分
// "Redis 不可用"与"锁不存在/已过期"（后者 TTL=-1/-2）。
func GetLockTTL(ctx context.Context, key string) (time.Duration, error) {
	rdb := database.GetRedis()
	if rdb == nil {
		return 0, ErrRedisNotReady
	}
	return rdb.TTL(ctx, key).Result()
}

// ForceUnlock 强制释放锁（危险操作，仅用于管理场景）
// 注意: 此函数不检查 Token，强制删除锁
//
// M-E 修复：Redis 未初始化时返回 ErrRedisNotReady 而非 nil——原 nil 让调用方误以为
// 已解锁成功、实则从未操作。管理脚本应据此重试或告警，而非假设成功。
func ForceUnlock(ctx context.Context, key string) error {
	rdb := database.GetRedis()
	if rdb == nil {
		return ErrRedisNotReady
	}
	return rdb.Del(ctx, key).Err()
}

// ===== 计数器操作 =====

// Incr 自增计数器
func Incr(ctx context.Context, key string) (int64, error) {
	rdb := database.GetRedis()
	if rdb == nil {
		return 0, ErrRedisNotReady
	}
	return rdb.Incr(ctx, key).Result()
}

// IncrBy 指定增量自增
func IncrBy(ctx context.Context, key string, value int64) (int64, error) {
	rdb := database.GetRedis()
	if rdb == nil {
		return 0, ErrRedisNotReady
	}
	return rdb.IncrBy(ctx, key, value).Result()
}

// Decr 自减计数器
func Decr(ctx context.Context, key string) (int64, error) {
	rdb := database.GetRedis()
	if rdb == nil {
		return 0, ErrRedisNotReady
	}
	return rdb.Decr(ctx, key).Result()
}

// GetTTL 获取键的剩余过期时间
func GetTTL(ctx context.Context, key string) (time.Duration, error) {
	rdb := database.GetRedis()
	if rdb == nil {
		return 0, ErrRedisNotReady
	}
	return rdb.TTL(ctx, key).Result()
}

// SetExpire 设置键的过期时间
func SetExpire(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	rdb := database.GetRedis()
	if rdb == nil {
		return false, ErrRedisNotReady
	}
	return rdb.Expire(ctx, key, ttl).Result()
}

// GetRaw 获取原始字符串值（不反序列化）
func GetRaw(ctx context.Context, key string) (string, error) {
	rdb := database.GetRedis()
	if rdb == nil {
		return "", ErrRedisNotReady
	}
	return rdb.Get(ctx, key).Result()
}

// SetRaw 设置原始值（不序列化）
func SetRaw(ctx context.Context, key string, value string, ttl time.Duration) error {
	rdb := database.GetRedis()
	if rdb == nil {
		return ErrRedisNotReady
	}
	return rdb.Set(ctx, key, value, ttl).Err()
}
