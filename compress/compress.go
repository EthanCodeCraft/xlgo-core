package compress

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// 解压安全相关错误。
var (
	// ErrPathTraversal zip 条目名逃逸目标目录（C5a Zip-Slip）。
	ErrPathTraversal = errors.New("zip entry escapes destination directory")
	// ErrSymlinkEntry zip 条目为符号链接，已拒绝（防经软链二次穿越）。
	ErrSymlinkEntry = errors.New("zip entry is a symlink, rejected")
	// ErrDecompressLimit 解压大小超过上限（C5b 解压炸弹）。
	ErrDecompressLimit = errors.New("decompress size limit exceeded")
)

const (
	// defaultDecompressLimit 单流/单条目默认解压上限（100MB），防 OOM / 磁盘耗尽（C5b）。
	defaultDecompressLimit int64 = 100 * 1024 * 1024
	// defaultDecompressTotalLimit Unzip 默认累计解压上限（1GB）。
	defaultDecompressTotalLimit int64 = 1 * 1024 * 1024 * 1024
)

// DecompressOptions 解压安全选项。Zip-Slip 防护（前缀锚定 + 拒绝符号链接）始终启用，无需配置；
// 本选项仅控制解压大小上限以防解压炸弹（C5b）。
type DecompressOptions struct {
	// MaxBytes 单流 / 单条目解压大小上限（字节）。0 = 默认 100MB，-1 = 不限制。
	MaxBytes int64
	// MaxTotalBytes Unzip 累计解压大小上限（字节）。0 = 默认 1GB，-1 = 不限制。
	// 仅 Unzip 生效。
	MaxTotalBytes int64
}

// resolveLimit 解析大小上限：n<0 不限，n==0 用 def，n>0 用 n。
func resolveLimit(n, def int64) int64 {
	if n < 0 {
		return -1
	}
	if n == 0 {
		return def
	}
	return n
}

// minLimit 返回两个上限中较小者；-1 视为无限。
func minLimit(a, b int64) int64 {
	if a < 0 {
		return b
	}
	if b < 0 {
		return a
	}
	if a < b {
		return a
	}
	return b
}

// GzipCompress 压缩数据
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

// GzipDecompress 解压缩数据。默认上限 100MB 防解压炸弹 OOM（C5b）；
// 需解压更大文件请用 GzipDecompressWithOptions。
func GzipDecompress(data []byte) ([]byte, error) {
	return GzipDecompressWithOptions(data, DecompressOptions{})
}

// GzipDecompressWithOptions 解压缩数据，可配置大小上限（C5b）。
func GzipDecompressWithOptions(data []byte, opts DecompressOptions) ([]byte, error) {
	buf := bytes.NewReader(data)
	gz, err := gzip.NewReader(buf)
	if err != nil {
		return nil, err
	}
	defer gz.Close()

	limit := resolveLimit(opts.MaxBytes, defaultDecompressLimit)
	var reader io.Reader = gz
	if limit > 0 {
		// 多读 1 字节用于判断是否超限。
		reader = io.LimitReader(gz, limit+1)
	}
	out, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if limit > 0 && int64(len(out)) > limit {
		return nil, fmt.Errorf("解压后大小超过上限 %d 字节: %w", limit, ErrDecompressLimit)
	}
	return out, nil
}

// GzipCompressFile 压缩文件
func GzipCompressFile(src, dst string) error {
	// #nosec G304 -- src/dst 为调用方提供的本地文件路径，压缩 API 固有语义，非不可信输入
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	// #nosec G304 -- 同上，dst 为调用方指定输出路径
	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}

	gz := gzip.NewWriter(dstFile)

	// 保留原文件名和时间戳
	if info, err := srcFile.Stat(); err == nil {
		gz.Name = info.Name()
		gz.ModTime = info.ModTime()
	}

	_, copyErr := io.Copy(gz, srcFile)
	// gz.Close 刷出 gzip 尾部（含 CRC/大小校验），失败说明归档损坏，必须向上传播（M16/B18）。
	closeErr := gz.Close()
	// dstFile.Close 失败（如延迟写盘）同样意味着归档可能不完整。
	dstErr := dstFile.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	return dstErr
}

