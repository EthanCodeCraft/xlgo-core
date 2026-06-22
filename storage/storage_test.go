package storage_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/EthanCodeCraft/xlgo-core/config"
	"github.com/EthanCodeCraft/xlgo-core/logger"
	"github.com/EthanCodeCraft/xlgo-core/storage"
)

func init() {
	// 初始化日志（测试需要）
	cfg := &config.Config{
		Log: config.LogConfig{
			Dir:        os.TempDir(),
			MaxSize:    10,
			MaxBackups: 1,
			MaxAge:     1,
			Compress:   false,
		},
	}
	logger.Init(cfg)
}

func TestStorageNotInitialized(t *testing.T) {
	storage.SetStorage(nil)

	if _, err := storage.UploadFromBytes([]byte("test"), "test.txt", "docs"); !errors.Is(err, storage.ErrStorageNotInitialized) {
		t.Fatalf("expected ErrStorageNotInitialized, got %v", err)
	}
	if err := storage.Delete("missing"); !errors.Is(err, storage.ErrStorageNotInitialized) {
		t.Fatalf("expected ErrStorageNotInitialized, got %v", err)
	}
	if _, err := storage.Get("missing"); !errors.Is(err, storage.ErrStorageNotInitialized) {
		t.Fatalf("expected ErrStorageNotInitialized, got %v", err)
	}
	if url := storage.GetURL("missing"); url != "" {
		t.Fatalf("expected empty URL, got %q", url)
	}
	if storage.Exists("missing") {
		t.Fatal("expected Exists false without storage")
	}
}

func TestLocalStorage(t *testing.T) {
	cfg := &config.LocalStorageConfig{
		Path:    "/tmp/uploads",
		BaseURL: "http://localhost/uploads",
	}

	local := storage.NewLocalStorage(cfg)
	if local == nil {
		t.Error("NewLocalStorage should not return nil")
	}
}

func TestStorageInterface(t *testing.T) {
	cfg := &config.LocalStorageConfig{
		Path:    "/tmp/uploads",
		BaseURL: "http://localhost/uploads",
	}

	var s storage.Storage = storage.NewLocalStorage(cfg)
	if s == nil {
		t.Error("LocalStorage should implement Storage interface")
	}
}

func TestLocalStorageGetURL(t *testing.T) {
	cfg := &config.LocalStorageConfig{
		Path:    "/uploads",
		BaseURL: "http://example.com",
	}

	local := storage.NewLocalStorage(cfg)
	url := local.GetURL("images/test.jpg")

	expected := "http://example.com/images/test.jpg"
	if url != expected {
		t.Errorf("GetURL = %s, want %s", url, expected)
	}
}

func TestLocalStorageDeleteGetExists(t *testing.T) {
	// 创建临时目录
	tmpDir := filepath.Join(os.TempDir(), "xlgo_storage_test")
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.LocalStorageConfig{
		Path:    tmpDir,
		BaseURL: "http://localhost/uploads",
	}

	local := storage.NewLocalStorage(cfg)

	// 创建测试文件
	testFile := filepath.Join(tmpDir, "test.txt")
	testData := []byte("hello world")
	if err := os.WriteFile(testFile, testData, 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	// 测试 Exists
	if !local.Exists("test.txt") {
		t.Error("Exists should return true for existing file")
	}

	// 测试 Get
	data, err := local.Get("test.txt")
	if err != nil {
		t.Errorf("Get failed: %v", err)
	}
	if string(data) != "hello world" {
		t.Errorf("Get data = %s, want 'hello world'", string(data))
	}

	// 测试 Delete
	err = local.Delete("test.txt")
	if err != nil {
		t.Errorf("Delete failed: %v", err)
	}

	// 验证删除后不存在
	if local.Exists("test.txt") {
		t.Error("Exists should return false after delete")
	}

	// 删除不存在的文件应该失败
	err = local.Delete("nonexistent.txt")
	if err == nil {
		t.Error("Delete should fail for nonexistent file")
	}

	// Get 不存在的文件应该失败
	_, err = local.Get("nonexistent.txt")
	if err == nil {
		t.Error("Get should fail for nonexistent file")
	}
}

func TestStorageInitInvalidDriver(t *testing.T) {
	cfg := &config.StorageConfig{
		Driver: "invalid",
	}

	err := storage.Init(cfg)
	if err == nil {
		t.Error("Init should fail with invalid driver")
	}
}

func TestOSSStorageConfig(t *testing.T) {
	cfg := &config.OSSStorageConfig{
		Endpoint:        "oss-cn-hangzhou.aliyuncs.com",
		Bucket:          "test-bucket",
		AccessKeyID:     "test-id",
		AccessKeySecret: "test-secret",
		BaseURL:         "https://test.oss-cn-hangzhou.aliyuncs.com",
	}

	if cfg.Endpoint != "oss-cn-hangzhou.aliyuncs.com" {
		t.Error("OSSStorageConfig Endpoint failed")
	}
}