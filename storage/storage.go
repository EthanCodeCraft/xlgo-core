package storage

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/EthanCodeCraft/xlgo-core/config"
	"github.com/EthanCodeCraft/xlgo-core/logger"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
	"go.uber.org/zap"
)

// Storage 存储接口
type Storage interface {
	Upload(file *multipart.FileHeader, subdir string) (string, error)
	UploadFromBytes(data []byte, filename, subdir string) (string, error)
	GetURL(path string) string
	Delete(path string) error
	Get(path string) ([]byte, error)
	Exists(path string) bool
}

// LocalStorage 本地存储
type LocalStorage struct {
	path    string
	baseURL string
}

// NewLocalStorage 创建本地存储实例
func NewLocalStorage(cfg *config.LocalStorageConfig) *LocalStorage {
	return &LocalStorage{
		path:    cfg.Path,
		baseURL: cfg.BaseURL,
	}
}

// Upload 上传文件
func (s *LocalStorage) Upload(file *multipart.FileHeader, subdir string) (string, error) {
	// 生成存储路径: /年/月/日/文件名
	now := time.Now()
	datePath := fmt.Sprintf("%d/%02d/%02d", now.Year(), now.Month(), now.Day())
	relativePath := filepath.Join(subdir, datePath)

	// 确保目录存在
	fullPath := filepath.Join(s.path, relativePath)
	if err := os.MkdirAll(fullPath, 0755); err != nil {
		logger.Error("创建目录失败", zap.Error(err), zap.String("path", fullPath))
		return "", fmt.Errorf("创建目录失败: %w", err)
	}

	// 生成唯一文件名
	ext := filepath.Ext(file.Filename)
	filename := fmt.Sprintf("%d%s", now.UnixNano(), ext)
	dst := filepath.Join(fullPath, filename)

	// 打开源文件
	src, err := file.Open()
	if err != nil {
		return "", fmt.Errorf("打开文件失败: %w", err)
	}
	defer src.Close()

	// 创建目标文件
	dstFile, err := os.Create(dst)
	if err != nil {
		return "", fmt.Errorf("创建文件失败: %w", err)
	}
	defer dstFile.Close()

	// 复制文件内容
	if _, err := io.Copy(dstFile, src); err != nil {
		return "", fmt.Errorf("保存文件失败: %w", err)
	}

	// 返回相对路径
	relativeFilePath := filepath.Join(relativePath, filename)
	// 统一使用正斜杠
	relativeFilePath = strings.ReplaceAll(relativeFilePath, "\\", "/")

	logger.Info("文件上传成功", zap.String("path", relativeFilePath))
	return relativeFilePath, nil
}

// UploadFromBytes 从字节数组上传文件
func (s *LocalStorage) UploadFromBytes(data []byte, filename, subdir string) (string, error) {
	// 生成存储路径: /年/月/日/文件名
	now := time.Now()
	datePath := fmt.Sprintf("%d/%02d/%02d", now.Year(), now.Month(), now.Day())
	relativePath := filepath.Join(subdir, datePath)

	// 确保目录存在
	fullPath := filepath.Join(s.path, relativePath)
	if err := os.MkdirAll(fullPath, 0755); err != nil {
		logger.Error("创建目录失败", zap.Error(err), zap.String("path", fullPath))
		return "", fmt.Errorf("创建目录失败: %w", err)
	}

	// 生成唯一文件名（如果未提供扩展名，添加时间戳）
	ext := filepath.Ext(filename)
	if ext == "" {
		ext = ".bin"
	}
	uniqueFilename := fmt.Sprintf("%d%s", now.UnixNano(), ext)
	dst := filepath.Join(fullPath, uniqueFilename)

	// 创建目标文件
	dstFile, err := os.Create(dst)
	if err != nil {
		return "", fmt.Errorf("创建文件失败: %w", err)
	}
	defer dstFile.Close()

	// 写入文件内容
	if _, err := io.Copy(dstFile, bytes.NewReader(data)); err != nil {
		return "", fmt.Errorf("保存文件失败: %w", err)
	}

	// 返回相对路径
	relativeFilePath := filepath.Join(relativePath, uniqueFilename)
	// 统一使用正斜杠
	relativeFilePath = strings.ReplaceAll(relativeFilePath, "\\", "/")

	logger.Info("文件上传成功", zap.String("path", relativeFilePath))
	return relativeFilePath, nil
}

// GetURL 获取文件访问 URL
func (s *LocalStorage) GetURL(path string) string {
	return fmt.Sprintf("%s/%s", s.baseURL, path)
}

// Delete 删除文件
func (s *LocalStorage) Delete(path string) error {
	fullPath := filepath.Join(s.path, path)
	if err := os.Remove(fullPath); err != nil {
		logger.Error("删除文件失败", zap.Error(err), zap.String("path", fullPath))
		return fmt.Errorf("删除文件失败: %w", err)
	}
	logger.Info("文件删除成功", zap.String("path", path))
	return nil
}

// Get 获取文件内容
func (s *LocalStorage) Get(path string) ([]byte, error) {
	fullPath := filepath.Join(s.path, path)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		logger.Error("读取文件失败", zap.Error(err), zap.String("path", fullPath))
		return nil, fmt.Errorf("读取文件失败: %w", err)
	}
	return data, nil
}

// Exists 检查文件是否存在
func (s *LocalStorage) Exists(path string) bool {
	fullPath := filepath.Join(s.path, path)
	_, err := os.Stat(fullPath)
	return err == nil
}

// OSSStorage OSS 存储
type OSSStorage struct {
	client     *oss.Client
	bucket     *oss.Bucket
	endpoint   string
	bucketName string
	baseURL    string
}

