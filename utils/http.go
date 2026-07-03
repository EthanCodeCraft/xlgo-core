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

// HTTPClient HTTP 客户端封装
type HTTPClient struct {
	// mu 保护 client/transport/cfg/timeout/skipTLS 的并发读写。SetSkipTLS/SetTimeout
	// 在写锁下重建 client/transport；do/DoWithResponse/Close 在读锁下快照后无锁调用
	// http.Client.Do（Do 自身并发安全），不持有锁发请求以避免序列化。
	mu        sync.RWMutex
	client    *http.Client
	transport *http.Transport
	cfg       HTTPClientConfig // 保留配置以便 SetSkipTLS 重建 transport
	timeout   time.Duration
	headers   map[string]string
	cookies   map[string]string
	skipTLS   bool
	// maxRespBodySize 响应体读取上限（字节），0 表示用默认 32MB，-1 表示不限。
	// 防止恶意/异常服务端返回超大响应打爆内存（C5/N5）。
	maxRespBodySize int64
}

// UploadFile 上传文件信息
type UploadFile struct {
	FieldName string // 表单字段名
	FilePath  string // 文件路径
}

// HTTPClientConfig HTTP 客户端配置
type HTTPClientConfig struct {
	Timeout            time.Duration // 请求超时时间
	MaxIdleConns       int           // 最大空闲连接数
	IdleConnTimeout    time.Duration // 空闲连接超时时间
	MaxConnsPerHost    int           // 每个主机最大连接数
	MaxIdleConnsPerHost int           // 每个主机最大空闲连接数
	SkipTLSVerify      bool          // 是否跳过 TLS 验证（默认 false 校验 TLS；自签证书场景需显式设 true）
	// MaxResponseBodySize 响应体读取上限（字节）。0 = 默认 32MB，-1 = 不限制。
	// 防止异常服务端返回超大响应打爆内存（C5/N5）。
	MaxResponseBodySize int64
	// BlockPrivateNetworks 启用 SSRF 防护（P0，默认 false 保持兼容）。启用后，连接建立时
	// 校验解析出的目标 IP，拒绝回环/私有(RFC1918+ULA)/链路本地/元数据(169.254.169.254)/
	// 未指定/多播等内网地址。校验在 DialContext.Control 中进行，对重定向的每一跳同样生效。
	// 当 URL 可能来自用户输入（webhook、头像抓取等）时应开启。
	BlockPrivateNetworks bool
}

// ErrSSRFBlocked 目标 IP 落在被拦截网段（SSRF 防护，P0）。
var ErrSSRFBlocked = errors.New("ssrf guard: 目标 IP 属被拦截网段（回环/私有/链路本地/元数据）")

// isBlockedIP 判断 IP 是否应被 SSRF 防护拦截。
// 覆盖：回环、私有(RFC1918 + ULA fc00::/7)、链路本地(含 169.254.169.254 云元数据 / fe80::)、
// 未指定(0.0.0.0/::)、多播。
func isBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	return ip.IsLoopback() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast()
}

// ssrfControl 是 net.Dialer.Control 回调：在 DNS 解析后、真正 dial 前校验目标 IP，
// 命中内网段即拒绝。放在 Control 层可覆盖 DNS 重绑定与重定向的每一跳（P0）。
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

// DefaultHTTPClientConfig 默认配置
var DefaultHTTPClientConfig = HTTPClientConfig{
	Timeout:             30 * time.Second,
	MaxIdleConns:        100,
	IdleConnTimeout:     90 * time.Second,
	MaxConnsPerHost:     10,
	MaxIdleConnsPerHost: 10,
	SkipTLSVerify:       false, // H2 修复：默认校验 TLS，防 MITM；自签证书需显式 SetSkipTLS(true)
	MaxResponseBodySize: 32 * 1024 * 1024,
}

// NewHTTPClient 创建 HTTP 客户端
func NewHTTPClient() *HTTPClient {
	cfg := DefaultHTTPClientConfig
	return NewHTTPClientWithConfig(cfg)
}

// NewSSRFSafeHTTPClient 创建启用 SSRF 防护的 HTTP 客户端（P0）：拒绝连接内网目标 IP。
// 适用于目标 URL 可能来自用户输入的场景（webhook 回调、远程图片抓取等）。
func NewSSRFSafeHTTPClient() *HTTPClient {
	cfg := DefaultHTTPClientConfig
	cfg.BlockPrivateNetworks = true
	return NewHTTPClientWithConfig(cfg)
}

