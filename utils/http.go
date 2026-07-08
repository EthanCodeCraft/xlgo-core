package utils

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// HTTPClient 封装可运行期调整配置的 HTTP 客户端。
type HTTPClient struct {
	mu        sync.RWMutex
	client    *http.Client
	transport *http.Transport
	cfg       HTTPClientConfig
	timeout   time.Duration
	headers   map[string]string
	cookies   map[string]string
	skipTLS   bool
	// maxRespBodySize 限制 do 读取的响应体大小；0 使用默认值，-1 表示不限制。
	maxRespBodySize int64
}

// UploadFile 描述 multipart 上传中的文件字段。
type UploadFile struct {
	FieldName string
	FilePath  string
}

// HTTPClientConfig 是 HTTPClient 的配置。
type HTTPClientConfig struct {
	Timeout              time.Duration
	MaxIdleConns         int
	IdleConnTimeout      time.Duration
	MaxConnsPerHost      int
	MaxIdleConnsPerHost  int
	SkipTLSVerify        bool
	MaxResponseBodySize  int64
	BlockPrivateNetworks bool
}

// ErrSSRFBlocked 表示 SSRF 防护拦截了目标 IP。
var ErrSSRFBlocked = errors.New("ssrf guard: 目标 IP 属于被拦截的网络范围")

// isBlockedIP 判断 ip 是否属于应被 SSRF 防护拦截的网络范围。
func isBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	return ip.IsLoopback() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast()
}

// ssrfControl 在拨号前拦截私有、回环、链路本地、未指定和多播 IP。
func ssrfControl(network, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("%w: 无法解析地址 %q", ErrSSRFBlocked, address)
	}
	if isBlockedIP(ip) {
		return fmt.Errorf("%w: %s", ErrSSRFBlocked, ip)
	}
	return nil
}

// DefaultHTTPClientConfig 是包级默认 HTTP 客户端配置。
var DefaultHTTPClientConfig = HTTPClientConfig{
	Timeout:             30 * time.Second,
	MaxIdleConns:        100,
	IdleConnTimeout:     90 * time.Second,
	MaxConnsPerHost:     10,
	MaxIdleConnsPerHost: 10,
	SkipTLSVerify:       false,
	MaxResponseBodySize: 32 * 1024 * 1024,
}

// NewHTTPClient 使用默认配置创建 HTTP 客户端。
func NewHTTPClient() *HTTPClient {
	cfg := DefaultHTTPClientConfig
	return NewHTTPClientWithConfig(cfg)
}

// NewSSRFSafeHTTPClient 创建启用 SSRF 防护的 HTTP 客户端。
func NewSSRFSafeHTTPClient() *HTTPClient {
	cfg := DefaultHTTPClientConfig
	cfg.BlockPrivateNetworks = true
	return NewHTTPClientWithConfig(cfg)
}

// NewHTTPClientWithConfig 使用 cfg 创建 HTTP 客户端。
func NewHTTPClientWithConfig(cfg HTTPClientConfig) *HTTPClient {
	transport, client := buildHTTPClientPair(cfg)
	return &HTTPClient{
		client:          client,
		transport:       transport,
		cfg:             cfg,
		timeout:         cfg.Timeout,
		headers:         make(map[string]string),
		cookies:         make(map[string]string),
		skipTLS:         cfg.SkipTLSVerify,
		maxRespBodySize: cfg.MaxResponseBodySize,
	}
}

// buildHTTPClientPair 根据 cfg 构造 transport/client 对。
func buildHTTPClientPair(cfg HTTPClientConfig) (*http.Transport, *http.Client) {
	transport := &http.Transport{
		// #nosec G402 -- SkipTLSVerify 仅在调用方显式配置时启用，默认校验 TLS。
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: cfg.SkipTLSVerify,
		},
		MaxIdleConns:        cfg.MaxIdleConns,
		IdleConnTimeout:     cfg.IdleConnTimeout,
		MaxConnsPerHost:     cfg.MaxConnsPerHost,
		MaxIdleConnsPerHost: cfg.MaxIdleConnsPerHost,
		DisableCompression:  false,
	}
	if cfg.BlockPrivateNetworks {
		dialer := &net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
			Control:   ssrfControl,
		}
		transport.DialContext = dialer.DialContext
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   cfg.Timeout,
	}
	return transport, client
}

// currentClient 返回当前 client 快照。
func (c *HTTPClient) currentClient() *http.Client {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.client
}

// currentTransport 返回当前 transport 快照。
func (c *HTTPClient) currentTransport() *http.Transport {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.transport
}

// SetTimeout 更新请求超时时间，并保留当前 transport。
func (c *HTTPClient) SetTimeout(timeout time.Duration) *HTTPClient {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.timeout = timeout
	c.cfg.Timeout = timeout
	_, client := buildHTTPClientPair(c.cfg)
	client.Transport = c.transport
	c.client = client
	return c
}

