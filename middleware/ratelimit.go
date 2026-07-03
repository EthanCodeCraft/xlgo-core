package middleware

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/EthanCodeCraft/xlgo-core/database"
	"github.com/EthanCodeCraft/xlgo-core/response"
	"github.com/gin-gonic/gin"
)

// RateLimiter 速率限制器（内存版，单实例使用）
type RateLimiter struct {
	visitors map[string]*visitor
	mu       sync.RWMutex
	rate     int           // 每分钟允许的请求数
	window   time.Duration // 时间窗口
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	nowFunc  func() time.Time // 时间源（默认 time.Now，测试可注入可控时钟）
}

type visitor struct {
	// windowStart 当前固定窗口的起点。仅在新窗口开始时设置，放行时不变更（H4a 修复）。
	// 旧实现每次放行都更新 lastSeen，导致 time.Since(lastSeen) > window 重置分支对持续
	// 客户端永不成立、count 单调累加，稳态客户端（低于 rate）被误限流。
	windowStart time.Time
	count       int
}

// NewRateLimiter 创建速率限制器（内存版）
func NewRateLimiter(rate int, window time.Duration) *RateLimiter {
	ctx, cancel := context.WithCancel(context.Background())
	limiter := &RateLimiter{
		visitors: make(map[string]*visitor),
		rate:     rate,
		window:   window,
		ctx:      ctx,
		cancel:   cancel,
		nowFunc:  time.Now,
	}

	limiter.wg.Add(1)
	go limiter.cleanupVisitors()

	return limiter
}

// now 返回当前时间（可被测试注入 nowFunc 覆盖）。
func (rl *RateLimiter) now() time.Time {
	if rl.nowFunc != nil {
		return rl.nowFunc()
	}
	return time.Now()
}

// SetNowFunc 注入时间源（默认 time.Now），供测试用可控时钟验证窗口语义。
// 生产代码通常无需调用。
func (rl *RateLimiter) SetNowFunc(f func() time.Time) {
	rl.mu.Lock()
	rl.nowFunc = f
	rl.mu.Unlock()
}

// Allow 检查是否允许请求。
//
// 固定窗口语义（H4a 修复）：windowStart 是当前窗口起点，仅在窗口过期重置时变更；
// 放行时不再更新 windowStart。窗口内 count 达到 rate 即拒绝，窗口过期则重置为新窗口。
// 旧实现每次放行更新 lastSeen，致重置分支对持续客户端永不成立、稳态客户端被误限流。
//
// 注意：固定窗口算法允许窗口边界突发（两窗口交界处瞬时可达 2×rate）。
// 如需平滑限流（无突发），请用 Redis 版 RedisRateLimiter（滑动窗口）。
func (rl *RateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := rl.now()
	v, exists := rl.visitors[ip]
	if !exists {
		rl.visitors[ip] = &visitor{
			windowStart: now,
			count:       1,
		}
		return true
	}

	// 窗口过期：开新窗口，count 重置为 1。
	if now.Sub(v.windowStart) > rl.window {
		v.windowStart = now
		v.count = 1
		return true
	}

	// 当前窗口内已达上限：拒绝（不更新 windowStart）。
	if v.count >= rl.rate {
		return false
	}

	// 放行：count++，windowStart 不变（固定窗口语义）。
	v.count++
	return true
}

// cleanupVisitors 清理过期的访问者记录
func (rl *RateLimiter) cleanupVisitors() {
	defer rl.wg.Done()

	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-rl.ctx.Done():
			return
		case <-ticker.C:
			rl.mu.Lock()
			for ip, v := range rl.visitors {
				// 窗口起点超 window 未活跃即淘汰（H4a：windowStart 是窗口起点，不再被放行更新）。
				if rl.now().Sub(v.windowStart) > rl.window {
					delete(rl.visitors, ip)
				}
			}
			rl.mu.Unlock()
		}
	}
}

// Stop 停止限流器（释放资源）
func (rl *RateLimiter) Stop() {
	rl.cancel()
	rl.wg.Wait()
}

