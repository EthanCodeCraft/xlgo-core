package main

// Templates 存放所有代码生成模板
var templates = struct {
	Main        string // api 模板：标准业务 API（mysql+redis+jwt+分层）
	MainMinimal string // minimal 模板：轻量 HTTP，无外部依赖
	MainFull    string // fullstack 模板：全组件
	Config      string // api 模板配置
	ConfigMinimal string
	ConfigFull    string
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
	"flag"
	"fmt"
	"os"

	xlgo "github.com/EthanCodeCraft/xlgo-core"
	"github.com/EthanCodeCraft/xlgo-core/middleware"
	"github.com/EthanCodeCraft/xlgo-core/response"
	"github.com/EthanCodeCraft/xlgo-core/router"
	"github.com/gin-gonic/gin"
)

var configPath string

func init() {
	flag.StringVar(&configPath, "config", "./config.yaml", "配置文件路径")
}

func main() {
	flag.Parse()

	app := xlgo.New(
		xlgo.WithConfigPath(configPath),
		xlgo.WithLogger(),
		xlgo.WithHealthRoutes(),
		// 如需 Swagger 文档：xlgo.WithSwaggerRoutes() 或 xlgo.WithDefaultRoutes()
		// 如需 MySQL/Redis/Storage：xlgo.WithMySQL() / xlgo.WithRedis() / xlgo.WithStorage()
		// 一键启用全部默认组件：使用 xlgo.NewFullStack(...) 替代 xlgo.New(...)
		xlgo.WithMiddlewares(middleware.Logger(), middleware.CORS()),
		xlgo.WithModules(router.ModuleFunc(registerRoutes)),
	)

	if err := app.Run(); err != nil {
		fmt.Printf("启动失败: %v\n", err)
		os.Exit(1)
	}
}

func registerRoutes(r *gin.RouterGroup) {
	api := r.Group("/api/v1")
	api.GET("/", func(c *gin.Context) {
		response.Success(c, gin.H{"message": "Welcome to {{.Name}}!"})
	})
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
  driver: mysql          # mysql（默认）或 postgres
  host: localhost
  port: 3306
  user: root
  password: your_password
  name: {{.NameLower}}
  max_idle_conns: 10
  max_open_conns: 100
  # dsn: "自定义连接字符串，设置后优先于上面的字段"

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

	// MainMinimal minimal 模板：轻量 HTTP 服务，不依赖 MySQL/Redis/Storage
	MainMinimal: `package main

import (
	"flag"
	"fmt"
	"os"

	xlgo "github.com/EthanCodeCraft/xlgo-core"
	"github.com/EthanCodeCraft/xlgo-core/middleware"
	"github.com/EthanCodeCraft/xlgo-core/response"
	"github.com/EthanCodeCraft/xlgo-core/router"
	"github.com/gin-gonic/gin"
)

var configPath string

func init() {
	flag.StringVar(&configPath, "config", "./config.yaml", "配置文件路径")
}

func main() {
	flag.Parse()

	app := xlgo.New(
		xlgo.WithConfigPath(configPath),
		xlgo.WithLogger(),
		xlgo.WithHealthRoutes(),
		xlgo.WithMiddlewares(middleware.Logger(), middleware.CORS()),
		xlgo.WithModules(router.ModuleFunc(registerRoutes)),
	)

	if err := app.Run(); err != nil {
		fmt.Printf("启动失败: %v\n", err)
		os.Exit(1)
	}
}

func registerRoutes(r *gin.RouterGroup) {
	api := r.Group("/api/v1")
	api.GET("/", func(c *gin.Context) {
		response.Success(c, gin.H{"message": "Hello {{.Name}}!"})
	})
}
`,

	// ConfigMinimal minimal 模板配置：仅 app + server + log，无数据库/Redis
	ConfigMinimal: `app:
  name: "{{.Name}}"
  site_name: "{{.NameLower}}"
  version: "1.0.0"
  env: "dev"
  debug: true
  base_url: "http://localhost:8080"

server:
  port: 8080
  mode: development

log:
  dir: ./logs
  max_size: 100
  max_backups: 30
  max_age: 30
  compress: true
`,

	// MainFull fullstack 模板：全组件（FullStack），含 Swagger + Storage
	MainFull: `package main

import (
	"flag"
	"fmt"
	"os"

	xlgo "github.com/EthanCodeCraft/xlgo-core"
	"github.com/EthanCodeCraft/xlgo-core/middleware"
	"github.com/EthanCodeCraft/xlgo-core/response"
	"github.com/EthanCodeCraft/xlgo-core/router"
	"github.com/gin-gonic/gin"
)

var configPath string

func init() {
	flag.StringVar(&configPath, "config", "./config.yaml", "配置文件路径")
}

func main() {
	flag.Parse()

	// NewFullStack 一键启用全部组件：Logger/MySQL/Redis/Storage/Wire/Health/Swagger/AutoMigrate
	// 如需排除个别组件，追加对应 Without* Option，例如 xlgo.WithoutSwaggerRoutes()
	app := xlgo.NewFullStack(
		xlgo.WithConfigPath(configPath),
		xlgo.WithMiddlewares(middleware.Logger(), middleware.CORS()),
		xlgo.WithModules(router.ModuleFunc(registerRoutes)),
		// xlgo.WithModels(&User{}, &Order{}),  // 注册模型以启用自动迁移
	)

	if err := app.Run(); err != nil {
		fmt.Printf("启动失败: %v\n", err)
		os.Exit(1)
	}
}

func registerRoutes(r *gin.RouterGroup) {
	api := r.Group("/api/v1")
	api.GET("/", func(c *gin.Context) {
		response.Success(c, gin.H{"message": "Welcome to {{.Name}} (fullstack)!"})
	})
}
`,

	// ConfigFull fullstack 模板配置：全组件配置
	ConfigFull: `app:
  name: "{{.Name}}"
  site_name: "{{.NameLower}}"
  version: "1.0.0"
  env: "dev"
  debug: true
  base_url: "http://localhost:8080"
  token_expire: 86400

server:
  port: 8080
  mode: development

database:
  driver: mysql          # mysql（默认）或 postgres
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
	// %s: module 名称；%s: xlgo 框架版本（来自 xlgo.Version，避免字面量散落）
	GoMod: `module %s

go 1.25

require (
	github.com/EthanCodeCraft/xlgo-core v%s
	github.com/gin-gonic/gin v1.9.1
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
	"github.com/EthanCodeCraft/xlgo-core/response"
	"github.com/gin-gonic/gin"
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
	"github.com/EthanCodeCraft/xlgo-core/response"
	"github.com/gin-gonic/gin"
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

	"github.com/EthanCodeCraft/xlgo-core/database"
	xlrepo "github.com/EthanCodeCraft/xlgo-core/repository"
	"xlgo/model"
)

// %sRepository %s 仓库
type %sRepository struct {
	*xlrepo.BaseRepo[model.%s]
}

// New%sRepository 创建 %s 仓库
func New%sRepository() *%sRepository {
	return &%sRepository{
		BaseRepo: xlrepo.NewBaseRepo[model.%s](database.GetDB()),
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

import xlmodel "github.com/EthanCodeCraft/xlgo-core/model"

// %s %s 模型
type %s struct {
	xlmodel.BaseModel
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