// GzipDecompressFile 解压文件。默认上限 100MB 防磁盘耗尽（C5b）；
// 需解压更大文件请用 GzipDecompressFileWithOptions。
func GzipDecompressFile(src, dst string) error {
	return GzipDecompressFileWithOptions(src, dst, DecompressOptions{})
}

// GzipDecompressFileWithOptions 解压文件，可配置大小上限（C5b）。
func GzipDecompressFileWithOptions(src, dst string, opts DecompressOptions) (err error) {
	// #nosec G304 -- src 为调用方提供的本地文件路径，解压 API 固有语义，非不可信输入
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

	// #nosec G304 -- dst 为调用方指定输出路径，解压 API 固有语义
	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	// L-A 修复：失败时（超限/拷贝错误）清理部分落盘的 dst，避免被拒炸弹遗留残片；
	// 成功时仅关闭。os.Remove 在 Close 之后，兼容 Windows（删除打开中的文件会失败）。
	defer func() {
		if cerr := dstFile.Close(); cerr != nil {
			err = errors.Join(err, cerr)
		}
		if err != nil {
			_ = os.Remove(dst)
		}
	}()

	limit := resolveLimit(opts.MaxBytes, defaultDecompressLimit)
	var written int64
	if limit > 0 {
		// CopyN 最多读 limit+1 字节，超限即判定为炸弹。
		written, err = io.CopyN(dstFile, gz, limit+1)
	} else {
		// #nosec G110 -- 仅当调用方显式 MaxBytes=-1 不限时走此分支，有限分支已用 CopyN 封顶防炸弹
		written, err = io.Copy(dstFile, gz)
	}
	if err != nil && err != io.EOF {
		return err
	}
	if limit > 0 && written > limit {
		return fmt.Errorf("解压后大小超过上限 %d 字节: %w", limit, ErrDecompressLimit)
	}
	return nil
}