// NewOSSStorage 创建 OSS 存储实例
func NewOSSStorage(cfg *config.OSSStorageConfig) (*OSSStorage, error) {
	client, err := oss.New(cfg.Endpoint, cfg.AccessKeyID, cfg.AccessKeySecret)
	if err != nil {
		return nil, fmt.Errorf("创建 OSS 客户端失败: %w", err)
	}

	bucket, err := client.Bucket(cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("获取 OSS Bucket 失败: %w", err)
	}

	return &OSSStorage{
		client:     client,
		bucket:     bucket,
		endpoint:   cfg.Endpoint,
		bucketName: cfg.Bucket,
		baseURL:    cfg.BaseURL,
	}, nil
}

// Upload 上传文件到 OSS
func (s *OSSStorage) Upload(file *multipart.FileHeader, subdir string) (string, error) {
	// 生成存储路径: /年/月/日/文件名
	now := time.Now()
	datePath := fmt.Sprintf("%d/%02d/%02d", now.Year(), now.Month(), now.Day())
	ext := filepath.Ext(file.Filename)
	objectKey := fmt.Sprintf("%s/%d%s", filepath.Join(subdir, datePath), now.UnixNano(), ext)

	// 打开源文件
	src, err := file.Open()
	if err != nil {
		return "", fmt.Errorf("打开文件失败: %w", err)
	}
	defer src.Close()

	// 上传到 OSS
	if err := s.bucket.PutObject(objectKey, src); err != nil {
		logger.Error("OSS 上传失败", zap.Error(err), zap.String("key", objectKey))
		return "", fmt.Errorf("OSS 上传失败: %w", err)
	}

	logger.Info("OSS 文件上传成功", zap.String("key", objectKey))
	return objectKey, nil
}

// UploadFromBytes 从字节数组上传文件到 OSS
func (s *OSSStorage) UploadFromBytes(data []byte, filename, subdir string) (string, error) {
	now := time.Now()
	datePath := fmt.Sprintf("%d/%02d/%02d", now.Year(), now.Month(), now.Day())
	ext := filepath.Ext(filename)
	if ext == "" {
		ext = ".bin"
	}
	objectKey := fmt.Sprintf("%s/%d%s", filepath.Join(subdir, datePath), now.UnixNano(), ext)

	// 上传到 OSS
	if err := s.bucket.PutObject(objectKey, bytes.NewReader(data)); err != nil {
		logger.Error("OSS 上传失败", zap.Error(err), zap.String("key", objectKey))
		return "", fmt.Errorf("OSS 上传失败: %w", err)
	}

	logger.Info("OSS 文件上传成功", zap.String("key", objectKey))
	return objectKey, nil
}

// GetURL 获取文件访问 URL
func (s *OSSStorage) GetURL(path string) string {
	if s.baseURL != "" {
		return fmt.Sprintf("%s/%s", s.baseURL, path)
	}
	return fmt.Sprintf("https://%s.%s/%s", s.bucketName, s.endpoint, path)
}

// GetSignedURL 获取带签名的临时访问 URL（用于私有文件）
func (s *OSSStorage) GetSignedURL(path string, expire time.Duration) (string, error) {
	return s.bucket.SignURL(path, oss.HTTPGet, int64(expire.Seconds()))
}

// Delete 删除 OSS 文件
func (s *OSSStorage) Delete(path string) error {
	if err := s.bucket.DeleteObject(path); err != nil {
		logger.Error("OSS 删除失败", zap.Error(err), zap.String("key", path))
		return fmt.Errorf("OSS 删除失败: %w", err)
	}
	logger.Info("OSS 文件删除成功", zap.String("key", path))
	return nil
}

// Get 获取 OSS 文件内容
func (s *OSSStorage) Get(path string) ([]byte, error) {
	body, err := s.bucket.GetObject(path)
	if err != nil {
		logger.Error("OSS 读取失败", zap.Error(err), zap.String("key", path))
		return nil, fmt.Errorf("OSS 读取失败: %w", err)
	}
	defer body.Close()

	data, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("读取 OSS 文件内容失败: %w", err)
	}
	return data, nil
}

// Exists 检查 OSS 文件是否存在
func (s *OSSStorage) Exists(path string) bool {
	_, err := s.bucket.GetObjectMeta(path)
	return err == nil
}

// 全局存储实例
var storage Storage

// Init 初始化存储
func Init(cfg *config.StorageConfig) error {
	switch cfg.Driver {
	case "local":
		storage = NewLocalStorage(&cfg.Local)
		logger.Info("使用本地存储", zap.String("path", cfg.Local.Path))
	case "oss":
		ossStorage, err := NewOSSStorage(&cfg.OSS)
		if err != nil {
			return err
		}
		storage = ossStorage
		logger.Info("使用 OSS 存储", zap.String("bucket", cfg.OSS.Bucket))
	default:
		return fmt.Errorf("不支持的存储驱动: %s", cfg.Driver)
	}
	return nil
}

// Upload 上传文件
func Upload(file *multipart.FileHeader, subdir string) (string, error) {
	return storage.Upload(file, subdir)
}

// UploadFromBytes 从字节数组上传文件
func UploadFromBytes(data []byte, filename, subdir string) (string, error) {
	return storage.UploadFromBytes(data, filename, subdir)
}

// GetURL 获取文件访问 URL
func GetURL(path string) string {
	return storage.GetURL(path)
}

// Delete 删除文件
func Delete(path string) error {
	return storage.Delete(path)
}

// Get 获取文件内容
func Get(path string) ([]byte, error) {
	return storage.Get(path)
}

// Exists 检查文件是否存在
func Exists(path string) bool {
	return storage.Exists(path)
}
