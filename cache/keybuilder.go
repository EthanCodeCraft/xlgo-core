package cache

import (
	"context"
	"strings"
	"time"

	"github.com/EthanCodeCraft/xlgo-core/config"
)

// KeyBuilder 缓存键名构建器
// 使用场景: 多个小项目共用一台 Redis 服务器，每个项目设置不同前缀
type KeyBuilder struct {
	prefix     string // 项目/站点前缀，如 "site_a"
	_separator string // 分隔符，默认 ":"
	_cacheType string // 缓存类型标识，如 "cache"
}

// KeyBuilderOption 配置选项
type KeyBuilderOption func(*KeyBuilder)

// WithPrefix 设置前缀（项目/站点别名）
// 示例: WithPrefix("site_a") -> 所有 key 自动添加 "site_a" 前缀
func WithPrefix(prefix string) KeyBuilderOption {
	return func(kb *KeyBuilder) {
		kb.prefix = prefix
	}
}

// WithSeparator 设置分隔符
// 示例: WithSeparator(":") -> "site_a:user:1"
func WithSeparator(separator string) KeyBuilderOption {
	return func(kb *KeyBuilder) {
		kb._separator = separator
	}
}

// WithCacheType 设置缓存类型标识
// 示例: WithCacheType("session") -> "session_site_a_user:1"
func WithCacheType(cacheType string) KeyBuilderOption {
	return func(kb *KeyBuilder) {
		kb._cacheType = cacheType
	}
}

// NewKeyBuilder 创建键名构建器
func NewKeyBuilder(opts ...KeyBuilderOption) *KeyBuilder {
	kb := &KeyBuilder{
		prefix:     "",
		_separator: ":",
		_cacheType: "cache",
	}
	for _, opt := range opts {
		opt(kb)
	}
	return kb
}

// Build 构建完整键名
// 格式: {cacheType}{separator}{prefix}{separator}{key}
// 示例: kb.Build("user:1") -> "cache_site_a_user:1"
func (kb *KeyBuilder) Build(key string) string {
	parts := []string{}
	if kb._cacheType != "" {
		parts = append(parts, kb._cacheType)
	}
	if kb.prefix != "" {
		parts = append(parts, kb.prefix)
	}
	parts = append(parts, key)
	return strings.Join(parts, kb._separator)
}

// BuildTemp 构建临时缓存键名（带过期时间）
// 格式: "temp{separator}{prefix}{separator}{key}"
func (kb *KeyBuilder) BuildTemp(key string) string {
	parts := []string{"temp"}
	if kb.prefix != "" {
		parts = append(parts, kb.prefix)
	}
	parts = append(parts, key)
	return strings.Join(parts, kb._separator)
}

// BuildPerm 构建永久缓存键名（不带过期时间）
// 格式: "perm{separator}{prefix}{separator}{key}"
func (kb *KeyBuilder) BuildPerm(key string) string {
	parts := []string{"perm"}
	if kb.prefix != "" {
		parts = append(parts, kb.prefix)
	}
	parts = append(parts, key)
	return strings.Join(parts, kb._separator)
}

// BuildLock 构建分布式锁键名
// 格式: "lock{separator}{prefix}{separator}{key}"
func (kb *KeyBuilder) BuildLock(key string) string {
	parts := []string{"lock"}
	if kb.prefix != "" {
		parts = append(parts, kb.prefix)
	}
	parts = append(parts, key)
	return strings.Join(parts, kb._separator)
}

// BuildCounter 构建计数器键名
// 格式: "counter{separator}{prefix}{separator}{key}"
func (kb *KeyBuilder) BuildCounter(key string) string {
	parts := []string{"counter"}
	if kb.prefix != "" {
		parts = append(parts, kb.prefix)
	}
	parts = append(parts, key)
	return strings.Join(parts, kb._separator)
}

// BuildSession 构建会话键名
// 格式: "session{separator}{prefix}{separator}{key}"
func (kb *KeyBuilder) BuildSession(key string) string {
	parts := []string{"session"}
	if kb.prefix != "" {
		parts = append(parts, kb.prefix)
	}
	parts = append(parts, key)
	return strings.Join(parts, kb._separator)
}

