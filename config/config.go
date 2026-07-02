package config

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/mitchellh/mapstructure"
	"github.com/spf13/viper"
)

// 配置错误
var (
	ErrConfigNotLoaded = fmt.Errorf("配置未加载")
)

// Config 全局配置结构体
type Config struct {
	App      AppConfig       `mapstructure:"app"`
	Server   ServerConfig    `mapstructure:"server"`
	Database DatabaseConfig  `mapstructure:"database"`
	Redis    RedisConfig     `mapstructure:"redis"`
	JWT      JWTConfig       `mapstructure:"jwt"`
	SMS      SMSConfig       `mapstructure:"sms"`
	Storage  StorageConfig   `mapstructure:"storage"`
	Upload   UploadConfig    `mapstructure:"upload"`
	Log      LogConfig       `mapstructure:"log"`
	CORS     CORSConfig      `mapstructure:"cors"`
}

// AppConfig 应用配置
// 使用场景:
//   - 缓存键名前缀: cache:{site_name}:user:1
//   - 日志标识: [site_a] 2024-01-01 10:00:00 ...
//   - 站点追踪: Request-ID 带站点标识
//   - 分布式锁: lock:{site_name}:order:123
type AppConfig struct {
	Name     string `mapstructure:"name"`      // 应用名称，如 "用户管理系统"
	SiteName string `mapstructure:"site_name"` // 站点别名，如 "site_a"、"user_api"
	Version  string `mapstructure:"version"`   // 应用版本
	Env      string `mapstructure:"env"`       // 运行环境: dev/test/prod
	Debug    bool   `mapstructure:"debug"`     // 是否开启调试模式
	BaseURL  string `mapstructure:"base_url"`  // 应用基础URL
}

// GetSiteName 获取站点别名，如果未设置则返回空字符串
func (c *AppConfig) GetSiteName() string {
	if c == nil {
		return ""
	}
	return c.SiteName
}

// GetCachePrefix 获取缓存键名前缀
func (c *AppConfig) GetCachePrefix() string {
	return c.GetSiteName()
}

// IsDebug 是否调试模式
func (c *AppConfig) IsDebug() bool {
	if c == nil {
		return false
	}
	return c.Debug
}

// IsDev 是否开发环境
func (c *AppConfig) IsDev() bool {
	if c == nil {
		return false
	}
	return c.Env == "dev" || c.Env == "development"
}

// IsProd 是否生产环境
func (c *AppConfig) IsProd() bool {
	if c == nil {
		return false
	}
	return c.Env == "prod" || c.Env == "production"
}

// TLSConfig HTTPS/TLS 配置
type TLSConfig struct {
	Enabled  bool   `mapstructure:"enabled"`
	CertFile string `mapstructure:"cert_file"`
	KeyFile  string `mapstructure:"key_file"`
}

// ServerConfig 服务配置
type ServerConfig struct {
	Host            string        `mapstructure:"host"`              // 绑定地址，空=监听所有接口(0.0.0.0)；"127.0.0.1"=仅本机；内网IP=绑定指定网卡
	Port            int           `mapstructure:"port"`
	Mode            string        `mapstructure:"mode"`             // development 或 production
	ReadTimeout     time.Duration `mapstructure:"read_timeout"`     // 读超时，如 "15s"
	WriteTimeout    time.Duration `mapstructure:"write_timeout"`    // 写超时，如 "30s"
	IdleTimeout     time.Duration `mapstructure:"idle_timeout"`     // 空闲超时，如 "60s"
	ShutdownTimeout time.Duration `mapstructure:"shutdown_timeout"` // 优雅关闭超时，如 "30s"
	MaxHeaderBytes  int           `mapstructure:"max_header_bytes"` // 最大请求头字节数
	TLS             TLSConfig     `mapstructure:"tls"`
	UnixSocket      string        `mapstructure:"unix_socket"` // 非空时优先于 Port，监听 unix socket
	ResponseMode    string        `mapstructure:"response_mode"` // business(默认) 或 rest，见 response.SetMode
}

// 默认值常量（ServerConfig 字段为零值时回退使用）
const (
	defaultReadTimeout     = 15 * time.Second
	defaultWriteTimeout    = 30 * time.Second
	defaultIdleTimeout     = 60 * time.Second
	defaultShutdownTimeout = 30 * time.Second
	defaultMaxHeaderBytes  = 1 << 20 // 1MB
)

