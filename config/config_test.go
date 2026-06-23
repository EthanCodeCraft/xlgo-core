package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EthanCodeCraft/xlgo-core/config"
)

func getTempDir() string {
	dir := os.TempDir()
	return filepath.Join(dir, "xlgo_test")
}

func setupTempFile(name, content string) (string, error) {
	dir := getTempDir()
	os.MkdirAll(dir, 0755)
	path := filepath.Join(dir, name)
	err := os.WriteFile(path, []byte(content), 0644)
	return path, err
}

func TestAppConfig(t *testing.T) {
	// 测试 AppConfig 方法
	app := config.AppConfig{
		Name:     "TestApp",
		SiteName: "test_site",
		Version:  "1.0.0",
		Env:      "dev",
		Debug:    true,
		BaseURL:  "https://test.example.com",
	}

	// GetSiteName
	if app.GetSiteName() != "test_site" {
		t.Error("GetSiteName failed")
	}

	// IsDebug
	if !app.IsDebug() {
		t.Error("IsDebug failed")
	}

	// IsDev
	if !app.IsDev() {
		t.Error("IsDev failed")
	}

	// IsProd
	if app.IsProd() {
		t.Error("IsProd should be false for dev")
	}

	// 测试 nil 安全性
	var nilApp *config.AppConfig
	if nilApp.GetSiteName() != "" {
		t.Error("nil GetSiteName should return empty")
	}
	if nilApp.IsDebug() {
		t.Error("nil IsDebug should return false")
	}
}

func TestAppConfigIsProd(t *testing.T) {
	app := config.AppConfig{Env: "prod"}
	if !app.IsProd() {
		t.Error("IsProd failed for prod")
	}

	app2 := config.AppConfig{Env: "production"}
	if !app2.IsProd() {
		t.Error("IsProd failed for production")
	}
}

func TestDatabaseConfigDSN(t *testing.T) {
	db := config.DatabaseConfig{
		Host:     "localhost",
		Port:     3306,
		User:     "root",
		Password: "password",
		Name:     "testdb",
	}

	dsn := db.DSN()
	expected := "root:password@tcp(localhost:3306)/testdb?charset=utf8mb4&parseTime=True&loc=Local"
	if dsn != expected {
		t.Errorf("DSN = %s, want %s", dsn, expected)
	}
}

func TestDatabaseConfigPostgresDSN(t *testing.T) {
	db := config.DatabaseConfig{
		Driver:   config.DriverPostgres,
		Host:     "localhost",
		Port:     5432,
		User:     "postgres",
		Password: "password",
		Name:     "testdb",
	}

	dsn := db.DSN()
	expected := "host=localhost port=5432 user=postgres password=password dbname=testdb sslmode=disable TimeZone=Asia/Shanghai"
	if dsn != expected {
		t.Errorf("Postgres DSN = %s, want %s", dsn, expected)
	}

	// 显式 MySQL DSN 不受 Driver 影响
	if db.MySQLDSN() == "" {
		t.Error("MySQLDSN should not be empty")
	}
}

func TestDatabaseConfigCustomDSN(t *testing.T) {
	db := config.DatabaseConfig{
		Driver:    config.DriverPostgres,
		CustomDSN: "custom-connection-string",
	}
	if db.DSN() != "custom-connection-string" {
		t.Errorf("CustomDSN should take precedence, got %s", db.DSN())
	}
}

func TestRedisConfigAddr(t *testing.T) {
	redis := config.RedisConfig{
		Host: "localhost",
		Port: 6379,
	}

	addr := redis.Addr()
	if addr != "localhost:6379" {
		t.Errorf("Addr = %s, want localhost:6379", addr)
	}
}

func TestConfigLoad(t *testing.T) {
	// 创建临时配置文件
	content := `
app:
  name: "测试应用"
  site_name: "test_api"
  version: "1.0.0"
  env: "dev"
  debug: true

server:
  port: 8080
  mode: "development"

database:
  host: "localhost"
  port: 3306
  user: "root"
  password: "test"
  name: "testdb"

redis:
  host: "localhost"
  port: 6379
  password: ""
  db: 0

jwt:
  secret: "test-secret-12345678901234567890123456789012"
  expire: "1h"
`
	tmpFile, err := setupTempFile("test_config.yaml", content)
	if err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}
	defer os.Remove(tmpFile)
	defer os.Remove(getTempDir())

	// 重置全局状态
	config.Set(nil)

	// 加载配置
	cfg, err := config.Load(tmpFile)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	// 验证配置
	if cfg.App.Name != "测试应用" {
		t.Errorf("App.Name = %s", cfg.App.Name)
	}
	if cfg.App.SiteName != "test_api" {
		t.Errorf("App.SiteName = %s", cfg.App.SiteName)
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("Server.Port = %d", cfg.Server.Port)
	}
	if cfg.Database.Host != "localhost" {
		t.Errorf("Database.Host = %s", cfg.Database.Host)
	}

	// 测试 Get
	cfg2 := config.Get()
	if cfg2 == nil {
		t.Error("Get returned nil")
	}

	// 测试 GetSiteName
	if cfg.GetSiteName() != "test_api" {
		t.Errorf("GetSiteName = %s", cfg.GetSiteName())
	}

	// 测试 GetAppName
	if cfg.GetAppName() != "测试应用" {
		t.Errorf("GetAppName = %s", cfg.GetAppName())
	}

	// 测试 IsDevelopment
	if !cfg.IsDevelopment() {
		t.Error("IsDevelopment should be true")
	}

	// 测试 IsProduction
	if cfg.IsProduction() {
		t.Error("IsProduction should be false")
	}

	// 测试 GetString/GetInt (子测试)
	t.Run("GetString", func(t *testing.T) {
		val := config.GetString("app.site_name")
		if val != "test_api" {
			t.Errorf("GetString = %s, want test_api", val)
		}
	})

	t.Run("GetInt", func(t *testing.T) {
		port := config.GetInt("server.port")
		if port != 8080 {
			t.Errorf("GetInt = %d, want 8080", port)
		}
	})

	t.Run("GetBool", func(t *testing.T) {
		if config.GetBool("nonexistent") != false {
			t.Error("GetBool should return false for nonexistent")
		}
	})
}

