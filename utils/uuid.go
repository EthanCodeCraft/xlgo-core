package utils

import (
	"github.com/google/uuid"
)

// UUID 生成 UUID v4 字符串
func UUID() string {
	return uuid.New().String()
}

// UUIDShort 生成短 UUID（无横线）
func UUIDShort() string {
	return uuid.New().String()[:32]
}

// UUIDParse 解析 UUID 字符串
func UUIDParse(s string) (uuid.UUID, error) {
	return uuid.Parse(s)
}

// UUIDValid 检查 UUID 字符串是否有效
func UUIDValid(s string) bool {
	_, err := uuid.Parse(s)
	return err == nil
}
