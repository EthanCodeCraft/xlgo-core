package storage

import (
	"strings"
	"testing"
)

// 回归 C4a：OSS object key 净化。拒绝空、NUL、含 `..`、绝对路径，防 key 注入与越权访问。
func TestSanitizeObjectKey(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"empty", "", true},
		{"dotdot only", "..", true},
		{"dotdot segment", "a/../b", true},
		{"dotdot prefix", "../etc/passwd", true},
		{"absolute", "/abs/path", true},
		{"nul byte", "a\x00b", true},
		{"normal", "images/2026/01/01/x.jpg", false},
		{"normal no ext", "docs/report", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := sanitizeObjectKey(c.in)
			if c.wantErr {
				if err == nil {
					t.Errorf("sanitizeObjectKey(%q) = %q, want error", c.in, got)
				}
				return
			}
			if err != nil {
				t.Errorf("sanitizeObjectKey(%q) unexpected err: %v", c.in, err)
			}
		})
	}
}

// 回归 HIGH（OSS key 跨平台）：Windows 反斜杠必须归一化为正斜杠，
// 否则 Windows 开发 / Linux 生产部署 OSS key 不一致、DB 迁移后取不到文件。
func TestSanitizeObjectKeyNormalizesBackslash(t *testing.T) {
	got, err := sanitizeObjectKey("avatars\\2026\\06\\28\\x.jpg")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if strings.Contains(got, "\\") {
		t.Errorf("sanitizeObjectKey left backslash: %q", got)
	}
	if want := "avatars/2026/06/28/x.jpg"; got != want {
		t.Errorf("sanitizeObjectKey backslash normalize = %q, want %q", got, want)
	}
}

// 回归 C4c：resolveMaxRead 语义。n<0 不限，n==0 默认，n>0 用 n。
func TestResolveMaxRead(t *testing.T) {
	if got := resolveMaxRead(-1); got != -1 {
		t.Errorf("resolveMaxRead(-1) = %d, want -1 (unlimited)", got)
	}
	if got := resolveMaxRead(0); got != defaultMaxReadBytes {
		t.Errorf("resolveMaxRead(0) = %d, want default %d", got, defaultMaxReadBytes)
	}
	if got := resolveMaxRead(2048); got != 2048 {
		t.Errorf("resolveMaxRead(2048) = %d, want 2048", got)
	}
}