// NewHTTPClientWithConfig 使用自定义配置创建 HTTP 客户端
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

// buildHTTPClientPair 按 cfg 构造 (transport, client) 对。SetSkipTLS 重建时复用。
func buildHTTPClientPair(cfg HTTPClientConfig) (*http.Transport, *http.Client) {
	// Transport 在初始化时创建，连接池可复用
	transport := &http.Transport{
		// #nosec G402 -- InsecureSkipVerify 仅在调用方显式设 cfg.SkipTLSVerify=true 时启用，
		// 默认 false 校验 TLS（H2 修复）。自签证书场景需 opt-in。
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: cfg.SkipTLSVerify,
		},
		MaxIdleConns:        cfg.MaxIdleConns,
		IdleConnTimeout:     cfg.IdleConnTimeout,
		MaxConnsPerHost:     cfg.MaxConnsPerHost,
		MaxIdleConnsPerHost: cfg.MaxIdleConnsPerHost,
		DisableCompression:  false,
	}
	// SSRF 防护（P0）：启用后为拨号器装 Control 回调，连接建立时拦截内网目标 IP。
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

// currentClient 在读锁下快照当前 *http.Client（H-12：与 SetSkipTLS/SetTimeout 重建无竞态）。
func (c *HTTPClient) currentClient() *http.Client {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.client
}

// currentTransport 在读锁下快照当前 *http.Transport。
func (c *HTTPClient) currentTransport() *http.Transport {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.transport
}

// SetTimeout 设置超时时间。
// H-12 修复：在写锁下重建 *http.Client（复用 transport 保留连接池），避免与并发 Do 对
// client.Timeout 字段的数据竞争。
func (c *HTTPClient) SetTimeout(timeout time.Duration) *HTTPClient {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.timeout = timeout
	c.cfg.Timeout = timeout
	_, client := buildHTTPClientPair(c.cfg)
	// 复用旧 transport 保留连接池：新 client 用旧 transport + 新 timeout。
	client.Transport = c.transport
	c.client = client
	return c
}

// SetHeader 设置请求头（P0：写锁保护，与并发 do/DoWithResponse 读 map 无竞态）。
func (c *HTTPClient) SetHeader(key, value string) *HTTPClient {
	c.mu.Lock()
	c.headers[key] = value
	c.mu.Unlock()
	return c
}

// SetHeaders 批量设置请求头（P0：写锁保护）。
func (c *HTTPClient) SetHeaders(headers map[string]string) *HTTPClient {
	c.mu.Lock()
	for k, v := range headers {
		c.headers[k] = v
	}
	c.mu.Unlock()
	return c
}

// SetCookie 设置 Cookie（P0：写锁保护）。
func (c *HTTPClient) SetCookie(key, value string) *HTTPClient {
	c.mu.Lock()
	c.cookies[key] = value
	c.mu.Unlock()
	return c
}

// snapshotHeadersCookies 在读锁下拷贝 headers/cookies，供 do/DoWithResponse 在锁外应用到请求，
// 消除与 SetHeader/SetHeaders/SetCookie 的 map 读写竞态（P0）。
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

// SetSkipTLS 设置是否跳过 TLS 验证。
// 跳过 TLS 校验会暴露于 MITM 攻击，仅在受控环境（如自签证书的内网服务）且明确风险时启用，
// 生产环境应保持 false。
//
// H-12 修复：原实现直接覆盖 c.transport.TLSClientConfig 指针，与并发 Do 读取该字段
// 存在数据竞争（-race 必采），且注释自承"需重建 Transport"却未重建。改为在写锁下用
// 新配置重建 transport+client 并原子替换，旧 transport 释放空闲连接（保留旧 TLS 配置的
// idle 连接不再被复用）。支持运行期并发调用与并发请求无竞态。
func (c *HTTPClient) SetSkipTLS(skip bool) *HTTPClient {
	c.mu.Lock()
	c.skipTLS = skip
	c.cfg.SkipTLSVerify = skip
	transport, client := buildHTTPClientPair(c.cfg)
	oldTransport := c.transport
	c.transport = transport
	c.client = client
	c.mu.Unlock()
	// 锁外释放旧 transport 的空闲连接（CloseIdleConnections 仅关 idle 连接，不影响在途请求）。
	if oldTransport != nil {
		oldTransport.CloseIdleConnections()
	}
	return c
}

