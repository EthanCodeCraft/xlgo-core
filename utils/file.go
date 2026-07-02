package utils

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// 本文件提供通用本地文件操作工具（读/写/复制/追加/存在性/删除）。
//
// ⚠️ 安全说明（M3）：这些函数直接操作调用方传入的路径，不做路径穿越校验。
// 若路径可能来自不可信输入（用户上传文件名、URL 参数等），调用方必须自行净化
// （如 filepath.Clean + 前缀锚定），否则存在任意文件读/写/删风险。
// 框架自身的不可信文件处理（storage 上传/下载）已在 storage 包内做穿越防护（C4）。

// FileExists 检查文件是否存在
func FileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// DirExists 检查目录是否存在
func DirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// EnsureDir 确保目录存在，不存在则创建
func EnsureDir(path string) error {
	return os.MkdirAll(path, 0755)
}

// ReadFile 读取文件内容
func ReadFile(path string) ([]byte, error) {
	if !FileExists(path) {
		return nil, fmt.Errorf("file not found: %s", path)
	}
	return os.ReadFile(path)
}

// WriteFile 写入文件内容（覆盖）
func WriteFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := EnsureDir(dir); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// AppendFile 追加内容到文件
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
func FileSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// RemoveFile 删除文件（忽略不存在的错误）
func RemoveFile(path string) error {
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
