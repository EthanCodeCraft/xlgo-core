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

// HTTPClient wraps an http.Client with shared runtime configuration.
type HTTPClient struct {
	mu        sync.RWMutex
	client    *http.Client
	transport *http.Transport
	cfg       HTTPClientConfig
	timeout   time.Duration
	headers   map[string]string
	cookies   map[string]string
	skipTLS   bool
	// maxRespBodySize caps response bodies read by do. 0 uses the default, -1 disables the cap.
	maxRespBodySize int64
}

// UploadFile describes a file field in a multipart upload.
type UploadFile struct {
	FieldName string
	FilePath  string
}

// HTTPClientConfig configures HTTPClient.
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

// ErrSSRFBlocked indicates that SSRF protection blocked the target IP.
var ErrSSRFBlocked = errors.New("ssrf guard: target IP is in a blocked network range")

// isBlockedIP reports whether ip belongs to a blocked network range.
func isBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	return ip.IsLoopback() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast()
}

// ssrfControl blocks connections to private, loopback, link-local, unspecified, and multicast IPs.
func ssrfControl(network, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("%w: cannot parse address %q", ErrSSRFBlocked, address)
	}
	if isBlockedIP(ip) {
		return fmt.Errorf("%w: %s", ErrSSRFBlocked, ip)
	}
	return nil
}

// DefaultHTTPClientConfig is the package default HTTP client configuration.
var DefaultHTTPClientConfig = HTTPClientConfig{
	Timeout:             30 * time.Second,
	MaxIdleConns:        100,
	IdleConnTimeout:     90 * time.Second,
	MaxConnsPerHost:     10,
	MaxIdleConnsPerHost: 10,
	SkipTLSVerify:       false,
	MaxResponseBodySize: 32 * 1024 * 1024,
}

// NewHTTPClient creates an HTTP client with the default configuration.
func NewHTTPClient() *HTTPClient {
	cfg := DefaultHTTPClientConfig
	return NewHTTPClientWithConfig(cfg)
}

// NewSSRFSafeHTTPClient creates an HTTP client with SSRF protection enabled.
func NewSSRFSafeHTTPClient() *HTTPClient {
	cfg := DefaultHTTPClientConfig
	cfg.BlockPrivateNetworks = true
	return NewHTTPClientWithConfig(cfg)
}

// NewHTTPClientWithConfig creates an HTTP client with cfg.
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

