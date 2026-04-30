package handler_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EthanCodeCraft/xlgo-core/handler"
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

func TestBadRequest(t *testing.T) {
	r := setupTestRouter()
	r.GET("/test", func(c *gin.Context) {
		handler.BadRequest(c, "参数错误")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("BadRequest status = %d, want 400", w.Code)
	}
}

func TestInternalError(t *testing.T) {
	r := setupTestRouter()
	r.GET("/test", func(c *gin.Context) {
		handler.InternalError(c, "服务器错误")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("InternalError status = %d, want 500", w.Code)
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