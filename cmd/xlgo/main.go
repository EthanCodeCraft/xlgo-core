package main

import (
	"fmt"
	"os"

	xlgo "github.com/EthanCodeCraft/xlgo-core"
)

func printUsage() {
	fmt.Println(`xlgo - Go Web 框架脚手架工具

用法:
  xlgo new <项目名> [--template <模板>] [--module <模块路径>]   创建新项目
  xlgo make handler <名称>    创建处理器
  xlgo make repository <名称> 创建仓库
  xlgo make model <名称>      创建模型
  xlgo make service <名称>    创建服务
  xlgo version                显示版本号

模板 (xlgo new --template <名称>):
  minimal    轻量 HTTP 服务，不依赖 MySQL/Redis（默认入门）
  api        标准业务 API，含 MySQL/Redis/JWT 与 handler/model/repository/service 分层（默认）
  fullstack  全组件，一键启用 MySQL/Redis/Storage/Swagger/AutoMigrate

示例:
  xlgo new myapp
  xlgo new myapp --template minimal
  xlgo new myapp --template fullstack --module github.com/me/myapp
  xlgo make handler user
  xlgo make repository user`)
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		return
	}

	command := os.Args[1]

	// P1 #21：命令执行失败时以非零码退出，便于 `xlgo new x && cd x` 等脚本/CI 正确中断。
	switch command {
	case "new":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "用法: xlgo new <项目名>")
			os.Exit(2)
		}
		if err := createProject(os.Args[2]); err != nil {
			fmt.Fprintf(os.Stderr, "错误: %s\n", err)
			os.Exit(1)
		}

	case "make":
		if len(os.Args) < 4 {
			fmt.Fprintln(os.Stderr, "用法: xlgo make <类型> <名称>")
			fmt.Fprintln(os.Stderr, "类型: handler, repository, model, service")
			os.Exit(2)
		}
		if err := makeFile(os.Args[2], os.Args[3]); err != nil {
			fmt.Fprintf(os.Stderr, "错误: %s\n", err)
			os.Exit(1)
		}

	case "version":
		fmt.Printf("xlgo v%s\n", xlgo.Version)

	default:
		printUsage()
	}
}
