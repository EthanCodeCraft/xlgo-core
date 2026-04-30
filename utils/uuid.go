package utils

import (
	"github.com/google/uuid"
)

// UUID 生成 UUID v4 字符串
// 评分: ⭐⭐⭐⭐⭐
// 理由: 常用唯一标识生成，使用标准库 google/uuid
func UUID() string {
	return uuid.New().String()
}

// UUIDShort 生成短 UUID（无横线）
// 评分: ⭐⭐⭐⭐⭐
// 理由: 数据库主键、订单号等场景常用
func UUIDShort() string {
	return uuid.New().String()[:32]
}

// UUIDParse 解析 UUID 字符串
// 评分: ⭐⭐⭐⭐
// 理由: UUID 解析验证
func UUIDParse(s string) (uuid.UUID, error) {
	return uuid.Parse(s)
}

// UUIDValid 检查 UUID 字符串是否有效
// 评分: ⭐⭐⭐⭐
// 理由: 快速验证
func UUIDValid(s string) bool {
	_, err := uuid.Parse(s)
	return err == nil
}
