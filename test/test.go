package test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// SetupRouter 创建测试路由
func SetupRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	return gin.New()
}

// Request 测试请求辅助函数
type Request struct {
	router  *gin.Engine
	method  string
	path    string
	body    any
	headers map[string]string
}

// NewRequest 创建测试请求
func NewRequest(router *gin.Engine, method, path string) *Request {
	return &Request{
		router:  router,
		method:  method,
		path:    path,
		headers: make(map[string]string),
	}
}

// GET 创建 GET 请求
func GET(router *gin.Engine, path string) *Request {
	return NewRequest(router, http.MethodGet, path)
}

// POST 创建 POST 请求
func POST(router *gin.Engine, path string) *Request {
	return NewRequest(router, http.MethodPost, path)
}

// PUT 创建 PUT 请求
func PUT(router *gin.Engine, path string) *Request {
	return NewRequest(router, http.MethodPut, path)
}

// DELETE 创建 DELETE 请求
func DELETE(router *gin.Engine, path string) *Request {
	return NewRequest(router, http.MethodDelete, path)
}

// PATCH 创建 PATCH 请求
func PATCH(router *gin.Engine, path string) *Request {
	return NewRequest(router, http.MethodPatch, path)
}

// WithBody 设置请求体
func (r *Request) WithBody(body any) *Request {
	r.body = body
	return r
}

// WithJSON 设置 JSON 请求体
func (r *Request) WithJSON(body any) *Request {
	r.body = body
	r.headers["Content-Type"] = "application/json"
	return r
}

// WithHeader 设置请求头
func (r *Request) WithHeader(key, value string) *Request {
	r.headers[key] = value
	return r
}

// WithToken 设置 Authorization 头
func (r *Request) WithToken(token string) *Request {
	r.headers["Authorization"] = "Bearer " + token
	return r
}

// Execute 执行请求
func (r *Request) Execute() *httptest.ResponseRecorder {
	var bodyReader *bytes.Reader

	if r.body != nil {
		bodyBytes, _ := json.Marshal(r.body)
		bodyReader = bytes.NewReader(bodyBytes)
	} else {
		bodyReader = bytes.NewReader([]byte{})
	}

	req := httptest.NewRequest(r.method, r.path, bodyReader)

	for key, value := range r.headers {
		req.Header.Set(key, value)
	}

	recorder := httptest.NewRecorder()
	r.router.ServeHTTP(recorder, req)

	return recorder
}

// Response 响应解析辅助
type Response struct {
	*httptest.ResponseRecorder
}

// ParseJSON 解析 JSON 响应
func (r *Response) ParseJSON(v any) error {
	return json.Unmarshal(r.Body.Bytes(), v)
}

// AssertStatus 断言状态码
func (r *Response) AssertStatus(t *testing.T, expected int) {
	t.Helper()
	if r.Code != expected {
		t.Errorf("状态码错误: 期望 %d, 实际 %d", expected, r.Code)
	}
}

// AssertOK 断言状态码为 200
func (r *Response) AssertOK(t *testing.T) {
	t.Helper()
	r.AssertStatus(t, http.StatusOK)
}

// AssertCreated 断言状态码为 201
func (r *Response) AssertCreated(t *testing.T) {
	t.Helper()
	r.AssertStatus(t, http.StatusCreated)
}

// AssertBadRequest 断言状态码为 400
func (r *Response) AssertBadRequest(t *testing.T) {
	t.Helper()
	r.AssertStatus(t, http.StatusBadRequest)
}

// AssertUnauthorized 断言状态码为 401
func (r *Response) AssertUnauthorized(t *testing.T) {
	t.Helper()
	r.AssertStatus(t, http.StatusUnauthorized)
}

// AssertForbidden 断言状态码为 403
func (r *Response) AssertForbidden(t *testing.T) {
	t.Helper()
	r.AssertStatus(t, http.StatusForbidden)
}

// AssertNotFound 断言状态码为 404
func (r *Response) AssertNotFound(t *testing.T) {
	t.Helper()
	r.AssertStatus(t, http.StatusNotFound)
}