// BuildPattern 构建匹配模式（用于 SCAN/Keys）
// 示例: kb.BuildPattern("user:*") -> "cache_site_a_user:*"
func (kb *KeyBuilder) BuildPattern(pattern string) string {
	return kb.Build(pattern)
}

// GetPrefix 获取当前前缀
func (kb *KeyBuilder) GetPrefix() string {
	return kb.prefix
}

// SetPrefix 动态设置前缀
func (kb *KeyBuilder) SetPrefix(prefix string) *KeyBuilder {
	kb.prefix = prefix
	return kb
}

// ===== 全局键名构建器 =====

var globalKeyBuilder *KeyBuilder

// InitKeyBuilder 初始化全局键名构建器
// 参数: prefix 站点别名，如果为空则自动从配置读取
// 示例:
//
//	InitKeyBuilder("site_a")                     // 手动指定
//	InitKeyBuilder("")                            // 自动从配置读取
//	InitKeyBuilder("", WithSeparator(":"))        // 自动读取 + 自定义分隔符
func InitKeyBuilder(prefix string, opts ...KeyBuilderOption) {
	// 如果 prefix 为空，尝试从配置读取
	if prefix == "" {
		cfg := config.Get()
		if cfg != nil {
			prefix = cfg.GetSiteName()
		}
	}

	opts = append([]KeyBuilderOption{WithPrefix(prefix)}, opts...)
	globalKeyBuilder = NewKeyBuilder(opts...)
}

// AutoInitKeyBuilder 自动从配置初始化键名构建器
// 配置示例:
//
//	app:
//	  site_name: "site_a"
//	  env: "prod"
func AutoInitKeyBuilder(opts ...KeyBuilderOption) {
	InitKeyBuilder("", opts...)
}

// GetKeyBuilder 获取全局键名构建器
func GetKeyBuilder() *KeyBuilder {
	if globalKeyBuilder == nil {
		// 自动从配置初始化
		AutoInitKeyBuilder()
	}
	return globalKeyBuilder
}

// K 快捷构建键名（使用全局构建器）
// 示例: cache.K("user:1") -> 自动添加前缀
func K(key string) string {
	return GetKeyBuilder().Build(key)
}

// KTemp 快捷构建临时缓存键名
func KTemp(key string) string {
	return GetKeyBuilder().BuildTemp(key)
}

// KPerm 快捷构建永久缓存键名
func KPerm(key string) string {
	return GetKeyBuilder().BuildPerm(key)
}

// KLock 快捷构建锁键名
func KLock(key string) string {
	return GetKeyBuilder().BuildLock(key)
}

// KCounter 快捷构建计数器键名
func KCounter(key string) string {
	return GetKeyBuilder().BuildCounter(key)
}

// KSession 快捷构建会话键名
func KSession(key string) string {
	return GetKeyBuilder().BuildSession(key)
}

// ===== 带键名构建器的缓存操作 =====

// SetWithPrefix 带前缀的缓存设置
func SetWithPrefix(ctx context.Context, key string, value any, ttl time.Duration, prefix string) error {
	kb := NewKeyBuilder(WithPrefix(prefix))
	return GetCache().Set(ctx, kb.Build(key), value, ttl)
}

// GetWithPrefix 带前缀的缓存获取
func GetWithPrefix(ctx context.Context, key string, dest any, prefix string) bool {
	kb := NewKeyBuilder(WithPrefix(prefix))
	return GetCache().Get(ctx, kb.Build(key), dest)
}

// DeleteWithPrefix 带前缀的缓存删除
func DeleteWithPrefix(ctx context.Context, key string, prefix string) error {
	kb := NewKeyBuilder(WithPrefix(prefix))
	return GetCache().Delete(ctx, kb.Build(key))
}

// LockWithPrefix 带前缀的分布式锁
func LockWithPrefix(ctx context.Context, key string, ttl time.Duration, prefix string) (bool, error) {
	kb := NewKeyBuilder(WithPrefix(prefix))
	return Lock(ctx, kb.BuildLock(key), ttl)
}

// UnlockWithPrefix 带前缀的锁释放
// 注意: 此函数不检查 Token，仅用于向后兼容，建议使用 NewLock/Unlock 组合
func UnlockWithPrefix(ctx context.Context, key string, prefix string) error {
	kb := NewKeyBuilder(WithPrefix(prefix))
	return UnlockByKey(ctx, kb.BuildLock(key))
}
