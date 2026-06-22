package main

import (
	"fmt"
	"os"

	xlgo "github.com/EthanCodeCraft/xlgo-core"
)

func printUsage() {
	fmt.Println(`xlgo - Go Web 框架脚手架工具

用法:
  xlgo new <项目名>          创建新项目
  xlgo make handler <名称>   创建处理器
  xlgo make repository <名称> 创建仓库
  xlgo make model <名称>      创建模型
  xlgo make service <名称>    创建服务
  xlgo version               显示版本号

示例:
  xlgo new myapp
  xlgo make handler user
  xlgo make repository user`)
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		return
	}

	command := os.Args[1]

	switch command {
	case "new":
		if len(os.Args) < 3 {
			fmt.Println("用法: xlgo new <项目名>")
			return
		}
		createProject(os.Args[2])

	case "make":
		if len(os.Args) < 4 {
			fmt.Println("用法: xlgo make <类型> <名称>")
			fmt.Println("类型: handler, repository, model, service")
			return
		}
		makeFile(os.Args[2], os.Args[3])

	case "version":
		fmt.Printf("xlgo v%s\n", xlgo.Version)

	default:
		printUsage()
	}
}