func TestConfigGetString(_ *testing.T) {
	// 已在 TestConfigLoad 中作为子测试完成
	// 此函数保留为占位符，避免删除后影响其他测试引用
}

func TestConfigLoadReloadsDifferentFiles(t *testing.T) {
	first, err := setupTempFile("first_config.yaml", "app:\n  name: first\nserver:\n  port: 1001\n")
	if err != nil {
		t.Fatalf("WriteFile first error: %v", err)
	}
	second, err := setupTempFile("second_config.yaml", "app:\n  name: second\nserver:\n  port: 1002\n")
	if err != nil {
		t.Fatalf("WriteFile second error: %v", err)
	}
	defer os.Remove(first)
	defer os.Remove(second)

	cfg, err := config.Load(first)
	if err != nil {
		t.Fatalf("Load first error: %v", err)
	}
	if cfg.App.Name != "first" || cfg.Server.Port != 1001 {
		t.Fatalf("unexpected first config: %+v", cfg)
	}

	cfg, err = config.Load(second)
	if err != nil {
		t.Fatalf("Load second error: %v", err)
	}
	if cfg.App.Name != "second" || cfg.Server.Port != 1002 {
		t.Fatalf("unexpected second config: %+v", cfg)
	}
}

func TestConfigManagerIsolation(t *testing.T) {
	first, err := setupTempFile("manager_first.yaml", "app:\n  name: manager_first\n")
	if err != nil {
		t.Fatalf("WriteFile first error: %v", err)
	}
	second, err := setupTempFile("manager_second.yaml", "app:\n  name: manager_second\n")
	if err != nil {
		t.Fatalf("WriteFile second error: %v", err)
	}
	defer os.Remove(first)
	defer os.Remove(second)

	m1 := config.NewManager(first)
	m2 := config.NewManager(second)
	cfg1, err := m1.Load()
	if err != nil {
		t.Fatalf("m1 Load error: %v", err)
	}
	cfg2, err := m2.Load()
	if err != nil {
		t.Fatalf("m2 Load error: %v", err)
	}
	if cfg1.App.Name != "manager_first" || cfg2.App.Name != "manager_second" {
		t.Fatalf("managers should be isolated: %+v %+v", cfg1, cfg2)
	}
}

func TestConfigSet(t *testing.T) {
	cfg := &config.Config{
		App: config.AppConfig{
			Name:    "Manual",
			SiteName: "manual_site",
		},
	}

	config.Set(cfg)

	if config.Get().App.Name != "Manual" {
		t.Error("Set failed")
	}
}

func TestConfigMethodsOnNil(t *testing.T) {
	// 测试 nil Config 的方法安全性
	var nilCfg *config.Config

	if nilCfg.IsDevelopment() {
		t.Error("nil IsDevelopment should be false")
	}
	if nilCfg.IsProduction() {
		t.Error("nil IsProduction should be false")
	}
	if nilCfg.GetAppName() != "" {
		t.Error("nil GetAppName should return empty")
	}
	if nilCfg.GetSiteName() != "" {
		t.Error("nil GetSiteName should return empty")
	}
}

func TestStorageConfig(t *testing.T) {
	cfg := config.StorageConfig{
		Driver: "local",
		Local: config.LocalStorageConfig{
			Path:    "/uploads",
			BaseURL: "http://localhost/uploads",
		},
	}

	if cfg.Driver != "local" {
		t.Error("StorageConfig Driver failed")
	}
}

func TestLogConfig(t *testing.T) {
	log := config.LogConfig{
		Dir:        "/logs",
		MaxSize:    100,
		MaxBackups: 5,
		MaxAge:     30,
		Compress:   true,
	}

	if log.Dir != "/logs" {
		t.Error("LogConfig Dir failed")
	}
}

func TestUploadConfig(t *testing.T) {
	upload := config.UploadConfig{
		MaxFileSize:       10,
		MaxVideoSize:      100,
		AllowedImageTypes: []string{"image/jpeg", "image/png"},
	}

	if upload.MaxFileSize != 10 {
		t.Error("UploadConfig failed")
	}
}