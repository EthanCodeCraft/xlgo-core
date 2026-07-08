// Package main 是 xlgo 的完整示例：MySQL + Redis + JWT + 一个 user CRUD。
//
// 运行前需准备：
//   - MySQL（config.yaml 中 database 配置）
//   - Redis（config.yaml 中 redis 配置）
//
// 启动后会自动迁移 user 表。访问：
//
//	POST /api/v1/login           {"username":"alice","password":"secret"}  → 返回 token
//	GET  /api/v1/users/:id       （需 Authorization: Bearer <token>）
//	POST /api/v1/users           （创建用户）
//
// 示例会在启动时初始化 alice/secret，密码以 bcrypt 哈希保存。
//
// 运行：
//
//	go run ./examples/full
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	xlgo "github.com/EthanCodeCraft/xlgo-core"
	"github.com/EthanCodeCraft/xlgo-core/database"
	"github.com/EthanCodeCraft/xlgo-core/jwt"
	"github.com/EthanCodeCraft/xlgo-core/middleware"
	"github.com/EthanCodeCraft/xlgo-core/repository"
	"github.com/EthanCodeCraft/xlgo-core/response"
	"github.com/EthanCodeCraft/xlgo-core/router"
	"github.com/EthanCodeCraft/xlgo-core/validation"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// User 示例模型
type User struct {
	gorm.Model
	Username string `gorm:"uniqueIndex;size:64" json:"username"`
	Password string `gorm:"size:128" json:"-"` // bcrypt 哈希，永不返回给客户端
	UserType string `gorm:"size:32" json:"user_type"`
}

var userRepo *repository.BaseRepo[User]

func main() {
	app := xlgo.NewFullStack(
		xlgo.WithConfigPath("./examples/full/config.yaml"),
		xlgo.WithModels(&User{}),
		xlgo.WithMiddlewares(middleware.Logger(), middleware.CORS()),
		xlgo.WithModules(router.ModuleFunc(registerRoutes)),
		xlgo.WithHook(xlgo.Hook{
			Name: "seed-example-user",
			OnInit: func(*xlgo.App) error {
				return ensureExampleUser(context.Background())
			},
		}),
	)

	// 初始化 user repository（App.Init 之后 master DB 才可用，这里在 registerRoutes 里延迟拿）
	_ = app

	if err := app.Run(); err != nil {
		fmt.Printf("启动失败: %v\n", err)
		os.Exit(1)
	}
}

func registerRoutes(r *gin.RouterGroup) {
	// 延迟初始化 repo：此时 App.Init 已完成，database.GetDB() 可用
	userRepo = repository.NewBaseRepo[User](database.GetDB())

	api := r.Group("/api/v1")

	// 公开路由：登录
	api.POST("/login", login)

	// 认证路由
	auth := api.Group("/", middleware.AuthRequired())
	auth.GET("/users/:id", getUser)
	auth.POST("/users", createUser)
}

func login(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, "参数错误")
		return
	}

	u, err := userRepo.FindOne(c.Request.Context(), "username = ?", req.Username)
	if err != nil {
		response.Fail(c, "用户名或密码错误")
		return
	}
	if !validation.CheckPassword(u.Password, req.Password) {
		response.Fail(c, "用户名或密码错误")
		return
	}

	token, err := jwt.GenerateToken(u.ID, u.Username, "user", u.UserType)
	if err != nil {
		response.ServerError(c, "生成 token 失败")
		return
	}
	response.Success(c, gin.H{"token": token})
}

func getUser(c *gin.Context) {
	id := c.Param("id")
	// 示例简化：直接用 repo 查询，实际应转 uint
	var uid uint
	if _, err := fmt.Sscanf(id, "%d", &uid); err != nil {
		response.Fail(c, "用户ID无效")
		return
	}
	u, err := userRepo.FindByID(c.Request.Context(), uid)
	if err != nil {
		response.NotFound(c, "用户不存在")
		return
	}
	response.Success(c, u)
}

func createUser(c *gin.Context) {
	var req struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
		UserType string `json:"user_type"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, "参数错误")
		return
	}

	hash, err := validation.HashPassword(req.Password)
	if err != nil {
		response.ServerError(c, "密码加密失败")
		return
	}
	u := User{
		Username: req.Username,
		Password: hash,
		UserType: req.UserType,
	}
	if u.UserType == "" {
		u.UserType = "user"
	}
	if err := userRepo.Create(c.Request.Context(), &u); err != nil {
		response.Fail(c, "创建失败: "+err.Error())
		return
	}
	response.Success(c, u)
}

func ensureExampleUser(ctx context.Context) error {
	_, err := userRepo.FindOne(ctx, "username = ?", "alice")
	if err == nil {
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	hash, err := validation.HashPassword("secret")
	if err != nil {
		return err
	}
	return userRepo.Create(ctx, &User{
		Username: "alice",
		Password: hash,
		UserType: "user",
	})
}