// buildHTTPClientPair builds a transport/client pair from cfg.
func buildHTTPClientPair(cfg HTTPClientConfig) (*http.Transport, *http.Client) {
	transport := &http.Transport{
		// #nosec G402 -- SkipTLSVerify is opt-in; the default verifies TLS.
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

// currentClient returns the current client snapshot.
func (c *HTTPClient) currentClient() *http.Client {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.client
}

// currentTransport returns the current transport snapshot.
func (c *HTTPClient) currentTransport() *http.Transport {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.transport
}

// SetTimeout updates request timeout while preserving the current transport.
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

// SetHeader sets a request header for future requests.
func (c *HTTPClient) SetHeader(key, value string) *HTTPClient {
	c.mu.Lock()
	c.headers[key] = value
	c.mu.Unlock()
	return c
}

// SetHeaders sets request headers for future requests.
func (c *HTTPClient) SetHeaders(headers map[string]string) *HTTPClient {
	c.mu.Lock()
	for k, v := range headers {
		c.headers[k] = v
	}
	c.mu.Unlock()
	return c
}

// SetCookie sets a cookie for future requests.
func (c *HTTPClient) SetCookie(key, value string) *HTTPClient {
	c.mu.Lock()
	c.cookies[key] = value
	c.mu.Unlock()
	return c
}

// snapshotHeadersCookies copies mutable header and cookie maps under lock.
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

// SetSkipTLS toggles TLS certificate verification for future requests.
func (c *HTTPClient) SetSkipTLS(skip bool) *HTTPClient {
	c.mu.Lock()
	c.skipTLS = skip
	c.cfg.SkipTLSVerify = skip
	transport, client := buildHTTPClientPair(c.cfg)
	oldTransport := c.transport
	c.transport = transport
	c.client = client
	c.mu.Unlock()
	// Release idle connections from the old transport outside the lock.
	if oldTransport != nil {
		oldTransport.CloseIdleConnections()
	}
	return c
}

// SetBlockPrivateNetworks toggles SSRF protection for future requests.
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

// Get sends a GET request.
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

// Post sends a form-encoded POST request.
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

// PostJSON sends a JSON POST request.
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

// Put sends a JSON PUT request.
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

// Delete sends a DELETE request.
func (c *HTTPClient) Delete(urlStr string) ([]byte, error) {
	req, err := http.NewRequest("DELETE", urlStr, nil)
	if err != nil {
		return nil, err
	}
	return c.do(req)
}

// Upload streams multipart files without buffering the full request body in memory.
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

// UploadFromBytes uploads an in-memory byte slice as a multipart file.
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

// Request sends a request with a caller-provided method and JSON body.
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

// do executes req and reads a bounded response body.
func (c *HTTPClient) do(req *http.Request) ([]byte, error) {
	headers, cookies := c.snapshotHeadersCookies()
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	for k, v := range cookies {
		// #nosec G124 -- this builds outbound Cookie request headers; Secure/HttpOnly/SameSite apply to Set-Cookie responses.
		req.AddCookie(&http.Cookie{Name: k, Value: v})
	}

	// #nosec G704 -- default client intentionally allows caller-supplied URLs for compatibility; use NewSSRFSafeHTTPClient/BlockPrivateNetworks for untrusted URLs.
	resp, err := c.currentClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Treat 4xx/5xx responses as request errors.
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("http error: %d %s", resp.StatusCode, resp.Status)
	}

	// maxRespBodySize: 0 uses the default 32MiB cap; -1 disables the cap.
	limit := c.maxRespBodySize
	if limit == 0 {
		limit = 32 * 1024 * 1024
	}
	var reader io.Reader = resp.Body
	if limit > 0 {
		// Read one extra byte so over-limit responses can be detected.
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

// DoWithResponse executes req and returns the raw response; caller must close resp.Body.
func (c *HTTPClient) DoWithResponse(req *http.Request) (*http.Response, error) {
	headers, cookies := c.snapshotHeadersCookies()
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	for k, v := range cookies {
		// #nosec G124 -- this builds outbound Cookie request headers; Secure/HttpOnly/SameSite apply to Set-Cookie responses.
		req.AddCookie(&http.Cookie{Name: k, Value: v})
	}

	// #nosec G704 -- default client intentionally allows caller-supplied URLs for compatibility; use NewSSRFSafeHTTPClient/BlockPrivateNetworks for untrusted URLs.
	return c.currentClient().Do(req)
}

// Close releases idle connections held by the client transport.
func (c *HTTPClient) Close() {
	if t := c.currentTransport(); t != nil {
		t.CloseIdleConnections()
	}
}

// JSONMarshal marshals v as JSON.
func JSONMarshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

// defaultClient is the package-level shared HTTP client.
var defaultClient *HTTPClient
var defaultClientOnce sync.Once

// DefaultHTTPClient returns the package-level shared HTTP client.
func DefaultHTTPClient() *HTTPClient {
	defaultClientOnce.Do(func() {
		defaultClient = NewHTTPClient()
	})
	return defaultClient
}

// HTTPGet sends a GET request with the default client.
func HTTPGet(url string, params map[string]string) ([]byte, error) {
	return DefaultHTTPClient().Get(url, params)
}

// HTTPPost sends a form POST request with the default client.
func HTTPPost(url string, params map[string]string) ([]byte, error) {
	return DefaultHTTPClient().Post(url, params)
}

// HTTPPostJSON sends a JSON POST request with the default client.
func HTTPPostJSON(url string, data any) ([]byte, error) {
	return DefaultHTTPClient().PostJSON(url, data)
}
