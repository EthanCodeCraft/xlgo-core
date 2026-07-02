package middleware

import "testing"

// 回归 C7a：通配符子域匹配必须锚定真实子域边界，拒绝后缀相同但非子域的域名。
// 旧实现 strings.HasSuffix(origin, domain) 接受 notexample.com / evil-example.com。
func TestMatchOriginWildcardBoundary(t *testing.T) {
	cases := []struct {
		name   string
		origin string
		ao     string
		want   bool
	}{
		// 精确匹配
		{"exact", "https://example.com", "https://example.com", true},
		{"exact with port", "https://example.com:8443", "https://example.com:8443", true},
		{"exact mismatch", "https://example.com", "https://other.com", false},

		// 通配子域 *.example.com
		{"subdomain ok", "https://a.example.com", "*.example.com", true},
		{"deep subdomain ok", "https://a.b.example.com", "*.example.com", true},
		{"subdomain with port ok", "https://a.example.com:3000", "*.example.com", true},
		{"apex not matched by wildcard", "https://example.com", "*.example.com", false},
		// C7a 核心：后缀相同但非真实子域必须拒绝
		{"notexample.com rejected", "https://notexample.com", "*.example.com", false},
		{"evil-example.com rejected", "https://evil-example.com", "*.example.com", false},
		{"villainexample.com rejected", "https://villainexample.com", "*.example.com", false},
		// 端口/协议不同的 origin host 仍按 host 判断
		{"http subdomain ok", "http://a.example.com", "*.example.com", true},

		// 通配仅匹配 host，scheme 不同的精确配置不被通配覆盖
		{"different scheme exact not wildcard", "http://example.com", "https://example.com", false},

		// 边界
		{"empty origin", "", "*.example.com", false},
		{"empty ao", "https://a.example.com", "", false},
		{"malformed origin", "://:bad", "*.example.com", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := matchOrigin(c.origin, c.ao)
			if got != c.want {
				t.Errorf("matchOrigin(%q, %q) = %v, want %v", c.origin, c.ao, got, c.want)
			}
		})
	}
}

// 回归 C7a：通配大小写不敏感（host 归一化为小写比较）。
func TestMatchOriginCaseInsensitive(t *testing.T) {
	if !matchOrigin("https://A.Example.COM", "*.example.com") {
		t.Error("matchOrigin should be case-insensitive on host")
	}
}

// 回归 C7a：trailing dot FQDN（a.example.com.）应等同 a.example.com 被接受（功能正确性）。
func TestMatchOriginTrailingDot(t *testing.T) {
	if !matchOrigin("https://a.example.com.", "*.example.com") {
		t.Error("trailing-dot subdomain should match wildcard")
	}
	if !matchOrigin("https://a.example.com", "*.example.com.") {
		t.Error("trailing-dot wildcard should match plain subdomain")
	}
}

// 回归 C7b：isLocalhostOrigin 仅允许 localhost/127.0.0.1/::1，拒绝任意其他来源。
func TestIsLocalhostOrigin(t *testing.T) {
	allowed := []string{
		"http://localhost:3000",
		"http://localhost",
		"http://127.0.0.1:8080",
		"http://127.0.0.1",
		"http://[::1]:4000",
		"http://[::1]",
	}
	for _, o := range allowed {
		if !isLocalhostOrigin(o) {
			t.Errorf("isLocalhostOrigin(%q) = false, want true", o)
		}
	}
	denied := []string{
		"https://evil.com",
		"https://localhost.evil.com", // 后缀含 localhost 但非 localhost host
		"https://notlocalhost.com",
		"https://example.com",
		"",
		"://bad",
	}
	for _, o := range denied {
		if isLocalhostOrigin(o) {
			t.Errorf("isLocalhostOrigin(%q) = true, want false", o)
		}
	}
}
