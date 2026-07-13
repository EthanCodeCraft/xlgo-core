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
	"github.com/redis/go-redis/v9"
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

// NewRateLimiter 创建速率限制器（内存版）。
// rate<=0 或 window<=0 时 panic。内部启动 cleanup goroutine，调用方负责在不再使用时
// 调用 (*RateLimiter).Stop 释放，否则 goroutine 泄漏。
func NewRateLimiter(rate int, window time.Duration) *RateLimiter {
	mustValidRateLimit(rate, window)
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

func mustValidRateLimit(rate int, window time.Duration) {
	if rate <= 0 {
		panic("rate limiter: rate must be positive")
	}
	if window <= 0 {
		panic("rate limiter: window must be positive")
	}
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
	keyPrefix  string        // 键名前缀
	rate       int           // 每分钟允许的请求数
	window     time.Duration // 时间窗口
	failClosed atomic.Bool   // H4c/H-6: Redis 错误/断言失败时是否拒绝（true=安全型 fail-closed）。atomic 以支持运行期 SetFailClosed 并发安全切换。
	client     *redis.Client // Phase 5: 注入的 redis client（nil 回退 database.GetRedis()，照 jwt.TokenBlacklist 模型）。App 经 WithRedisClient 注入 per-App client 实现隔离。
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

// RedisRateLimiterOption 配置 RedisRateLimiter 的可选策略。
type RedisRateLimiterOption func(*RedisRateLimiter)

// WithFailClosed 设置 Redis 故障时 fail-closed（拒绝请求）。
// 不传或传 false 为 fail-open（放行，兼容默认）。安全敏感场景（登录防爆破等）应传 true。
func WithFailClosed(failClosed bool) RedisRateLimiterOption {
	return func(rl *RedisRateLimiter) {
		rl.failClosed.Store(failClosed)
	}
}

// WithRedisClient 注入指定 Redis 客户端（Phase 5，多 Redis/测试隔离）。
// 不传则回退 database.GetRedis()（全局）。App 可经 app.RedisClient() 取自身 client 注入。
func WithRedisClient(client *redis.Client) RedisRateLimiterOption {
	return func(rl *RedisRateLimiter) {
		rl.client = client
	}
}

// NewRedisRateLimiter 创建 Redis 分布式限流器。
// 默认 fail-open（Redis 故障时放行，避免影响业务）；安全敏感场景传 WithFailClosed(true)。
func NewRedisRateLimiter(keyPrefix string, rate int, window time.Duration, opts ...RedisRateLimiterOption) *RedisRateLimiter {
	mustValidRateLimit(rate, window)
	rl := &RedisRateLimiter{
		keyPrefix: keyPrefix,
		rate:      rate,
		window:    window,
		// failClosed 零值 false（fail-open，兼容默认）
	}
	for _, opt := range opts {
		if opt != nil {
			opt(rl)
		}
	}
	return rl
}

// SetFailClosed 设置 Redis 故障时的策略：true=拒绝（安全型），false=放行（兼容默认）。
// 供已创建的限流器切换策略。并发安全（atomic.Store），可与并发 Allow 调用共存。
func (rl *RedisRateLimiter) SetFailClosed(failClosed bool) {
	rl.failClosed.Store(failClosed)
}

// redisClient 返回注入的 redis client，未注入则回退 database.GetRedis()（jwt 模型）。
// M-C 教训：取一次复用，避免 nil 检查与 Eval 各调一次 GetRedis() 之间的 CloseRedis 竞态。
func (rl *RedisRateLimiter) redisClient() *redis.Client {
	if rl != nil && rl.client != nil {
		return rl.client
	}
	return database.GetRedis()
}

// Allow 检查是否允许请求。
//
// H4c 修复：
//   - result.(int64) 改 comma-ok，断言失败返 ErrRedisRateLimiterUnexpectedResult 而非 panic。
//   - Redis 错误/断言失败时按 failClosed 策略决定：fail-closed 返 (false, err) 拒绝，
//     fail-open 返 (true, err) 放行（兼容旧行为）。中间件层据此 allowed 值决定放行/拒绝，
//     不再无条件 fail-open--登录防爆破等安全场景用 fail-closed 限流器即可在 Redis 故障时拒绝。
//
// Redis 未启用（redisClient() == nil）时：fail-closed 返 (false, ErrRedisRateLimiterUnavailable)，
// fail-open 返 (true, nil)（兼容旧行为）。安全场景必须确保 Redis 已启用。
//
// Phase 5：redisClient() 注入优先、全局兜底，App 经 WithRedisClient 注入 per-App client。
func (rl *RedisRateLimiter) Allow(ctx context.Context, identifier string) (bool, error) {
	// M-C 修复：取一次复用。原实现 nil 检查与 Eval 各调一次 GetRedis()，
	// 两次之间若 CloseRedis，第二次返回 nil -> nil.Eval panic。
	rdb := rl.redisClient()
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
	// M-C 修复（Phase 5：改 redisClient）：取一次复用（原三次调用存在 nil-deref 窗口）。
	rdb := rl.redisClient()
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
	// M-C 修复（Phase 5：改 redisClient）：取一次复用（原两次调用存在 nil-deref 窗口）。
	rdb := rl.redisClient()
	if rdb == nil {
		return nil
	}

	key := rl.keyPrefix + ":" + identifier
	return rdb.Del(ctx, key).Err()
}

// ===== 全局限速器 Registry（Phase 5 实例化） =====
//
// 内存限流器（RateLimiter）持 cleanup goroutine，进程级共享会在多 App 下互相污染
// （App A Shutdown 经 StopRateLimiters 把 App B 的 loginLimiter 也停了）。故照
// cache.CacheManager / cron.Scheduler 模式引入 RateLimitRegistry：实例化 + 全局默认
// + 包级 facade 代理，App 持自己的 Registry。
//
// 包级 LoginRateLimit() 等改为"请求时解析当前默认 Registry"懒创建限流器，使 App.Init
// swap 后请求落到 App 的 Registry（避免 pre-Init 注册到 init Registry 被 swap 丢弃）。

// RateLimitRegistry 持一组内存限流器，支持 per-App 隔离。
type RateLimitRegistry struct {
	mu             sync.Mutex
	loginLimiter   *RateLimiter
	apiLimiter     *RateLimiter
	uploadLimiter  *RateLimiter
	customLimiters []*RateLimiter
}

// NewRateLimitRegistry 创建限流器注册表实例。
func NewRateLimitRegistry() *RateLimitRegistry {
	return &RateLimitRegistry{}
}

func (r *RateLimitRegistry) loginLimiterInstance() *RateLimiter {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.loginLimiter == nil {
		r.loginLimiter = NewRateLimiter(10, time.Minute)
	}
	return r.loginLimiter
}

func (r *RateLimitRegistry) apiLimiterInstance() *RateLimiter {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.apiLimiter == nil {
		r.apiLimiter = NewRateLimiter(100, time.Minute)
	}
	return r.apiLimiter
}

func (r *RateLimitRegistry) uploadLimiterInstance() *RateLimiter {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.uploadLimiter == nil {
		r.uploadLimiter = NewRateLimiter(20, time.Minute)
	}
	return r.uploadLimiter
}

// registerCustom 登记自定义限流器供 Stop 统一停止（H4b）。
func (r *RateLimitRegistry) registerCustom(l *RateLimiter) {
	r.mu.Lock()
	r.customLimiters = append(r.customLimiters, l)
	r.mu.Unlock()
}

// Init 预初始化标准限流器（可选；不调则首次请求时懒创建）。先停旧的同名限流器释放 goroutine。
func (r *RateLimitRegistry) Init() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.loginLimiter != nil {
		r.loginLimiter.Stop()
	}
	if r.apiLimiter != nil {
		r.apiLimiter.Stop()
	}
	if r.uploadLimiter != nil {
		r.uploadLimiter.Stop()
	}
	r.loginLimiter = NewRateLimiter(10, time.Minute)
	r.apiLimiter = NewRateLimiter(100, time.Minute)
	r.uploadLimiter = NewRateLimiter(20, time.Minute)
}

// Stop 停止注册表中所有限流器（释放 cleanup goroutine）。幂等。
func (r *RateLimitRegistry) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.loginLimiter != nil {
		r.loginLimiter.Stop()
		r.loginLimiter = nil
	}
	if r.apiLimiter != nil {
		r.apiLimiter.Stop()
		r.apiLimiter = nil
	}
	if r.uploadLimiter != nil {
		r.uploadLimiter.Stop()
		r.uploadLimiter = nil
	}
	for _, l := range r.customLimiters {
		l.Stop()
	}
	r.customLimiters = nil
}