// ===== Redis 分布式限流器 =====

// H4c: Redis 限流器故障相关错误。
var (
	// ErrRedisRateLimiterUnavailable Redis 未启用（database.GetRedis() == nil）。
	// fail-closed 限流器在此情况下拒绝请求；fail-open 限流器放行。
	ErrRedisRateLimiterUnavailable = errors.New("redis rate limiter: redis client unavailable")
	// ErrRedisRateLimiterUnexpectedResult Redis 返回非预期的结果类型（非 int64）。
	// fail-closed 限流器拒绝；fail-open 限流器放行。旧实现裸断言会 panic。
	ErrRedisRateLimiterUnexpectedResult = errors.New("redis rate limiter: unexpected result type")
)

// RedisRateLimiter Redis 分布式限流器
type RedisRateLimiter struct {
	keyPrefix   string        // 键名前缀
	rate        int           // 每分钟允许的请求数
	window      time.Duration // 时间窗口
	failClosed  atomic.Bool   // H4c/H-6: Redis 错误/断言失败时是否拒绝（true=安全型 fail-closed）。atomic 以支持运行期 SetFailClosed 并发安全切换。
}

// slidingWindowLua 滑动窗口限流 Lua 脚本
// 返回: 当前窗口内的请求数
const slidingWindowLua = `
local key = KEYS[1]
local now = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local rate = tonumber(ARGV[3])

-- 移除窗口外的旧记录
redis.call('ZREMRANGEBYSCORE', key, 0, now - window)

-- 获取当前窗口内的请求数
local count = redis.call('ZCARD', key)

if count < rate then
    -- 添加当前请求
    redis.call('ZADD', key, now, now .. '-' .. math.random())
    redis.call('PEXPIRE', key, window)
    return 0
else
    return count
end
`

// NewRedisRateLimiter 创建 Redis 分布式限流器（默认 fail-open：Redis 错误时放行，避免影响业务）。
// 安全敏感场景（如登录防爆破）应使用 NewRedisRateLimiterFailClosed，Redis 故障时拒绝以防限流失效。
func NewRedisRateLimiter(keyPrefix string, rate int, window time.Duration) *RedisRateLimiter {
	return &RedisRateLimiter{
		keyPrefix: keyPrefix,
		rate:      rate,
		window:    window,
		// failClosed 零值 false（fail-open，兼容默认）
	}
}

// NewRedisRateLimiterFailClosed 创建安全型 Redis 分布式限流器（fail-closed）：
// Redis 不可用/错误/返回非预期类型时拒绝请求，避免限流静默失效（防爆破场景必备）。
func NewRedisRateLimiterFailClosed(keyPrefix string, rate int, window time.Duration) *RedisRateLimiter {
	rl := &RedisRateLimiter{
		keyPrefix: keyPrefix,
		rate:      rate,
		window:    window,
	}
	rl.failClosed.Store(true)
	return rl
}

// SetFailClosed 设置 Redis 故障时的策略：true=拒绝（安全型），false=放行（兼容默认）。
// 供已创建的限流器切换策略。并发安全（atomic.Store），可与并发 Allow 调用共存。
func (rl *RedisRateLimiter) SetFailClosed(failClosed bool) {
	rl.failClosed.Store(failClosed)
}

