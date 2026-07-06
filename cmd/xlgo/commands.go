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

func createProject(name string) error {
	// P1 #21：校验项目名，拒绝路径穿越与非法 Go 包名。
	if err := validateProjectName(name); err != nil {
		return err
	}
	// #nosec G703 -- name was validated by validateProjectName: no path separators, no "..", no leading dot, Go identifier only.
	if _, err := os.Stat(name); !os.IsNotExist(err) {
		return fmt.Errorf("目录 %s 已存在", name)
	}

	// 解析 --template 与 --module 参数（默认 template=api）
	tmplName := "api"
	module := name
	args := os.Args[3:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--template", "-t":
			if i+1 >= len(args) {
				return fmt.Errorf("%s 缺少参数值", args[i])
			}
			tmplName = args[i+1]
			i++
		case "--module", "-m":
			if i+1 >= len(args) {
				return fmt.Errorf("%s 缺少参数值", args[i])
			}
			module = args[i+1]
			i++
		default:
			// P1 #21：未知参数显式报错，不再静默忽略。
			return fmt.Errorf("未知参数: %s", args[i])
		}
	}

	// P1 #21：校验 module 路径，避免模板元字符 {{ }} 经 Sprintf 进 go.mod 后触发 Parse 报错。
	if err := validateModulePath(module); err != nil {
		return err
	}

	// 校验模板名
	switch tmplName {
	case "minimal", "api", "fullstack":
		// ok
	default:
		return fmt.Errorf("未知模板: %s（可选: minimal / api / fullstack）", tmplName)
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
		// #nosec G301,G703 -- dir is built from validated project name plus fixed scaffold subdirectories; 0755 is intentional for generated project dirs.
		if err := os.MkdirAll(dir, 0755); err != nil {
			// #nosec G703 -- name was validated by validateProjectName and is the scaffold root being rolled back.
			_ = os.RemoveAll(name) // P1 #21：清理半成品
			return fmt.Errorf("创建目录失败: %w", err)
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
		if err := renderTemplateFile(path, content, data); err != nil {
			// #nosec G703 -- name was validated by validateProjectName and is the scaffold root being rolled back.
			_ = os.RemoveAll(name) // P1 #21：部分失败回滚，避免留下半成品项目
			return err
		}
	}

	fmt.Printf("✓ 项目 %s 创建成功（模板: %s）\n", name, tmplName)
	fmt.Println("\n下一步:")
	fmt.Printf("  cd %s\n", name)
	fmt.Println("  go mod tidy")
	fmt.Println("  go run main.go")
	return nil
}

// renderTemplateFile 解析并渲染单个模板文件，每次调用显式关闭句柄
// （P1 #21：不在循环内 defer 累积句柄，且 Close 错误纳入返回）。
func renderTemplateFile(path, content string, data TemplateData) (retErr error) {
	tmpl, err := template.New(path).Parse(content)
	if err != nil {
		return fmt.Errorf("解析模板 %s 失败: %w", path, err)
	}
	// #nosec G304 -- path is from createProject's files map built from validated project name plus fixed filenames.
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("创建文件 %s 失败: %w", path, err)
	}
	defer func() {
		if cerr := file.Close(); cerr != nil && retErr == nil {
			retErr = fmt.Errorf("关闭文件 %s 失败: %w", path, cerr)
		}
	}()
	if err := tmpl.Execute(file, data); err != nil {
		return fmt.Errorf("写入文件 %s 失败: %w", path, err)
	}
	return nil
}

// validateProjectName 校验项目名（P1 #21）：非空、无路径分隔符/.. /前导点，
// 且可派生为合法 Go 包名（须以字母开头，仅含字母/数字/下划线）。
func validateProjectName(name string) error {
	if name == "" {
		return fmt.Errorf("项目名不能为空")
	}
	if strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return fmt.Errorf("项目名不能包含路径分隔符或 ..（防止在预期目录外创建文件）: %q", name)
	}
	if strings.HasPrefix(name, ".") {
		return fmt.Errorf("项目名不能以 . 开头: %q", name)
	}
	if !isValidGoIdentifier(name) {
		return fmt.Errorf("项目名 %q 无法生成合法 Go 包名：须以字母开头，仅含字母、数字、下划线", name)
	}
	return nil
}

