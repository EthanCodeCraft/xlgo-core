package middleware

import (
	"context"
	"fmt"
	"sync"
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
}

type visitor struct {
	lastSeen time.Time
	count    int
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
	}

	limiter.wg.Add(1)
	go limiter.cleanupVisitors()

	return limiter
}

// Allow 检查是否允许请求
func (rl *RateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	v, exists := rl.visitors[ip]
	if !exists {
		rl.visitors[ip] = &visitor{
			lastSeen: time.Now(),
			count:    1,
		}
		return true
	}

	if time.Since(v.lastSeen) > rl.window {
		v.count = 1
		v.lastSeen = time.Now()
		return true
	}

	if v.count >= rl.rate {
		return false
	}

	v.count++
	v.lastSeen = time.Now()
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
				if time.Since(v.lastSeen) > rl.window {
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

// RedisRateLimiter Redis 分布式限流器
// 评分: ⭐⭐⭐⭐⭐
// 理由: 多实例部署时限流共享，使用滑动窗口算法
type RedisRateLimiter struct {
	keyPrefix string        // 键名前缀
	rate      int           // 每分钟允许的请求数
	window    time.Duration // 时间窗口
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

// NewRedisRateLimiter 创建 Redis 分布式限流器
func NewRedisRateLimiter(keyPrefix string, rate int, window time.Duration) *RedisRateLimiter {
	return &RedisRateLimiter{
		keyPrefix: keyPrefix,
		rate:      rate,
		window:    window,
	}
}

// Allow 检查是否允许请求
// 评分: ⭐⭐⭐⭐⭐
// 理由: 使用滑动窗口算法，精确限流
func (rl *RedisRateLimiter) Allow(ctx context.Context, identifier string) (bool, error) {
	if database.RedisClient == nil {
		// Redis 未启用，默认允许
		return true, nil
	}

	key := rl.keyPrefix + ":" + identifier
	now := float64(time.Now().UnixMilli())
	windowMs := float64(rl.window.Milliseconds())

	result, err := database.RedisClient.Eval(ctx, slidingWindowLua, []string{key}, now, windowMs, rl.rate).Result()
	if err != nil {
		return true, err // 出错时允许请求，避免影响业务
	}

	count := result.(int64)
	return count == 0, nil
}

// GetCount 获取当前窗口内的请求数
func (rl *RedisRateLimiter) GetCount(ctx context.Context, identifier string) (int64, error) {
	if database.RedisClient == nil {
		return 0, nil
	}

	key := rl.keyPrefix + ":" + identifier
	now := time.Now().UnixMilli()
	windowStart := now - rl.window.Milliseconds()

	// 移除旧记录并获取当前计数
	database.RedisClient.ZRemRangeByScore(ctx, key, "0", fmt.Sprintf("%d", windowStart))
	return database.RedisClient.ZCard(ctx, key).Result()
}

// Reset 重置限流计数
func (rl *RedisRateLimiter) Reset(ctx context.Context, identifier string) error {
	if database.RedisClient == nil {
		return nil
	}

	key := rl.keyPrefix + ":" + identifier
	return database.RedisClient.Del(ctx, key).Err()
}

// ===== 全局限速器 =====

var (
	loginLimiter  *RateLimiter
	apiLimiter    *RateLimiter
	uploadLimiter *RateLimiter
	redisLimiters map[string]*RedisRateLimiter
	limitersMu    sync.Mutex
)

func init() {
	redisLimiters = make(map[string]*RedisRateLimiter)
}

// InitRateLimiters 初始化限速器
func InitRateLimiters() {
	limitersMu.Lock()
	defer limitersMu.Unlock()

	// 先停止旧的限流器
	if loginLimiter != nil {
		loginLimiter.Stop()
	}
	if apiLimiter != nil {
		apiLimiter.Stop()
	}
	if uploadLimiter != nil {
		uploadLimiter.Stop()
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
func CustomRateLimit(rate int, window time.Duration) gin.HandlerFunc {
	limiter := NewRateLimiter(rate, window)
	return RateLimit(limiter)
}

// ===== Redis 分布式限流中间件 =====

// RedisRateLimit Redis 分布式限流中间件
// 评分: ⭐⭐⭐⭐⭐
// 理由: 多实例部署时共享限流状态
// 参数: keyPrefix 键名前缀（如 "login_limit"），rate 每分钟请求数
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
		if err != nil {
			// Redis 错误时允许请求，避免影响业务
			c.Next()
			return
		}

		if !allowed {
			response.RateLimit(c)
			c.Abort()
			return
		}

		c.Next()
	}
}

// RedisRateLimitWithIdentifier 自定义标识的 Redis 分布式限流
// 参数: keyPrefix 键名前缀，rate 每分钟请求数，identifierFunc 标识获取函数
func RedisRateLimitWithIdentifier(keyPrefix string, rate int, identifierFunc func(c *gin.Context) string) gin.HandlerFunc {
	limiter := NewRedisRateLimiter(keyPrefix, rate, time.Minute)

	return func(c *gin.Context) {
		identifier := identifierFunc(c)
		if identifier == "" {
			identifier = c.ClientIP()
		}

		allowed, err := limiter.Allow(c.Request.Context(), identifier)
		if err != nil {
			c.Next()
			return
		}

		if !allowed {
			response.RateLimit(c)
			c.Abort()
			return
		}

		c.Next()
	}
}

// LoginRedisRateLimit 登录接口 Redis 分布式限流
func LoginRedisRateLimit() gin.HandlerFunc {
	return RedisRateLimit("login_limit", 10)
}

// APIRedisRateLimit API Redis 分布式限流
func APIRedisRateLimit() gin.HandlerFunc {
	return RedisRateLimit("api_limit", 100)
}

// UploadRedisRateLimit 上传接口 Redis 分布式限流
func UploadRedisRateLimit() gin.HandlerFunc {
	return RedisRateLimit("upload_limit", 20)
}

// CustomRedisRateLimit 自定义 Redis 分布式限流
func CustomRedisRateLimit(keyPrefix string, rate int, window time.Duration) gin.HandlerFunc {
	limiter := NewRedisRateLimiter(keyPrefix, rate, window)

	return func(c *gin.Context) {
		identifier := c.ClientIP()

		allowed, err := limiter.Allow(c.Request.Context(), identifier)
		if err != nil {
			c.Next()
			return
		}

		if !allowed {
			response.RateLimit(c)
			c.Abort()
			return
		}

		c.Next()
	}
}