// --- App-bound 中间件（捕获此 Registry，不查全局默认，多 App per-App 隔离） ---
//
// 与包级 LoginRateLimit() 等的区别：包级在请求时查 GetDefaultRateLimitRegistry()（多 App 下
// 仅最后 Init 的 App 是全局默认，故包级 facade 不提供 per-App 计数隔离）；本组方法捕获 r，
// 始终用此 Registry 的 limiter。多 App 场景用 app.RateLimitRegistry().LoginRateLimit() 装配路由。

// LoginRateLimit 返回绑定到此 Registry 的登录限流中间件。
func (r *RateLimitRegistry) LoginRateLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		rateLimitAllow(c, r.loginLimiterInstance())
	}
}

// APIRateLimit 返回绑定到此 Registry 的普通 API 限流中间件。
func (r *RateLimitRegistry) APIRateLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		rateLimitAllow(c, r.apiLimiterInstance())
	}
}

// UploadRateLimit 返回绑定到此 Registry 的上传限流中间件。
func (r *RateLimitRegistry) UploadRateLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		rateLimitAllow(c, r.uploadLimiterInstance())
	}
}

// CustomRateLimit 返回绑定到此 Registry 的自定义限流中间件。limiter 在首次请求时创建并
// 登记到此 Registry（r.registerCustom），由 App.Shutdown 的 registry.Stop() 收口，无泄漏。
func (r *RateLimitRegistry) CustomRateLimit(rate int, window time.Duration) gin.HandlerFunc {
	var (
		once    sync.Once
		limiter *RateLimiter
	)
	return func(c *gin.Context) {
		once.Do(func() {
			limiter = NewRateLimiter(rate, window)
			r.registerCustom(limiter)
		})
		rateLimitAllow(c, limiter)
	}
}

