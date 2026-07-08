package middleware

import (
	"bytes"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// newCtxWithBody 构造一个带请求体的 gin.Context，仅用于 logger 内部测试。
func newCtxWithBody(body []byte) *gin.Context {
	req := httptest.NewRequest("POST", "/", bytes.NewReader(body))
	c := &gin.Context{}
	c.Request = req
	return c
}

// TestReadBodyBounded_TruncatesLogCopy 复现 H3：日志副本必须封顶到 maxLen，
// 而不是把整个 body 读入内存后再截断（原 io.ReadAll 无上限 → OOM）。
func TestReadBodyBounded_TruncatesLogCopy(t *testing.T) {
	cases := []struct {
		name    string
		bodyLen int
		maxLen  int
	}{
		{"smaller_than_limit", 100, 1024},
		{"equal_to_limit", 1024, 1024},
		{"larger_than_limit", 100_000, 1024},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := bytes.Repeat([]byte("a"), tc.bodyLen)
			c := newCtxWithBody(body)

			got := readBodyBounded(c, tc.maxLen)

			want := tc.bodyLen
			if want > tc.maxLen {
				want = tc.maxLen
			}
			if len(got) != want {
				t.Fatalf("log copy len = %d, want %d (maxLen=%d, bodyLen=%d)", len(got), want, tc.maxLen, tc.bodyLen)
			}
		})
	}
}

// TestReadBodyBounded_RestoresFullBody 是 H3 修复的闭环断言：
// 即使日志副本被截断，下游处理器仍必须能读到完整请求体（io.MultiReader 复原）。
func TestReadBodyBounded_RestoresFullBody(t *testing.T) {
	const maxLen = 64
	body := []byte(strings.Repeat("ABCDEFGH", 1000)) // 8000 字节，远超 maxLen
	c := newCtxWithBody(body)

	logCopy := readBodyBounded(c, maxLen)

	// 日志副本封顶
	if len(logCopy) != maxLen {
		t.Fatalf("log copy len = %d, want %d", len(logCopy), maxLen)
	}

	// 下游必须拿到完整原始 body
	restored, err := io.ReadAll(c.Request.Body)
	if err != nil {
		t.Fatalf("read restored body: %v", err)
	}
	if !bytes.Equal(restored, body) {
		t.Fatalf("downstream body corrupted: got len=%d, want len=%d", len(restored), len(body))
	}
}

// TestReadBodyBounded_PreservesSmallBodyExactly 小于上限时日志副本与原始一致。
func TestReadBodyBounded_PreservesSmallBodyExactly(t *testing.T) {
	body := []byte(`{"user":"alice","password":"secret"}`)
	c := newCtxWithBody(body)

	got := readBodyBounded(c, 1024)
	if !bytes.Equal(got, body) {
		t.Fatalf("log copy = %q, want %q", got, body)
	}

	restored, _ := io.ReadAll(c.Request.Body)
	if !bytes.Equal(restored, body) {
		t.Fatalf("downstream body = %q, want %q", restored, body)
	}
}

// TestBodyLogWriter_Bounded 复现 H3 响应侧：响应体捕获缓冲区必须封顶，
// 完整响应仍写入下游 ResponseWriter。
func TestBodyLogWriter_Bounded(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const maxLen = 32

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	w := &bodyLogWriter{
		ResponseWriter: c.Writer,
		body:           new(bytes.Buffer),
		maxLen:         maxLen,
	}

	large := bytes.Repeat([]byte("Z"), 10_000)
	n, err := w.Write(large)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if n != len(large) {
		t.Fatalf("Write returned %d, want %d (下游必须收到完整响应)", n, len(large))
	}
	if w.body.Len() > maxLen {
		t.Fatalf("captured buffer len = %d, must be <= %d (OOM 防护失效)", w.body.Len(), maxLen)
	}
	if w.body.Len() != maxLen {
		t.Fatalf("captured buffer len = %d, want exactly %d", w.body.Len(), maxLen)
	}
	// 下游 ResponseWriter 收到完整内容
	if rec.Body.Len() != len(large) {
		t.Fatalf("downstream response len = %d, want %d", rec.Body.Len(), len(large))
	}
}

// TestBodyLogWriter_NoLimit maxLen<=0 时不限制（向后兼容）。
func TestBodyLogWriter_NoLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	w := &bodyLogWriter{
		ResponseWriter: c.Writer,
		body:           new(bytes.Buffer),
		maxLen:         0,
	}
	data := []byte("hello world")
	if _, err := w.Write(data); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !bytes.Equal(w.body.Bytes(), data) {
		t.Fatalf("captured = %q, want %q", w.body.Bytes(), data)
	}
}

