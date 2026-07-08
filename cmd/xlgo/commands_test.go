package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateMakeNameRejectsUnsafeOrInvalidNames(t *testing.T) {
	tests := []string{
		"",
		"../user",
		`..\user`,
		".user",
		"my-thing",
		"123user",
	}

	for _, tt := range tests {
		t.Run(tt, func(t *testing.T) {
			if err := validateMakeName(tt); err == nil {
				t.Fatalf("名称 %q 应返回错误", tt)
			}
		})
	}
}

func TestMakeFileAcceptsSnakeCaseIdentifier(t *testing.T) {
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("获取当前目录失败: %v", err)
	}
	tmp := t.TempDir()
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("切换测试目录失败: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldWd); err != nil {
			t.Fatalf("恢复测试目录失败: %v", err)
		}
	})

	if err := makeFile("model", "user_profile"); err != nil {
		t.Fatalf("生成模型失败: %v", err)
	}

	path := filepath.Join(tmp, "model", "user_profile.go")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取生成文件失败: %v", err)
	}
	if !strings.Contains(string(content), "type UserProfile struct") {
		t.Fatalf("生成类型名不符合预期:\n%s", content)
	}
}
