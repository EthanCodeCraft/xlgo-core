package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/EthanCodeCraft/xlgo-core/middleware"
	"github.com/gin-gonic/gin"
)

func TestTimeoutAllowsFastHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.Timeout(time.Second))
	r.GET("/ok", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/ok", nil))
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

func TestTimeoutCancelsSlowHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.Timeout(50 * time.Millisecond))
	r.GET("/slow", func(c *gin.Context) {
		select {
		case <-c.Request.Context().Done():
			// 超时取消生效
			return
		case <-time.After(time.Second):
			t.Error("handler should have been cancelled by timeout")
		}
	})

	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		r.ServeHTTP(w, httptest.NewRequest("GET", "/slow", nil))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("request did not return in time")
	}
}

func TestTimeoutZeroDisables(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.Timeout(0)) // 不启用
	ctxCancelled := false
	r.GET("/x", func(c *gin.Context) {
		ctxCancelled = c.Request.Context() == context.Background()
		_ = ctxCancelled
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/x", nil))
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}