// AssertJSONContains 断言 JSON 包含指定字段
func (r *Response) AssertJSONContains(t *testing.T, key string, expected any) {
	t.Helper()

	var result map[string]any
	if err := r.ParseJSON(&result); err != nil {
		t.Errorf("解析 JSON 失败: %v", err)
		return
	}

	// 支持嵌套键（如 "data.user.id"）
	keys := splitKey(key)
	current := result

	for i, k := range keys {
		if i == len(keys)-1 {
			if current[k] != expected {
				t.Errorf("JSON 字段 %s 错误: 期望 %v, 实际 %v", key, expected, current[k])
			}
			return
		}

		next, ok := current[k].(map[string]any)
		if !ok {
			t.Errorf("JSON 字段 %s 不是对象", k)
			return
		}
		current = next
	}
}

func splitKey(key string) []string {
	var keys []string
	start := 0
	for i := 0; i < len(key); i++ {
		if key[i] == '.' {
			keys = append(keys, key[start:i])
			start = i + 1
		}
	}
	keys = append(keys, key[start:])
	return keys
}

// Mock 模拟工具

// MockDB 模拟数据库（用于简单测试）
type MockDB struct {
	data map[any]any
}

// NewMockDB 创建模拟数据库
func NewMockDB() *MockDB {
	return &MockDB{
		data: make(map[any]any),
	}
}

// Set 设置数据
func (m *MockDB) Set(key, value any) {
	m.data[key] = value
}

// Get 获取数据
func (m *MockDB) Get(key any) (any, bool) {
	v, ok := m.data[key]
	return v, ok
}

// Delete 删除数据
func (m *MockDB) Delete(key any) {
	delete(m.data, key)
}

// Clear 清空数据
func (m *MockDB) Clear() {
	m.data = make(map[any]any)
}

// MockCache 模拟缓存
type MockCache struct {
	data map[string][]byte
}

// NewMockCache 创建模拟缓存
func NewMockCache() *MockCache {
	return &MockCache{
		data: make(map[string][]byte),
	}
}

// Set 设置缓存
func (m *MockCache) Set(key string, value []byte) {
	m.data[key] = value
}

// Get 获取缓存
func (m *MockCache) Get(key string) ([]byte, bool) {
	v, ok := m.data[key]
	return v, ok
}

// Delete 删除缓存
func (m *MockCache) Delete(key string) {
	delete(m.data, key)
}

// Exists 检查缓存是否存在
func (m *MockCache) Exists(key string) bool {
	_, ok := m.data[key]
	return ok
}

// Clear 清空缓存
func (m *MockCache) Clear() {
	m.data = make(map[string][]byte)
}

// MockStorage 模拟存储
type MockStorage struct {
	files map[string][]byte
	urls  map[string]string
}

// NewMockStorage 创建模拟存储
func NewMockStorage() *MockStorage {
	return &MockStorage{
		files: make(map[string][]byte),
		urls:  make(map[string]string),
	}
}

// Upload 模拟上传
func (m *MockStorage) Upload(data []byte, filename string) (string, error) {
	path := "/mock/" + filename
	m.files[path] = data
	return path, nil
}

// GetURL 获取 URL
func (m *MockStorage) GetURL(path string) string {
	return "http://mock.test" + path
}

// AssertEqual 通用相等断言
func AssertEqual(t *testing.T, expected, actual any) {
	t.Helper()
	if expected != actual {
		t.Errorf("不相等: 期望 %v, 实际 %v", expected, actual)
	}
}

// AssertNotNil 断言不为空
func AssertNotNil(t *testing.T, value any) {
	t.Helper()
	if value == nil {
		t.Error("期望非空，实际为空")
	}
}

// AssertNil 断言为空
func AssertNil(t *testing.T, value any) {
	t.Helper()
	if value != nil {
		t.Errorf("期望为空，实际为: %v", value)
	}
}

// AssertTrue 断言为真
func AssertTrue(t *testing.T, value bool) {
	t.Helper()
	if !value {
		t.Error("期望为真，实际为假")
	}
}

// AssertFalse 断言为假
func AssertFalse(t *testing.T, value bool) {
	t.Helper()
	if value {
		t.Error("期望为假，实际为真")
	}
}

// AssertError 断言有错误
func AssertError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Error("期望有错误，实际无错误")
	}
}

// AssertNoError 断言无错误
func AssertNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Errorf("期望无错误，实际错误: %v", err)
	}
}