// TestBodyLogWriter_MultiWriteAccumulation 多次小写累积不超过 maxLen，
// 下游仍收到完整拼接结果。
func TestBodyLogWriter_MultiWriteAccumulation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const maxLen = 32
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	w := &bodyLogWriter{
		ResponseWriter: c.Writer,
		body:           new(bytes.Buffer),
		maxLen:         maxLen,
	}
	chunks := [][]byte{[]byte("0123456789"), []byte("abcdefghij"), []byte("ABCDEFGHIJ"), []byte("zzzzzzzzzz")}
	var downstream bytes.Buffer
	for _, ch := range chunks {
		n, err := w.Write(ch)
		if err != nil || n != len(ch) {
			t.Fatalf("Write chunk %q: n=%d err=%v", ch, n, err)
		}
		downstream.Write(ch)
	}
	if w.body.Len() > maxLen {
		t.Fatalf("captured len = %d, must be <= %d", w.body.Len(), maxLen)
	}
	if w.body.Len() != maxLen {
		t.Fatalf("captured len = %d, want exactly %d", w.body.Len(), maxLen)
	}
	if !bytes.Equal(rec.Body.Bytes(), downstream.Bytes()) {
		t.Fatalf("downstream = %q, want %q", rec.Body.Bytes(), downstream.Bytes())
	}
}

// TestBodyLogWriter_WriteStringBounded WriteString 路径同样封顶。
func TestBodyLogWriter_WriteStringBounded(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const maxLen = 16
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	w := &bodyLogWriter{
		ResponseWriter: c.Writer,
		body:           new(bytes.Buffer),
		maxLen:         maxLen,
	}
	large := strings.Repeat("S", 500)
	n, err := w.WriteString(large)
	if err != nil {
		t.Fatalf("WriteString: %v", err)
	}
	if n != len(large) {
		t.Fatalf("WriteString returned %d, want %d", n, len(large))
	}
	if w.body.Len() > maxLen {
		t.Fatalf("captured len = %d, must be <= %d", w.body.Len(), maxLen)
	}
	if rec.Body.Len() != len(large) {
		t.Fatalf("downstream len = %d, want %d", rec.Body.Len(), len(large))
	}
}

// TestFilterSensitiveQuery P1 #14：query 中的敏感参数值必须脱敏，非敏感原样保留。
func TestFilterSensitiveQuery(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		contains string // 期望结果包含
		absent   string // 期望结果不含（敏感原值）
	}{
		{"empty", "", "", ""},
		{"no sensitive", "page=1&size=20", "page=1", ""},
		{"access_token redacted", "access_token=supersecret123&page=1", "FILTERED", "supersecret123"},
		{"password redacted", "user=alice&password=hunter2", "FILTERED", "hunter2"},
		{"case insensitive key", "Token=abc.def.ghi", "FILTERED", "abc.def.ghi"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := filterSensitiveQuery(tc.in)
			if tc.contains != "" && !strings.Contains(got, tc.contains) {
				t.Errorf("filterSensitiveQuery(%q) = %q, want contains %q", tc.in, got, tc.contains)
			}
			if tc.absent != "" && strings.Contains(got, tc.absent) {
				t.Errorf("filterSensitiveQuery(%q) = %q, must NOT contain sensitive value %q", tc.in, got, tc.absent)
			}
		})
	}
}

// TestLoggerWithConfig_NormalizesMaxBodyLength MaxBodyLength<=0 时归一化为默认值，
// 确保响应侧捕获缓冲区仍有上限（H3 复审 MEDIUM：消除请求/响应侧 maxLen<=0 不对称）。
func TestLoggerWithConfig_NormalizesMaxBodyLength(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	cfg := LoggerConfig{
		LogRequestBody:       true,
		LogResponseBody:      true,
		MaxBodyLength:        0, // 手滑置 0，应被归一化为默认 1024
		SkipPaths:            []string{},
		SkipPathPrefixes:     []string{},
		SlowRequestThreshold: 500 * time.Millisecond,
	}
	r.Use(LoggerWithConfig(cfg))
	r.POST("/", func(c *gin.Context) {
		body, _ := io.ReadAll(c.Request.Body)
		c.Data(200, "text/plain", body)
	})

	// 请求体 5000 字节（> 默认 1024），下游应仍得完整 body
	reqBody := bytes.Repeat([]byte("a"), 5000)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", bytes.NewReader(reqBody))
	r.ServeHTTP(w, req)

	if w.Body.Len() != len(reqBody) {
		t.Fatalf("downstream body len = %d, want %d (请求体必须完整复原)", w.Body.Len(), len(reqBody))
	}
	if !bytes.Equal(w.Body.Bytes(), reqBody) {
		t.Fatalf("downstream body corrupted")
	}
}

func TestLoggerSkipPathPrefixesAnchorsBoundary_M8(t *testing.T) {
	cfg := LoggerConfig{SkipPathPrefixes: []string{"/api"}}
	if !shouldSkipPath("/api", cfg) {
		t.Fatal("/api should be skipped")
	}
	if !shouldSkipPath("/api/users", cfg) {
		t.Fatal("/api/users should be skipped")
	}
	if shouldSkipPath("/api2/users", cfg) {
		t.Fatal("/api2/users should not be skipped by /api prefix")
	}
}
