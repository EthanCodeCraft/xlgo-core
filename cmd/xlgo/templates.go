package main

// Templates 存放所有代码生成模板
var templates = struct {
	Main        string
	Config      string
	GoMod       string
	Makefile    string
	Gitignore   string
	Handler     string
	HandlerMake string

	RepositoryMake string
	ModelMake      string
	ServiceMake    string
}{
	// Main 新项目主文件模板
	Main: `package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	"{{.Module}}/config"
	"{{.Module}}/database"
	"{{.Module}}/handler"
	"{{.Module}}/logger"
	"{{.Module}}/middleware"
	"{{.Module}}/storage"
	"{{.Module}}/validation"
)

var configPath string

func init() {
	flag.StringVar(&configPath, "config", "./config.yaml", "配置文件路径")
}

func main() {
	flag.Parse()

	// 加载配置
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Printf("加载配置失败: %v\n", err)
		os.Exit(1)
	}

	// 初始化日志
	if err := logger.Init(cfg); err != nil {
		fmt.Printf("初始化日志失败: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()

	// 初始化数据库
	if err := database.InitMySQL(cfg); err != nil {
		logger.Fatalf("初始化 MySQL 失败: %v", err)
	}
	defer database.Close()

	// 初始化 Redis
	if err := database.InitRedis(cfg); err != nil {
		logger.Fatalf("初始化 Redis 失败: %v", err)
	}
	defer database.CloseRedis()

	// 初始化存储
	if err := storage.Init(&cfg.Storage); err != nil {
		logger.Fatalf("初始化存储失败: %v", err)
	}

	// 初始化验证器
	validation.InitValidator()

	// 设置 Gin 模式
	if cfg.IsProduction() {
		gin.SetMode(gin.ReleaseMode)
	}

	// 创建路由
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.Logger())
	r.Use(middleware.CORS())

	// 静态文件服务
	r.Static("/public", "./public")

	// 注册路由
	setupRoutes(r)

	// 启动服务器
	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Server.Port),
		Handler: r,
	}

	// 优雅关闭
	go func() {
		logger.Infof("服务器启动，监听端口 %d", cfg.Server.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatalf("服务器启动失败: %v", err)
		}
	}()

	// 等待中断信号
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("正在关闭服务器...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logger.Fatalf("服务器强制关闭: %v", err)
	}

	logger.Info("服务器已关闭")
}

func setupRoutes(r *gin.Engine) {
	// 初始化限速器
	middleware.InitRateLimiters()

	// Swagger 文档
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	// 健康检查
	r.GET("/health", handler.HealthCheck)

	// API 路由
	api := r.Group("/api/v1")
	{
		// 首页
		api.GET("/", handler.Home)
	}
}
`,

	// Config 配置文件模板
	Config: `app:
  name: "{{.Name}}"
  site_name: "{{.NameLower}}"  # 站点别名，用于缓存键前缀、日志标识、多站点区分
  version: "1.0.0"
  env: "dev"                    # dev/test/prod
  debug: true
  base_url: "http://localhost:8080"
  token_expire: 86400           # Token过期时间(秒)

server:
  port: 8080
  mode: development

database:
  host: localhost
  port: 3306
  user: root
  password: your_password
  name: {{.NameLower}}
  max_idle_conns: 10
  max_open_conns: 100

redis:
  host: localhost
  port: 6379
  password: ""
  db: 0

jwt:
  secret: your_jwt_secret_key_here
  expire: 86400

storage:
  driver: local
  local:
    path: ./public
    base_url: http://localhost:8080/public

log:
  dir: ./logs
  max_size: 100
  max_backups: 30
  max_age: 30
  compress: true
`,

	// GoMod go.mod 文件模板
	GoMod: `module %s

go 1.25

require (
	github.com/gin-gonic/gin v1.9.1
	github.com/golang-jwt/jwt/v5 v5.2.0
	github.com/google/uuid v1.6.0
	github.com/redis/go-redis/v9 v9.5.1
	github.com/spf13/viper v1.18.2
	github.com/swaggo/files v1.0.1
	github.com/swaggo/gin-swagger v1.6.0
	github.com/swaggo/swag v1.16.3
	go.uber.org/zap v1.27.0
	golang.org/x/crypto v0.21.0
	gopkg.in/natefinch/lumberjack.v2 v2.2.1
	gorm.io/driver/mysql v1.5.4
	gorm.io/gorm v1.25.7
)
`,

	// Makefile 模板
	Makefile: `.PHONY: build run test clean tidy swagger

build:
	go build -o bin/server .

run:
	go run main.go

test:
	go test ./...

clean:
	rm -rf bin/
	rm -rf logs/

tidy:
	go mod tidy

swagger:
	swag init -g main.go -o ./swagger
`,

	// Gitignore 模板
	Gitignore: `# Binaries
bin/
*.exe
*.exe~
*.dll
*.so
*.dylib

# Test files
*.test
*.out
coverage.txt

# Go workspace file
go.work

# IDE
.idea/
.vscode/
*.swp
*.swo

# Environment
.env
.env.local

# Logs
logs/
*.log

# Config (keep example)
config.local.yaml
`,

	// Handler 新项目默认处理器模板
	Handler: `package handler

import (
	"github.com/gin-gonic/gin"

	"{{.Module}}/response"
)

// HealthCheck 健康检查
func HealthCheck(c *gin.Context) {
	response.Success(c, gin.H{
		"status": "ok",
	})
}

// Home 首页
func Home(c *gin.Context) {
	response.Success(c, gin.H{
		"message": "Welcome to {{.Name}}!",
	})
}
`,

	// HandlerMake make handler 命令模板
	HandlerMake: `package handler

import (
	"github.com/gin-gonic/gin"
	"xlgo/response"
)

// %sHandler %s 处理器
type %sHandler struct {
	// 可以注入 service
}

// New%sHandler 创建 %s 处理器
func New%sHandler() *%sHandler {
	return &%sHandler{}
}

// List 获取列表
// @Summary 获取%s列表
// @Tags %s
// @Accept json
// @Produce json
// @Success 200 {object} response.Response
// @Router /api/v1/%ss [get]
func (h *%sHandler) List(c *gin.Context) {
	response.Success(c, gin.H{
		"Items": []string{},
	})
}

// Get 获取详情
// @Summary 获取%s详情
// @Tags %s
// @Accept json
// @Produce json
// @Param id path int true "ID"
// @Success 200 {object} response.Response
// @Router /api/v1/%ss/{id} [get]
func (h *%sHandler) Get(c *gin.Context) {
	response.Success(c, gin.H{
		"id": c.Param("id"),
	})
}

// Create 创建
// @Summary 创建%s
// @Tags %s
// @Accept json
// @Produce json
// @Success 200 {object} response.Response
// @Router /api/v1/%ss [post]
func (h *%sHandler) Create(c *gin.Context) {
	response.Success(c, nil)
}

// Update 更新
// @Summary 更新%s
// @Tags %s
// @Accept json
// @Produce json
// @Param id path int true "ID"
// @Success 200 {object} response.Response
// @Router /api/v1/%ss/{id} [put]
func (h *%sHandler) Update(c *gin.Context) {
	response.Success(c, nil)
}

// Delete 删除
// @Summary 删除%s
// @Tags %s
// @Accept json
// @Produce json
// @Param id path int true "ID"
// @Success 200 {object} response.Response
// @Router /api/v1/%ss/{id} [delete]
func (h *%sHandler) Delete(c *gin.Context) {
	response.Success(c, nil)
}
`,

	// RepositoryMake make repository 命令模板
	RepositoryMake: `package repository

import (
	"context"

	"xlgo/database"
	"xlgo/model"
)

// %sRepository %s 仓库
type %sRepository struct {
	*BaseRepo[model.%s]
}

// New%sRepository 创建 %s 仓库
func New%sRepository() *%sRepository {
	return &%sRepository{
		BaseRepo: NewBaseRepo[model.%s](database.GetDB()),
	}
}

// FindByName 根据名称查询
func (r *%sRepository) FindByName(ctx context.Context, name string) (*model.%s, error) {
	var m model.%s
	err := r.GetDB().WithContext(ctx).Where("name = ?", name).First(&m).Error
	if err != nil {
		return nil, err
	}
	return &m, nil
}
`,

	// ModelMake make model 命令模板
	ModelMake: `package model

// %s %s 模型
type %s struct {
	BaseModel
	Name        string ` + "`" + `gorm:"size:100;not null" json:"name"` + "`" + `
	Description string ` + "`" + `gorm:"size:500" json:"description"` + "`" + `
	Status      int    ` + "`" + `gorm:"default:1" json:"status"` + "`" + ` // 1: 启用, 0: 禁用
}

// TableName 表名
func (%s) TableName() string {
	return "%ss"
}
`,

	// ServiceMake make service 命令模板
	ServiceMake: `package service

import (
	"context"

	"xlgo/model"
	"xlgo/repository"
)

// %sService %s 服务
type %sService struct {
	repo *repository.%sRepository
}

// New%sService 创建 %s 服务
func New%sService() *%sService {
	return &%sService{
		repo: repository.New%sRepository(),
	}
}

// List 获取列表
func (s *%sService) List(ctx context.Context, page, pageSize int) ([]model.%s, int64, error) {
	// TODO: 实现列表查询
	return nil, 0, nil
}

// GetByID 根据 ID 获取
func (s *%sService) GetByID(ctx context.Context, id uint) (*model.%s, error) {
	return s.repo.FindByID(ctx, id)
}

// Create 创建
func (s *%sService) Create(ctx context.Context, m *model.%s) error {
	return s.repo.Create(ctx, m)
}

// Update 更新
func (s *%sService) Update(ctx context.Context, m *model.%s) error {
	return s.repo.Update(ctx, m)
}

// Delete 删除
func (s *%sService) Delete(ctx context.Context, id uint) error {
	return s.repo.Delete(ctx, id)
}
`,
}