// SetHeader 设置后续请求使用的请求头。
func (c *HTTPClient) SetHeader(key, value string) *HTTPClient {
	c.mu.Lock()
	c.headers[key] = value
	c.mu.Unlock()
	return c
}

// SetHeaders 批量设置后续请求使用的请求头。
func (c *HTTPClient) SetHeaders(headers map[string]string) *HTTPClient {
	c.mu.Lock()
	for k, v := range headers {
		c.headers[k] = v
	}
	c.mu.Unlock()
	return c
}

// SetCookie 设置后续请求使用的 Cookie。
func (c *HTTPClient) SetCookie(key, value string) *HTTPClient {
	c.mu.Lock()
	c.cookies[key] = value
	c.mu.Unlock()
	return c
}

// snapshotHeadersCookies 在锁内复制可变的 headers/cookies。
func (c *HTTPClient) snapshotHeadersCookies() (headers, cookies map[string]string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	headers = make(map[string]string, len(c.headers))
	for k, v := range c.headers {
		headers[k] = v
	}
	cookies = make(map[string]string, len(c.cookies))
	for k, v := range c.cookies {
		cookies[k] = v
	}
	return headers, cookies
}

// SetSkipTLS 切换后续请求是否校验 TLS 证书。
func (c *HTTPClient) SetSkipTLS(skip bool) *HTTPClient {
	c.mu.Lock()
	c.skipTLS = skip
	c.cfg.SkipTLSVerify = skip
	transport, client := buildHTTPClientPair(c.cfg)
	oldTransport := c.transport
	c.transport = transport
	c.client = client
	c.mu.Unlock()
	// 在锁外释放旧 transport 的空闲连接。
	if oldTransport != nil {
		oldTransport.CloseIdleConnections()
	}
	return c
}

// SetBlockPrivateNetworks 切换后续请求的 SSRF 防护。
func (c *HTTPClient) SetBlockPrivateNetworks(block bool) *HTTPClient {
	c.mu.Lock()
	c.cfg.BlockPrivateNetworks = block
	transport, client := buildHTTPClientPair(c.cfg)
	oldTransport := c.transport
	c.transport = transport
	c.client = client
	c.mu.Unlock()
	if oldTransport != nil {
		oldTransport.CloseIdleConnections()
	}
	return c
}

// Get 发送 GET 请求。
func (c *HTTPClient) Get(urlStr string, params map[string]string) ([]byte, error) {
	if len(params) > 0 {
		u, err := url.Parse(urlStr)
		if err != nil {
			return nil, err
		}
		q := u.Query()
		for k, v := range params {
			q.Set(k, v)
		}
		u.RawQuery = q.Encode()
		urlStr = u.String()
	}

	req, err := http.NewRequest("GET", urlStr, nil)
	if err != nil {
		return nil, err
	}
	return c.do(req)
}

// Post 发送表单编码的 POST 请求。
func (c *HTTPClient) Post(urlStr string, params map[string]string) ([]byte, error) {
	data := url.Values{}
	for k, v := range params {
		data.Set(k, v)
	}

	req, err := http.NewRequest("POST", urlStr, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return c.do(req)
}

// PostJSON 发送 JSON POST 请求。
func (c *HTTPClient) PostJSON(urlStr string, data any) ([]byte, error) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", urlStr, bytes.NewReader(jsonData))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req)
}

// Put 发送 JSON PUT 请求。
func (c *HTTPClient) Put(urlStr string, data any) ([]byte, error) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("PUT", urlStr, bytes.NewReader(jsonData))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req)
}

// Delete 发送 DELETE 请求。
func (c *HTTPClient) Delete(urlStr string) ([]byte, error) {
	req, err := http.NewRequest("DELETE", urlStr, nil)
	if err != nil {
		return nil, err
	}
	return c.do(req)
}

// Upload 以流式 multipart 上传文件，避免把完整请求体缓存在内存中。
func (c *HTTPClient) Upload(urlStr string, files []UploadFile, params map[string]string) ([]byte, error) {
	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)
	errCh := make(chan error, 1)

	go func() {
		errCh <- writeMultipartUpload(pw, writer, files, params)
	}()

	req, err := http.NewRequest("POST", urlStr, pr)
	if err != nil {
		_ = pr.CloseWithError(err)
		_ = pw.CloseWithError(err)
		<-errCh
		return nil, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.do(req)
	if err != nil {
		_ = pr.CloseWithError(err)
		<-errCh
		return nil, err
	}
	if err := <-errCh; err != nil {
		return nil, err
	}
	return resp, nil
}

func writeMultipartUpload(pw *io.PipeWriter, writer *multipart.Writer, files []UploadFile, params map[string]string) (err error) {
	defer func() {
		if err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		if closeErr := writer.Close(); closeErr != nil {
			_ = pw.CloseWithError(closeErr)
			err = closeErr
			return
		}
		err = pw.Close()
	}()

	for _, f := range files {
		if err = writeMultipartFile(writer, f); err != nil {
			return err
		}
	}

	for k, v := range params {
		if err = writer.WriteField(k, v); err != nil {
			return err
		}
	}
	return nil
}

