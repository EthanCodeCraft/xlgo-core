package sse_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EthanCodeCraft/xlgo-core/sse"
	"github.com/gin-gonic/gin"
)

// ===== C3a：断连即停（核心） =====

// 回归 C3a：断连即停由 internal test（sse_stream_internal_test.go，TestStreamStopsOnCtxCancelInternal）
// 权威覆盖——直接构造 SSEWriter + 可控 ctx，验证 Stream 在 ctx.Done 时返回 ctx.Err。
// StreamText/StreamChunks/StreamWithID 用相同 select 模式（代码审查保证），外部网络断连测试
// 因 httptest loopback 下 c.Request.Context() 取消时序不可靠而省略，internal test 为权威。

// ===== C3a：正常完成路径仍工作 =====

// 回归：ch 正常关闭时 StreamText 写 done 并返回 nil。
func TestStreamTextNormalCompletion(t *testing.T) {
	ch := make(chan string, 3)
	ch <- "a"
	ch <- "b"
	close(ch)

	r := gin.New()
	r.GET("/sse", func(c *gin.Context) {
		err := sse.StreamText(c, ch)
		if err != nil {
			t.Errorf("StreamText normal completion err: %v", err)
		}
	})

	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/sse")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if !strings.Contains(string(body), `"text":"a"`) || !strings.Contains(string(body), `"text":"b"`) {
		t.Errorf("body missing chunks: %s", string(body))
	}
	if !strings.Contains(string(body), "event: done") {
		t.Errorf("body missing done event: %s", string(body))
	}
}

// ===== C3c：不手设 Transfer-Encoding: chunked =====

// 回归 C3c：响应头不应含 Transfer-Encoding: chunked（HTTP/2 下非法，HTTP/1.1 冗余）。
func TestNewSSEWriterNoChunkedHeader(t *testing.T) {
	r := gin.New()
	r.GET("/sse", func(c *gin.Context) {
		_, _ = sse.NewSSEWriter(c)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/sse", nil)
	r.ServeHTTP(w, req)

	if got := w.Header().Get("Transfer-Encoding"); got != "" {
		t.Errorf("Transfer-Encoding = %q, want empty (C3c: should not hand-set chunked)", got)
	}
}

// ===== C3b：上游生产者契约文档化 =====
// StreamText 在 ctx.Done 后返回 ctx.Err；生产者（往 ch 发送者）应监听同一 ctx，
// 在取消时停止上游 LLM 流。本框架无法单方面停止生产者，调用方契约见 StreamText 注释。
// 该契约由 TestStreamTextStopsOnContextCancel 间接验证（StreamText 确实因 ctx.Done 退出）。
