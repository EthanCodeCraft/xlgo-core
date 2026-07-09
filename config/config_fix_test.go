package config_test

import (
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/EthanCodeCraft/xlgo-core/config"
)

// TestSetKeepsViperInSync_Hconfig1 固化 H-config-1：Set(cfg) 后 Get() 与
// GetString/GetInt/GetBool/GetViper 必须读到同一配置世界，消除原"Set 只更新类型化视图、
// viper 视图停留旧值"的静默分裂（违反 C1 单一配置源）。
func TestSetKeepsViperInSync_Hconfig1(t *testing.T) {
	cfg := &config.Config{
		App:    config.AppConfig{Name: "sync-test", SiteName: "sync_site", Env: "prod", Debug: true},
		Server: config.ServerConfig{Port: 7777},
	}
	if err := config.Set(cfg); err != nil {
		t.Fatalf("Set: %v", err)
	}
	defer config.SetDefaultManager(nil)

	// 类型化视图
	got := config.Get()
	if got == nil || got.App.Name != "sync-test" || got.Server.Port != 7777 {
		t.Fatalf("Get() 不一致: %+v", got)
	}
	// viper 视图必须同源
	if v := config.GetString("app.name"); v != "sync-test" {
		t.Errorf("GetString(app.name) = %q, want sync-test（H-config-1 同源）", v)
	}
	if v := config.GetInt("server.port"); v != 7777 {
		t.Errorf("GetInt(server.port) = %d, want 7777（H-config-1）", v)
	}
	if v := config.GetBool("app.debug"); v != true {
		t.Errorf("GetBool(app.debug) = %v, want true（H-config-1）", v)
	}
	if v := config.GetString("app.env"); v != "prod" {
		t.Errorf("GetString(app.env) = %q, want prod（H-config-1）", v)
	}
	vp := config.GetViper()
	if vp == nil || vp.GetString("app.name") != "sync-test" {
		t.Errorf("GetViper().GetString(app.name) 不一致（H-config-1）: %v", vp)
	}

	// Set(nil) 清空，GetString 返回空（m.v=nil）
	if err := config.Set(nil); err != nil {
		t.Fatalf("Set(nil): %v", err)
	}
	if v := config.GetString("app.name"); v != "" {
		t.Errorf("Set(nil) 后 GetString 应为空, got %q", v)
	}
}

// TestSetDurationGetDurationConsistent_Hconfig1 锁定 H-config-1 已知行为：Duration 字段经 Set
// 重建 viper 后，GetString 字面格式与文件加载不同（mapstructure 存 time.Duration -> Duration.String()），
// 但 typed view 与 GetDuration 在两条路径下一致。Duration 应经 typed view / GetDuration 读取。
func TestSetDurationGetDurationConsistent_Hconfig1(t *testing.T) {
	cfg := &config.Config{
		JWT: config.JWTConfig{Secret: strings.Repeat("k", 32), Expire: 24 * time.Hour},
	}
	if err := config.Set(cfg); err != nil {
		t.Fatalf("Set: %v", err)
	}
	defer config.SetDefaultManager(nil)

	if got := config.Get().JWT.Expire; got != 24*time.Hour {
		t.Errorf("Get().JWT.Expire = %v, want 24h", got)
	}
	if got := config.GetViper().GetDuration("jwt.expire"); got != 24*time.Hour {
		t.Errorf("GetDuration(jwt.expire) = %v, want 24h（Duration 同源应读 GetDuration/typed view）", got)
	}
}