func writeMultipartFile(writer *multipart.Writer, f UploadFile) (err error) {
	file, err := os.Open(f.FilePath)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := file.Close(); cerr != nil {
			err = errors.Join(err, cerr)
		}
	}()

	part, err := writer.CreateFormFile(f.FieldName, filepath.Base(f.FilePath))
	if err != nil {
		return err
	}
	_, err = io.Copy(part, file)
	return err
}

// UploadFromBytes 将内存中的字节切片作为 multipart 文件上传。
func (c *HTTPClient) UploadFromBytes(urlStr string, fieldName string, filename string, data []byte, params map[string]string) ([]byte, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	part, err := writer.CreateFormFile(fieldName, filename)
	if err != nil {
		return nil, err
	}
	if _, err = io.Copy(part, bytes.NewReader(data)); err != nil {
		return nil, err
	}

	for k, v := range params {
		if err := writer.WriteField(k, v); err != nil {
			return nil, err
		}
	}

	if err := writer.Close(); err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", urlStr, &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return c.do(req)
}

// Request 使用调用方指定的方法和 JSON 请求体发送请求。
func (c *HTTPClient) Request(method, urlStr string, body []byte) ([]byte, error) {
	req, err := http.NewRequest(method, urlStr, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.do(req)
}

// do 执行 req，并按配置封顶读取响应体。
func (c *HTTPClient) do(req *http.Request) ([]byte, error) {
	headers, cookies := c.snapshotHeadersCookies()
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	for k, v := range cookies {
		// #nosec G124 -- 这里构造的是出站 Cookie 请求头；Secure/HttpOnly/SameSite 适用于响应 Set-Cookie。
		req.AddCookie(&http.Cookie{Name: k, Value: v})
	}

	// #nosec G704 -- 默认客户端为兼容性允许调用方传入 URL；不可信 URL 请使用 NewSSRFSafeHTTPClient/BlockPrivateNetworks。
	resp, err := c.currentClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// 将 4xx/5xx 响应视为请求错误。
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP 请求失败: %d %s", resp.StatusCode, resp.Status)
	}

	// maxRespBodySize: 0 使用默认 32MiB 上限；-1 表示不限制。
	limit := c.maxRespBodySize
	if limit == 0 {
		limit = 32 * 1024 * 1024
	}
	var reader io.Reader = resp.Body
	if limit > 0 {
		// 多读 1 字节用于判断响应体是否超限。
		reader = io.LimitReader(resp.Body, limit+1)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if limit > 0 && int64(len(data)) > limit {
		return nil, fmt.Errorf("响应体超过限制 %d 字节", limit)
	}
	return data, nil
}

// DoWithResponse 执行 req 并返回原始响应；调用方必须关闭 resp.Body。
func (c *HTTPClient) DoWithResponse(req *http.Request) (*http.Response, error) {
	headers, cookies := c.snapshotHeadersCookies()
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	for k, v := range cookies {
		// #nosec G124 -- 这里构造的是出站 Cookie 请求头；Secure/HttpOnly/SameSite 适用于响应 Set-Cookie。
		req.AddCookie(&http.Cookie{Name: k, Value: v})
	}

	// #nosec G704 -- 默认客户端为兼容性允许调用方传入 URL；不可信 URL 请使用 NewSSRFSafeHTTPClient/BlockPrivateNetworks。
	return c.currentClient().Do(req)
}

// Close 释放客户端 transport 持有的空闲连接。
func (c *HTTPClient) Close() {
	if t := c.currentTransport(); t != nil {
		t.CloseIdleConnections()
	}
}

// JSONMarshal 将 v 序列化为 JSON。
func JSONMarshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

// defaultClient 是包级共享 HTTP 客户端。
var defaultClient *HTTPClient
var defaultClientOnce sync.Once

// DefaultHTTPClient 返回包级共享 HTTP 客户端。
func DefaultHTTPClient() *HTTPClient {
	defaultClientOnce.Do(func() {
		defaultClient = NewHTTPClient()
	})
	return defaultClient
}

// HTTPGet 使用默认客户端发送 GET 请求。
func HTTPGet(url string, params map[string]string) ([]byte, error) {
	return DefaultHTTPClient().Get(url, params)
}

// HTTPPost 使用默认客户端发送表单 POST 请求。
func HTTPPost(url string, params map[string]string) ([]byte, error) {
	return DefaultHTTPClient().Post(url, params)
}

// HTTPPostJSON 使用默认客户端发送 JSON POST 请求。
func HTTPPostJSON(url string, data any) ([]byte, error) {
	return DefaultHTTPClient().PostJSON(url, data)
}
