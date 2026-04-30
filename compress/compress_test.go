package compress_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EthanCodeCraft/xlgo-core/compress"
	"github.com/EthanCodeCraft/xlgo-core/utils"
)

func fileExists(path string) bool {
	return utils.FileExists(path)
}

func getTempDir() string {
	return filepath.Join(os.TempDir(), "xlgo_compress_test")
}

func setupTestFiles(t *testing.T) (string, string, string) {
	dir := getTempDir()
	os.MkdirAll(dir, 0755)

	// 创建测试文件
	srcFile := filepath.Join(dir, "test.txt")
	content := "Hello, this is a test file for compression!"
	os.WriteFile(srcFile, []byte(content), 0644)

	// 创建子目录和文件
	subDir := filepath.Join(dir, "subdir")
	os.MkdirAll(subDir, 0755)
	subFile := filepath.Join(subDir, "sub.txt")
	os.WriteFile(subFile, []byte("Subdir content"), 0644)

	return dir, srcFile, content
}

func cleanupTestDir(t *testing.T) {
	os.RemoveAll(getTempDir())
}

func TestGzipCompress(t *testing.T) {
	defer cleanupTestDir(t)

	data := []byte("Hello World, this is test data for gzip compression!")
	compressed, err := compress.GzipCompress(data)
	if err != nil {
		t.Fatalf("GzipCompress error: %v", err)
	}

	if len(compressed) == 0 {
		t.Error("Compressed data is empty")
	}

	// 压缩后的数据应该比原数据小（对于重复数据）
	// 但对于很短的数据可能更大，所以不做严格长度比较

	// 解压缩验证
	decompressed, err := compress.GzipDecompress(compressed)
	if err != nil {
		t.Fatalf("GzipDecompress error: %v", err)
	}

	if string(decompressed) != string(data) {
		t.Errorf("Decompressed data mismatch: got %s, want %s", decompressed, data)
	}
}

func TestGzipDecompressInvalid(t *testing.T) {
	// 测试无效数据解压
	_, err := compress.GzipDecompress([]byte("invalid gzip data"))
	if err == nil {
		t.Error("GzipDecompress should fail with invalid data")
	}
}

func TestGzipCompressEmpty(t *testing.T) {
	data := []byte("")
	compressed, err := compress.GzipCompress(data)
	if err != nil {
		t.Fatalf("GzipCompress empty error: %v", err)
	}

	decompressed, err := compress.GzipDecompress(compressed)
	if err != nil {
		t.Fatalf("GzipDecompress empty error: %v", err)
	}

	if len(decompressed) != 0 {
		t.Error("Decompressed empty data should be empty")
	}
}

func TestGzipCompressFile(t *testing.T) {
	defer cleanupTestDir(t)
	dir, srcFile, content := setupTestFiles(t)

	dstFile := filepath.Join(dir, "test.txt.gz")

	// 压缩文件
	err := compress.GzipCompressFile(srcFile, dstFile)
	if err != nil {
		t.Fatalf("GzipCompressFile error: %v", err)
	}

	// 验证压缩文件存在
	if !fileExists(dstFile) {
		t.Error("Compressed file not created")
	}

	// 解压文件
	outFile := filepath.Join(dir, "test_out.txt")
	err = compress.GzipDecompressFile(dstFile, outFile)
	if err != nil {
		t.Fatalf("GzipDecompressFile error: %v", err)
	}

	// 验证解压内容
	outContent, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}

	if string(outContent) != content {
		t.Errorf("Decompressed content mismatch: got %s, want %s", outContent, content)
	}
}

func TestGzipCompressFileNonexistent(t *testing.T) {
	err := compress.GzipCompressFile("/nonexistent/file.txt", "/tmp/out.gz")
	if err == nil {
		t.Error("GzipCompressFile should fail with nonexistent source")
	}
}

func TestZip(t *testing.T) {
	defer cleanupTestDir(t)
	dir, srcFile, _ := setupTestFiles(t)

	zipPath := filepath.Join(dir, "test.zip")
	paths := []string{srcFile, filepath.Join(dir, "subdir")}

	// 创建 zip
	err := compress.Zip(zipPath, paths)
	if err != nil {
		t.Fatalf("Zip error: %v", err)
	}

	// 验证 zip 文件存在
	if !fileExists(zipPath) {
		t.Error("Zip file not created")
	}

	// 解压 zip
	dstDir := filepath.Join(dir, "unzipped")
	err = compress.Unzip(zipPath, dstDir)
	if err != nil {
		t.Fatalf("Unzip error: %v", err)
	}

	// 验证解压后的文件
	outFile := filepath.Join(dstDir, "test.txt")
	if !fileExists(outFile) {
		t.Error("Unzipped file not found")
	}
}

func TestZipSingleFile(t *testing.T) {
	defer cleanupTestDir(t)
	dir, srcFile, content := setupTestFiles(t)

	zipPath := filepath.Join(dir, "single.zip")

	err := compress.Zip(zipPath, []string{srcFile})
	if err != nil {
		t.Fatalf("Zip single file error: %v", err)
	}

	dstDir := filepath.Join(dir, "single_unzipped")
	err = compress.Unzip(zipPath, dstDir)
	if err != nil {
		t.Fatalf("Unzip error: %v", err)
	}

	// 验证内容
	outContent, err := os.ReadFile(filepath.Join(dstDir, "test.txt"))
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}

	if string(outContent) != content {
		t.Error("Unzipped content mismatch")
	}
}

func TestUnzipInvalid(t *testing.T) {
	defer cleanupTestDir(t)
	dir := getTempDir()
	os.MkdirAll(dir, 0755)

	// 无效 zip 文件
	invalidZip := filepath.Join(dir, "invalid.zip")
	os.WriteFile(invalidZip, []byte("not a zip file"), 0644)

	dstDir := filepath.Join(dir, "out")
	err := compress.Unzip(invalidZip, dstDir)
	if err == nil {
		t.Error("Unzip should fail with invalid zip file")
	}
}

func TestUnzipNonexistent(t *testing.T) {
	err := compress.Unzip("/nonexistent/file.zip", "/tmp/out")
	if err == nil {
		t.Error("Unzip should fail with nonexistent file")
	}
}

// ===== Benchmarks =====

func BenchmarkGzipCompress(b *testing.B) {
	data := make([]byte, 1024)
	for i := range data {
		data[i] = byte(i % 256)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		compress.GzipCompress(data)
	}
}

func BenchmarkGzipDecompress(b *testing.B) {
	data := make([]byte, 1024)
	for i := range data {
		data[i] = byte(i % 256)
	}
	compressed, _ := compress.GzipCompress(data)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		compress.GzipDecompress(compressed)
	}
}