// Allow 检查是否允许请求。
//
// H4c 修复：
//   - result.(int64) 改 comma-ok，断言失败返 ErrRedisRateLimiterUnexpectedResult 而非 panic。
//   - Redis 错误/断言失败时按 failClosed 策略决定：fail-closed 返 (false, err) 拒绝，
//     fail-open 返 (true, err) 放行（兼容旧行为）。中间件层据此 allowed 值决定放行/拒绝，
//     不再无条件 fail-open——登录防爆破等安全场景用 fail-closed 限流器即可在 Redis 故障时拒绝。
//
// Redis 未启用（database.GetRedis() == nil）时：fail-closed 返 (false, ErrRedisRateLimiterUnavailable)，
// fail-open 返 (true, nil)（兼容旧行为）。安全场景必须确保 Redis 已启用。
func (rl *RedisRateLimiter) Allow(ctx context.Context, identifier string) (bool, error) {
	// M-C 修复：GetRedis() 仅取一次复用。原实现 nil 检查与 Eval 各调一次 GetRedis()，
	// 两次之间若 CloseRedis，第二次返回 nil → nil.Eval panic。
	rdb := database.GetRedis()
	if rdb == nil {
		if rl.failClosed.Load() {
			return false, ErrRedisRateLimiterUnavailable
		}
		return true, nil
	}

	key := rl.keyPrefix + ":" + identifier
	now := float64(time.Now().UnixMilli())
	windowMs := float64(rl.window.Milliseconds())

	result, err := rdb.Eval(ctx, slidingWindowLua, []string{key}, now, windowMs, rl.rate).Result()
	if err != nil {
		if rl.failClosed.Load() {
			return false, err
		}
		return true, err // 出错时允许请求，避免影响业务（兼容旧行为）
	}

	// H4c: comma-ok 断言，避免 Redis 返回非 int64 时 panic。
	count, ok := result.(int64)
	if !ok {
		if rl.failClosed.Load() {
			return false, ErrRedisRateLimiterUnexpectedResult
		}
		return true, ErrRedisRateLimiterUnexpectedResult
	}
	return count == 0, nil
}

// GetCount 获取当前窗口内的请求数
func (rl *RedisRateLimiter) GetCount(ctx context.Context, identifier string) (int64, error) {
	// M-C 修复：GetRedis() 取一次复用（原三次调用存在 nil-deref 窗口）。
	rdb := database.GetRedis()
	if rdb == nil {
		return 0, nil
	}

	key := rl.keyPrefix + ":" + identifier
	now := time.Now().UnixMilli()
	windowStart := now - rl.window.Milliseconds()

	// 移除旧记录并获取当前计数
	rdb.ZRemRangeByScore(ctx, key, "0", fmt.Sprintf("%d", windowStart))
	return rdb.ZCard(ctx, key).Result()
}

// Reset 重置限流计数
func (rl *RedisRateLimiter) Reset(ctx context.Context, identifier string) error {
	// M-C 修复：GetRedis() 取一次复用（原两次调用存在 nil-deref 窗口）。
	rdb := database.GetRedis()
	if rdb == nil {
		return nil
	}

	key := rl.keyPrefix + ":" + identifier
	return rdb.Del(ctx, key).Err()
}

// ===== 全局限速器 =====

var (
	loginLimiter   *RateLimiter
	apiLimiter     *RateLimiter
	uploadLimiter  *RateLimiter
	customLimiters []*RateLimiter // H4b: CustomRateLimit 创建的限流器登记表，供 StopRateLimiters/InitRateLimiters 统一停止，避免 cleanup goroutine 泄漏
	limitersMu     sync.Mutex
)

// drainCustomLimiters 取出已登记的自定义限流器并清空登记表。调用方须持有 limitersMu。
func drainCustomLimiters() []*RateLimiter {
	drained := customLimiters
	customLimiters = nil
	return drained
}

// InitRateLimiters 初始化限速器
func InitRateLimiters() {
	limitersMu.Lock()
	defer limitersMu.Unlock()

	// 先停止旧的限流器（含自定义），释放 cleanup goroutine。
	// 持锁期间调 Stop() 安全：Stop→wg.Wait 等待的 cleanupVisitors 取的是 limiter 自身的 rl.mu，
	// 非 limitersMu，无死锁；全程持锁避免与 LoginRateLimit 等懒初始化路径交错致覆盖泄漏。
	if loginLimiter != nil {
		loginLimiter.Stop()
	}
	if apiLimiter != nil {
		apiLimiter.Stop()
	}
	if uploadLimiter != nil {
		uploadLimiter.Stop()
	}
	for _, l := range drainCustomLimiters() {
		l.Stop()
	}

	// 内存限流器（单实例）
	loginLimiter = NewRateLimiter(10, time.Minute)
	apiLimiter = NewRateLimiter(100, time.Minute)
	uploadLimiter = NewRateLimiter(20, time.Minute)
}

