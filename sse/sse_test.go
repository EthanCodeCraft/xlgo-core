package sse_test

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EthanCodeCraft/xlgo-core/sse"
	"github.com/gin-gonic/gin"
)

func setupTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	return r
}

func TestNewSSEWriter(t *testing.T) {
	r := setupTestRouter()
	r.GET("/sse", func(c *gin.Context) {
		writer, err := sse.NewSSEWriter(c)
		if err != nil {
			t.Errorf("NewSSEWriter error: %v", err)
			return
		}

		if writer == nil {
			t.Error("NewSSEWriter should not return nil")
		}
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/sse", nil)
	r.ServeHTTP(w, req)
}

func TestSSEWriterWriteEvent(t *testing.T) {
	r := setupTestRouter()
	r.GET("/sse", func(c *gin.Context) {
		writer, _ := sse.NewSSEWriter(c)
		writer.WriteEvent("message", "test data")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/sse", nil)
	r.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "event: message") {
		t.Errorf("WriteEvent body should contain 'event: message', got %s", body)
	}
	if !strings.Contains(body, "data: test data") {
		t.Errorf("WriteEvent body should contain 'data: test data', got %s", body)
	}
}

func TestSSEWriterWriteMessage(t *testing.T) {
	r := setupTestRouter()
	r.GET("/sse", func(c *gin.Context) {
		writer, _ := sse.NewSSEWriter(c)
		writer.WriteMessage("hello world")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/sse", nil)
	r.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "data: hello world") {
		t.Errorf("WriteMessage body should contain data, got %s", body)
	}
}

func TestSSEWriterWriteJSON(t *testing.T) {
	r := setupTestRouter()
	r.GET("/sse", func(c *gin.Context) {
		writer, _ := sse.NewSSEWriter(c)
		writer.WriteJSON("message", gin.H{"text": "hello"})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/sse", nil)
	r.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "event: message") {
		t.Error("WriteJSON should set event type")
	}
	if !strings.Contains(body, `"text":"hello"`) {
		t.Error("WriteJSON should contain JSON data")
	}
}

func TestSSEMiddleware(t *testing.T) {
	r := setupTestRouter()
	r.Use(sse.SSE())
	r.GET("/test", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	// 验证 SSE 响应头
	contentType := w.Header().Get("Content-Type")
	if contentType != "text/event-stream" {
		t.Errorf("SSE Content-Type = %s, want text/event-stream", contentType)
	}

	cacheControl := w.Header().Get("Cache-Control")
	if cacheControl != "no-cache" {
		t.Errorf("SSE Cache-Control = %s, want no-cache", cacheControl)
	}
}

func TestSSEHeaders(t *testing.T) {
	r := setupTestRouter()
	r.GET("/sse", func(c *gin.Context) {
		writer, err := sse.NewSSEWriter(c)
		if err != nil {
			t.Errorf("NewSSEWriter error: %v", err)
			return
		}
		writer.WriteMessage("test")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/sse", nil)
	r.ServeHTTP(w, req)

	// 验证必要响应头
	if w.Header().Get("Content-Type") != "text/event-stream" {
		t.Error("Content-Type should be text/event-stream")
	}
	if w.Header().Get("Cache-Control") != "no-cache" {
		t.Error("Cache-Control should be no-cache")
	}
	if w.Header().Get("Connection") != "keep-alive" {
		t.Error("Connection should be keep-alive")
	}
}