// TestReloadCallbackPanicIsolation_Mconfig1 固化 M-config-1：单个热重载回调 panic 不得
// 向上传播、不得阻断后续回调、不得让 watcher/reload 链路静默失效。
func TestReloadCallbackPanicIsolation_Mconfig1(t *testing.T) {
	p := writeConfig(t, "m1_panic.yaml", validConfigYAML(8301))
	defer os.Remove(p)

	m := config.NewManager(p)
	if _, err := m.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	called := make(chan int, 4)
	m.RegisterCallback(func(*config.Config) {
		panic("boom from user callback") // 模拟用户回调 panic
	})
	m.RegisterCallback(func(c *config.Config) {
		called <- c.Server.Port
	})

	// Reload 不应把回调 panic 向上传播
	if err := m.Reload(); err != nil {
		t.Fatalf("Reload 不应传播回调 panic: %v", err)
	}
	// 第二个回调仍触发：panic 被隔离，未阻断后续回调
	select {
	case port := <-called:
		if port != 8301 {
			t.Errorf("第二个回调端口 = %d, want 8301", port)
		}
	case <-time.After(time.Second):
		t.Fatal("panic 后续回调未触发（M-config-1 隔离失败）")
	}

	// 后续 reload 仍正常，watcher/reload 链路未静默失效
	if err := os.WriteFile(p, []byte(validConfigYAML(8302)), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := m.Reload(); err != nil {
		t.Fatalf("panic 后 Reload 应仍可用: %v", err)
	}
	select {
	case port := <-called:
		if port != 8302 {
			t.Errorf("第二次 reload 回调端口 = %d, want 8302", port)
		}
	case <-time.After(time.Second):
		t.Fatal("panic 后 reload 链路静默失效（M-config-1）")
	}
}

// TestMySQLDSN_TLS_Mconfig2 固化 M-config-2 MySQL TLS：TLS=false 无 tls 参数；
// TLS=true 无 CA 用内置 tls=true；TLS=true 有 CA 用 tls=MySQLTLSConfigName。
func TestMySQLDSN_TLS_Mconfig2(t *testing.T) {
	db := config.DatabaseConfig{Host: "h", Port: 3306, User: "u", Password: "p", Name: "n"}
	if dsn := db.MySQLDSN(); strings.Contains(dsn, "tls=") {
		t.Errorf("TLS=false 不应含 tls 参数, dsn=%s", dsn)
	}

	db.TLS = true
	if dsn := db.MySQLDSN(); !strings.Contains(dsn, "&tls=true") {
		t.Errorf("TLS=true 无 CA 应用内置 tls=true, dsn=%s", dsn)
	}

	db.TLSRootCA = "/path/ca.pem"
	if dsn := db.MySQLDSN(); !strings.Contains(dsn, "&tls="+config.MySQLTLSConfigName) {
		t.Errorf("TLS=true 有 CA 应用 tls=%s, dsn=%s", config.MySQLTLSConfigName, dsn)
	}

	// CustomDSN 优先，TLS 字段不影响 DSN()
	db.CustomDSN = "custom-dsn"
	if dsn := db.DSN(); dsn != "custom-dsn" {
		t.Errorf("CustomDSN 应优先, got %s", dsn)
	}
}

// TestPostgresDSN_SSLMode_Mconfig2 固化 M-config-2 Postgres SSLMode：空默认 prefer；
// 显式值透传（sslmode 不加引号，与原 disable 格式一致）。
func TestPostgresDSN_SSLMode_Mconfig2(t *testing.T) {
	db := config.DatabaseConfig{Driver: config.DriverPostgres, Host: "h", Port: 5432, User: "u", Password: "p", Name: "n"}
	if dsn := db.PostgresDSN(); !strings.Contains(dsn, "sslmode=prefer ") {
		t.Errorf("空 SSLMode 默认 prefer, dsn=%s", dsn)
	}

	for _, mode := range []string{"disable", "allow", "require", "verify-ca", "verify-full"} {
		db.SSLMode = mode
		if dsn := db.PostgresDSN(); !strings.Contains(dsn, "sslmode="+mode+" ") {
			t.Errorf("SSLMode=%s 应透传, dsn=%s", mode, dsn)
		}
	}
}

// TestValidateMaxIdleExceedsOpen_Lconfig3 固化 L-config-3：MaxOpenConns>0 时
// MaxIdleConns>MaxOpenConns 视为配置错误；MaxOpenConns=0（无限）不校验。
func TestValidateMaxIdleExceedsOpen_Lconfig3(t *testing.T) {
	cfg := &config.Config{
		Database: config.DatabaseConfig{
			Host: "h", Port: 3306, User: "u", Password: "p", Name: "n",
			MaxOpenConns: 10, MaxIdleConns: 20,
		},
	}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "max_idle_conns") {
		t.Fatalf("MaxIdleConns>MaxOpenConns 应报错, got: %v", err)
	}

	cfg.Database.MaxOpenConns = 0 // 未配置/无限，不校验 idle
	if err := cfg.Validate(); err != nil {
		t.Fatalf("MaxOpenConns=0 时不该校验 idle, got: %v", err)
	}

	cfg.Database.MaxOpenConns = 20 // 合法
	if err := cfg.Validate(); err != nil {
		t.Fatalf("合法配置不应报错, got: %v", err)
	}
}

// TestValidatePostgresSSLMode_Mconfig2 固化 M-config-2：非法 SSLMode 在 Validate 阶段报错。
func TestValidatePostgresSSLMode_Mconfig2(t *testing.T) {
	cfg := &config.Config{
		Database: config.DatabaseConfig{
			Driver: config.DriverPostgres, Host: "h", Port: 5432, User: "u", Password: "p", Name: "n",
			SSLMode: "invalid-mode",
		},
	}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "ssl_mode") {
		t.Fatalf("非法 SSLMode 应报错, got: %v", err)
	}
	cfg.Database.SSLMode = "require"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("合法 SSLMode=require 不应报错, got: %v", err)
	}
}