// isValidGoIdentifier 判断 s 是否为合法 Go 标识符（ASCII 范围）。
func isValidGoIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		isLetter := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_'
		isNum := r >= '0' && r <= '9'
		if i == 0 {
			if !isLetter {
				return false
			}
		} else if !isLetter && !isNum {
			return false
		}
	}
	return true
}

// validateModulePath 校验 --module（P1 #21）：非空、不含模板元字符与空白。
func validateModulePath(module string) error {
	if module == "" {
		return fmt.Errorf("模块路径不能为空")
	}
	if strings.Contains(module, "{{") || strings.Contains(module, "}}") {
		return fmt.Errorf("模块路径不能包含模板元字符 {{ }}: %q", module)
	}
	if strings.ContainsAny(module, " \t\r\n") {
		return fmt.Errorf("模块路径不能包含空白字符: %q", module)
	}
	return nil
}

func makeFile(fileType, name string) error {
	// 文件名小写，但保留原分隔用于多词；标识符须为合法 Go 标识符（仅字母数字下划线）。
	// 将连字符/空格等转为下划线后再 Title，避免 "my-thing" → "My-Thing" 生成非法标识符（M20）。
	name = strings.ToLower(name)
	// P1 #21：拒绝含路径分隔符的名称，避免生成到目标目录之外。
	if strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return fmt.Errorf("名称不能包含路径分隔符或 ..: %q", name)
	}
	identBase := sanitizeIdent(name)
	caser := cases.Title(language.English)
	nameTitle := caser.String(strings.ReplaceAll(identBase, "_", " "))
	nameTitle = strings.ReplaceAll(nameTitle, " ", "") // 拼回 CamelCase

	switch fileType {
	case "handler":
		return createHandler(name, nameTitle)
	case "repository":
		return createRepository(name, nameTitle)
	case "model":
		return createModel(name, nameTitle)
	case "service":
		return createService(name, nameTitle)
	default:
		return fmt.Errorf("未知类型: %s（可用类型: handler, repository, model, service）", fileType)
	}
}

// sanitizeIdent 把 name 中的非字母数字字符替换为下划线，生成合法 Go 标识符基串（M20）。
// 如 "my-thing" → "my_thing"，后续 Title 后得到 "MyThing"。
func sanitizeIdent(name string) string {
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else if r >= 'A' && r <= 'Z' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	s := strings.Trim(b.String(), "_")
	if s == "" {
		return "xlgo"
	}
	return s
}

func createHandler(name, nameTitle string) error {
	path := fmt.Sprintf("handler/%s.go", name)
	if fileExists(path) {
		return fmt.Errorf("文件 %s 已存在", path)
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

	if err := writeFile(path, content); err != nil {
		return err
	}
	fmt.Printf("✓ 创建处理器: %s\n", path)
	return nil
}

func createRepository(name, nameTitle string) error {
	path := fmt.Sprintf("repository/%s_repository.go", name)
	if fileExists(path) {
		return fmt.Errorf("文件 %s 已存在", path)
	}

	content := fmt.Sprintf(templates.RepositoryMake,
		nameTitle, name, nameTitle, nameTitle,
		nameTitle, name, nameTitle, nameTitle, nameTitle, nameTitle,
		nameTitle, nameTitle,
	)
	content = replaceModuleImports(content)

	if err := writeFile(path, content); err != nil {
		return err
	}
	fmt.Printf("✓ 创建仓库: %s\n", path)
	return nil
}

func createModel(name, nameTitle string) error {
	path := fmt.Sprintf("model/%s.go", name)
	if fileExists(path) {
		return fmt.Errorf("文件 %s 已存在", path)
	}

	content := fmt.Sprintf(templates.ModelMake,
		nameTitle, name, nameTitle, nameTitle, name,
	)

	if err := writeFile(path, content); err != nil {
		return err
	}
	fmt.Printf("✓ 创建模型: %s\n", path)
	return nil
}

func createService(name, nameTitle string) error {
	path := fmt.Sprintf("service/%s_service.go", name)
	if fileExists(path) {
		return fmt.Errorf("文件 %s 已存在", path)
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

	if err := writeFile(path, content); err != nil {
		return err
	}
	fmt.Printf("✓ 创建服务: %s\n", path)
	return nil
}
