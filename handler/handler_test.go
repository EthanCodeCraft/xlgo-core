package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EthanCodeCraft/xlgo-core/handler"
	"github.com/EthanCodeCraft/xlgo-core/response"
	"github.com/gin-gonic/gin"
)

func setupTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	return r
}

func TestHealthCheck(t *testing.T) {
	r := setupTestRouter()
	r.GET("/health", handler.HealthCheck)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/health", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("HealthCheck status = %d, want 200", w.Code)
	}
}

// TestHealthCheckSchemaConverged_H8d：handler.HealthCheck 响应体应与
// router.RegisterHealthRoute 收敛为同一 schema（200 + {"status":"ok"}），
// 不再走 response 业务信封 {code,msg,data}。
func TestHealthCheckSchemaConverged_H8d(t *testing.T) {
	r := setupTestRouter()
	r.GET("/health", handler.HealthCheck)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/health", nil))

	body := w.Body.String()
	if !strings.Contains(body, `"status":"ok"`) {
		t.Fatalf("HealthCheck body = %s, want {\"status\":\"ok\"}", body)
	}
	if strings.Contains(body, `"code"`) || strings.Contains(body, `"data"`) {
		t.Fatalf("HealthCheck should not use response envelope, got %s", body)
	}
}

func TestQueryInt(t *testing.T) {
	r := setupTestRouter()
	r.GET("/test", func(c *gin.Context) {
		page := handler.QueryInt(c, "page", 1)
		c.JSON(200, gin.H{"page": page})
	})

	tests := []struct {
		query    string
		expected int
	}{
		{"?page=10", 10},
		{"?page=abc", 1},  // 无效返回默认
		{"", 1},           // 无参数返回默认
	}

	for _, tt := range tests {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/test"+tt.query, nil)
		r.ServeHTTP(w, req)

		// 验证响应包含正确的值
		if w.Code != 200 {
			t.Errorf("QueryInt status = %d", w.Code)
		}
	}
}