// Zip 压缩文件或目录
// 参数: zipPath 目标zip文件路径，paths 要压缩的文件或目录列表
func Zip(zipPath string, paths []string) error {
	// 创建目标目录
	if err := os.MkdirAll(filepath.Dir(zipPath), 0750); err != nil {
		return err
	}

	// 创建 zip 文件
	// #nosec G304 -- zipPath 为调用方指定输出路径，压缩 API 固有语义
	archive, err := os.Create(zipPath)
	if err != nil {
		return err
	}

	zipWriter := zip.NewWriter(archive)

	walkErr := func() error {
		for _, srcPath := range paths {
			srcPath = strings.TrimSuffix(srcPath, string(os.PathSeparator))

			// #nosec G122 -- 压缩调用方提供的源路径，非解压不可信输入；symlink 跟随是压缩场景的可接受行为
			err := filepath.Walk(srcPath, func(path string, info fs.FileInfo, err error) error {
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

				// #nosec G304,G122 -- path 为 Walk 遍历调用方源路径产生，非不可信输入；压缩场景接受 symlink 跟随语义。
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
	}()

	// zipWriter.Close 刷出中央目录记录，失败说明归档损坏，必须向上传播（M16/B18）。
	zipCloseErr := zipWriter.Close()
	archiveCloseErr := archive.Close()
	if walkErr != nil {
		return walkErr
	}
	if zipCloseErr != nil {
		return zipCloseErr
	}
	return archiveCloseErr
}

// Unzip 解压 zip 文件。默认启用 Zip-Slip 防护（前缀锚定 + 拒绝符号链接），
// 单条目上限 100MB、累计上限 1GB 防解压炸弹（C5b）；需自定义上限请用 UnzipWithOptions。
func Unzip(zipPath, dstDir string) error {
	return UnzipWithOptions(zipPath, dstDir, DecompressOptions{})
}

// UnzipWithOptions 解压 zip 文件，可配置大小上限（C5b）。Zip-Slip 防护始终启用。
func UnzipWithOptions(zipPath, dstDir string, opts DecompressOptions) error {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer reader.Close()

	// 用绝对路径作目标锚定根，避免相对路径 + `..` 组合绕过前缀校验（C5a）。
	absDst, err := filepath.Abs(dstDir)
	if err != nil {
		return fmt.Errorf("resolve unzip destination failed: %w", err)
	}
	absDst = filepath.Clean(absDst)

	entryLimit := resolveLimit(opts.MaxBytes, defaultDecompressLimit)
	totalLimit := resolveLimit(opts.MaxTotalBytes, defaultDecompressTotalLimit)
	var total int64

	for _, file := range reader.File {
		written, err := unzipFile(file, absDst, entryLimit, totalLimit, total)
		if err != nil {
			return err
		}
		total += written
	}
	return nil
}

// unzipFile 解压单个 zip 条目到 absDst 下。
// entryLimit: 单条目上限（-1 不限）；totalLimit: 累计上限（-1 不限）；accrued: 已解压累计字节。
// 返回本条目写入字节数。
func unzipFile(file *zip.File, absDst string, entryLimit, totalLimit, accrued int64) (written int64, err error) {
	// 拒绝符号链接条目，防经软链二次穿越（C5a）。
	if file.Mode()&os.ModeSymlink != 0 {
		return 0, fmt.Errorf("条目 %s 为符号链接: %w", file.Name, ErrSymlinkEntry)
	}

	// zip 条目名规范用正斜杠；转成当前平台分隔符后再 Join，并以前缀锚定拒绝 `..` 逃逸（C5a）。
	name := filepath.FromSlash(file.Name)
	// 拒绝绝对路径与以分隔符开头的条目（非标准、可疑，避免平台语义差异）。
	// filepath.IsAbs 在 Windows 不认 "/x"（无盘符）为绝对路径，故补充分隔符前缀检查。
	if filepath.IsAbs(name) || strings.HasPrefix(name, string(os.PathSeparator)) || strings.HasPrefix(file.Name, "/") {
		return 0, fmt.Errorf("条目 %s 为绝对路径: %w", file.Name, ErrPathTraversal)
	}
	target := filepath.Join(absDst, name)
	if target == absDst || !strings.HasPrefix(target, absDst+string(os.PathSeparator)) {
		return 0, fmt.Errorf("条目 %s 逃逸目标目录: %w", file.Name, ErrPathTraversal)
	}

	if file.FileInfo().IsDir() {
		if err := os.MkdirAll(target, 0750); err != nil {
			return 0, err
		}
		return 0, nil
	}

	// 创建父目录
	if err := os.MkdirAll(filepath.Dir(target), 0750); err != nil {
		return 0, err
	}

	// 打开 zip 中的文件
	rc, err := file.Open()
	if err != nil {
		return 0, err
	}
	defer rc.Close()

	// 创建目标文件
	// #nosec G304 -- target 经前缀锚定校验（absDst+sep），已防 Zip-Slip 逃逸
	dstFile, err := os.Create(target)
	if err != nil {
		return 0, err
	}
	// L-A 修复：失败时（超限/拷贝错误）清理部分落盘的 target，避免被拒炸弹遗留残片；
	// 成功时仅关闭。os.Remove 在 Close 之后，兼容 Windows（删除打开中的文件会失败）。
	defer func() {
		if cerr := dstFile.Close(); cerr != nil {
			err = errors.Join(err, cerr)
		}
		if err != nil {
			_ = os.Remove(target)
		}
	}()

	// 计算本次拷贝上限：单条目上限与累计剩余上限中较小者（-1 视为无限）。
	// remaining: 累计剩余（-1 表示累计不限）；totalLimit>0 时若已无剩余，直接判超限。
	remaining := int64(-1)
	if totalLimit > 0 {
		remaining = totalLimit - accrued
		if remaining <= 0 {
			return 0, fmt.Errorf("累计解压超过上限 %d 字节: %w", totalLimit, ErrDecompressLimit)
		}
	}
	cap := minLimit(entryLimit, remaining)
	// cap 为 -1（两者皆不限）或 >0（有限上限，剩余已保证 >0），不会是 0。

	if cap > 0 {
		written, err = io.CopyN(dstFile, rc, cap+1)
	} else {
		// #nosec G110 -- 仅当调用方显式 MaxBytes=-1 不限时走此分支，有限分支已用 CopyN 封顶防炸弹
		written, err = io.Copy(dstFile, rc)
	}
	if err != nil && err != io.EOF {
		return written, err
	}
	if cap > 0 && written > cap {
		return written, fmt.Errorf("条目 %s 超过解压上限 %d 字节: %w", file.Name, cap, ErrDecompressLimit)
	}
	return written, nil
}
