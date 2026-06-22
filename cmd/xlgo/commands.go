package main

import (
	"fmt"
	"os"
	"strings"
	"text/template"
	"time"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	xlgo "github.com/EthanCodeCraft/xlgo-core"
)

func createProject(name string) {
	if _, err := os.Stat(name); !os.IsNotExist(err) {
		fmt.Printf("目录 %s 已存在\n", name)
		return
	}

	// 创建目录结构
	dirs := []string{
		name,
		name + "/config",
		name + "/handler",
		name + "/model",
		name + "/repository",
		name + "/service",
		name + "/middleware",
		name + "/public",
		name + "/logs",
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			fmt.Printf("创建目录失败: %s\n", err)
			return
		}
	}

	// 获取模块路径
	module := name
	if len(os.Args) > 3 && os.Args[3] == "--module" && len(os.Args) > 4 {
		module = os.Args[4]
	}

	caser := cases.Title(language.English)
	data := TemplateData{
		Package:   caser.String(name),
		Name:      caser.String(name),
		NameLower: strings.ToLower(name),
		Module:    module,
		Year:      time.Now().Year(),
	}

	// 创建文件
	files := map[string]string{
		name + "/main.go":         templates.Main,
		name + "/config.yaml":     templates.Config,
		name + "/go.mod":          fmt.Sprintf(templates.GoMod, module, xlgo.Version),
		name + "/Makefile":        templates.Makefile,
		name + "/.gitignore":      templates.Gitignore,
		name + "/handler/home.go": templates.Handler,
	}

	for path, content := range files {
		tmpl, err := template.New(path).Parse(content)
		if err != nil {
			fmt.Printf("解析模板失败: %s\n", err)
			return
		}

		file, err := os.Create(path)
		if err != nil {
			fmt.Printf("创建文件失败: %s\n", err)
			return
		}
		defer file.Close()

		if err := tmpl.Execute(file, data); err != nil {
			fmt.Printf("写入文件失败: %s\n", err)
			return
		}
	}

	fmt.Printf("✓ 项目 %s 创建成功\n", name)
	fmt.Println("\n下一步:")
	fmt.Printf("  cd %s\n", name)
	fmt.Println("  go mod tidy")
	fmt.Println("  go run main.go")
}

func makeFile(fileType, name string) {
	name = strings.ToLower(name)
	caser := cases.Title(language.English)
	nameTitle := caser.String(name)

	switch fileType {
	case "handler":
		createHandler(name, nameTitle)
	case "repository":
		createRepository(name, nameTitle)
	case "model":
		createModel(name, nameTitle)
	case "service":
		createService(name, nameTitle)
	default:
		fmt.Printf("未知类型: %s\n", fileType)
		fmt.Println("可用类型: handler, repository, model, service")
	}
}

func createHandler(name, nameTitle string) {
	path := fmt.Sprintf("handler/%s.go", name)
	if fileExists(path) {
		fmt.Printf("文件 %s 已存在\n", path)
		return
	}

	content := fmt.Sprintf(templates.HandlerMake,
		nameTitle, name, nameTitle,
		nameTitle, name, nameTitle, nameTitle, nameTitle,
		name, nameTitle, name, nameTitle,
		nameTitle, name, name, nameTitle,
		nameTitle, name, name, nameTitle,
		nameTitle, name, name, nameTitle,
		nameTitle, name, name, nameTitle,
	)
	content = replaceModuleImports(content)

	writeFile(path, content)
	fmt.Printf("✓ 创建处理器: %s\n", path)
}

func createRepository(name, nameTitle string) {
	path := fmt.Sprintf("repository/%s_repository.go", name)
	if fileExists(path) {
		fmt.Printf("文件 %s 已存在\n", path)
		return
	}

	content := fmt.Sprintf(templates.RepositoryMake,
		nameTitle, name, nameTitle, nameTitle,
		nameTitle, name, nameTitle, nameTitle, nameTitle, nameTitle,
		nameTitle, nameTitle, nameTitle,
	)
	content = replaceModuleImports(content)

	writeFile(path, content)
	fmt.Printf("✓ 创建仓库: %s\n", path)
}

func createModel(name, nameTitle string) {
	path := fmt.Sprintf("model/%s.go", name)
	if fileExists(path) {
		fmt.Printf("文件 %s 已存在\n", path)
		return
	}

	content := fmt.Sprintf(templates.ModelMake,
		nameTitle, name, nameTitle, nameTitle, name,
	)

	writeFile(path, content)
	fmt.Printf("✓ 创建模型: %s\n", path)
}

func createService(name, nameTitle string) {
	path := fmt.Sprintf("service/%s_service.go", name)
	if fileExists(path) {
		fmt.Printf("文件 %s 已存在\n", path)
		return
	}

	content := fmt.Sprintf(templates.ServiceMake,
		nameTitle, name, nameTitle, nameTitle,
		nameTitle, name, nameTitle, nameTitle, nameTitle, nameTitle, nameTitle,
		nameTitle, nameTitle,
		nameTitle, nameTitle,
		nameTitle, nameTitle,
		nameTitle, nameTitle,
	)
	content = replaceModuleImports(content)

	writeFile(path, content)
	fmt.Printf("✓ 创建服务: %s\n", path)
}