// SetBlockPrivateNetworks 运行期开关 SSRF 防护（P0）。开启后连接内网 IP（回环/私有/链路本地/
// 元数据等）会被拒绝并返回 ErrSSRFBlocked。写锁下重建 transport+client 并原子替换，
// 与并发请求无竞态；旧 transport 的空闲连接在锁外释放。
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

// Get 发送 GET 请求
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

// Post 发送 POST 请求（form 表单格式）
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

// PostJSON 发送 POST 请求（JSON 格式）
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

// Put 发送 PUT 请求（JSON 格式）
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

// Delete 发送 DELETE 请求
func (c *HTTPClient) Delete(urlStr string) ([]byte, error) {
	req, err := http.NewRequest("DELETE", urlStr, nil)
	if err != nil {
		return nil, err
	}
	return c.do(req)
}

// Upload 上传文件
func (c *HTTPClient) Upload(urlStr string, files []UploadFile, params map[string]string) ([]byte, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	for _, f := range files {
		file, err := os.Open(f.FilePath)
		if err != nil {
			return nil, err
		}

		part, err := writer.CreateFormFile(f.FieldName, filepath.Base(f.FilePath))
		if err != nil {
			file.Close()
			return nil, err
		}
		if _, err = io.Copy(part, file); err != nil {
			file.Close()
			return nil, err
		}
		// 显式关闭，避免循环内 defer 累积 FD（N5/C5）。
		file.Close()
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

// UploadFromBytes 从字节数据上传文件
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

// Request 发送自定义请求
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

// do 执行请求（使用共享的 client 和 transport）
func (c *HTTPClient) do(req *http.Request) ([]byte, error) {
	// P0：读锁快照 headers/cookies 后在锁外应用，与并发 Set* 无竞态。
	headers, cookies := c.snapshotHeadersCookies()
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	for k, v := range cookies {
		req.AddCookie(&http.Cookie{Name: k, Value: v})
	}

	// 发送请求（H-12：快照 client，与 SetSkipTLS/SetTimeout 重建无竞态）
	resp, err := c.currentClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// 检查状态码
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("http error: %d %s", resp.StatusCode, resp.Status)
	}

	// 响应体读取封顶，防异常服务端返回超大响应打爆内存（C5/N5）。
	// maxRespBodySize: 0=默认 32MB，-1=不限。
	limit := c.maxRespBodySize
	if limit == 0 {
		limit = 32 * 1024 * 1024
	}
	var reader io.Reader = resp.Body
	if limit > 0 {
		// 多读 1 字节用于判断是否超限。
		reader = io.LimitReader(resp.Body, limit+1)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if limit > 0 && int64(len(data)) > limit {
		return nil, fmt.Errorf("response body exceeds limit %d bytes", limit)
	}
	return data, nil
}

// DoWithResponse 执行请求并返回完整响应。
//
// 注意：调用方负责关闭返回的 resp.Body；且此方法不套用 maxRespBodySize 读封顶（由调用方掌控）。
func (c *HTTPClient) DoWithResponse(req *http.Request) (*http.Response, error) {
	// P0：读锁快照 headers/cookies 后在锁外应用，与并发 Set* 无竞态。
	headers, cookies := c.snapshotHeadersCookies()
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	for k, v := range cookies {
		req.AddCookie(&http.Cookie{Name: k, Value: v})
	}

	return c.currentClient().Do(req)
}

// Close 关闭客户端（释放连接池资源）
func (c *HTTPClient) Close() {
	if t := c.currentTransport(); t != nil {
		t.CloseIdleConnections()
	}
}

// JSONMarshal 内部 JSON 序列化函数
func JSONMarshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

// 全局默认 HTTP 客户端
var defaultClient *HTTPClient
var defaultClientOnce sync.Once

// DefaultHTTPClient 获取全局默认 HTTP 客户端
func DefaultHTTPClient() *HTTPClient {
	defaultClientOnce.Do(func() {
		defaultClient = NewHTTPClient()
	})
	return defaultClient
}

// HTTPGet 使用默认客户端发送 GET 请求
func HTTPGet(url string, params map[string]string) ([]byte, error) {
	return DefaultHTTPClient().Get(url, params)
}

// HTTPPost 使用默认客户端发送 POST 请求
func HTTPPost(url string, params map[string]string) ([]byte, error) {
	return DefaultHTTPClient().Post(url, params)
}

// HTTPPostJSON 使用默认客户端发送 JSON POST 请求
func HTTPPostJSON(url string, data any) ([]byte, error) {
	return DefaultHTTPClient().PostJSON(url, data)
}