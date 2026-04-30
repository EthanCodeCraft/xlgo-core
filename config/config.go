package config

import (
	"fmt"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
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
// 评分: ⭐⭐⭐⭐⭐
// 理由: 多站点共用 Redis/日志时，通过 SiteName 区分来源
// 使用场景:
//   - 缓存键名前缀: cache:{site_name}:user:1
//   - 日志标识: [site_a] 2024-01-01 10:00:00 ...
//   - 站点追踪: Request-ID 带站点标识
//   - 分布式锁: lock:{site_name}:order:123
type AppConfig struct {
	Name        string `mapstructure:"name"`         // 应用名称，如 "用户管理系统"
	SiteName    string `mapstructure:"site_name"`    // 站点别名，如 "site_a"、"user_api"
	Version     string `mapstructure:"version"`      // 应用版本
	Env         string `mapstructure:"env"`          // 运行环境: dev/test/prod
	Debug       bool   `mapstructure:"debug"`        // 是否开启调试模式
	BaseURL     string `mapstructure:"base_url"`     // 应用基础URL
	TokenExpire int    `mapstructure:"token_expire"` // Token过期时间(秒)
}

// GetSiteName 获取站点别名，如果未设置则返回空字符串
// 评分: ⭐⭐⭐⭐⭐
// 理由: 安全获取，避免空指针
func (c *AppConfig) GetSiteName() string {
	if c == nil {
		return ""
	}
	return c.SiteName
}

// GetCachePrefix 获取缓存键名前缀
// 评分: ⭐⭐⭐⭐⭐
// 理由: 统一的缓存前缀生成方法
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

// ServerConfig 服务配置
type ServerConfig struct {
	Port int    `mapstructure:"port"`
	Mode string `mapstructure:"mode"` // development 或 production
}

// DatabaseConfig 数据库配置
type DatabaseConfig struct {
	Host         string `mapstructure:"host"`
	Port         int    `mapstructure:"port"`
	User         string `mapstructure:"user"`
	Password     string `mapstructure:"password"`
	Name         string `mapstructure:"name"`
	MaxIdleConns int    `mapstructure:"max_idle_conns"`
	MaxOpenConns int    `mapstructure:"max_open_conns"`
}

// DSN 返回 MySQL 连接字符串
func (c *DatabaseConfig) DSN() string {
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		c.User, c.Password, c.Host, c.Port, c.Name)
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
	Secret string `mapstructure:"secret"`
	Expire int    `mapstructure:"expire"` // 过期时间（秒）
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

// LocalStorageConfig 本地存储配置
type LocalStorageConfig struct {
	Path    string `mapstructure:"path"`
	BaseURL string `mapstructure:"base_url"`
}

// OSSStorageConfig OSS 存储配置
type OSSStorageConfig struct {
	Endpoint        string `mapstructure:"endpoint"`
	Bucket          string `mapstructure:"bucket"`
	AccessKeyID     string `mapstructure:"access_key_id"`
	AccessKeySecret string `mapstructure:"access_key_secret"`
	BaseURL         string `mapstructure:"base_url"`
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

var (
	globalConfig  *Config
	configOnce    sync.Once
	configMutex   sync.RWMutex
	loadErr       error
	viperInstance *viper.Viper
	callbacks     []func(*Config)
	callbacksMu   sync.RWMutex
)

// Load 加载配置文件（使用 sync.Once 确保只加载一次）
func Load(configPath string) (*Config, error) {
	configOnce.Do(func() {
		v := viper.New()
		v.SetConfigFile(configPath)
		v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
		v.AutomaticEnv()

		if err := v.ReadInConfig(); err != nil {
			loadErr = fmt.Errorf("读取配置文件失败: %w", err)
			return
		}

		var cfg Config
		if err := v.Unmarshal(&cfg); err != nil {
			loadErr = fmt.Errorf("解析配置文件失败: %w", err)
			return
		}

		configMutex.Lock()
		globalConfig = &cfg
		viperInstance = v
		configMutex.Unlock()
	})

	if loadErr != nil {
		return nil, loadErr
	}

	return Get(), nil
}

// LoadWithWatch 加载配置文件并启用热更新
func LoadWithWatch(configPath string, onChange func(*Config)) (*Config, error) {
	cfg, err := Load(configPath)
	if err != nil {
		return nil, err
	}

	// 注册回调
	if onChange != nil {
		RegisterCallback(onChange)
	}

	// 启动文件监听
	if err := StartWatcher(); err != nil {
		return nil, fmt.Errorf("启动配置监听失败: %w", err)
	}

	return cfg, nil
}

// RegisterCallback 注册配置变更回调
func RegisterCallback(cb func(*Config)) {
	callbacksMu.Lock()
	defer callbacksMu.Unlock()
	callbacks = append(callbacks, cb)
}

// StartWatcher 启动配置文件监听
func StartWatcher() error {
	configMutex.RLock()
	v := viperInstance
	configMutex.RUnlock()

	if v == nil {
		return fmt.Errorf("配置未加载")
	}

	// 使用 viper 内置的文件监听
	v.WatchConfig()

	// 设置配置变更回调
	v.OnConfigChange(func(e fsnotify.Event) {
		var newCfg Config
		if err := v.Unmarshal(&newCfg); err != nil {
			return
		}

		configMutex.Lock()
		globalConfig = &newCfg
		configMutex.Unlock()

		// 触发所有回调
		callbacksMu.RLock()
		cbs := make([]func(*Config), len(callbacks))
		copy(cbs, callbacks)
		callbacksMu.RUnlock()

		for _, cb := range cbs {
			cb(&newCfg)
		}
	})

	return nil
}

// StopWatcher 停止配置文件监听
func StopWatcher() {
	configMutex.RLock()
	v := viperInstance
	configMutex.RUnlock()

	if v != nil {
		// viper 的 WatchConfig 没有直接的停止方法
		// 但会在 viper 实例销毁时自动停止
	}
}

// Get 获取全局配置（使用读锁保护）
func Get() *Config {
	configMutex.RLock()
	defer configMutex.RUnlock()
	return globalConfig
}

// GetViper 获取 viper 实例（用于扩展配置）
func GetViper() *viper.Viper {
	configMutex.RLock()
	defer configMutex.RUnlock()
	return viperInstance
}

// Set 手动设置配置（用于测试或动态修改）
func Set(cfg *Config) {
	configMutex.Lock()
	defer configMutex.Unlock()
	globalConfig = cfg
}

// Reload 重新加载配置文件
func Reload() error {
	configMutex.RLock()
	v := viperInstance
	configMutex.RUnlock()

	if v == nil {
		return ErrConfigNotLoaded
	}

	if err := v.ReadInConfig(); err != nil {
		return fmt.Errorf("读取配置文件失败: %w", err)
	}

	var newCfg Config
	if err := v.Unmarshal(&newCfg); err != nil {
		return fmt.Errorf("解析配置文件失败: %w", err)
	}

	configMutex.Lock()
	globalConfig = &newCfg
	configMutex.Unlock()

	// 触发回调
	callbacksMu.RLock()
	cbs := make([]func(*Config), len(callbacks))
	copy(cbs, callbacks)
	callbacksMu.RUnlock()

	for _, cb := range cbs {
		cb(&newCfg)
	}

	return nil
}

// GetString 获取字符串配置
func GetString(key string) string {
	configMutex.RLock()
	defer configMutex.RUnlock()
	if viperInstance == nil {
		return ""
	}
	return viperInstance.GetString(key)
}

// GetInt 获取整数配置
func GetInt(key string) int {
	configMutex.RLock()
	defer configMutex.RUnlock()
	if viperInstance == nil {
		return 0
	}
	return viperInstance.GetInt(key)
}

// GetBool 获取布尔配置
func GetBool(key string) bool {
	configMutex.RLock()
	defer configMutex.RUnlock()
	if viperInstance == nil {
		return false
	}
	return viperInstance.GetBool(key)
}

// GetStringMap 获取字符串映射配置
func GetStringMap(key string) map[string]any {
	configMutex.RLock()
	defer configMutex.RUnlock()
	if viperInstance == nil {
		return nil
	}
	return viperInstance.GetStringMap(key)
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
// 评分: ⭐⭐⭐⭐⭐
// 理由: 全局统一获取站点别名，用于缓存前缀、日志标识等
func (c *Config) GetSiteName() string {
	if c == nil {
		return ""
	}
	return c.App.GetSiteName()
}