// StopRateLimiters 停止所有限速器（应用关闭时调用）
func StopRateLimiters() {
	limitersMu.Lock()
	defer limitersMu.Unlock()

	if loginLimiter != nil {
		loginLimiter.Stop()
		loginLimiter = nil
	}
	if apiLimiter != nil {
		apiLimiter.Stop()
		apiLimiter = nil
	}
	if uploadLimiter != nil {
		uploadLimiter.Stop()
		uploadLimiter = nil
	}
	for _, l := range drainCustomLimiters() {
		l.Stop()
	}
}

// RateLimit 通用速率限制中间件（内存版）
func RateLimit(limiter *RateLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.ClientIP()
		if !limiter.Allow(ip) {
			response.RateLimit(c)
			c.Abort()
			return
		}
		c.Next()
	}
}

// LoginRateLimit 登录接口速率限制
func LoginRateLimit() gin.HandlerFunc {
	limitersMu.Lock()
	if loginLimiter == nil {
		loginLimiter = NewRateLimiter(10, time.Minute)
	}
	limiter := loginLimiter
	limitersMu.Unlock()

	return RateLimit(limiter)
}

// APIRateLimit 普通 API 速率限制
func APIRateLimit() gin.HandlerFunc {
	limitersMu.Lock()
	if apiLimiter == nil {
		apiLimiter = NewRateLimiter(100, time.Minute)
	}
	limiter := apiLimiter
	limitersMu.Unlock()

	return RateLimit(limiter)
}

// UploadRateLimit 上传接口速率限制
func UploadRateLimit() gin.HandlerFunc {
	limitersMu.Lock()
	if uploadLimiter == nil {
		uploadLimiter = NewRateLimiter(20, time.Minute)
	}
	limiter := uploadLimiter
	limitersMu.Unlock()

	return RateLimit(limiter)
}

// CustomRateLimit 自定义速率限制（内存版）
//
// 创建的限流器登记入包级 customLimiters 表，StopRateLimiters / InitRateLimiters
// 会统一停止其 cleanup goroutine（H4b 修复：原实现每次路由构造创建 limiter 无句柄，
// StopRateLimiters 不感知 → cleanup goroutine 泄漏）。
func CustomRateLimit(rate int, window time.Duration) gin.HandlerFunc {
	limiter := NewRateLimiter(rate, window)
	limitersMu.Lock()
	customLimiters = append(customLimiters, limiter)
	limitersMu.Unlock()
	return RateLimit(limiter)
}

// ===== Redis 分布式限流中间件 =====

// redisLimitDecision 处理 RedisRateLimiter.Allow 的结果，按 allowed 值决定放行/拒绝。
//
// H4c: 不再无条件 fail-open。Allow 已按 limiter 的 failClosed 策略把 Redis 故障翻成
// allowed 值——fail-closed 时 allowed=false（拒绝），fail-open 时 allowed=true（放行）。
// 故 err 与 allowed 的组合语义：
//   - err==nil, allowed==true  → 放行
//   - err==nil, allowed==false → 真超限，返 429（response.RateLimit）
//   - err!=nil, allowed==false → fail-closed 限流器在 Redis 故障下拒绝，返 503（服务不可用）
//   - err!=nil, allowed==true  → fail-open 限流器在 Redis 故障下放行（兼容旧行为）
func redisLimitDecision(c *gin.Context, allowed bool, err error) {
	if err != nil && !allowed {
		// fail-closed：Redis 故障时拒绝（防限流静默失效）。返 503 区别于真实超限的 429。
		response.Custom(c, http.StatusServiceUnavailable, response.CodeServiceUnavailable,
			"限流服务暂时不可用", nil)
		c.Abort()
		return
	}
	if !allowed {
		response.RateLimit(c)
		c.Abort()
		return
	}
	c.Next()
}

