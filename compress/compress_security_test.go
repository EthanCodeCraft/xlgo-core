package compress_test

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EthanCodeCraft/xlgo-core/compress"
)

// makeZipAt 构造一个 zip 文件，entries 为 name -> content；dir 条目用空 content 且 IsDir=true。
func makeZipAt(t *testing.T, zipPath string, entries []struct {
	name    string
	content string
	isDir   bool
}) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(zipPath), 0750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for _, e := range entries {
		hdr := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		if e.isDir {
			hdr.SetMode(os.ModeDir | 0750)
		}
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			t.Fatalf("CreateHeader %s: %v", e.name, err)
		}
		if !e.isDir {
			if _, err := w.Write([]byte(e.content)); err != nil {
				t.Fatalf("write %s: %v", e.name, err)
			}
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}
}

// ===== C5a：Zip-Slip =====

// 回归 C5a：zip 条目名含 `..` 逃逸目标目录必须被拒绝，且不在 dst 外创建/覆盖文件。
func TestUnzipZipSlipRejected(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "evil.zip")
	// 在 dst 的父目录放一个蜜罐文件，确保穿越不会覆盖它。
	canary := filepath.Join(dir, "canary.txt")
	if err := os.WriteFile(canary, []byte("original"), 0644); err != nil {
		t.Fatalf("write canary: %v", err)
	}

	dst := filepath.Join(dir, "out")
	makeZipAt(t, zipPath, []struct {
		name    string
		content string
		isDir   bool
	}{
		{name: "../canary.txt", content: "pwned"},
	})

	err := compress.Unzip(zipPath, dst)
	if !errors.Is(err, compress.ErrPathTraversal) {
		t.Errorf("Unzip with ../ entry err = %v, want ErrPathTraversal", err)
	}
	// 蜜罐文件必须未被覆盖。
	data, err := os.ReadFile(canary)
	if err != nil {
		t.Fatalf("read canary: %v", err)
	}
	if string(data) != "original" {
		t.Errorf("canary overwritten by Zip-Slip: got %q", string(data))
	}
}

// 回归 C5a：绝对路径条目必须被拒绝。
func TestUnzipAbsolutePathRejected(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "abs.zip")
	dst := filepath.Join(dir, "out")
	makeZipAt(t, zipPath, []struct {
		name    string
		content string
		isDir   bool
	}{
		{name: "/etc/evil.txt", content: "x"},
	})
	if err := compress.Unzip(zipPath, dst); !errors.Is(err, compress.ErrPathTraversal) {
		t.Errorf("Unzip absolute path err = %v, want ErrPathTraversal", err)
	}
}

// 回归 C5a：合法相对路径条目不误伤（含子目录）。
func TestUnzipNormalEntriesWork(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "ok.zip")
	dst := filepath.Join(dir, "out")
	makeZipAt(t, zipPath, []struct {
		name    string
		content string
		isDir   bool
	}{
		{name: "sub/", isDir: true},
		{name: "sub/a.txt", content: "hello"},
		{name: "top.txt", content: "world"},
	})
	if err := compress.Unzip(zipPath, dst); err != nil {
		t.Fatalf("Unzip normal: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(dst, "sub", "a.txt")); err != nil || string(b) != "hello" {
		t.Errorf("sub/a.txt = %q, err=%v, want 'hello'", string(b), err)
	}
	if b, err := os.ReadFile(filepath.Join(dst, "top.txt")); err != nil || string(b) != "world" {
		t.Errorf("top.txt = %q, err=%v, want 'world'", string(b), err)
	}
}

// 回归 C5a：符号链接条目必须被拒绝（防经软链二次穿越）。
func TestUnzipSymlinkRejected(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "symlink.zip")
	dst := filepath.Join(dir, "out")
	makeZipAt(t, zipPath, []struct {
		name    string
		content string
		isDir   bool
	}{
		{name: "lnk", content: "/etc/passwd"}, // content 为链接目标，mode 设为 symlink
	})
	// 把条目 mode 改成符号链接：重建 zip 时设置 ModeSymlink。
	// makeZipAt 不支持 symlink mode，这里直接用底层 API 重建。
	zipPath2 := filepath.Join(dir, "symlink2.zip")
	f, err := os.Create(zipPath2)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	hdr := &zip.FileHeader{Name: "lnk", Method: zip.Deflate}
	hdr.SetMode(os.ModeSymlink | 0777)
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		t.Fatalf("CreateHeader: %v", err)
	}
	if _, err := w.Write([]byte("/etc/passwd")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if err := compress.Unzip(zipPath2, dst); !errors.Is(err, compress.ErrSymlinkEntry) {
		t.Errorf("Unzip symlink err = %v, want ErrSymlinkEntry", err)
	}
}

// ===== C5b：解压炸弹 =====

// 回归 C5b：GzipDecompress 超限返回错误而非 OOM。
// 用显式小上限验证限流代码路径（与默认 100MB 走同一 io.LimitReader + 超限判定逻辑）。
func TestGzipDecompressBombLimit(t *testing.T) {
	// 2MB 解压后数据。
	big := bytes.Repeat([]byte("A"), 2*1024*1024)
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(big); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	compressed := buf.Bytes()

	// 显式上限 1MB → 必须拒绝（2MB 超限）。
	if _, err := compress.GzipDecompressWithOptions(compressed, compress.DecompressOptions{MaxBytes: 1 * 1024 * 1024}); !errors.Is(err, compress.ErrDecompressLimit) {
		t.Errorf("GzipDecompress over-limit err = %v, want ErrDecompressLimit", err)
	}

	// 显式 -1 不限 → 成功解压完整 2MB。
	out, err := compress.GzipDecompressWithOptions(compressed, compress.DecompressOptions{MaxBytes: -1})
	if err != nil {
		t.Errorf("GzipDecompress unlimited err = %v", err)
	}
	if len(out) != len(big) {
		t.Errorf("unlimited decompressed len = %d, want %d", len(out), len(big))
	}

	// 默认上限（100MB）放行 2MB 正常数据。
	if _, err := compress.GzipDecompress(compressed); err != nil {
		t.Errorf("GzipDecompress default err = %v (2MB should pass default 100MB limit)", err)
	}
}

