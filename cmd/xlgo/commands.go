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

	// 解析 --template 与 --module 参数（默认 template=api）
	tmplName := "api"
	module := name
	args := os.Args[3:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--template", "-t":
			if i+1 < len(args) {
				tmplName = args[i+1]
				i++
			}
		case "--module", "-m":
			if i+1 < len(args) {
				module = args[i+1]
				i++
			}
		}
	}

	// 校验模板名
	switch tmplName {
	case "minimal", "api", "fullstack":
		// ok
	default:
		fmt.Printf("未知模板: %s（可选: minimal / api / fullstack）\n", tmplName)
		return
	}

	// minimal 模板目录结构最小化；api/fullstack 含完整分层目录
	var dirs []string
	dirs = append(dirs, name, name+"/public", name+"/logs")
	if tmplName != "minimal" {
		dirs = append(dirs,
			name+"/config",
			name+"/handler",
			name+"/model",
			name+"/repository",
			name+"/service",
			name+"/middleware",
		)
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			fmt.Printf("创建目录失败: %s\n", err)
			return
		}
	}

	caser := cases.Title(language.English)
	data := TemplateData{
		Package:   caser.String(name),
		Name:      caser.String(name),
		NameLower: strings.ToLower(name),
		Module:    module,
		Year:      time.Now().Year(),
	}

	// 按模板选择 main.go 与 config.yaml
	var mainTmpl, configTmpl string
	switch tmplName {
	case "minimal":
		mainTmpl, configTmpl = templates.MainMinimal, templates.ConfigMinimal
	case "fullstack":
		mainTmpl, configTmpl = templates.MainFull, templates.ConfigFull
	default: // api
		mainTmpl, configTmpl = templates.Main, templates.Config
	}

	// 创建文件
	files := map[string]string{
		name + "/main.go":     mainTmpl,
		name + "/config.yaml": configTmpl,
		name + "/go.mod":      fmt.Sprintf(templates.GoMod, module, xlgo.Version),
		name + "/Makefile":    templates.Makefile,
		name + "/.gitignore":  templates.Gitignore,
	}
	// api/fullstack 模板带示例 handler
	if tmplName != "minimal" {
		files[name+"/handler/home.go"] = templates.Handler
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

	fmt.Printf("✓ 项目 %s 创建成功（模板: %s）\n", name, tmplName)
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
