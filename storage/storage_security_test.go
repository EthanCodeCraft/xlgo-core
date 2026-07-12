package storage_test

import (
	"bytes"
	"errors"
	"mime/multipart"
	"os"
	"path/filepath"
	"testing"

	"github.com/EthanCodeCraft/xlgo-core/config"
	"github.com/EthanCodeCraft/xlgo-core/storage"
)

// makeFileHeader 用 multipart 真实构造一个 *multipart.FileHeader，content 为文件内容。
func makeFileHeader(t *testing.T, field, filename, contentType string, content []byte) *multipart.FileHeader {
	t.Helper()
	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	h := make(map[string][]string)
	h["Content-Disposition"] = []string{`form-data; name="` + field + `"; filename="` + filename + `"`}
	h["Content-Type"] = []string{contentType}
	part, err := w.CreatePart(h)
	if err != nil {
		t.Fatalf("CreatePart: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("part.Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("writer.Close: %v", err)
	}
	r := multipart.NewReader(body, w.Boundary())
	form, err := r.ReadForm(int64(len(content) + 1024))
	if err != nil {
		t.Fatalf("ReadForm: %v", err)
	}
	defer form.RemoveAll()
	if len(form.File[field]) == 0 {
		t.Fatalf("no file field %q in form", field)
	}
	return form.File[field][0]
}

func newLocalStorageWithPolicy(t *testing.T, policy config.UploadPolicy, maxRead int64) *storage.LocalStorage {
	t.Helper()
	dir := t.TempDir()
	return storage.NewLocalStorage(&config.LocalStorageConfig{
		Path:         dir,
		BaseURL:      "http://localhost/uploads",
		Upload:       policy,
		MaxReadBytes: maxRead,
	})
}

// ===== C4a：路径穿越 =====

// 回归 C4a：Delete/Get/Exists 的 `..` 路径必须被拒绝，不能触碰根目录之外的文件。
func TestLocalStoragePathTraversal(t *testing.T) {
	dir := t.TempDir()
	s := storage.NewLocalStorage(&config.LocalStorageConfig{Path: dir, BaseURL: "http://localhost/uploads"})

	// 在 TempDir 之外放一个蜜罐文件，确保穿越不会删/读它。
	sibling := filepath.Join(filepath.Dir(dir), "xlgo_c4_canary_"+filepath.Base(dir)+".txt")
	if err := os.WriteFile(sibling, []byte("canary"), 0644); err != nil {
		t.Fatalf("write canary: %v", err)
	}
	defer os.Remove(sibling)

	// 用 `..` 指向 canary（dir 的父目录下）。
	escapeRel := "../" + filepath.Base(sibling)

	// Delete 必须拒绝
	if err := s.Delete(escapeRel); !errors.Is(err, storage.ErrPathTraversal) {
		t.Errorf("Delete(%q) err = %v, want ErrPathTraversal", escapeRel, err)
	}
	// canary 必须仍然存在（未被删除）
	if _, err := os.Stat(sibling); err != nil {
		t.Errorf("canary file was deleted by traversal Delete: %v", err)
	}

	// Get 必须拒绝
	if _, err := s.Get(escapeRel); !errors.Is(err, storage.ErrPathTraversal) {
		t.Errorf("Get(%q) err = %v, want ErrPathTraversal", escapeRel, err)
	}

	// Exists 必须返回 (false, ErrPathTraversal)（而非穿越探测到 canary）
	if ok, err := s.Exists(escapeRel); ok || !errors.Is(err, storage.ErrPathTraversal) {
		t.Errorf("Exists(%q) = (%v, %v), want (false, ErrPathTraversal)", escapeRel, ok, err)
	}

	// 绝对路径也必须拒绝
	abs := sibling
	if err := s.Delete(abs); !errors.Is(err, storage.ErrPathTraversal) {
		t.Errorf("Delete(absolute %q) err = %v, want ErrPathTraversal", abs, err)
	}
}

// 回归 C4a：正常相对路径不受误伤（合法用法回归）。
func TestLocalStorageNormalPathStillWorks(t *testing.T) {
	dir := t.TempDir()
	s := storage.NewLocalStorage(&config.LocalStorageConfig{Path: dir, BaseURL: "http://localhost/uploads"})

	// 直接在 root 下放一个文件，用正常相对路径访问。
	target := filepath.Join(dir, "sub", "ok.txt")
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(target, []byte("hello"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	rel := filepath.ToSlash(filepath.Join("sub", "ok.txt"))
	if ok, err := s.Exists(rel); err != nil || !ok {
		t.Errorf("Exists(normal) = (%v, %v), want (true, nil)", ok, err)
	}
	data, err := s.Get(rel)
	if err != nil {
		t.Errorf("Get(normal) err = %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("Get(normal) = %q, want 'hello'", string(data))
	}
	if err := s.Delete(rel); err != nil {
		t.Errorf("Delete(normal) err = %v", err)
	}
}

// 回归 C4a：Upload 的 subdir 含 `..` 必须拒绝，且不在根目录外创建文件。
func TestLocalStorageUploadTraversalSubdir(t *testing.T) {
	dir := t.TempDir()
	s := storage.NewLocalStorage(&config.LocalStorageConfig{Path: dir, BaseURL: "http://localhost/uploads"})

	fh := makeFileHeader(t, "file", "ok.txt", "text/plain", []byte("hi"))
	if _, err := s.Upload(fh, "../evil"); !errors.Is(err, storage.ErrPathTraversal) {
		t.Errorf("Upload subdir ../ err = %v, want ErrPathTraversal", err)
	}
	// 根目录之外不应出现 evil 目录
	evilDir := filepath.Join(filepath.Dir(dir), "evil")
	if _, err := os.Stat(evilDir); err == nil {
		t.Errorf("traversal Upload created dir outside root: %s", evilDir)
	}

	// 绝对路径 subdir 也拒绝
	if _, err := s.Upload(fh, dir); !errors.Is(err, storage.ErrPathTraversal) {
		t.Errorf("Upload absolute subdir err = %v, want ErrPathTraversal", err)
	}
}

// 回归 C4a：UploadFromBytes 的 subdir 含 `..` 必须拒绝。
func TestLocalStorageUploadFromBytesTraversalSubdir(t *testing.T) {
	dir := t.TempDir()
	s := storage.NewLocalStorage(&config.LocalStorageConfig{Path: dir, BaseURL: "http://localhost/uploads"})

	if _, err := s.UploadFromBytes([]byte("hi"), "ok.txt", "../../etc"); !errors.Is(err, storage.ErrPathTraversal) {
		t.Errorf("UploadFromBytes subdir ../../etc err = %v, want ErrPathTraversal", err)
	}
}

// ===== C4c：Get 读封顶 =====

// 回归 C4c：MaxReadBytes 封顶，超限文件 Get 返回错误，不再 OOM。
func TestLocalStorageGetReadLimit(t *testing.T) {
	s := newLocalStorageWithPolicy(t, config.UploadPolicy{}, 10) // 限制 10 字节

	// MaxReadBytes=10 的实例不限上传；上传 20 字节后 Get 应被封顶拒绝。
	content := []byte("0123456789ABCDEFGHIJ") // 20 字节
	fh := makeFileHeader(t, "file", "big.txt", "text/plain", content)
	rel, err := s.Upload(fh, "docs")
	if err != nil {
		t.Fatalf("Upload big file: %v", err)
	}
	if _, err := s.Get(rel); !errors.Is(err, storage.ErrReadTooLarge) {
		t.Errorf("Get over-limit err = %v, want ErrReadTooLarge", err)
	}

	// 小文件应正常读取。
	small := newLocalStorageWithPolicy(t, config.UploadPolicy{}, 100)
	fh2 := makeFileHeader(t, "file", "small.txt", "text/plain", []byte("small"))
	rel2, err := small.Upload(fh2, "docs")
	if err != nil {
		t.Fatalf("Upload small: %v", err)
	}
	data, err := small.Get(rel2)
	if err != nil {
		t.Errorf("Get small err = %v", err)
	}
	if string(data) != "small" {
		t.Errorf("Get small = %q, want 'small'", string(data))
	}
}

// ===== C4b：上传策略 =====

// 回归 C4b：MaxSizeBytes 超限拒绝。
func TestLocalStorageUploadSizeLimit(t *testing.T) {
	s := newLocalStorageWithPolicy(t, config.UploadPolicy{MaxSizeBytes: 5}, 0)
	fh := makeFileHeader(t, "file", "big.txt", "text/plain", []byte("0123456789")) // 10 字节
	if _, err := s.Upload(fh, "docs"); !errors.Is(err, storage.ErrUploadTooLarge) {
		t.Errorf("Upload over size err = %v, want ErrUploadTooLarge", err)
	}
}

// 回归 C4b：AllowedExts 白名单——不允许的扩展名拒绝，允许的通过。
func TestLocalStorageUploadExtWhitelist(t *testing.T) {
	s := newLocalStorageWithPolicy(t, config.UploadPolicy{AllowedExts: []string{".jpg"}}, 0)

	// evil.php 拒绝
	fh := makeFileHeader(t, "file", "evil.php", "text/plain", []byte("<?php"))
	if _, err := s.Upload(fh, "docs"); !errors.Is(err, storage.ErrInvalidPath) {
		t.Errorf("Upload evil.php err = %v, want ErrInvalidPath", err)
	}

	// ok.jpg 通过
	fh2 := makeFileHeader(t, "file", "ok.jpg", "image/jpeg", []byte("\xff\xd8\xff\xe0"))
	if _, err := s.Upload(fh2, "docs"); err != nil {
		t.Errorf("Upload ok.jpg err = %v", err)
	}
}

// 回归 C4b：AllowedMIMEs 嗅探——扩展名伪装但内容不符的拒绝。
func TestLocalStorageUploadMIMESniff(t *testing.T) {
	s := newLocalStorageWithPolicy(t, config.UploadPolicy{AllowedMIMEs: []string{"image/jpeg"}}, 0)

	// 文件名伪装成 jpg，但内容是文本 → 嗅探为 text/plain → 拒绝
	fh := makeFileHeader(t, "file", "fake.jpg", "image/jpeg", []byte("not an image at all"))
	if _, err := s.Upload(fh, "docs"); !errors.Is(err, storage.ErrInvalidPath) {
		t.Errorf("Upload fake.jpg (text content) err = %v, want ErrInvalidPath", err)
	}

	// 真实 jpeg 头 → 通过
	jpeg := []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 'J', 'F', 'I', 'F'}
	fh2 := makeFileHeader(t, "file", "real.jpg", "image/jpeg", jpeg)
	if _, err := s.Upload(fh2, "docs"); err != nil {
		t.Errorf("Upload real.jpg err = %v", err)
	}
}

// 回归 C4b：UploadFromBytes 同样受策略约束（大小 / 扩展名 / MIME）。
func TestLocalStorageUploadFromBytesPolicy(t *testing.T) {
	s := newLocalStorageWithPolicy(t, config.UploadPolicy{
		MaxSizeBytes: 100,
		AllowedExts:  []string{".txt"},
		AllowedMIMEs: []string{"text/plain"},
	}, 0)

	// 超限
	if _, err := s.UploadFromBytes(make([]byte, 200), "ok.txt", "docs"); !errors.Is(err, storage.ErrUploadTooLarge) {
		t.Errorf("UploadFromBytes over size err = %v, want ErrUploadTooLarge", err)
	}
	// 扩展名不符
	if _, err := s.UploadFromBytes([]byte("hi"), "evil.php", "docs"); !errors.Is(err, storage.ErrInvalidPath) {
		t.Errorf("UploadFromBytes evil.php err = %v, want ErrInvalidPath", err)
	}
	// MIME 不符（内容是 jpeg 头，但只允许 text/plain）
	if _, err := s.UploadFromBytes([]byte{0xff, 0xd8, 0xff, 0xe0}, "ok.txt", "docs"); !errors.Is(err, storage.ErrInvalidPath) {
		t.Errorf("UploadFromBytes wrong MIME err = %v, want ErrInvalidPath", err)
	}
	// 合法通过
	if _, err := s.UploadFromBytes([]byte("hello"), "ok.txt", "docs"); err != nil {
		t.Errorf("UploadFromBytes ok err = %v", err)
	}
}

// ===== P0：上传大小拷贝阶段实测封顶（客户端 Size 不可信）=====

// 回归 P0：客户端谎报 file.Size 绕过前置校验时，拷贝阶段按实际字节封顶必须拦截，
// 返回 ErrUploadTooLarge，防止声明小体积却流式发送大 body 撑爆磁盘。
func TestLocalStorageUploadSizeEnforcedOnCopy(t *testing.T) {
	s := newLocalStorageWithPolicy(t, config.UploadPolicy{MaxSizeBytes: 5}, 0)

	content := make([]byte, 100) // 真实 100 字节
	fh := makeFileHeader(t, "file", "big.bin", "application/octet-stream", content)
	fh.Size = 1 // 谎报大小，绕过前置 validateUploadSize

	if _, err := s.Upload(fh, "docs"); !errors.Is(err, storage.ErrUploadTooLarge) {
		t.Errorf("Upload with lied Size err = %v, want ErrUploadTooLarge", err)
	}
}

// 回归 P0：实际大小恰好等于上限应通过（边界不误伤）。
func TestLocalStorageUploadSizeBoundaryExact(t *testing.T) {
	s := newLocalStorageWithPolicy(t, config.UploadPolicy{MaxSizeBytes: 8}, 0)
	fh := makeFileHeader(t, "file", "ok.bin", "application/octet-stream", []byte("12345678")) // 恰好 8 字节
	if _, err := s.Upload(fh, "docs"); err != nil {
		t.Errorf("Upload exactly-at-limit err = %v, want success", err)
	}
}

// 兼容性回归：零值 UploadPolicy（默认配置）不上传限制，正常文件通过。
func TestLocalStorageZeroPolicyAllowsAll(t *testing.T) {
	s := newLocalStorageWithPolicy(t, config.UploadPolicy{}, 0)
	fh := makeFileHeader(t, "file", "any.bin", "application/octet-stream", []byte("whatever"))
	if _, err := s.Upload(fh, "docs"); err != nil {
		t.Errorf("Upload with zero policy err = %v (must remain unrestricted for backward compat)", err)
	}
}

func TestLocalStorageNilConfigFailsClosed(t *testing.T) {
	s := storage.NewLocalStorage(nil)
	if _, err := s.Get("x.txt"); !errors.Is(err, storage.ErrStorageNotInitialized) {
		t.Fatalf("Get with nil config err = %v, want ErrStorageNotInitialized", err)
	}
}

func TestStorageInitNilConfigNoPanic(t *testing.T) {
	if err := storage.NewStorageManager().Init(nil); !errors.Is(err, storage.ErrStorageNotInitialized) {
		t.Fatalf("Init(nil) err = %v, want ErrStorageNotInitialized", err)
	}
}

func TestLocalStorageUploadNilFile(t *testing.T) {
	s := newLocalStorageWithPolicy(t, config.UploadPolicy{}, 0)
	if _, err := s.Upload(nil, "docs"); !errors.Is(err, storage.ErrInvalidFile) {
		t.Fatalf("Upload(nil) err = %v, want ErrInvalidFile", err)
	}
}

func TestLocalStorageRejectsSymlinkRoot(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "target")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	link := filepath.Join(parent, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink not available on this system: %v", err)
	}

	s := storage.NewLocalStorage(&config.LocalStorageConfig{Path: link, BaseURL: "http://localhost/uploads"})
	if _, err := s.Get("x.txt"); !errors.Is(err, storage.ErrStorageNotInitialized) {
		t.Fatalf("Get with symlink root err = %v, want ErrStorageNotInitialized", err)
	}
}

func TestLocalStorageGetURLSanitizesPath(t *testing.T) {
	s := newLocalStorageWithPolicy(t, config.UploadPolicy{}, 0)
	if got := s.GetURL("docs//a.txt"); got != "http://localhost/uploads/docs/a.txt" {
		t.Fatalf("GetURL clean path = %q", got)
	}
	if got := s.GetURL("../secret.txt"); got != "" {
		t.Fatalf("GetURL traversal = %q, want empty", got)
	}
}