// defaultRateLimitRegistry 全局默认 Registry（atomic，照 cache/cron 模式）。
var defaultRateLimitRegistry atomic.Pointer[RateLimitRegistry]

func init() {
	defaultRateLimitRegistry.Store(NewRateLimitRegistry())
}

// GetDefaultRateLimitRegistry 返回全局默认 Registry。
func GetDefaultRateLimitRegistry() *RateLimitRegistry {
	return defaultRateLimitRegistry.Load()
}

// SwapDefaultRateLimitRegistry 置为全局默认，返回被替换的旧（照 Swap 模式）。nil 忽略，返回当前默认。
func SwapDefaultRateLimitRegistry(r *RateLimitRegistry) *RateLimitRegistry {
	if r == nil {
		return defaultRateLimitRegistry.Load()
	}
	return defaultRateLimitRegistry.Swap(r)
}

// rateLimitAllow 公共放行/拒绝逻辑（内存版）。
func rateLimitAllow(c *gin.Context, rl *RateLimiter) {
	if rl == nil {
		response.Custom(c, http.StatusServiceUnavailable, response.CodeServiceUnavailable,
			"限流器未初始化", nil)
		c.Abort()
		return
	}
	if !rl.Allow(c.ClientIP()) {
		response.RateLimit(c)
		c.Abort()
		return
	}
	c.Next()
}

// RateLimit 通用速率限制中间件（内存版，用户自持 limiter 时使用）。
func RateLimit(limiter *RateLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		rateLimitAllow(c, limiter)
	}
}

// LoginRateLimit 登录接口速率限制。限流器在首次请求时从当前默认 Registry 懒创建，
// 故 App.Init swap 后用的是 App 的 Registry（避免 pre-Init 注册到 init Registry 被丢弃）。
func LoginRateLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		rateLimitAllow(c, GetDefaultRateLimitRegistry().loginLimiterInstance())
	}
}

// APIRateLimit 普通 API 速率限制。
func APIRateLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		rateLimitAllow(c, GetDefaultRateLimitRegistry().apiLimiterInstance())
	}
}

// UploadRateLimit 上传接口速率限制。
func UploadRateLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		rateLimitAllow(c, GetDefaultRateLimitRegistry().uploadLimiterInstance())
	}
}

// CustomRateLimit 自定义速率限制（内存版）。限流器在首次请求时创建并登记到当前默认
// Registry（供 Stop 统一停止，避免 cleanup goroutine 泄漏，H4b）。
func CustomRateLimit(rate int, window time.Duration) gin.HandlerFunc {
	var (
		once    sync.Once
		limiter *RateLimiter
	)
	return func(c *gin.Context) {
		once.Do(func() {
			limiter = NewRateLimiter(rate, window)
			GetDefaultRateLimitRegistry().registerCustom(limiter)
		})
		rateLimitAllow(c, limiter)
	}
}

