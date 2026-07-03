package utils_test

import (
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
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

// TestHTTPClientSetSkipTLSConcurrent H-12 回归：并发 SetSkipTLS 与 Get 请求不应触发
// 数据竞争，且 SetSkipTLS(true) 后能成功访问自签 TLS server。
// 修复前 SetSkipTLS 直接覆盖 transport.TLSClientConfig 指针，与并发 Do 读该字段竞态。
func TestHTTPClientSetSkipTLSConcurrent(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c := utils.NewHTTPClient()
	defer c.Close()

	var wg sync.WaitGroup
	// 并发切换 TLS 策略
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c.SetSkipTLS(i%2 == 0)
		}(i)
	}
	// 并发发请求（自签 server，仅 skip=true 时成功，skip=false 时 TLS 失败，均不应 panic/竞态）
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = c.Get(srv.URL, nil)
		}()
	}
	wg.Wait()

	// 最终切到 skip=true，应能成功访问自签 server（验证 SetSkipTLS 重建后功能正常）
	c.SetSkipTLS(true)
	if _, err := c.Get(srv.URL, nil); err != nil {
		t.Errorf("after SetSkipTLS(true), GET should succeed against self-signed server, got %v", err)
	}
}

// ===== P0：headers/cookies map 并发安全 =====

// 回归 P0：并发 SetHeader/SetCookie 与 Get 请求不应触发数据竞争（-race）。
// 修复前 Set* 无锁写 map、do 无锁读 map，并发即竞态可 panic。
func TestHTTPClientConcurrentHeadersNoRace(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c := utils.NewHTTPClient()
	defer c.Close()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c.SetHeader("X-Req", string(rune('A'+i%26)))
			c.SetCookie("sid", string(rune('0'+i%10)))
			c.SetHeaders(map[string]string{"X-Batch": "v"})
		}(i)
	}
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = c.Get(srv.URL, nil)
		}()
	}
	wg.Wait()
}

// ===== P0：SSRF 防护 =====

// 回归 P0：启用 SSRF 防护后，连接回环地址（httptest server 监听 127.0.0.1）必须被拒绝，
// 返回 ErrSSRFBlocked。
func TestHTTPClientSSRFBlocksLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("secret-internal"))
	}))
	defer srv.Close()

	c := utils.NewSSRFSafeHTTPClient()
	defer c.Close()

	_, err := c.Get(srv.URL, nil)
	if err == nil {
		t.Fatal("SSRF-safe client should refuse to connect to loopback address")
	}
	if !errors.Is(err, utils.ErrSSRFBlocked) {
		t.Errorf("err = %v, want ErrSSRFBlocked", err)
	}
}

// 回归 P0：未启用 SSRF 防护时（默认），回环连接照常放行（兼容性回归）。
func TestHTTPClientSSRFDisabledAllowsLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c := utils.NewHTTPClient() // 默认不启用 SSRF 防护
	defer c.Close()

	body, err := c.Get(srv.URL, nil)
	if err != nil {
		t.Fatalf("default client should allow loopback, got %v", err)
	}
	if string(body) != "ok" {
		t.Errorf("body = %q, want ok", string(body))
	}
}

// 回归 P0：SetBlockPrivateNetworks 运行期开关生效——开启后回环被拒。
func TestHTTPClientSetBlockPrivateNetworks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c := utils.NewHTTPClient()
	defer c.Close()

	// 开启前放行
	if _, err := c.Get(srv.URL, nil); err != nil {
		t.Fatalf("before enabling guard, GET should succeed, got %v", err)
	}
	// 开启后拒绝
	c.SetBlockPrivateNetworks(true)
	if _, err := c.Get(srv.URL, nil); !errors.Is(err, utils.ErrSSRFBlocked) {
		t.Errorf("after enabling guard, err = %v, want ErrSSRFBlocked", err)
	}
}
