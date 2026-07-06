package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func writeFile(path, content string) error {
	dir := filepath.Dir(path)
	// #nosec G301,G703 -- path is built by makeFile from fixed scaffold directories and a name that rejects separators/".."; 0755 is intentional for generated dirs.
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}

	// #nosec G306,G703 -- path is built by makeFile from fixed scaffold directories and a validated name; 0644 is intentional for generated source files.
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("写入文件失败: %w", err)
	}
	return nil
}

func fileExists(path string) bool {
	// #nosec G703 -- path is built by makeFile from fixed scaffold directories and a name that rejects separators/"..".
	_, err := os.Stat(path)
	if err == nil {
		return true
	}
	// 仅当"不存在"才返回 false；权限错误等其它错误视为"可能存在/不可覆写"，
	// 避免把无权限访问的路径误判为可创建（M20：原 !os.IsNotExist 把权限错误当存在，
	// 反而安全；但语义模糊，显式区分更清晰）。
	return !os.IsNotExist(err)
}

func currentModule() string {
	data, err := os.ReadFile("go.mod")
	if err != nil {
		return "xlgo"
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module "))
		}
	}
	return "xlgo"
}

func replaceModuleImports(content string) string {
	module := currentModule()
	content = strings.ReplaceAll(content, "\"xlgo/model\"", fmt.Sprintf("\"%s/model\"", module))
	content = strings.ReplaceAll(content, "\"xlgo/repository\"", fmt.Sprintf("\"%s/repository\"", module))
	content = strings.ReplaceAll(content, "\"xlgo/database\"", fmt.Sprintf("\"%s/database\"", module))
	content = strings.ReplaceAll(content, "\"xlgo/response\"", "\"github.com/EthanCodeCraft/xlgo-core/response\"")
	return content
}