// EffectiveReadTimeout 返回生效的读超时（零值回退默认）
func (c ServerConfig) EffectiveReadTimeout() time.Duration {
	if c.ReadTimeout > 0 {
		return c.ReadTimeout
	}
	return defaultReadTimeout
}

// EffectiveWriteTimeout 返回生效的写超时（零值回退默认）
func (c ServerConfig) EffectiveWriteTimeout() time.Duration {
	if c.WriteTimeout > 0 {
		return c.WriteTimeout
	}
	return defaultWriteTimeout
}

// EffectiveIdleTimeout 返回生效的空闲超时（零值回退默认）
func (c ServerConfig) EffectiveIdleTimeout() time.Duration {
	if c.IdleTimeout > 0 {
		return c.IdleTimeout
	}
	return defaultIdleTimeout
}

// EffectiveShutdownTimeout 返回生效的关闭超时（零值回退默认）
func (c ServerConfig) EffectiveShutdownTimeout() time.Duration {
	if c.ShutdownTimeout > 0 {
		return c.ShutdownTimeout
	}
	return defaultShutdownTimeout
}

// EffectiveMaxHeaderBytes 返回生效的最大请求头字节数（零值回退默认）
func (c ServerConfig) EffectiveMaxHeaderBytes() int {
	if c.MaxHeaderBytes > 0 {
		return c.MaxHeaderBytes
	}
	return defaultMaxHeaderBytes
}

// 数据库驱动常量
const (
	DriverMySQL    = "mysql"
	DriverPostgres = "postgres"
)

// DSNBuilder 根据 DatabaseConfig 生成连接字符串
type DSNBuilder func(*DatabaseConfig) string

var (
	dsnBuildersMu sync.RWMutex
	dsnBuilders   = map[string]DSNBuilder{}
)

// RegisterDSNBuilder 为指定驱动注册 DSN 构建器（驱动名大小写不敏感）。
// aliases 用于注册同一驱动的别名，例如 postgres 的 "postgresql"、"pg"。
// 通常由 database 包通过 database.RegisterDialect 间接调用，
// 应用代码也可直接使用以扩展自定义驱动。
func RegisterDSNBuilder(name string, builder DSNBuilder, aliases ...string) {
	if builder == nil {
		return
	}
	dsnBuildersMu.Lock()
	defer dsnBuildersMu.Unlock()
	for _, n := range append([]string{name}, aliases...) {
		key := strings.ToLower(strings.TrimSpace(n))
		if key != "" {
			dsnBuilders[key] = builder
		}
	}
}

// LookupDSNBuilder 查找已注册的 DSN 构建器
func LookupDSNBuilder(name string) (DSNBuilder, bool) {
	key := strings.ToLower(strings.TrimSpace(name))
	dsnBuildersMu.RLock()
	defer dsnBuildersMu.RUnlock()
	b, ok := dsnBuilders[key]
	return b, ok
}

// RegisteredDrivers 返回所有已注册 DSN 构建器的驱动名（用于诊断）
func RegisteredDrivers() []string {
	dsnBuildersMu.RLock()
	defer dsnBuildersMu.RUnlock()
	names := make([]string, 0, len(dsnBuilders))
	for k := range dsnBuilders {
		names = append(names, k)
	}
	return names
}

func init() {
	// 内置 MySQL / PostgreSQL 的 DSN 构建器
	RegisterDSNBuilder(DriverMySQL, func(c *DatabaseConfig) string { return c.MySQLDSN() })
	RegisterDSNBuilder(DriverPostgres, func(c *DatabaseConfig) string { return c.PostgresDSN() }, "postgresql", "pg")
}