// RedisRateLimit Redis 分布式限流中间件（fail-open：Redis 故障时放行，避免影响业务）。
// 参数: keyPrefix 键名前缀（如 "login_limit"），rate 每分钟请求数
// 安全敏感场景（登录防爆破等）应使用 RedisRateLimitFailClosed，Redis 故障时拒绝以防限流失效。
func RedisRateLimit(keyPrefix string, rate int) gin.HandlerFunc {
	limiter := NewRedisRateLimiter(keyPrefix, rate, time.Minute)

	return func(c *gin.Context) {
		identifier := c.ClientIP()

		// 可选：使用用户ID作为标识（登录后）
		// userID := GetUserID(c)
		// if userID > 0 {
		//     identifier = fmt.Sprintf("user:%d", userID)
		// }

		allowed, err := limiter.Allow(c.Request.Context(), identifier)
		redisLimitDecision(c, allowed, err)
	}
}

// RedisRateLimitFailClosed 安全型 Redis 分布式限流中间件（fail-closed）：
// Redis 故障时拒绝（503），避免限流静默失效。用于登录防爆破等安全场景。
func RedisRateLimitFailClosed(keyPrefix string, rate int) gin.HandlerFunc {
	limiter := NewRedisRateLimiterFailClosed(keyPrefix, rate, time.Minute)

	return func(c *gin.Context) {
		identifier := c.ClientIP()
		allowed, err := limiter.Allow(c.Request.Context(), identifier)
		redisLimitDecision(c, allowed, err)
	}
}

// RedisRateLimitWithIdentifier 自定义标识的 Redis 分布式限流（fail-open）。
// 参数: keyPrefix 键名前缀，rate 每分钟请求数，identifierFunc 标识获取函数
func RedisRateLimitWithIdentifier(keyPrefix string, rate int, identifierFunc func(c *gin.Context) string) gin.HandlerFunc {
	limiter := NewRedisRateLimiter(keyPrefix, rate, time.Minute)

	return func(c *gin.Context) {
		identifier := identifierFunc(c)
		if identifier == "" {
			identifier = c.ClientIP()
		}

		allowed, err := limiter.Allow(c.Request.Context(), identifier)
		redisLimitDecision(c, allowed, err)
	}
}

// LoginRedisRateLimit 登录接口 Redis 分布式限流（fail-closed）。
//
// H4c: 登录防爆破场景必须 fail-closed——Redis 故障时若 fail-open 则限流失效、
// 攻击者可借 Redis 抖动窗口无限爆破。改为 fail-closed：Redis 故障时返 503 拒绝。
func LoginRedisRateLimit() gin.HandlerFunc {
	return RedisRateLimitFailClosed("login_limit", 10)
}

// APIRedisRateLimit API Redis 分布式限流（fail-open，避免影响业务）。
func APIRedisRateLimit() gin.HandlerFunc {
	return RedisRateLimit("api_limit", 100)
}

// UploadRedisRateLimit 上传接口 Redis 分布式限流（fail-open）。
func UploadRedisRateLimit() gin.HandlerFunc {
	return RedisRateLimit("upload_limit", 20)
}

// CustomRedisRateLimit 自定义 Redis 分布式限流（fail-open）。
func CustomRedisRateLimit(keyPrefix string, rate int, window time.Duration) gin.HandlerFunc {
	limiter := NewRedisRateLimiter(keyPrefix, rate, window)

	return func(c *gin.Context) {
		identifier := c.ClientIP()
		allowed, err := limiter.Allow(c.Request.Context(), identifier)
		redisLimitDecision(c, allowed, err)
	}
}

// CustomRedisRateLimitFailClosed 自定义安全型 Redis 分布式限流（fail-closed）。
func CustomRedisRateLimitFailClosed(keyPrefix string, rate int, window time.Duration) gin.HandlerFunc {
	limiter := NewRedisRateLimiterFailClosed(keyPrefix, rate, window)

	return func(c *gin.Context) {
		identifier := c.ClientIP()
		allowed, err := limiter.Allow(c.Request.Context(), identifier)
		redisLimitDecision(c, allowed, err)
	}
}