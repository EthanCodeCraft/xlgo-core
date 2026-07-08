package storage

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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

var (
	// ErrStorageNotInitialized storage 未初始化。
	ErrStorageNotInitialized = errors.New("storage not initialized")
	// ErrPathTraversal 路径穿越被拒绝（C4a）。Delete/Get/Exists/Upload 的相对路径
	// 含 `..` 或绝对路径、逃逸根目录时返回。
	ErrPathTraversal = errors.New("path traversal detected")
	// ErrInvalidPath 路径非法（空、含 NUL 等）。
	ErrInvalidPath = errors.New("invalid path")
	// ErrInvalidFile 上传文件参数非法，例如 nil *multipart.FileHeader。
	ErrInvalidFile = errors.New("invalid file")
	// ErrUploadTooLarge 上传声明大小或实际字节数超过 MaxSizeBytes（P0）。客户端声明的 file.Size
	// 不可信，故除前置校验外，拷贝阶段也按实际字节封顶，防止声明小体积却流式发送大 body 撑爆磁盘/OSS。
	ErrUploadTooLarge = errors.New("upload exceeds max size")
)

const (
	// defaultMaxReadBytes Get 默认读取上限（100MB），防止全量读入内存 OOM（C4c）。
	defaultMaxReadBytes int64 = 100 * 1024 * 1024
	// mimeSniffPrefixLen http.DetectContentType 最多嗅探 512 字节。
	mimeSniffPrefixLen = 512
)

// resolveMaxRead 解析 Get 读取上限：n<0 不限，n==0 用默认，n>0 用 n。
func resolveMaxRead(n int64) int64 {
	if n < 0 {
		return -1
	}
	if n == 0 {
		return defaultMaxReadBytes
	}
	return n
}

// validateUploadSize 校验上传文件大小（C4b）。MaxSizeBytes<=0 表示不限。
//
// 注意：此处的 size 来自客户端声明（multipart.FileHeader.Size），仅作前置快速拒绝，
// 不可信——攻击者可声明小体积却流式发送大 body。真正的落盘/上传封顶由 enforceUploadSize
// 在拷贝阶段按实际字节数二次把关（P0）。
func validateUploadSize(p config.UploadPolicy, size int64) error {
	if p.MaxSizeBytes > 0 && size > p.MaxSizeBytes {
		return fmt.Errorf("文件大小 %d 超过上限 %d: %w", size, p.MaxSizeBytes, ErrUploadTooLarge)
	}
	return nil
}

// enforceMaxReader 包装底层 reader，实际读取累计超过 max 字节即返回 ErrUploadTooLarge（P0）。
// max<=0 表示不限。用于上传拷贝阶段的真实大小封顶，弥补客户端声明 Size 不可信。
type enforceMaxReader struct {
	src   io.Reader
	max   int64
	count int64
}

func (r *enforceMaxReader) Read(p []byte) (int, error) {
	if r.max > 0 && r.count >= r.max {
		// 上限内的字节已全部读出：用独立探针读 1 字节判断源是否还有数据，有则超限。
		var probe [1]byte
		if n, _ := r.src.Read(probe[:]); n > 0 {
			return 0, ErrUploadTooLarge
		}
		// 源已到 EOF，实际大小恰好等于上限，放行结束。
		return 0, io.EOF
	}
	n, err := r.src.Read(p)
	r.count += int64(n)
	if r.max > 0 && r.count > r.max {
		return 0, ErrUploadTooLarge
	}
	return n, err
}

// enforceUploadSize 若策略配置了 MaxSizeBytes，则包装 src 在拷贝阶段实测封顶（P0）。
// 未配置上限时原样返回，零开销。
func enforceUploadSize(p config.UploadPolicy, src io.Reader) io.Reader {
	if p.MaxSizeBytes <= 0 {
		return src
	}
	return &enforceMaxReader{src: src, max: p.MaxSizeBytes}
}

// validateUploadExt 校验上传文件扩展名（C4b）。AllowedExts 为空表示不限。
func validateUploadExt(p config.UploadPolicy, filename string) error {
	if len(p.AllowedExts) == 0 {
		return nil
	}
	ext := strings.ToLower(filepath.Ext(filename))
	if !containsString(p.AllowedExts, ext) {
		return fmt.Errorf("扩展名 %s 不在白名单: %w", ext, ErrInvalidPath)
	}
	return nil
}

