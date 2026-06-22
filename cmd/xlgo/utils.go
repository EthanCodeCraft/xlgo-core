package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func writeFile(path, content string) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		fmt.Printf("创建目录失败: %s\n", err)
		return
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		fmt.Printf("写入文件失败: %s\n", err)
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
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
