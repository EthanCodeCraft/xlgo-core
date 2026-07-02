package ws

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestSameOriginCheck_C7 验证默认 CheckOrigin 同源校验（防 CSWSH）：
// 空 Origin 放行（非浏览器）、同源放行、跨域拒绝。
func TestSameOriginCheck_C7(t *testing.T) {
	cases := []struct {
		name   string
		origin string
		host   string
		want   bool
	}{
		{"empty origin (non-browser)", "", "example.com", true},
		{"same origin", "https://example.com", "example.com", true},
		{"cross origin", "https://evil.com", "example.com", false},
		{"origin with port same", "http://example.com:8080", "example.com:8080", true},
		{"origin with port diff", "http://example.com:8080", "example.com:9090", false},
		{"malformed origin", "://bad", "example.com", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			req.Host = tc.host
			got := sameOriginCheck(req)
			if got != tc.want {
				t.Errorf("sameOriginCheck origin=%q host=%q = %v, want %v", tc.origin, tc.host, got, tc.want)
			}
		})
	}
}

// TestAllowOrigins_C7：AllowOrigins 仅放行白名单 Origin。
func TestAllowOrigins_C7(t *testing.T) {
	check := AllowOrigins("https://a.com", "https://b.com")
	cases := []struct {
		origin string
		want   bool
	}{
		{"", true},                 // 非浏览器放行
		{"https://a.com", true},    // 白名单
		{"https://b.com", true},    // 白名单
		{"https://evil.com", false},
		{"http://a.com", false},    // scheme 不符
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if tc.origin != "" {
			req.Header.Set("Origin", tc.origin)
		}
		if got := check(req); got != tc.want {
			t.Errorf("AllowOrigins origin=%q = %v, want %v", tc.origin, got, tc.want)
		}
	}
}