// sniffUploadMIME 嗅探 src 前 512 字节并按 AllowedMIMEs 校验（C4b）。
// 返回的 reader 已拼回已读头部，后续可继续读到完整内容。
// AllowedMIMEs 为空时直接返回原 src 不嗅探。
func sniffUploadMIME(p config.UploadPolicy, src io.Reader) (io.Reader, error) {
	if len(p.AllowedMIMEs) == 0 {
		return src, nil
	}
	head := make([]byte, mimeSniffPrefixLen)
	n, err := io.ReadFull(src, head)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, fmt.Errorf("读取文件头失败: %w", err)
	}
	// http.DetectContentType 可能返回 "text/plain; charset=utf-8"，
	// 与白名单 "text/plain" 比较时取主类型（分号前）。
	detected := strings.ToLower(strings.TrimSpace(strings.Split(http.DetectContentType(head[:n]), ";")[0]))
	allowed := make([]string, len(p.AllowedMIMEs))
	for i, m := range p.AllowedMIMEs {
		allowed[i] = strings.ToLower(strings.TrimSpace(strings.Split(m, ";")[0]))
	}
	if !containsString(allowed, detected) {
		return nil, fmt.Errorf("文件类型 %s 不在白名单: %w", detected, ErrInvalidPath)
	}
	return io.MultiReader(bytes.NewReader(head[:n]), src), nil
}

func containsString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// uniqueFilename 生成带随机后缀的唯一文件名，避免同一纳秒内并发上传导致文件名冲突。
// 格式: <unixNano>-<8字节随机hex>.<ext>
func uniqueFilename(now time.Time, ext string) string {
	randBytes := make([]byte, 8)
	// crypto/rand 失败极少见；退化为全零也能由纳秒时间戳保证基本唯一性
	_, _ = rand.Read(randBytes)
	return fmt.Sprintf("%d-%x%s", now.UnixNano(), randBytes, ext)
}

// sanitizeObjectKey 净化 OSS object key（C4a）。OSS key 为扁平字符串无 FS 穿越语义，
// 但拒绝含 `..`、绝对路径、NUL、空值等可疑输入，防止 key 注入与越权访问。
// 同时将 Windows 反斜杠归一化为 OSS 规范的正斜杠，保证跨平台 key 一致。
func sanitizeObjectKey(key string) (string, error) {
	if key == "" || strings.ContainsRune(key, 0) {
		return "", ErrInvalidPath
	}
	// 归一化 Windows 反斜杠为正斜杠（OSS key 规范分隔符），保证 Windows/Linux 部署 key 一致。
	key = strings.ReplaceAll(key, "\\", "/")
	if strings.Contains(key, "..") {
		return "", ErrPathTraversal
	}
	// 框架生成的 key 不以 / 开头；拒绝绝对路径形式避免歧义。
	if strings.HasPrefix(key, "/") {
		return "", ErrPathTraversal
	}
	// 规范化多余分隔符，不改变合法 key 语义。
	return path.Clean(key), nil
}

func publicURL(baseURL, key string) string {
	cleanKey, err := sanitizeObjectKey(key)
	if err != nil {
		return ""
	}
	return strings.TrimRight(baseURL, "/") + "/" + cleanKey
}

// LocalStorage 本地存储
type LocalStorage struct {
	rootAbs      string // 绝对根路径（已 Clean），前缀锚定用（C4a）
	baseURL      string
	policy       config.UploadPolicy
	maxReadBytes int64
}