// InitRateLimiters 初始化默认 Registry 的标准限流器（可选，不调则懒创建）。
func InitRateLimiters() {
	GetDefaultRateLimitRegistry().Init()
}

// StopRateLimiters 停止默认 Registry 的所有限流器。注意：仅停默认 Registry--
// App 持自己的 Registry 时应在 Shutdown 调 app 自己的 Stop（Phase 5）。
func StopRateLimiters() {
	GetDefaultRateLimitRegistry().Stop()
}

// ===== Redis 分布式限流中间件 =====

// redisLimitDecision 处理 RedisRateLimiter.Allow 的结果，按 allowed 值决定放行/拒绝。
//
// H4c: 不再无条件 fail-open。Allow 已按 limiter 的 failClosed 策略把 Redis 故障翻成
// allowed 值--fail-closed 时 allowed=false（拒绝），fail-open 时 allowed=true（放行）。
// 故 err 与 allowed 的组合语义：
//   - err==nil, allowed==true  -> 放行
//   - err==nil, allowed==false -> 真超限，返 429（response.RateLimit）
//   - err!=nil, allowed==false -> fail-closed 限流器在 Redis 故障下拒绝，返 503（服务不可用）
//   - err!=nil, allowed==true  -> fail-open 限流器在 Redis 故障下放行（兼容旧行为）
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

// RedisRateLimit Redis 分布式限流中间件。
// 默认 fail-open（Redis 故障时放行，避免影响业务）；安全敏感场景（登录防爆破等）
// 传 WithFailClosed(true)，Redis 故障时拒绝以防限流失效。
func RedisRateLimit(keyPrefix string, rate int, opts ...RedisRateLimiterOption) gin.HandlerFunc {
	limiter := NewRedisRateLimiter(keyPrefix, rate, time.Minute, opts...)

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

// RedisRateLimitWithIdentifier 自定义标识的 Redis 分布式限流。
// 参数: keyPrefix 键名前缀，rate 1 分钟窗口内允许的请求数，identifierFunc 标识获取函数。
// identifierFunc 为 nil 或返回空串时回退到 c.ClientIP()。默认 fail-open，传 WithFailClosed(true) 切换。
func RedisRateLimitWithIdentifier(keyPrefix string, rate int, identifierFunc func(c *gin.Context) string, opts ...RedisRateLimiterOption) gin.HandlerFunc {
	limiter := NewRedisRateLimiter(keyPrefix, rate, time.Minute, opts...)

	return func(c *gin.Context) {
		identifier := ""
		if identifierFunc != nil {
			identifier = identifierFunc(c)
		}
		if identifier == "" {
			identifier = c.ClientIP()
		}

		allowed, err := limiter.Allow(c.Request.Context(), identifier)
		redisLimitDecision(c, allowed, err)
	}
}

// LoginRedisRateLimit 登录接口 Redis 分布式限流（fail-closed）。
//
// H4c: 登录防爆破场景必须 fail-closed--Redis 故障时若 fail-open 则限流失效、
// 攻击者可借 Redis 抖动窗口无限爆破。改为 fail-closed：Redis 故障时返 503 拒绝。
func LoginRedisRateLimit() gin.HandlerFunc {
	return RedisRateLimit("login_limit", 10, WithFailClosed(true))
}

// APIRedisRateLimit API Redis 分布式限流（fail-open，避免影响业务）。
func APIRedisRateLimit() gin.HandlerFunc {
	return RedisRateLimit("api_limit", 100)
}

// UploadRedisRateLimit 上传接口 Redis 分布式限流（fail-closed）。
// 上传属资源敏感操作，Redis 故障时拒绝以防限流静默失效。
func UploadRedisRateLimit() gin.HandlerFunc {
	return RedisRateLimit("upload_limit", 20, WithFailClosed(true))
}

// CustomRedisRateLimit 自定义 Redis 分布式限流。
// 默认 fail-open，传 WithFailClosed(true) 切换为 fail-closed。
func CustomRedisRateLimit(keyPrefix string, rate int, window time.Duration, opts ...RedisRateLimiterOption) gin.HandlerFunc {
	limiter := NewRedisRateLimiter(keyPrefix, rate, window, opts...)

	return func(c *gin.Context) {
		identifier := c.ClientIP()
		allowed, err := limiter.Allow(c.Request.Context(), identifier)
		redisLimitDecision(c, allowed, err)
	}
}