// TestValidateTLSRootCAWithoutTLS_Mconfig2 固化 M-config-2：TLSRootCA 需配合 tls:true。
func TestValidateTLSRootCAWithoutTLS_Mconfig2(t *testing.T) {
	cfg := &config.Config{
		Database: config.DatabaseConfig{
			Host: "h", Port: 3306, User: "u", Password: "p", Name: "n",
			TLSRootCA: "/path/ca.pem", // TLS=false
		},
	}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "tls_root_ca") {
		t.Fatalf("TLSRootCA 无 tls:true 应报错, got: %v", err)
	}
	cfg.Database.TLS = true
	if err := cfg.Validate(); err != nil {
		t.Fatalf("TLS=true + TLSRootCA 合法不应报错, got: %v", err)
	}
}

// TestDSNAddrNilReceiver_Lconfig6 固化 L-config-6：DSN/MySQLDSN/PostgresDSN/Addr 对 nil receiver 安全。
func TestDSNAddrNilReceiver_Lconfig6(t *testing.T) {
	var db *config.DatabaseConfig
	if v := db.DSN(); v != "" {
		t.Errorf("nil DatabaseConfig.DSN() = %q, want empty", v)
	}
	if v := db.MySQLDSN(); v != "" {
		t.Errorf("nil MySQLDSN() = %q, want empty", v)
	}
	if v := db.PostgresDSN(); v != "" {
		t.Errorf("nil PostgresDSN() = %q, want empty", v)
	}
	var r *config.RedisConfig
	if v := r.Addr(); v != "" {
		t.Errorf("nil RedisConfig.Addr() = %q, want empty", v)
	}
}

// TestCloneDeepCopiesAllSliceFields_Mconfig4 固化 M-config-4：反射枚举 Config 所有切片/map 字段，
// 断言 Clone 深拷贝（不同底层数组/map）。fixture 必须填充每个切片字段--新增切片字段若未在 Clone
// 中处理、或未在 fixture 中填充，本测试都会失败，形成机械守卫。
func TestCloneDeepCopiesAllSliceFields_Mconfig4(t *testing.T) {
	original := allSlicesPopulatedConfig()
	clone := original.Clone()

	var check func(o, c reflect.Value, path string)
	check = func(o, c reflect.Value, path string) {
		for o.Kind() == reflect.Pointer {
			o = o.Elem()
		}
		for c.Kind() == reflect.Pointer {
			c = c.Elem()
		}
		if o.Kind() != reflect.Struct {
			return
		}
		for i := 0; i < o.NumField(); i++ {
			of := o.Field(i)
			cf := c.Field(i)
			p := path + "." + o.Type().Field(i).Name
			switch of.Kind() {
			case reflect.Pointer, reflect.Struct:
				check(of, cf, p)
			case reflect.Slice:
				if of.IsNil() || of.Len() == 0 {
					t.Errorf("M-config-4: fixture 未填充切片 %s（新增切片字段须同步 fixture 与 Clone）", p)
					continue
				}
				if cf.IsNil() || cf.Len() != of.Len() {
					t.Errorf("M-config-4: Clone 后切片 %s 为 nil 或长度不一致", p)
					continue
				}
				if of.Pointer() == cf.Pointer() {
					t.Errorf("M-config-4: Clone 未深拷贝切片 %s（共享底层数组）", p)
				}
			case reflect.Map:
				if of.IsNil() || of.Len() == 0 {
					t.Errorf("M-config-4: fixture 未填充 map %s", p)
					continue
				}
				if of.Pointer() == cf.Pointer() {
					t.Errorf("M-config-4: Clone 未深拷贝 map %s（共享 map）", p)
				}
			}
		}
	}
	check(reflect.ValueOf(original), reflect.ValueOf(clone), "Config")
}

// allSlicesPopulatedConfig 返回一个填充了所有切片字段的 Config，供 Clone 覆盖断言使用。
// 新增任何切片/map 字段到 Config 或其子结构体时，必须同步在此填充，否则
// TestCloneDeepCopiesAllSliceFields_Mconfig4 会以"未填充"失败提醒。
func allSlicesPopulatedConfig() *config.Config {
	return &config.Config{
		CORS: config.CORSConfig{
			AllowedOrigins: []string{"https://a.example.com"},
			AllowedMethods: []string{"GET"},
			AllowedHeaders: []string{"X-Test"},
			ExposedHeaders: []string{"X-Exp"},
		},
		Upload: config.UploadConfig{
			AllowedImageTypes: []string{"image/jpeg"},
			AllowedVideoTypes: []string{"video/mp4"},
		},
		Storage: config.StorageConfig{
			Local: config.LocalStorageConfig{
				Upload: config.UploadPolicy{
					AllowedExts:  []string{".jpg"},
					AllowedMIMEs: []string{"image/jpeg"},
				},
			},
			OSS: config.OSSStorageConfig{
				Upload: config.UploadPolicy{
					AllowedExts:  []string{".png"},
					AllowedMIMEs: []string{"image/png"},
				},
			},
		},
	}
}