// NewLocalStorage 创建本地存储实例
//
// 安全约束：Path 指向的根目录应为框架独占目录，不与用户可控内容混用。
// safeJoin 已防 `..` 路径穿越，但若根目录内已存在指向外部的符号链接，
// Get 会跟随 symlink 读到根外内容——需保证攻击者无法在根目录内创建 symlink。
// cfg 为 nil、Path 无法解析或根目录本身是 symlink 时，实例 fail-closed：
// 后续文件操作返回 ErrStorageNotInitialized。
func NewLocalStorage(cfg *config.LocalStorageConfig) *LocalStorage {
	if cfg == nil {
		return &LocalStorage{maxReadBytes: resolveMaxRead(0)}
	}
	// 用绝对路径作根锚定，避免相对路径 + `..` 组合绕过前缀校验（C4a）。
	var rootAbs string
	abs, err := filepath.Abs(cfg.Path)
	if err != nil {
		// P1 #20：Abs 失败（如 os.Getwd 失败）不再回退相对 cfg.Path——相对根会使 safeJoin
		// 前缀校验基于相对路径、削弱穿越防御。改为 fail-closed：置空 rootAbs，
		// safeJoin 据此拒绝一切操作，并记录错误，避免"看似可用实则防御失效"的本地存储。
		logger.Error("storage: 解析存储根目录绝对路径失败，本地存储将不可用（fail-closed）",
			zap.String("path", cfg.Path), zap.Error(err))
		rootAbs = ""
	} else {
		rootAbs = filepath.Clean(abs)
	}
	if rootAbs != "" {
		if info, err := os.Lstat(rootAbs); err == nil && info.Mode()&os.ModeSymlink != 0 {
			logger.Error("storage: local storage root must not be a symlink", zap.String("path", rootAbs))
			rootAbs = ""
		}
	}
	return &LocalStorage{
		rootAbs:      rootAbs,
		baseURL:      cfg.BaseURL,
		policy:       cfg.Upload,
		maxReadBytes: resolveMaxRead(cfg.MaxReadBytes),
	}
}

// safeJoin 将根路径与相对片段拼接为绝对路径，并以前缀锚定拒绝穿越（C4a）。
// 任何片段为绝对路径或含 NUL、最终路径逃逸 rootAbs 时返回错误。
func (s *LocalStorage) safeJoin(parts ...string) (string, error) {
	// P1 #20：rootAbs 为空表示构造时 Abs 失败，fail-closed 拒绝所有路径操作。
	if s.rootAbs == "" {
		return "", ErrStorageNotInitialized
	}
	for _, p := range parts {
		if filepath.IsAbs(p) {
			return "", ErrPathTraversal
		}
		if strings.ContainsRune(p, 0) {
			return "", ErrInvalidPath
		}
	}
	joined := filepath.Join(append([]string{s.rootAbs}, parts...)...)
	if joined == s.rootAbs || !strings.HasPrefix(joined, s.rootAbs+string(os.PathSeparator)) {
		return "", ErrPathTraversal
	}
	return joined, nil
}

