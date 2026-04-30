package utils

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// FileExists 检查文件是否存在
// 评分: ⭐⭐⭐⭐⭐
// 理由: 常用检查，语义清晰
func FileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// DirExists 检查目录是否存在
// 评分: ⭐⭐⭐⭐⭐
// 理由: 区分文件和目录检查，更精确
func DirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// EnsureDir 确保目录存在，不存在则创建
// 评分: ⭐⭐⭐⭐⭐
// 理由: 日志、上传等场景常用，避免每次判断
func EnsureDir(path string) error {
	return os.MkdirAll(path, 0755)
}

// ReadFile 读取文件内容
// 评分: ⭐⭐⭐⭐
// 理由: 简化 os.ReadFile，增加存在性检查
func ReadFile(path string) ([]byte, error) {
	if !FileExists(path) {
		return nil, fmt.Errorf("file not found: %s", path)
	}
	return os.ReadFile(path)
}

// WriteFile 写入文件内容（覆盖）
// 评分: ⭐⭐⭐⭐
// 理由: 简化文件写入，自动创建目录
func WriteFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := EnsureDir(dir); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// AppendFile 追加内容到文件
// 评分: ⭐⭐⭐⭐
// 理由: 日志追加场景常用
func AppendFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := EnsureDir(dir); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
}

// CopyFile 复制文件
// 评分: ⭐⭐⭐⭐⭐
// 理由: 文件操作常用，使用 io.Copy 高效复制
func CopyFile(dst, src string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	// 确保目标目录存在
	dir := filepath.Dir(dst)
	if err := EnsureDir(dir); err != nil {
		return err
	}

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

// FileSize 获取文件大小
// 评分: ⭐⭐⭐⭐
// 理由: 上传文件大小检查常用
func FileSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// RemoveFile 删除文件（忽略不存在的错误）
// 评分: ⭐⭐⭐⭐
// 理由: 清理临时文件时常用，避免额外判断
func RemoveFile(path string) error {
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
