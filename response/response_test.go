package response_test

import (
	"net/http/httptest"
	"testing"

	"github.com/EthanCodeCraft/xlgo-core/response"
	"github.com/gin-gonic/gin"
)

func setupTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	return r
}

func TestSuccess(t *testing.T) {
	r := setupTestRouter()
	r.GET("/test", func(c *gin.Context) {
		response.Success(c, gin.H{"message": "ok"})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("Success status = %d", w.Code)
	}

	// 验证响应体包含 code=1
	body := w.Body.String()
	if !contains(body, `"code":1`) {
		t.Errorf("Success body should contain code:1, got %s", body)
	}
}

func TestSuccessWithMsg(t *testing.T) {
	r := setupTestRouter()
	r.GET("/test", func(c *gin.Context) {
		response.SuccessWithMsg(c, "操作成功", gin.H{"id": 1})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("SuccessWithMsg status = %d", w.Code)
	}
}

func TestFail(t *testing.T) {
	r := setupTestRouter()
	r.GET("/test", func(c *gin.Context) {
		response.Fail(c, "参数错误")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("Fail status = %d", w.Code)
	}

	body := w.Body.String()
	if !contains(body, `"code":0`) {
		t.Errorf("Fail body should contain code:0, got %s", body)
	}
}

func TestFailWithCode(t *testing.T) {
	r := setupTestRouter()
	r.GET("/test", func(c *gin.Context) {
		response.FailWithCode(c, response.CodeUnauthorized, "未授权")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("FailWithCode status = %d", w.Code)
	}
}

func TestUnauthorized(t *testing.T) {
	r := setupTestRouter()
	r.GET("/test", func(c *gin.Context) {
		response.Unauthorized(c, "请先登录")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("Unauthorized status = %d", w.Code)
	}
}

func TestNotFound(t *testing.T) {
	r := setupTestRouter()
	r.GET("/test", func(c *gin.Context) {
		response.NotFound(c, "资源不存在")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("NotFound status = %d", w.Code)
	}
}

func TestServerError(t *testing.T) {
	r := setupTestRouter()
	r.GET("/test", func(c *gin.Context) {
		response.ServerError(c, "服务器错误")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("ServerError status = %d", w.Code)
	}
}

func TestRateLimit(t *testing.T) {
	r := setupTestRouter()
	r.GET("/test", func(c *gin.Context) {
		response.RateLimit(c)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("RateLimit status = %d", w.Code)
	}

	body := w.Body.String()
	if !contains(body, `"code":`) {
		t.Errorf("RateLimit body should contain code, got %s", body)
	}
}

func TestPage(t *testing.T) {
	r := setupTestRouter()
	r.GET("/test", func(c *gin.Context) {
		items := []string{"a", "b", "c"}
		response.Page(c, items, 100, 1, 20)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("Page status = %d", w.Code)
	}

	body := w.Body.String()
	if !contains(body, `"total":100`) {
		t.Errorf("Page body should contain total:100, got %s", body)
	}
}

func TestDownload(t *testing.T) {
	r := setupTestRouter()
	r.GET("/test", func(c *gin.Context) {
		data := []byte("test file content")
		response.Download(c, "test.txt", data)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("Download status = %d", w.Code)
	}

	// 验证响应头
	contentType := w.Header().Get("Content-Type")
	if contentType != "application/octet-stream" {
		t.Errorf("Download Content-Type = %s, want application/octet-stream", contentType)
	}

	disposition := w.Header().Get("Content-Disposition")
	if !contains(disposition, "test.txt") {
		t.Errorf("Download Content-Disposition should contain filename, got %s", disposition)
	}
}

func TestDownloadWithContentType(t *testing.T) {
	r := setupTestRouter()
	r.GET("/test", func(c *gin.Context) {
		data := []byte("test file content")
		response.DownloadWithContentType(c, "test.pdf", "application/pdf", data)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("DownloadWithContentType status = %d", w.Code)
	}

	contentType := w.Header().Get("Content-Type")
	if contentType != "application/pdf" {
		t.Errorf("DownloadWithContentType = %s, want application/pdf", contentType)
	}
}

func TestHTML(t *testing.T) {
	r := setupTestRouter()
	r.GET("/test", func(c *gin.Context) {
		response.HTML(c, "<html><body>Hello</body></html>")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("HTML status = %d", w.Code)
	}

	contentType := w.Header().Get("Content-Type")
	if !contains(contentType, "text/html") {
		t.Errorf("HTML Content-Type should be text/html, got %s", contentType)
	}
}

func TestRedirect(t *testing.T) {
	r := setupTestRouter()
	r.GET("/test", func(c *gin.Context) {
		response.Redirect(c, 302, "/new-location")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	// 重定向返回 302
	if w.Code != 302 {
		t.Errorf("Redirect status = %d, want 302", w.Code)
	}

	location := w.Header().Get("Location")
	if location != "/new-location" {
		t.Errorf("Redirect Location = %s, want /new-location", location)
	}
}

func TestErrorCodes(t *testing.T) {
	// 测试错误码定义
	if response.CodeSuccess != 1 {
		t.Errorf("CodeSuccess = %d, want 1", response.CodeSuccess)
	}
	if response.CodeFail != 0 {
		t.Errorf("CodeFail = %d, want 0", response.CodeFail)
	}
	if response.CodeUnauthorized == 0 {
		t.Error("CodeUnauthorized should not be 0")
	}
	if response.CodeNotFound == 0 {
		t.Error("CodeNotFound should not be 0")
	}
	if response.CodeServerError == 0 {
		t.Error("CodeServerError should not be 0")
	}
	if response.CodeRateLimit == 0 {
		t.Error("CodeRateLimit should not be 0")
	}
}

func TestResponseStructure(t *testing.T) {
	// 测试 Response 结构体
	resp := response.Response{
		Code:      1,
		Msg:       "成功",
		Data:      gin.H{"id": 1},
		RequestID: "test-123",
	}

	if resp.Code != 1 {
		t.Error("Response Code failed")
	}
	if resp.Msg != "成功" {
		t.Error("Response Msg failed")
	}
}

func TestPageDataStructure(t *testing.T) {
	// 测试 PageData 结构体
	data := response.PageData{
		Items:    []string{"a", "b"},
		Total:    100,
		Page:     1,
		PageSize: 20,
	}

	if data.Total != 100 {
		t.Error("PageData Total failed")
	}
}

func contains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}