// 回归 C5b：Unzip 单条目上限——超大条目被拒。
func TestUnzipEntryBombLimit(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "bomb.zip")
	dst := filepath.Join(dir, "out")

	// 构造一个含 2MB（解压后）条目的 zip。
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	w, err := zw.Create("big.txt")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := w.Write(bytes.Repeat([]byte("A"), 2 * 1024 * 1024)); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// 显式单条目上限 1MB → 必须拒绝。
	opts := compress.DecompressOptions{MaxBytes: 1 * 1024 * 1024}
	if err := compress.UnzipWithOptions(zipPath, dst, opts); !errors.Is(err, compress.ErrDecompressLimit) {
		t.Errorf("Unzip entry bomb err = %v, want ErrDecompressLimit", err)
	}
}

// 回归 C5b：Unzip 累计上限——多个条目累计超限被拒。
func TestUnzipTotalBombLimit(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "many.zip")
	dst := filepath.Join(dir, "out")

	// 5 个 1MB 条目 = 累计 5MB；单条目上限 2MB（不超），累计上限 3MB → 第 4 个累计 4MB 超限。
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	chunk := bytes.Repeat([]byte("B"), 1*1024*1024)
	for i := 0; i < 5; i++ {
		w, err := zw.Create("file" + string(rune('0'+i)) + ".dat")
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if _, err := w.Write(chunk); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	opts := compress.DecompressOptions{MaxBytes: 2 * 1024 * 1024, MaxTotalBytes: 3 * 1024 * 1024}
	if err := compress.UnzipWithOptions(zipPath, dst, opts); !errors.Is(err, compress.ErrDecompressLimit) {
		t.Errorf("Unzip total bomb err = %v, want ErrDecompressLimit", err)
	}
}

// 回归 C5b：GzipDecompressFile 单流封顶——超限返回错误而非磁盘耗尽。
func TestGzipDecompressFileBombLimit(t *testing.T) {
	dir := t.TempDir()
	// 2MB 解压后数据写入 gzip 文件。
	big := bytes.Repeat([]byte("A"), 2*1024*1024)
	srcGz := filepath.Join(dir, "src.gz")
	{
		f, err := os.Create(srcGz)
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		gz := gzip.NewWriter(f)
		if _, err := gz.Write(big); err != nil {
			t.Fatalf("write: %v", err)
		}
		if err := gz.Close(); err != nil {
			t.Fatalf("close gz: %v", err)
		}
		f.Close()
	}

	// 显式上限 1MB → 必须拒绝（2MB 超限）。
	dst1 := filepath.Join(dir, "out1.txt")
	if err := compress.GzipDecompressFileWithOptions(srcGz, dst1, compress.DecompressOptions{MaxBytes: 1 * 1024 * 1024}); !errors.Is(err, compress.ErrDecompressLimit) {
		t.Errorf("GzipDecompressFile over-limit err = %v, want ErrDecompressLimit", err)
	}

	// 显式 -1 不限 → 成功解压完整 2MB。
	dst2 := filepath.Join(dir, "out2.txt")
	if err := compress.GzipDecompressFileWithOptions(srcGz, dst2, compress.DecompressOptions{MaxBytes: -1}); err != nil {
		t.Errorf("GzipDecompressFile unlimited err = %v", err)
	}
	b, err := os.ReadFile(dst2)
	if err != nil {
		t.Fatalf("read out: %v", err)
	}
	if len(b) != len(big) {
		t.Errorf("unlimited out len = %d, want %d", len(b), len(big))
	}

	// 默认上限（100MB）放行 2MB。
	dst3 := filepath.Join(dir, "out3.txt")
	if err := compress.GzipDecompressFile(srcGz, dst3); err != nil {
		t.Errorf("GzipDecompressFile default err = %v (2MB should pass default 100MB limit)", err)
	}
}

// 兼容性回归：原有 Zip/Unzip 闭环（合法归档）在默认防护下仍正常。
func TestZipUnzipRoundTripStillWorks(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	content := "round trip content"
	if err := os.WriteFile(src, []byte(content), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	zipPath := filepath.Join(dir, "rt.zip")
	if err := compress.Zip(zipPath, []string{src}); err != nil {
		t.Fatalf("Zip: %v", err)
	}
	dst := filepath.Join(dir, "out")
	if err := compress.Unzip(zipPath, dst); err != nil {
		t.Fatalf("Unzip: %v", err)
	}
	// Zip 用 filepath.Rel(src 的父目录, src) = "src.txt"。
	b, err := os.ReadFile(filepath.Join(dst, "src.txt"))
	if err != nil {
		// 目录结构可能因平台分隔符略有差异，尝试找 src.txt。
		_ = filepath.Walk(dst, func(p string, _ os.FileInfo, _ error) error {
			if strings.HasSuffix(p, "src.txt") {
				b, err = os.ReadFile(p)
				return filepath.SkipDir
			}
			return nil
		})
	}
	if string(b) != content {
		t.Errorf("round trip = %q, want %q", string(b), content)
	}
}
