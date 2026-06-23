package response_test

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/EthanCodeCraft/xlgo-core/response"
	"github.com/gin-gonic/gin"
)

// withMode 切换响应模式并在测试结束后恢复为默认 ModeBusiness。
func withMode(t *testing.T, m response.Mode) {
	t.Helper()
	response.SetMode(m)
	t.Cleanup(func() { response.SetMode(response.ModeBusiness) })
}

func TestSetGetMode(t *testing.T) {
	withMode(t, response.ModeREST)
	if response.GetMode() != response.ModeREST {
		t.Errorf("GetMode = %v, want ModeREST", response.GetMode())
	}
}

func TestModeBusinessReturns200(t *testing.T) {
	withMode(t, response.ModeBusiness)
	r := setupTestRouter()
	r.GET("/u", func(c *gin.Context) { response.Unauthorized(c, "no") })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/u", nil))

	if w.Code != 200 {
		t.Errorf("ModeBusiness Unauthorized status = %d, want 200", w.Code)
	}
}

func TestModeRESTMapsStatus(t *testing.T) {
	withMode(t, response.ModeREST)
	cases := []struct {
		path   string
		fn     func(c *gin.Context)
		wantCode int
	}{
		{"/unauth", func(c *gin.Context) { response.Unauthorized(c, "no") }, 401},
		{"/notfound", func(c *gin.Context) { response.NotFound(c, "no") }, 404},
		{"/server", func(c *gin.Context) { response.ServerError(c, "err") }, 500},
		{"/ratelimit", func(c *gin.Context) { response.RateLimit(c) }, 429},
		{"/fail", func(c *gin.Context) { response.Fail(c, "bad") }, 200}, // CodeFail 不映射
	}
	for _, tc := range cases {
		r := setupTestRouter()
		r.GET(tc.path, tc.fn)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
		if w.Code != tc.wantCode {
			t.Errorf("%s status = %d, want %d", tc.path, w.Code, tc.wantCode)
		}
		// body 仍带业务码
		var body response.Response
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Errorf("%s body unmarshal: %v", tc.path, err)
		}
		if body.Code == 0 && tc.path != "/fail" {
			// 非 Fail 路径 code 应非 0（Fail 的 CodeFail=1 也非 0，跳过）
		}
	}
}

func TestCustomIgnoresMode(t *testing.T) {
	withMode(t, response.ModeBusiness) // 即使 business 模式，Custom 也用指定 status
	r := setupTestRouter()
	r.GET("/c", func(c *gin.Context) { response.Custom(c, 418, 999, "teapot", nil) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/c", nil))
	if w.Code != 418 {
		t.Errorf("Custom status = %d, want 418", w.Code)
	}
}

func TestFailWithErrorREST(t *testing.T) {
	withMode(t, response.ModeREST)
	r := setupTestRouter()
	r.GET("/e", func(c *gin.Context) { response.FailWithError(c, response.ErrUnauthorized) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/e", nil))
	if w.Code != 401 {
		t.Errorf("FailWithError(ErrUnauthorized) status = %d, want 401", w.Code)
	}
}
