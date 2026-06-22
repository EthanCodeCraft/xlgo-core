package xlgo_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	xlgo "github.com/EthanCodeCraft/xlgo-core"
	"github.com/EthanCodeCraft/xlgo-core/config"
	"github.com/EthanCodeCraft/xlgo-core/router"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func testConfig(port int) *config.Config {
	return &config.Config{
		App:    config.AppConfig{Env: "test"},
		Server: config.ServerConfig{Port: port, Mode: "development"},
		Log:    config.LogConfig{Dir: filepath.Join(os.TempDir(), "xlgo_app_test_logs"), MaxSize: 1, MaxBackups: 1, MaxAge: 1},
	}
}

func writeConfig(t *testing.T, name string, port int) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	content := "app:\n  env: test\nserver:\n  port: " + strconv.Itoa(port) + "\n  mode: development\nlog:\n  dir: " + filepath.ToSlash(filepath.Join(dir, "logs")) + "\n  max_size: 1\n  max_backups: 1\n  max_age: 1\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestAppWithConfigPath(t *testing.T) {
	path := writeConfig(t, "config.yaml", 18081)
	app := xlgo.New(xlgo.WithConfigPath(path))
	if err := app.Init(); err != nil {
		t.Fatalf("Init error: %v", err)
	}
	if app.GetServer() != nil {
		t.Fatal("Init should not start server")
	}
}

func TestAppWithConfigNoDefaultRoutes(t *testing.T) {
	app := xlgo.New(xlgo.WithConfig(testConfig(18082)))
	if err := app.Init(); err != nil {
		t.Fatalf("Init error: %v", err)
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	app.GetRouter().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected /health 404 without health routes, got %d", w.Code)
	}
}

func TestAppWithHealthRoutes(t *testing.T) {
	app := xlgo.New(xlgo.WithConfig(testConfig(18083)), xlgo.WithHealthRoutes())
	if err := app.Init(); err != nil {
		t.Fatalf("Init error: %v", err)
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	app.GetRouter().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected /health 200, got %d", w.Code)
	}
}

func TestAppWithHealthCheckFailure(t *testing.T) {
	app := xlgo.New(
		xlgo.WithConfig(testConfig(18084)),
		xlgo.WithHealthRoutes(),
		xlgo.WithHealthCheck("custom", func(_ context.Context) error { return errors.New("down") }),
	)
	if err := app.Init(); err != nil {
		t.Fatalf("Init error: %v", err)
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	app.GetRouter().ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected /health 503, got %d", w.Code)
	}
}

func TestAppMigratorRequiresMySQL(t *testing.T) {
	app := xlgo.New(
		xlgo.WithConfig(testConfig(18085)),
		xlgo.WithMigrator(func(_ *gorm.DB) error { return nil }),
	)
	err := app.Init()
	if err == nil || !strings.Contains(err.Error(), "未启用 MySQL") {
		t.Fatalf("expected mysql disabled migrator error, got %v", err)
	}
}

func TestAppRoutesModule(t *testing.T) {
	app := xlgo.New(
		xlgo.WithConfig(testConfig(18086)),
		xlgo.WithModules(router.ModuleFunc(func(r *gin.RouterGroup) {
			r.GET("/hello", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"hello": "world"}) })
		})),
	)
	if err := app.Init(); err != nil {
		t.Fatalf("Init error: %v", err)
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/hello", nil)
	app.GetRouter().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected module route 200, got %d", w.Code)
	}
}

func TestAppWithoutDefaultRoutesOverrides(t *testing.T) {
	app := xlgo.New(
		xlgo.WithConfig(testConfig(18087)),
		xlgo.WithDefaultRoutes(),
		xlgo.WithoutDefaultRoutes(),
	)
	if err := app.Init(); err != nil {
		t.Fatalf("Init error: %v", err)
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	app.GetRouter().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected /health 404 after WithoutDefaultRoutes, got %d", w.Code)
	}
}

// TestAppWithSwaggerRoutesOnly 验证可以单独启用 Swagger 而不开启 health。
func TestAppWithSwaggerRoutesOnly(t *testing.T) {
	app := xlgo.New(
		xlgo.WithConfig(testConfig(18088)),
		xlgo.WithSwaggerRoutes(),
	)
	if err := app.Init(); err != nil {
		t.Fatalf("Init error: %v", err)
	}

	// /health 不应注册
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	app.GetRouter().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected /health 404 when only Swagger enabled, got %d", w.Code)
	}

	// /swagger/* 应注册（具体子路径返回什么取决于 swag 文档是否生成，这里只验证未 404）
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/swagger/index.html", nil)
	app.GetRouter().ServeHTTP(w, req)
	if w.Code == http.StatusNotFound {
		t.Fatalf("expected /swagger/index.html to be registered, got 404")
	}
}

// TestAppWithoutAutoMigrateOverride 验证 WithoutAutoMigrate 能覆盖
// 之前通过 WithMigrator 隐式开启的迁移，不再要求 MySQL 已启用。
func TestAppWithoutAutoMigrateOverride(t *testing.T) {
	migratorCalled := false
	app := xlgo.New(
		xlgo.WithConfig(testConfig(18089)),
		xlgo.WithMigrator(func(_ *gorm.DB) error {
			migratorCalled = true
			return nil
		}),
		xlgo.WithoutAutoMigrate(),
	)
	if err := app.Init(); err != nil {
		t.Fatalf("Init should succeed when AutoMigrate is disabled, got %v", err)
	}
	if migratorCalled {
		t.Fatal("migrator must not be called when WithoutAutoMigrate is applied")
	}
}

// TestAppDefaultsLightweight 验证 v1.0.2 起 xlgo.New 默认是轻量应用：
// 不启 MySQL/Redis/Storage、不注册 health/swagger 路由。
func TestAppDefaultsLightweight(t *testing.T) {
	app := xlgo.New(xlgo.WithConfig(testConfig(18090)))
	if err := app.Init(); err != nil {
		t.Fatalf("Init error: %v", err)
	}

	r := app.GetRouter()

	for _, path := range []string{"/health", "/swagger/index.html"} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected %s 404 by default, got %d", path, w.Code)
		}
	}

	// 默认未注册 MySQL/Redis 健康检查项，故 healthChecks 应为空
	// 这里通过启用 health 路由后只返回 status:ok 来间接验证
	app2 := xlgo.New(xlgo.WithConfig(testConfig(18091)), xlgo.WithHealthRoutes())
	if err := app2.Init(); err != nil {
		t.Fatalf("Init error: %v", err)
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	app2.GetRouter().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected /health 200 with no checks, got %d", w.Code)
	}
}

// TestAppWithConfigPathDrivesManager 验证 WithConfigPath 真正驱动 App 自己的
// config.Manager，并把它推到全局 default，使 config.Get 能取到加载后的配置。
func TestAppWithConfigPathDrivesManager(t *testing.T) {
	path := writeConfig(t, "config_drive.yaml", 18092)
	app := xlgo.New(xlgo.WithConfigPath(path))
	if err := app.Init(); err != nil {
		t.Fatalf("Init error: %v", err)
	}

	cfg := config.Get()
	if cfg == nil {
		t.Fatal("config.Get returned nil after WithConfigPath; manager not promoted to default")
	}
	if cfg.Server.Port != 18092 {
		t.Fatalf("expected port 18092 from loaded config, got %d", cfg.Server.Port)
	}
	if config.GetInt("server.port") != 18092 {
		t.Fatalf("expected GetInt(server.port)=18092, got %d", config.GetInt("server.port"))
	}
}