// Upload 上传文件
func (s *LocalStorage) Upload(file *multipart.FileHeader, subdir string) (ret string, err error) {
	if file == nil {
		return "", ErrInvalidFile
	}
	// 上传安全策略校验（大小 / 扩展名）；file.Size 由 multipart 解析时填，无需打开文件（C4b）。
	if err := validateUploadSize(s.policy, file.Size); err != nil {
		return "", err
	}
	if err := validateUploadExt(s.policy, file.Filename); err != nil {
		return "", err
	}

	// 生成存储路径: /年/月/日/文件名
	now := time.Now()
	datePath := fmt.Sprintf("%d/%02d/%02d", now.Year(), now.Month(), now.Day())
	relativePath := filepath.Join(subdir, datePath)

	// 确保目录存在，且未逃逸根目录（C4a：subdir 含 `..` 会被 safeJoin 拒绝）
	fullPath, err := s.safeJoin(relativePath)
	if err != nil {
		logger.Warn("上传路径被拒绝", zap.String("subdir", subdir), zap.Error(err))
		return "", err
	}
	if err := os.MkdirAll(fullPath, 0750); err != nil {
		logger.Error("创建目录失败", zap.Error(err), zap.String("path", fullPath))
		return "", fmt.Errorf("创建目录失败: %w", err)
	}

	// 生成唯一文件名（服务端随机，可信）
	ext := filepath.Ext(file.Filename)
	filename := uniqueFilename(now, ext)
	dst := filepath.Join(fullPath, filename)

	// 打开源文件
	src, err := file.Open()
	if err != nil {
		return "", fmt.Errorf("打开文件失败: %w", err)
	}
	defer src.Close()

	// MIME 嗅探（如配置 AllowedMIMEs），嗅探后拼回头部
	var srcReader io.Reader = src
	srcReader, err = sniffUploadMIME(s.policy, srcReader)
	if err != nil {
		return "", err
	}

	// 创建目标文件
	// #nosec G304 -- dst 由 safeJoin 净化后的 fullPath 与服务端随机生成的 filename 拼成，路径已防穿越
	dstFile, err := os.Create(dst)
	if err != nil {
		return "", fmt.Errorf("创建文件失败: %w", err)
	}
	defer func() {
		if cerr := dstFile.Close(); cerr != nil {
			err = errors.Join(err, cerr)
		}
		if err != nil {
			ret = ""
			_ = os.Remove(dst)
		}
	}()

	// 复制文件内容。按策略实测封顶（P0）：超限即报错并清理已落盘的部分文件。
	if _, err := io.Copy(dstFile, enforceUploadSize(s.policy, srcReader)); err != nil {
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
func (s *LocalStorage) UploadFromBytes(data []byte, filename, subdir string) (ret string, err error) {
	// 上传安全策略校验（C4b）
	if err := validateUploadSize(s.policy, int64(len(data))); err != nil {
		return "", err
	}
	if err := validateUploadExt(s.policy, filename); err != nil {
		return "", err
	}

	// 生成存储路径: /年/月/日/文件名
	now := time.Now()
	datePath := fmt.Sprintf("%d/%02d/%02d", now.Year(), now.Month(), now.Day())
	relativePath := filepath.Join(subdir, datePath)

	// 确保目录存在，且未逃逸根目录（C4a）
	fullPath, err := s.safeJoin(relativePath)
	if err != nil {
		logger.Warn("上传路径被拒绝", zap.String("subdir", subdir), zap.Error(err))
		return "", err
	}
	if err := os.MkdirAll(fullPath, 0750); err != nil {
		logger.Error("创建目录失败", zap.Error(err), zap.String("path", fullPath))
		return "", fmt.Errorf("创建目录失败: %w", err)
	}

	// 生成唯一文件名（如果未提供扩展名，添加 .bin）
	ext := filepath.Ext(filename)
	if ext == "" {
		ext = ".bin"
	}
	fname := uniqueFilename(now, ext)
	dst := filepath.Join(fullPath, fname)

	// MIME 嗅探（如配置 AllowedMIMEs）
	var srcReader io.Reader = bytes.NewReader(data)
	srcReader, err = sniffUploadMIME(s.policy, srcReader)
	if err != nil {
		return "", err
	}

	// 创建目标文件
	// #nosec G304 -- dst 由 safeJoin 净化后的 fullPath 与服务端随机生成的 filename 拼成，路径已防穿越
	dstFile, err := os.Create(dst)
	if err != nil {
		return "", fmt.Errorf("创建文件失败: %w", err)
	}
	defer func() {
		if cerr := dstFile.Close(); cerr != nil {
			err = errors.Join(err, cerr)
		}
		if err != nil {
			ret = ""
			_ = os.Remove(dst)
		}
	}()

	// 写入文件内容
	if _, err := io.Copy(dstFile, srcReader); err != nil {
		return "", fmt.Errorf("保存文件失败: %w", err)
	}

	// 返回相对路径
	relativeFilePath := filepath.Join(relativePath, fname)
	// 统一使用正斜杠
	relativeFilePath = strings.ReplaceAll(relativeFilePath, "\\", "/")

	logger.Info("文件上传成功", zap.String("path", relativeFilePath))
	return relativeFilePath, nil
}

// GetURL 获取文件访问 URL
func (s *LocalStorage) GetURL(path string) string {
	return publicURL(s.baseURL, path)
}

// Delete 删除文件
func (s *LocalStorage) Delete(p string) error {
	fullPath, err := s.safeJoin(p)
	if err != nil {
		logger.Warn("删除路径被拒绝", zap.String("path", p), zap.Error(err))
		return err
	}
	if err := os.Remove(fullPath); err != nil {
		logger.Error("删除文件失败", zap.Error(err), zap.String("path", fullPath))
		return fmt.Errorf("删除文件失败: %w", err)
	}
	logger.Info("文件删除成功", zap.String("path", p))
	return nil
}

// Get 获取文件内容。读取受 maxReadBytes 封顶，防止全量读入内存 OOM（C4c）。
func (s *LocalStorage) Get(p string) ([]byte, error) {
	fullPath, err := s.safeJoin(p)
	if err != nil {
		logger.Warn("读取路径被拒绝", zap.String("path", p), zap.Error(err))
		return nil, err
	}
	// #nosec G304 -- fullPath 经 safeJoin 前缀锚定净化，已防穿越
	f, err := os.Open(fullPath)
	if err != nil {
		logger.Error("读取文件失败", zap.Error(err), zap.String("path", fullPath))
		return nil, fmt.Errorf("读取文件失败: %w", err)
	}
	defer f.Close()

	var reader io.Reader = f
	if s.maxReadBytes > 0 {
		// 多读 1 字节用于判断是否超限
		reader = io.LimitReader(f, s.maxReadBytes+1)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("读取文件内容失败: %w", err)
	}
	if s.maxReadBytes > 0 && int64(len(data)) > s.maxReadBytes {
		return nil, fmt.Errorf("文件超过最大读取限制 %d 字节: %w", s.maxReadBytes, ErrInvalidPath)
	}
	return data, nil
}

// Exists 检查文件是否存在
func (s *LocalStorage) Exists(p string) bool {
	fullPath, err := s.safeJoin(p)
	if err != nil {
		return false
	}
	_, err = os.Stat(fullPath)
	return err == nil
}

// OSSStorage OSS 存储
type OSSStorage struct {
	client       *oss.Client
	bucket       *oss.Bucket
	endpoint     string
	bucketName   string
	baseURL      string
	policy       config.UploadPolicy
	maxReadBytes int64
}

// NewOSSStorage 创建 OSS 存储实例
func NewOSSStorage(cfg *config.OSSStorageConfig) (*OSSStorage, error) {
	if cfg == nil {
		return nil, ErrStorageNotInitialized
	}
	client, err := oss.New(cfg.Endpoint, cfg.AccessKeyID, cfg.AccessKeySecret)
	if err != nil {
		return nil, fmt.Errorf("创建 OSS 客户端失败: %w", err)
	}

	bucket, err := client.Bucket(cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("获取 OSS Bucket 失败: %w", err)
	}

	return &OSSStorage{
		client:       client,
		bucket:       bucket,
		endpoint:     cfg.Endpoint,
		bucketName:   cfg.Bucket,
		baseURL:      cfg.BaseURL,
		policy:       cfg.Upload,
		maxReadBytes: resolveMaxRead(cfg.MaxReadBytes),
	}, nil
}

// Upload 上传文件到 OSS
func (s *OSSStorage) Upload(file *multipart.FileHeader, subdir string) (string, error) {
	if file == nil {
		return "", ErrInvalidFile
	}
	// 上传安全策略校验（C4b）
	if err := validateUploadSize(s.policy, file.Size); err != nil {
		return "", err
	}
	if err := validateUploadExt(s.policy, file.Filename); err != nil {
		return "", err
	}

	// 生成存储路径: /年/月/日/文件名
	now := time.Now()
	datePath := fmt.Sprintf("%d/%02d/%02d", now.Year(), now.Month(), now.Day())
	ext := filepath.Ext(file.Filename)
	// OSS object key 规范用正斜杠；用 path.Join（POSIX）而非 filepath.Join，
	// 避免 Windows 产反斜杠导致跨平台 key 不一致。
	rawKey := path.Join(filepath.ToSlash(subdir), datePath, uniqueFilename(now, ext))
	// 净化 object key，拒绝含 `..` 的 subdir（C4a key 注入）
	objectKey, err := sanitizeObjectKey(rawKey)
	if err != nil {
		logger.Warn("OSS 上传 key 被拒绝", zap.String("subdir", subdir), zap.Error(err))
		return "", err
	}

	// 打开源文件
	src, err := file.Open()
	if err != nil {
		return "", fmt.Errorf("打开文件失败: %w", err)
	}
	defer src.Close()

	// MIME 嗅探（如配置）
	var srcReader io.Reader = src
	srcReader, err = sniffUploadMIME(s.policy, srcReader)
	if err != nil {
		return "", err
	}

	// 上传到 OSS。按策略实测封顶（P0）：超限时 PutObject 读到 ErrUploadTooLarge 而失败，
	// 简单 PutObject 语义下对象不会被提交，无需额外清理。
	if err := s.bucket.PutObject(objectKey, enforceUploadSize(s.policy, srcReader)); err != nil {
		logger.Error("OSS 上传失败", zap.Error(err), zap.String("key", objectKey))
		return "", fmt.Errorf("OSS 上传失败: %w", err)
	}

	logger.Info("OSS 文件上传成功", zap.String("key", objectKey))
	return objectKey, nil
}

// UploadFromBytes 从字节数组上传文件到 OSS
func (s *OSSStorage) UploadFromBytes(data []byte, filename, subdir string) (string, error) {
	// 上传安全策略校验（C4b）
	if err := validateUploadSize(s.policy, int64(len(data))); err != nil {
		return "", err
	}
	if err := validateUploadExt(s.policy, filename); err != nil {
		return "", err
	}

	now := time.Now()
	datePath := fmt.Sprintf("%d/%02d/%02d", now.Year(), now.Month(), now.Day())
	ext := filepath.Ext(filename)
	if ext == "" {
		ext = ".bin"
	}
	// OSS object key 规范用正斜杠；用 path.Join（POSIX）而非 filepath.Join，
	// 避免 Windows 产反斜杠导致跨平台 key 不一致。
	rawKey := path.Join(filepath.ToSlash(subdir), datePath, uniqueFilename(now, ext))
	objectKey, err := sanitizeObjectKey(rawKey)
	if err != nil {
		logger.Warn("OSS 上传 key 被拒绝", zap.String("subdir", subdir), zap.Error(err))
		return "", err
	}

	// MIME 嗅探（如配置）
	var srcReader io.Reader = bytes.NewReader(data)
	srcReader, err = sniffUploadMIME(s.policy, srcReader)
	if err != nil {
		return "", err
	}

	// 上传到 OSS
	if err := s.bucket.PutObject(objectKey, srcReader); err != nil {
		logger.Error("OSS 上传失败", zap.Error(err), zap.String("key", objectKey))
		return "", fmt.Errorf("OSS 上传失败: %w", err)
	}

	logger.Info("OSS 文件上传成功", zap.String("key", objectKey))
	return objectKey, nil
}

// GetURL 获取文件访问 URL
func (s *OSSStorage) GetURL(path string) string {
	cleanKey, err := sanitizeObjectKey(path)
	if err != nil {
		return ""
	}
	if s.baseURL != "" {
		return strings.TrimRight(s.baseURL, "/") + "/" + cleanKey
	}
	return fmt.Sprintf("https://%s.%s/%s", s.bucketName, s.endpoint, cleanKey)
}

// GetSignedURL 获取带签名的临时访问 URL（用于私有文件）
func (s *OSSStorage) GetSignedURL(path string, expire time.Duration) (string, error) {
	key, err := sanitizeObjectKey(path)
	if err != nil {
		return "", err
	}
	return s.bucket.SignURL(key, oss.HTTPGet, int64(expire.Seconds()))
}

// Delete 删除 OSS 文件
func (s *OSSStorage) Delete(p string) error {
	key, err := sanitizeObjectKey(p)
	if err != nil {
		return err
	}
	if err := s.bucket.DeleteObject(key); err != nil {
		logger.Error("OSS 删除失败", zap.Error(err), zap.String("key", key))
		return fmt.Errorf("OSS 删除失败: %w", err)
	}
	logger.Info("OSS 文件删除成功", zap.String("key", key))
	return nil
}

// Get 获取 OSS 文件内容。读取受 maxReadBytes 封顶，防止 OOM（C4c）。
func (s *OSSStorage) Get(p string) ([]byte, error) {
	key, err := sanitizeObjectKey(p)
	if err != nil {
		return nil, err
	}
	body, err := s.bucket.GetObject(key)
	if err != nil {
		logger.Error("OSS 读取失败", zap.Error(err), zap.String("key", key))
		return nil, fmt.Errorf("OSS 读取失败: %w", err)
	}
	defer body.Close()

	var reader io.Reader = body
	if s.maxReadBytes > 0 {
		reader = io.LimitReader(body, s.maxReadBytes+1)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("读取 OSS 文件内容失败: %w", err)
	}
	if s.maxReadBytes > 0 && int64(len(data)) > s.maxReadBytes {
		return nil, fmt.Errorf("文件超过最大读取限制 %d 字节: %w", s.maxReadBytes, ErrInvalidPath)
	}
	return data, nil
}

// Exists 检查 OSS 文件是否存在
func (s *OSSStorage) Exists(p string) bool {
	key, err := sanitizeObjectKey(p)
	if err != nil {
		return false
	}
	_, err = s.bucket.GetObjectMeta(key)
	return err == nil
}

// StorageManager 存储管理器（#10）。照 database.Manager 模式：
// 实例化 + DefaultStorage 全局默认 + 包级 facade 代理，支持多实例与测试注入。
type StorageManager struct {
	mu      sync.Mutex
	cfg     *config.StorageConfig
	current Storage
}

// DefaultStorage 默认存储管理器，包级 facade 代理到它。
//
// 主线A 修复：改用 atomic.Pointer 保护读写，消除原裸指针置换（SetDefaultStorageManager）
// 与 facade 无锁读之间的数据竞争，与 config.defaultManager / database.DefaultRedis 对齐。
// 类型由 *StorageManager 变更为 atomic.Pointer[StorageManager]（breaking：下游若直接
// 调用 DefaultStorage.Init 等方法需改用 Init 等 facade，或 DefaultStorage.Load().Init）。
var DefaultStorage atomic.Pointer[StorageManager]

func init() {
	DefaultStorage.Store(NewStorageManager())
}

// NewStorageManager 创建存储管理器实例。
func NewStorageManager() *StorageManager { return &StorageManager{} }

// SetDefaultStorageManager 提升指定 StorageManager 为全局默认。并发安全（atomic.Store）。
func SetDefaultStorageManager(m *StorageManager) {
	if m != nil {
		DefaultStorage.Store(m)
	}
}

// Init 初始化存储
func (m *StorageManager) Init(cfg *config.StorageConfig) error {
	if cfg == nil {
		return ErrStorageNotInitialized
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	var s Storage
	switch cfg.Driver {
	case "local":
		s = NewLocalStorage(&cfg.Local)
		logger.Info("使用本地存储", zap.String("path", cfg.Local.Path))
	case "oss":
		ossStorage, err := NewOSSStorage(&cfg.OSS)
		if err != nil {
			return err
		}
		s = ossStorage
		logger.Info("使用 OSS 存储", zap.String("bucket", cfg.OSS.Bucket))
	default:
		return fmt.Errorf("不支持的存储驱动: %s", cfg.Driver)
	}

	m.cfg = cfg
	m.current = s
	return nil
}

// Get 返回当前存储实例。
func (m *StorageManager) Get() Storage {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.current
}

// Set 设置存储实例（用于注入 mock 或自定义实现）。
func (m *StorageManager) Set(s Storage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.current = s
}

// --- 包级 facade（代理到 DefaultStorage，兼容存量） ---

// Init 初始化存储
func Init(cfg *config.StorageConfig) error {
	return DefaultStorage.Load().Init(cfg)
}

// GetStorage 获取全局存储实例
func GetStorage() Storage {
	return DefaultStorage.Load().Get()
}

// SetStorage 设置全局存储实例
func SetStorage(s Storage) {
	DefaultStorage.Load().Set(s)
}

// Upload 上传文件
func Upload(file *multipart.FileHeader, subdir string) (string, error) {
	s := GetStorage()
	if s == nil {
		return "", ErrStorageNotInitialized
	}
	return s.Upload(file, subdir)
}

// UploadFromBytes 从字节数组上传文件
func UploadFromBytes(data []byte, filename, subdir string) (string, error) {
	s := GetStorage()
	if s == nil {
		return "", ErrStorageNotInitialized
	}
	return s.UploadFromBytes(data, filename, subdir)
}

// GetURL 获取文件访问 URL
func GetURL(path string) string {
	s := GetStorage()
	if s == nil {
		return ""
	}
	return s.GetURL(path)
}

// Delete 删除文件
func Delete(path string) error {
	s := GetStorage()
	if s == nil {
		return ErrStorageNotInitialized
	}
	return s.Delete(path)
}

// Get 获取文件内容
func Get(path string) ([]byte, error) {
	s := GetStorage()
	if s == nil {
		return nil, ErrStorageNotInitialized
	}
	return s.Get(path)
}

// Exists 检查文件是否存在
func Exists(path string) bool {
	s := GetStorage()
	if s == nil {
		return false
	}
	return s.Exists(path)
}
