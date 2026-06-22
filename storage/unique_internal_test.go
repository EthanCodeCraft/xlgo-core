package storage

import (
	"strings"
	"testing"
	"time"
)

func TestUniqueFilename(t *testing.T) {
	now := time.Now()

	name := uniqueFilename(now, ".jpg")
	if !strings.HasSuffix(name, ".jpg") {
		t.Errorf("uniqueFilename should preserve extension, got %q", name)
	}
	if !strings.Contains(name, "-") {
		t.Errorf("uniqueFilename should contain random separator, got %q", name)
	}

	// 同一时刻多次生成应彼此不同（随机后缀防冲突）
	seen := make(map[string]struct{}, 100)
	for i := 0; i < 100; i++ {
		seen[uniqueFilename(now, ".jpg")] = struct{}{}
	}
	if len(seen) < 99 {
		t.Errorf("expected near-100%% uniqueness, got %d distinct out of 100", len(seen))
	}

	// 空扩展名也应可用
	nameNoExt := uniqueFilename(now, "")
	if nameNoExt == "" {
		t.Error("uniqueFilename should return non-empty with empty ext")
	}
}
