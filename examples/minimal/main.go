// Package main 是 xlgo 的最小可运行示例。
//
// 仅依赖一个 config.yaml，不初始化 MySQL / Redis / Storage，
// 适合第一次接触 xlgo、或纯 HTTP 场景。
//
// 运行：
//
//	go run ./examples/minimal
package main

import (
	"fmt"
	"os"

	xlgo "github.com/EthanCodeCraft/xlgo-core"
	"github.com/EthanCodeCraft/xlgo-core/middleware"
	"github.com/EthanCodeCraft/xlgo-core/response"
	"github.com/EthanCodeCraft/xlgo-core/router"
	"github.com/gin-gonic/gin"
)

func main() {
	app := xlgo.New(
		xlgo.WithConfigPath("./examples/minimal/config.yaml"),
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
		response.Success(c, gin.H{"message": "Hello xlgo!"})
	})
}
