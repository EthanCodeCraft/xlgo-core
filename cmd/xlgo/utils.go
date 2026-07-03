package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func writeFile(path, content string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("写入文件失败: %w", err)
	}
	return nil
}

func fileExists(path string) bool {
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
