package utils

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// HTTPClient HTTP 客户端封装
type HTTPClient struct {
	client    *http.Client
	transport *http.Transport
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

// NewHTTPClientWithConfig 使用自定义配置创建 HTTP 客户端
func NewHTTPClientWithConfig(cfg HTTPClientConfig) *HTTPClient {
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

	client := &http.Client{
		Transport: transport,
		Timeout:   cfg.Timeout,
	}

	return &HTTPClient{
		client:          client,
		transport:       transport,
		timeout:         cfg.Timeout,
		headers:         make(map[string]string),
		cookies:         make(map[string]string),
		skipTLS:         cfg.SkipTLSVerify,
		maxRespBodySize: cfg.MaxResponseBodySize,
	}
}

// SetTimeout 设置超时时间
func (c *HTTPClient) SetTimeout(timeout time.Duration) *HTTPClient {
	c.timeout = timeout
	c.client.Timeout = timeout
	return c
}

// SetHeader 设置请求头
func (c *HTTPClient) SetHeader(key, value string) *HTTPClient {
	c.headers[key] = value
	return c
}

// SetHeaders 批量设置请求头
func (c *HTTPClient) SetHeaders(headers map[string]string) *HTTPClient {
	for k, v := range headers {
		c.headers[k] = v
	}
	return c
}

// SetCookie 设置 Cookie
func (c *HTTPClient) SetCookie(key, value string) *HTTPClient {
	c.cookies[key] = value
	return c
}

// SetSkipTLS 设置是否跳过 TLS 验证。
// 注意: 修改 TLS 配置需要重新创建 Transport。跳过 TLS 校验会暴露于 MITM 攻击，
// 仅在受控环境（如自签证书的内网服务）且明确风险时启用，生产环境应保持 false。
func (c *HTTPClient) SetSkipTLS(skip bool) *HTTPClient {
	c.skipTLS = skip
	// #nosec G402 -- skip 由调用方显式传入，opt-in 跳过 TLS 校验（默认 false）。
	c.transport.TLSClientConfig = &tls.Config{
		InsecureSkipVerify: skip,
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
	// 设置请求头
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}

	// 设置 Cookie
	for k, v := range c.cookies {
		req.AddCookie(&http.Cookie{Name: k, Value: v})
	}

	// 发送请求（使用初始化时创建的 client）
	resp, err := c.client.Do(req)
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

// DoWithResponse 执行请求并返回完整响应
func (c *HTTPClient) DoWithResponse(req *http.Request) (*http.Response, error) {
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}

	for k, v := range c.cookies {
		req.AddCookie(&http.Cookie{Name: k, Value: v})
	}

	return c.client.Do(req)
}

// Close 关闭客户端（释放连接池资源）
func (c *HTTPClient) Close() {
	c.transport.CloseIdleConnections()
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