// DatabaseConfig 数据库配置
type DatabaseConfig struct {
	// Driver 数据库驱动，支持 mysql（默认）与 postgres
	Driver string `mapstructure:"driver"`
	// Host 数据库主机
	Host string `mapstructure:"host"`
	// Port 数据库端口
	Port int `mapstructure:"port"`
	// User 数据库用户名
	User string `mapstructure:"user"`
	// Password 数据库密码
	Password string `mapstructure:"password"`
	// Name 数据库名
	Name string `mapstructure:"name"`
	// Timezone 连接时区。MySQL 用作 loc 参数、Postgres 用作 TimeZone 参数。
	// 空时 MySQL 默认 "Local"、Postgres 默认 "Asia/Shanghai"（向后兼容，M9）。
	Timezone string `mapstructure:"timezone"`
	// CustomDSN 自定义连接字符串，设置后优先于由 Host/Port 等字段生成的 DSN
	CustomDSN string `mapstructure:"dsn"`
	// MaxIdleConns 最大空闲连接数
	MaxIdleConns int `mapstructure:"max_idle_conns"`
	// MaxOpenConns 最大打开连接数
	MaxOpenConns int `mapstructure:"max_open_conns"`
	// ConnMaxIdleTime 连接最大空闲时间，如 "5m"（#21）。0 表示用驱动默认
	ConnMaxIdleTime time.Duration `mapstructure:"conn_max_idle_time"`
	// HealthCheckInterval 主库探活间隔，如 "30s"（#21）。0 表示用默认 30s
	HealthCheckInterval time.Duration `mapstructure:"health_check_interval"`
	// HealthCheckFailureThreshold 连续探活失败多少次标记不健康（#21）。0 表示用默认 3
	HealthCheckFailureThreshold int `mapstructure:"health_check_failure_threshold"`
}

// DSN 根据驱动返回连接字符串。设置了 CustomDSN 时优先返回 CustomDSN；
// 未指定 Driver 时按 MySQL 处理（向后兼容）。
// 若驱动通过 RegisterDSNBuilder 注册过自定义构建器，则使用注册的构建器。
func (c *DatabaseConfig) DSN() string {
	if c.CustomDSN != "" {
		return c.CustomDSN
	}
	if builder, ok := LookupDSNBuilder(c.Driver); ok {
		return builder(c)
	}
	// 未注册时回退到 MySQL（保持向后兼容）
	return c.MySQLDSN()
}

// MySQLDSN 返回 MySQL 连接字符串。
// 密码经 url.QueryEscape 转义，避免含 @/:/空格 等特殊字符破坏 DSN（M9）。
// loc 由 Timezone 配置，空则默认 "Local"（向后兼容）。
func (c *DatabaseConfig) MySQLDSN() string {
	loc := c.Timezone
	if loc == "" {
		loc = "Local"
	}
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=%s",
		c.User, url.QueryEscape(c.Password), c.Host, c.Port, c.Name, url.QueryEscape(loc))
}

// PostgresDSN 返回 PostgreSQL 连接字符串。
// 密码经单引号转义（内嵌单引号翻倍），避免含空格/引号/反斜杠破坏 key=value DSN（M9）。
// TimeZone 由 Timezone 配置，空则默认 "Asia/Shanghai"（向后兼容）。
func (c *DatabaseConfig) PostgresDSN() string {
	tz := c.Timezone
	if tz == "" {
		tz = "Asia/Shanghai"
	}
	return fmt.Sprintf("host=%s port=%d user=%s password='%s' dbname=%s sslmode=disable TimeZone=%s",
		c.Host, c.Port, c.User, strings.ReplaceAll(c.Password, "'", "''"), c.Name, tz)
}

