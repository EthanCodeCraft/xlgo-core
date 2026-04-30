package compress

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// GzipCompress 压缩数据
// 评分: ⭐⭐⭐⭐⭐
// 理由: API响应压缩、数据传输常用
func GzipCompress(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(data); err != nil {
		_ = gz.Close()
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// GzipDecompress 解压缩数据
// 评分: ⭐⭐⭐⭐⭐
// 理由: GzipCompress 的反向操作
func GzipDecompress(data []byte) ([]byte, error) {
	buf := bytes.NewReader(data)
	gz, err := gzip.NewReader(buf)
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	return io.ReadAll(gz)
}

// GzipCompressFile 压缩文件
// 评分: ⭐⭐⭐⭐⭐
// 理由: 日志归档、文件传输常用
func GzipCompressFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	gz := gzip.NewWriter(dstFile)
	defer gz.Close()

	// 保留原文件名和时间戳
	if info, err := srcFile.Stat(); err == nil {
		gz.Name = info.Name()
		gz.ModTime = info.ModTime()
	}

	_, err = io.Copy(gz, srcFile)
	return err
}

// GzipDecompressFile 解压文件
// 评分: ⭐⭐⭐⭐⭐
// 理由: GzipCompressFile 的反向操作
func GzipDecompressFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	gz, err := gzip.NewReader(srcFile)
	if err != nil {
		return err
	}
	defer gz.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, gz)
	return err
}

// Zip 压缩文件或目录
// 评分: ⭐⭐⭐⭐⭐
// 理由: 批量打包下载、备份常用
// 参数: zipPath 目标zip文件路径，paths 要压缩的文件或目录列表
func Zip(zipPath string, paths []string) error {
	// 创建目标目录
	if err := os.MkdirAll(filepath.Dir(zipPath), 0755); err != nil {
		return err
	}

	// 创建 zip 文件
	archive, err := os.Create(zipPath)
	if err != nil {
		return err
	}
	defer archive.Close()

	zipWriter := zip.NewWriter(archive)
	defer zipWriter.Close()

	for _, srcPath := range paths {
		srcPath = strings.TrimSuffix(srcPath, string(os.PathSeparator))

		err = filepath.Walk(srcPath, func(path string, info fs.FileInfo, err error) error {
			if err != nil {
				return err
			}

			// 创建文件头
			header, err := zip.FileInfoHeader(info)
			if err != nil {
				return err
			}
			header.Method = zip.Deflate

			// 设置相对路径
			header.Name, err = filepath.Rel(filepath.Dir(srcPath), path)
			if err != nil {
				return err
			}
			if info.IsDir() {
				header.Name += string(os.PathSeparator)
			}

			writer, err := zipWriter.CreateHeader(header)
			if err != nil {
				return err
			}

			if info.IsDir() {
				return nil
			}

			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer file.Close()

			_, err = io.Copy(writer, file)
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// Unzip 解压 zip 文件
// 评分: ⭐⭐⭐⭐⭐
// 理由: Zip 的反向操作
// 参数: zipPath zip文件路径，dstDir 目标目录
func Unzip(zipPath, dstDir string) error {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer reader.Close()

	for _, file := range reader.File {
		if err := unzipFile(file, dstDir); err != nil {
			return err
		}
	}
	return nil
}

func unzipFile(file *zip.File, dstDir string) error {
	filePath := path.Join(dstDir, file.Name)

	if file.FileInfo().IsDir() {
		return os.MkdirAll(filePath, 0755)
	}

	// 创建父目录
	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return err
	}

	// 打开 zip 中的文件
	rc, err := file.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	// 创建目标文件
	dstFile, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, rc)
	return err
}
