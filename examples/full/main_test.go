package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EthanCodeCraft/xlgo-core/config"
	"github.com/EthanCodeCraft/xlgo-core/database"
	"github.com/EthanCodeCraft/xlgo-core/validation"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupExampleRouter(t *testing.T) (*gin.Engine, *gorm.DB) {
	t.Helper()

	database.RegisterDialect(database.DialectSpec{
		Name:      "sqlite_full_example_test",
		Dialector: func(dsn string) gorm.Dialector { return sqlite.Open(dsn) },
		DSN: func(c *config.DatabaseConfig) string {
			return c.CustomDSN
		},
	})

	cfg := &config.Config{
		Database: config.DatabaseConfig{
			Driver:    "sqlite_full_example_test",
			CustomDSN: filepath.Join(t.TempDir(), "full-example.db"),
		},
		JWT: config.JWTConfig{
			Secret:        "test-secret-for-full-example-at-least-32-bytes",
			Expire:        time.Hour,
			RefreshExpire: 24 * time.Hour,
			Issuer:        "xlgo",
			Algorithm:     "HS256",
		},
	}
	if err := config.Set(cfg); err != nil {
		t.Fatalf("设置测试配置失败: %v", err)
	}

	mgr := database.NewManager(cfg)
	if err := mgr.InitDB(context.Background(), cfg); err != nil {
		t.Fatalf("初始化测试数据库失败: %v", err)
	}
	db := mgr.Master()
	if err := db.AutoMigrate(&User{}); err != nil {
		t.Fatalf("迁移示例用户表失败: %v", err)
	}
	prev := database.SwapDefaultManager(mgr)
	t.Cleanup(func() {
		current := database.SwapDefaultManager(prev)
		if current != nil && current != prev {
			_ = current.Close()
		}
	})

	gin.SetMode(gin.TestMode)
	r := gin.New()
	registerRoutes(r.Group(""))
	if err := ensureExampleUser(context.Background()); err != nil {
		t.Fatalf("初始化示例用户失败: %v", err)
	}
	return r, db
}

func postJSON(t *testing.T, r http.Handler, path string, body string, token string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析响应 JSON 失败: %v, body=%s", err, rec.Body.String())
	}
	return rec.Code, payload
}

func tokenFromResponse(t *testing.T, payload map[string]any) string {
	t.Helper()
	data, ok := payload["data"].(map[string]any)
	if !ok {
		t.Fatalf("响应缺少 data 对象: %#v", payload)
	}
	token, ok := data["token"].(string)
	if !ok || strings.TrimSpace(token) == "" {
		t.Fatalf("响应缺少 token: %#v", payload)
	}
	return token
}

func TestFullExampleLoginAndPasswordFlow(t *testing.T) {
	r, db := setupExampleRouter(t)

	var seeded User
	if err := db.Where("username = ?", "alice").First(&seeded).Error; err != nil {
		t.Fatalf("示例用户 alice 应在启动时初始化: %v", err)
	}
	if seeded.Password == "" || seeded.Password == "secret" {
		t.Fatal("示例用户密码必须保存为 bcrypt 哈希，不能保存明文")
	}
	if !validation.CheckPassword(seeded.Password, "secret") {
		t.Fatal("示例用户 alice/secret 应能通过密码校验")
	}

	status, wrong := postJSON(t, r, "/api/v1/login", `{"username":"alice","password":"wrong"}`, "")
	if status != http.StatusOK {
		t.Fatalf("业务失败模式下 HTTP 状态应为 200，got %d", status)
	}
	if code, _ := wrong["code"].(float64); code == 0 {
		t.Fatalf("错误密码应返回业务失败: %#v", wrong)
	}

	_, loginPayload := postJSON(t, r, "/api/v1/login", `{"username":"alice","password":"secret"}`, "")
	token := tokenFromResponse(t, loginPayload)

	_, createPayload := postJSON(t, r, "/api/v1/users", `{"username":"bob","password":"s3cret"}`, token)
	if code, _ := createPayload["code"].(float64); code != 0 {
		t.Fatalf("带 token 创建用户应成功: %#v", createPayload)
	}

	var bob User
	if err := db.Where("username = ?", "bob").First(&bob).Error; err != nil {
		t.Fatalf("创建后的用户应写入数据库: %v", err)
	}
	if bob.Password == "" || bob.Password == "s3cret" {
		t.Fatal("创建用户时必须保存 bcrypt 哈希，不能保存明文")
	}
	if !validation.CheckPassword(bob.Password, "s3cret") {
		t.Fatal("创建用户的密码哈希应能通过校验")
	}

	_, bobLogin := postJSON(t, r, "/api/v1/login", `{"username":"bob","password":"s3cret"}`, "")
	_ = tokenFromResponse(t, bobLogin)
}