// RedisConfig Redis 配置
type RedisConfig struct {
	Host     string `mapstructure:"host"`
	Port     int    `mapstructure:"port"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

// Addr 返回 Redis 地址
func (c *RedisConfig) Addr() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

// JWTConfig JWT 配置
type JWTConfig struct {
	Secret        string        `mapstructure:"secret"`
	Expire        time.Duration `mapstructure:"expire"`         // 过期时间，如 "24h"（time.Duration）
	RefreshExpire time.Duration `mapstructure:"refresh_expire"` // 刷新 token 过期时间，如 "168h"
	Issuer        string        `mapstructure:"issuer"`         // 签发者
	Algorithm     string        `mapstructure:"algorithm"`      // 签名算法：HS256(默认)/RS256
}

// SMSConfig 短信配置
type SMSConfig struct {
	Enabled         bool   `mapstructure:"enabled"`
	Provider        string `mapstructure:"provider"`
	AccessKeyID     string `mapstructure:"access_key_id"`
	AccessKeySecret string `mapstructure:"access_key_secret"`
	SignName        string `mapstructure:"sign_name"`
	TemplateCode    string `mapstructure:"template_code"`
}

// StorageConfig 文件存储配置
type StorageConfig struct {
	Driver string             `mapstructure:"driver"` // local 或 oss
	Local  LocalStorageConfig `mapstructure:"local"`
	OSS    OSSStorageConfig   `mapstructure:"oss"`
}

// UploadPolicy 上传安全策略（C4b）。零值表示不限制，向后兼容；
// 生产环境强烈建议显式配置 MaxSizeBytes 与 AllowedExts / AllowedMIMEs。
type UploadPolicy struct {
	// MaxSizeBytes 单文件大小上限（字节）。0 = 不限制。
	MaxSizeBytes int64 `mapstructure:"max_size_bytes"`
	// AllowedExts 允许的扩展名白名单（小写、含点，如 ".jpg"）。空 = 不限制。
	AllowedExts []string `mapstructure:"allowed_exts"`
	// AllowedMIMEs 允许的 MIME 类型白名单（小写，如 "image/jpeg"）。
	// 非空时用 http.DetectContentType 嗅探文件前 512 字节校验。空 = 不嗅探。
	AllowedMIMEs []string `mapstructure:"allowed_mime_types"`
}

// LocalStorageConfig 本地存储配置
type LocalStorageConfig struct {
	Path    string `mapstructure:"path"`
	BaseURL string `mapstructure:"base_url"`
	// Upload 上传安全策略（可选，零值不限制）。
	Upload UploadPolicy `mapstructure:"upload"`
	// MaxReadBytes Get 读取单文件上限（字节）。0 = 默认 100MB，-1 = 不限制。
	MaxReadBytes int64 `mapstructure:"max_read_bytes"`
}

// OSSStorageConfig OSS 存储配置
type OSSStorageConfig struct {
	Endpoint        string `mapstructure:"endpoint"`
	Bucket          string `mapstructure:"bucket"`
	AccessKeyID     string `mapstructure:"access_key_id"`
	AccessKeySecret string `mapstructure:"access_key_secret"`
	BaseURL         string `mapstructure:"base_url"`
	// Upload 上传安全策略（可选，零值不限制）。
	Upload UploadPolicy `mapstructure:"upload"`
	// MaxReadBytes Get 读取单文件上限（字节）。0 = 默认 100MB，-1 = 不限制。
	MaxReadBytes int64 `mapstructure:"max_read_bytes"`
}

// LogConfig 日志配置
type LogConfig struct {
	Dir        string `mapstructure:"dir"`
	MaxSize    int    `mapstructure:"max_size"` // MB
	MaxBackups int    `mapstructure:"max_backups"`
	MaxAge     int    `mapstructure:"max_age"` // 天
	Compress   bool   `mapstructure:"compress"`
}

// UploadConfig 上传配置
type UploadConfig struct {
	MaxFileSize       int      `mapstructure:"max_file_size"`       // 最大图片大小（MB）
	MaxVideoSize      int      `mapstructure:"max_video_size"`      // 最大视频大小（MB）
	MaxAvatarSize     int      `mapstructure:"max_avatar_size"`     // 最大头像大小（MB）
	AllowedImageTypes []string `mapstructure:"allowed_image_types"` // 允许的图片 MIME 类型
	AllowedVideoTypes []string `mapstructure:"allowed_video_types"` // 允许的视频 MIME 类型
}

// CORSConfig CORS 跨域配置
type CORSConfig struct {
	AllowedOrigins   []string `mapstructure:"allowed_origins"`   // 允许的域名列表
	AllowedMethods   []string `mapstructure:"allowed_methods"`   // 允许的方法
	AllowedHeaders   []string `mapstructure:"allowed_headers"`   // 允许的请求头
	ExposedHeaders   []string `mapstructure:"exposed_headers"`   // 暴露的响应头
	AllowCredentials bool     `mapstructure:"allow_credentials"` // 是否允许携带凭证
	MaxAge           int      `mapstructure:"max_age"`           // 预检请求缓存时间（秒）
}

// GetAllowedOrigins 获取允许的域名列表
func (c *CORSConfig) GetAllowedOrigins() []string {
	if c == nil || len(c.AllowedOrigins) == 0 {
		return []string{}
	}
	return c.AllowedOrigins
}

// GetAllowedMethods 获取允许的方法列表
func (c *CORSConfig) GetAllowedMethods() []string {
	if c == nil || len(c.AllowedMethods) == 0 {
		return []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"}
	}
	return c.AllowedMethods
}

// GetAllowedHeaders 获取允许的请求头列表
func (c *CORSConfig) GetAllowedHeaders() []string {
	if c == nil || len(c.AllowedHeaders) == 0 {
		return []string{"Origin", "Content-Type", "Content-Length", "Accept-Encoding", "X-CSRF-Token", "Authorization", "X-Requested-With"}
	}
	return c.AllowedHeaders
}

// GetExposedHeaders 获取暴露的响应头列表
func (c *CORSConfig) GetExposedHeaders() []string {
	if c == nil || len(c.ExposedHeaders) == 0 {
		return []string{"Content-Length", "Access-Control-Allow-Origin", "Access-Control-Allow-Headers", "Content-Type"}
	}
	return c.ExposedHeaders
}

// GetMaxAge 获取预检请求缓存时间
func (c *CORSConfig) GetMaxAge() int {
	if c == nil || c.MaxAge <= 0 {
		return 86400 // 默认 24 小时
	}
	return c.MaxAge
}

// Manager 配置管理器
type Manager struct {
	mu        sync.RWMutex
	path      string
	v         *viper.Viper
	cfg       *Config
	callbacks []func(*Config)
	// watcher 是自管的 fsnotify 监听器（C10d）。nil 表示未启用文件监听。
	// 由 StartWatcher 创建、StopWatcher 关闭，避免依赖 viper 内部无法停止的 watcher。
	watcher *fsnotify.Watcher
	// watchDone 在监听 goroutine 退出时被 close，供 StopWatcher 等待退出确认。
	watchDone chan struct{}
}

// defaultManager 是包级默认管理器（C10a）。改用 atomic.Pointer 保护读写，
// 消除原裸指针置换与请求 goroutine 无锁读之间的数据竞争。
var defaultManager atomic.Pointer[Manager]

func init() {
	defaultManager.Store(NewManager(""))
}

// NewManager 创建配置管理器
func NewManager(configPath string) *Manager {
	return &Manager{path: configPath}
}

func newViper(configPath string) *viper.Viper {
	v := viper.New()
	v.SetConfigFile(configPath)
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	return v
}

// unmarshalConfig 将 viper 解析到 Config，启用 string→time.Duration decode hook，
// 使 ServerConfig/JWTConfig 的 Duration 字段可写 "24h"/"15s" 等字符串。
func unmarshalConfig(v *viper.Viper, cfg *Config) error {
	return v.Unmarshal(cfg, viper.DecodeHook(mapstructure.StringToTimeDurationHookFunc()))
}

// Load 加载配置文件
func (m *Manager) Load() (*Config, error) {
	if m == nil || m.path == "" {
		return nil, ErrConfigNotLoaded
	}

	v := newViper(m.path)
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	var cfg Config
	if err := unmarshalConfig(v, &cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	m.mu.Lock()
	m.v = v
	m.cfg = &cfg
	m.mu.Unlock()

	// 防御性拷贝（C10c）：返回独立副本，避免调用方与框架内部 m.cfg 共享同一可变指针。
	// 调用方修改返回值的标量字段不影响 Get() 等读取路径。
	// 注意：这是浅拷贝——切片字段（如 CORSConfig.AllowedOrigins）仍与 m.cfg 共享底层数组，
	// 调用方不得修改切片元素（约定配置对象为只读）。需要完全独立可变副本时自行深拷贝。
	out := cfg
	return &out, nil
}

// LoadWithWatch 加载配置文件并启用热更新
func (m *Manager) LoadWithWatch(onChange func(*Config)) (*Config, error) {
	cfg, err := m.Load()
	if err != nil {
		return nil, err
	}
	if onChange != nil {
		m.RegisterCallback(onChange)
	}
	if err := m.StartWatcher(); err != nil {
		return nil, fmt.Errorf("启动配置监听失败: %w", err)
	}
	return cfg, nil
}

// RegisterCallback 注册配置变更回调
func (m *Manager) RegisterCallback(cb func(*Config)) {
	if m == nil || cb == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callbacks = append(m.callbacks, cb)
}

// StartWatcher 启动配置文件监听。使用自管的 fsnotify.Watcher（监听配置文件
// 所在目录以兼容编辑器改写/k8s ConfigMap 原子替换），文件变更时去抖后重新加载。
// 幂等：重复调用不会创建多个监听 goroutine。
func (m *Manager) StartWatcher() error {
	if m == nil {
		return ErrConfigNotLoaded
	}

	m.mu.Lock()
	if m.watcher != nil {
		// 已在监听，幂等返回
		m.mu.Unlock()
		return nil
	}
	if m.v == nil || m.path == "" {
		m.mu.Unlock()
		return ErrConfigNotLoaded
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		m.mu.Unlock()
		return fmt.Errorf("创建文件监听失败: %w", err)
	}
	// 监听父目录而非文件本身：vim/k8s 等通过"写临时文件 + rename"替换配置，
	// 直接监听文件会在 rename 后丢失。监听目录并按文件名过滤更稳健。
	dir := filepath.Dir(m.path)
	if err := w.Add(dir); err != nil {
		m.mu.Unlock()
		_ = w.Close()
		return fmt.Errorf("监听配置目录失败: %w", err)
	}
	m.watcher = w
	m.watchDone = make(chan struct{})
	target := filepath.Base(m.path)
	done := m.watchDone
	m.mu.Unlock()

	go m.watchLoop(w, target, done)
	return nil
}

// watchLoop 是文件监听 goroutine 主体。文件变更经去抖后调用 reload；
// watcher 被 Close（Events 通道关闭）时退出并 close done。
// done 由 StartWatcher 在锁内捕获传入，避免本 goroutine 读取 m.watchDone 字段
// 与 StopWatcher 写入竞争。
func (m *Manager) watchLoop(w *fsnotify.Watcher, target string, done chan struct{}) {
	defer close(done)
	const debounce = 200 * time.Millisecond
	var timer *time.Timer
	for {
		select {
		case ev, ok := <-w.Events:
			if !ok {
				return
			}
			if filepath.Base(ev.Name) != target {
				continue
			}
			if !ev.Has(fsnotify.Create) && !ev.Has(fsnotify.Write) &&
				!ev.Has(fsnotify.Remove) && !ev.Has(fsnotify.Rename) {
				continue
			}
			// 去抖：合并编辑器/工具的连续写事件，仅最后一次触发重载。
			if timer != nil {
				timer.Stop()
			}
			timer = time.AfterFunc(debounce, func() {
				// reload 内部对非法配置保留旧配置（C10b），错误被忽略——
				// 监听路径无法向上传播错误，保留旧配置即正确语义。
				_ = m.reload()
			})
		case _, ok := <-w.Errors:
			if !ok {
				return
			}
			// 非致命错误：继续监听。
		}
	}
}

// StopWatcher 停止配置文件监听并释放 watcher（C10d）。幂等。
// 关闭 fsnotify watcher → Events 通道关闭 → watchLoop 退出 → 等待 watchDone。
func (m *Manager) StopWatcher() {
	if m == nil {
		return
	}
	m.mu.Lock()
	w := m.watcher
	done := m.watchDone
	m.watcher = nil
	m.watchDone = nil
	m.mu.Unlock()
	if w == nil {
		return
	}
	_ = w.Close()
	if done != nil {
		<-done
	}
}

// Get 获取配置。
//
// 返回的是 Manager 内部持有的配置指针（共享），调用方**必须视为只读**：
// 修改返回值的标量或切片元素会污染全局配置并与其他读取 goroutine 竞争。
// 需要可变副本时用 Load()（返回防御性拷贝）或自行深拷贝。
func (m *Manager) Get() *Config {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg
}

// GetViper 获取 viper 实例
func (m *Manager) GetViper() *viper.Viper {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.v
}

// Set 手动设置配置
func (m *Manager) Set(cfg *Config) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg = cfg
	if cfg == nil {
		m.v = nil
	}
}

// Reload 重新加载配置文件。读取、解析、校验（C10b）任一步失败均保留旧配置并返回错误；
// 仅当新配置通过 Validate 后才替换 m.cfg 并触发回调。
func (m *Manager) Reload() error {
	if m == nil {
		return ErrConfigNotLoaded
	}
	return m.reload()
}

// reload 是 Reload 与文件监听共享的重载实现。全程持写锁以串行化对 viper 的
// ReadInConfig 访问（viper 非完全并发安全），并在替换前强制 Validate（C10b）。
func (m *Manager) reload() error {
	m.mu.Lock()
	v := m.v
	if v == nil {
		m.mu.Unlock()
		return ErrConfigNotLoaded
	}
	if err := v.ReadInConfig(); err != nil {
		m.mu.Unlock()
		return fmt.Errorf("读取配置文件失败: %w", err)
	}
	var newCfg Config
	if err := unmarshalConfig(v, &newCfg); err != nil {
		m.mu.Unlock()
		return fmt.Errorf("解析配置文件失败: %w", err)
	}
	if err := newCfg.Validate(); err != nil {
		// 非法配置保留旧配置，不得静默发布（C10b）
		m.mu.Unlock()
		return err
	}
	m.cfg = &newCfg
	cbs := make([]func(*Config), len(m.callbacks))
	copy(cbs, m.callbacks)
	m.mu.Unlock()

	for _, cb := range cbs {
		cb(&newCfg)
	}
	return nil
}

// Load 加载配置文件
func Load(configPath string) (*Config, error) {
	m := NewManager(configPath)
	defaultManager.Store(m)
	return m.Load()
}

// LoadWithWatch 加载配置文件并启用热更新
func LoadWithWatch(configPath string, onChange func(*Config)) (*Config, error) {
	m := NewManager(configPath)
	defaultManager.Store(m)
	return m.LoadWithWatch(onChange)
}

// RegisterCallback 注册配置变更回调
func RegisterCallback(cb func(*Config)) {
	defaultManager.Load().RegisterCallback(cb)
}

// StartWatcher 启动配置文件监听
func StartWatcher() error {
	return defaultManager.Load().StartWatcher()
}

// StopWatcher 停止配置文件监听
func StopWatcher() {
	defaultManager.Load().StopWatcher()
}

// Get 获取全局配置
func Get() *Config {
	return defaultManager.Load().Get()
}

// GetViper 获取 viper 实例（用于扩展配置）
func GetViper() *viper.Viper {
	return defaultManager.Load().GetViper()
}

// Set 手动设置配置（用于测试或动态修改）
func Set(cfg *Config) {
	defaultManager.Load().Set(cfg)
}

// Reload 重新加载配置文件
func Reload() error {
	return defaultManager.Load().Reload()
}

// SetDefaultManager 替换全局默认配置管理器。
// 主要供应用层（如 App）在持有自己的 Manager 时使用，
// 使 config.Get / config.GetString 等便捷函数仍然能取到正确的配置。
// 传入 nil 表示重置为空管理器。
//
// C10a：经 atomic.Pointer.Store 原子置换，消除与并发读取（Get 等）的数据竞争。
func SetDefaultManager(m *Manager) {
	if m == nil {
		defaultManager.Store(NewManager(""))
		return
	}
	defaultManager.Store(m)
}

// GetString 获取字符串配置
func GetString(key string) string {
	v := GetViper()
	if v == nil {
		return ""
	}
	return v.GetString(key)
}

// GetInt 获取整数配置
func GetInt(key string) int {
	v := GetViper()
	if v == nil {
		return 0
	}
	return v.GetInt(key)
}

// GetBool 获取布尔配置
func GetBool(key string) bool {
	v := GetViper()
	if v == nil {
		return false
	}
	return v.GetBool(key)
}

// GetStringMap 获取字符串映射配置
func GetStringMap(key string) map[string]any {
	v := GetViper()
	if v == nil {
		return nil
	}
	return v.GetStringMap(key)
}

// IsDevelopment 是否开发环境
func (c *Config) IsDevelopment() bool {
	if c == nil {
		return false
	}
	// 优先使用 App.Env
	if c.App.Env != "" {
		return c.App.IsDev()
	}
	return c.Server.Mode == "development"
}

// IsProduction 是否生产环境
func (c *Config) IsProduction() bool {
	if c == nil {
		return false
	}
	// 优先使用 App.Env
	if c.App.Env != "" {
		return c.App.IsProd()
	}
	return c.Server.Mode == "production"
}

// GetAppName 获取应用名称
func (c *Config) GetAppName() string {
	if c == nil {
		return ""
	}
	return c.App.Name
}

// GetSiteName 获取站点别名
func (c *Config) GetSiteName() string {
	if c == nil {
		return ""
	}
	return c.App.GetSiteName()
}
