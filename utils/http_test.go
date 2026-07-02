package utils_test

import (
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/EthanCodeCraft/xlgo-core/utils"
)

// 回归 H2：默认 HTTPClient 必须 校验 TLS（InsecureSkipVerify=false），
// 访问自签证书的 https server 应失败。旧实现 DefaultHTTPClientConfig.SkipTLSVerify=true，
// HTTPGet/Post 默认可被 MITM。
func TestHTTPClientDefaultVerifiesTLS(t *testing.T) {
	// 启动一个使用自签证书的 TLS server（httptest 自动生成证书，未被客户端信任）。
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	// 默认客户端（H2 修复后 SkipTLSVerify=false）应因证书校验失败而报错。
	c := utils.NewHTTPClient()
	if _, err := c.Get(srv.URL, nil); err == nil {
		t.Error("default HTTPClient should fail TLS verification against self-signed cert (H2: was InsecureSkipVerify=true)")
	}
}

// 回归 H2：HTTPGet 包级函数（经 DefaultHTTPClient）默认同样校验 TLS。
func TestHTTPGetDefaultVerifiesTLS(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	if _, err := utils.HTTPGet(srv.URL, nil); err == nil {
		t.Error("HTTPGet should fail TLS verification against self-signed cert by default")
	}
}

// 回归 H2：显式 SetSkipTLS(true) 后可访问自签证书 server（opt-in 跳过校验）。
func TestHTTPClientSkipTLSOptIn(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c := utils.NewHTTPClient()
	c.SetSkipTLS(true)

	body, err := c.Get(srv.URL, nil)
	if err != nil {
		t.Fatalf("Get with SkipTLS=true should succeed against self-signed cert, got: %v", err)
	}
	if string(body) != "ok" {
		t.Errorf("body = %q, want ok", string(body))
	}
}

// 回归 H2：NewHTTPClientWithConfig 显式设 SkipTLSVerify=true 可跳过校验。
func TestHTTPClientWithConfigSkipTLS(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c := utils.NewHTTPClientWithConfig(utils.HTTPClientConfig{
		SkipTLSVerify: true,
	})
	body, err := c.Get(srv.URL, nil)
	if err != nil {
		t.Fatalf("Get with config SkipTLSVerify=true should succeed, got: %v", err)
	}
	if string(body) != "ok" {
		t.Errorf("body = %q, want ok", string(body))
	}
}

// 回归 H2：DefaultHTTPClientConfig.SkipTLSVerify 默认 false。
func TestDefaultHTTPClientConfigNoSkipTLS(t *testing.T) {
	if utils.DefaultHTTPClientConfig.SkipTLSVerify {
		t.Error("DefaultHTTPClientConfig.SkipTLSVerify = true, want false (H2: default must verify TLS)")
	}
}

// 回归 H2：默认 transport 的 TLSClientConfig.InsecureSkipVerify 为 false。
// 直接构造一个对 https 的请求，确认未跳过校验。用 errors 判断是否为 x509 校验类错误。
func TestDefaultClientTransportVerifiesTLS(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c := utils.NewHTTPClient()
	// 用自定义 client 配自定义 transport 不可行（封装），直接调 Get 验证错误类型。
	_, err := c.Get(srv.URL, nil)
	if err == nil {
		t.Fatal("expected TLS verification error, got nil")
	}
	// 错误应包含证书校验相关提示（如 x509 / certificate / tls）。
	if !isTLSError(err) {
		t.Logf("err = %v (not strictly x509 classified, but default did reject — acceptable)", err)
	}
}

// isTLSError 粗略判断错误是否为 TLS 证书校验失败。
func isTLSError(err error) bool {
	if err == nil {
		return false
	}
	var ve *tls.CertificateVerificationError
	return errors.As(err, &ve)
}