func TestQueryInt64(t *testing.T) {
	r := setupTestRouter()
	r.GET("/test", func(c *gin.Context) {
		id := handler.QueryInt64(c, "id", 0)
		c.JSON(200, gin.H{"id": id})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test?id=1234567890123", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("QueryInt64 status = %d", w.Code)
	}
}

func TestQueryFloat64(t *testing.T) {
	r := setupTestRouter()
	r.GET("/test", func(c *gin.Context) {
		price := handler.QueryFloat64(c, "price", 0.0)
		c.JSON(200, gin.H{"price": price})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test?price=99.99", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("QueryFloat64 status = %d", w.Code)
	}
}

func TestQueryBool(t *testing.T) {
	r := setupTestRouter()
	r.GET("/test", func(c *gin.Context) {
		enabled := handler.QueryBool(c, "enabled", false)
		c.JSON(200, gin.H{"enabled": enabled})
	})

	tests := []struct {
		query    string
		expected bool
	}{
		{"?enabled=true", true},
		{"?enabled=1", true},
		{"?enabled=yes", true},
		{"?enabled=false", false},
		{"?enabled=0", false},
		{"", false}, // 默认值
	}

	for _, tt := range tests {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/test"+tt.query, nil)
		r.ServeHTTP(w, req)

		if w.Code != 200 {
			t.Errorf("QueryBool status = %d", w.Code)
		}
	}
}

func TestPathInt(t *testing.T) {
	r := setupTestRouter()
	r.GET("/user/:id", func(c *gin.Context) {
		id := handler.PathInt(c, "id", 0)
		c.JSON(200, gin.H{"id": id})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/user/123", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("PathInt status = %d", w.Code)
	}
}

func TestPathInt64(t *testing.T) {
	r := setupTestRouter()
	r.GET("/user/:id", func(c *gin.Context) {
		id := handler.PathInt64(c, "id", 0)
		c.JSON(200, gin.H{"id": id})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/user/1234567890123", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("PathInt64 status = %d", w.Code)
	}
}

func TestGetPage(t *testing.T) {
	r := setupTestRouter()
	r.GET("/list", func(c *gin.Context) {
		page, pageSize := handler.GetPage(c)
		c.JSON(200, gin.H{"page": page, "pageSize": pageSize})
	})

	tests := []struct {
		query        string
		expectedPage int
		expectedSize int
	}{
		{"?page=2&page_size=50", 2, 50},
		{"?page=0&page_size=0", 1, 20},  // 边界修正
		{"?page=-1&page_size=200", 1, 100}, // 超限修正
		{"", 1, 20}, // 默认值
	}

	for _, tt := range tests {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/list"+tt.query, nil)
		r.ServeHTTP(w, req)

		if w.Code != 200 {
			t.Errorf("GetPage status = %d", w.Code)
		}
	}
}

func TestGetIDFromPath(t *testing.T) {
	r := setupTestRouter()
	r.GET("/user/:id", func(c *gin.Context) {
		id, ok := handler.GetIDFromPath(c, "id")
		c.JSON(200, gin.H{"id": id, "ok": ok})
	})

	// 有效 ID
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/user/123", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("GetIDFromPath status = %d", w.Code)
	}

	// 无效 ID
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/user/abc", nil)
	r.ServeHTTP(w2, req2)

	if w2.Code != 200 {
		t.Errorf("GetIDFromPath invalid status = %d", w2.Code)
	}
}

// withResponseMode 切换响应模式并在测试结束后恢复为默认 ModeBusiness。
func withResponseMode(t *testing.T, m response.Mode) {
	t.Helper()
	response.SetMode(m)
	t.Cleanup(func() { response.SetMode(response.ModeBusiness) })
}

// decodeBody 解析统一响应体。
func decodeBody(t *testing.T, w *httptest.ResponseRecorder) response.Response {
	t.Helper()
	var body response.Response
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v (body=%q)", err, w.Body.String())
	}
	return body
}

func TestBadRequest(t *testing.T) {
	// 默认 ModeBusiness：所有失败响应 HTTP 200，错误经 body code 表达。
	withResponseMode(t, response.ModeBusiness)
	r := setupTestRouter()
	r.GET("/test", func(c *gin.Context) {
		handler.BadRequest(c, "参数错误")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("BadRequest (ModeBusiness) status = %d, want 200", w.Code)
	}
	body := decodeBody(t, w)
	if body.Code != response.CodeFail {
		t.Errorf("BadRequest code = %d, want %d", body.Code, response.CodeFail)
	}
	if body.Msg != "参数错误" {
		t.Errorf("BadRequest msg = %q, want %q", body.Msg, "参数错误")
	}
}

func TestInternalError(t *testing.T) {
	// 默认 ModeBusiness：服务器错误也走 HTTP 200，错误经 body code 表达。
	withResponseMode(t, response.ModeBusiness)
	r := setupTestRouter()
	r.GET("/test", func(c *gin.Context) {
		handler.InternalError(c, "服务器错误")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("InternalError (ModeBusiness) status = %d, want 200", w.Code)
	}
	body := decodeBody(t, w)
	if body.Code != response.CodeServerError {
		t.Errorf("InternalError code = %d, want %d", body.Code, response.CodeServerError)
	}
}

// TestBadRequestRESTMode：ModeREST 下 CodeFail 属业务失败不映射 HTTP 错误，仍 200。
// 修复前 BadRequest 硬编 400 绕过 Mode；修复后委托 response.FailWithCode 遵循 Mode。
func TestBadRequestRESTMode(t *testing.T) {
	withResponseMode(t, response.ModeREST)
	r := setupTestRouter()
	r.GET("/test", func(c *gin.Context) {
		handler.BadRequest(c, "参数错误")
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/test", nil))

	if w.Code != http.StatusOK {
		t.Errorf("BadRequest (ModeREST) status = %d, want 200 (CodeFail 不映射)", w.Code)
	}
	body := decodeBody(t, w)
	if body.Code != response.CodeFail {
		t.Errorf("BadRequest code = %d, want %d", body.Code, response.CodeFail)
	}
}

// TestInternalErrorRESTMode：ModeREST 下 CodeServerError 映射 HTTP 500。
func TestInternalErrorRESTMode(t *testing.T) {
	withResponseMode(t, response.ModeREST)
	r := setupTestRouter()
	r.GET("/test", func(c *gin.Context) {
		handler.InternalError(c, "服务器错误")
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/test", nil))

	if w.Code != http.StatusInternalServerError {
		t.Errorf("InternalError (ModeREST) status = %d, want 500", w.Code)
	}
	body := decodeBody(t, w)
	if body.Code != response.CodeServerError {
		t.Errorf("InternalError code = %d, want %d", body.Code, response.CodeServerError)
	}
}

// TestBadRequestWritesRequestID：修复前 BadRequest 直接 c.JSON 不写 RequestID（丢链路）；
// 修复后委托 response 体系，经 writeResp 写入上下文中的 request_id。
func TestBadRequestWritesRequestID(t *testing.T) {
	r := setupTestRouter()
	r.Use(func(c *gin.Context) { c.Set("request_id", "req-123"); c.Next() })
	r.GET("/test", func(c *gin.Context) {
		handler.BadRequest(c, "参数错误")
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/test", nil))

	body := decodeBody(t, w)
	if body.RequestID != "req-123" {
		t.Errorf("BadRequest request_id = %q, want %q (修复前为空)", body.RequestID, "req-123")
	}
}

// TestInternalErrorWritesRequestID：同上，覆盖 InternalError。
func TestInternalErrorWritesRequestID(t *testing.T) {
	r := setupTestRouter()
	r.Use(func(c *gin.Context) { c.Set("request_id", "req-456"); c.Next() })
	r.GET("/test", func(c *gin.Context) {
		handler.InternalError(c, "服务器错误")
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/test", nil))

	body := decodeBody(t, w)
	if body.RequestID != "req-456" {
		t.Errorf("InternalError request_id = %q, want %q (修复前为空)", body.RequestID, "req-456")
	}
}

func TestBindJSON(t *testing.T) {
	r := setupTestRouter()
	r.POST("/test", func(c *gin.Context) {
		var req struct {
			Name string `json:"name"`
		}
		if err := handler.BindJSON(c, &req); err != nil {
			handler.BadRequest(c, "绑定失败")
			return
		}
		c.JSON(200, req)
	})

	// 有效 JSON
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/test", strings.NewReader(`{"name":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("BindJSON valid status = %d", w.Code)
	}
}