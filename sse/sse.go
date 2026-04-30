package sse

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
)

// SSEWriter SSE 写入器
type SSEWriter struct {
	writer  gin.ResponseWriter
	flusher http.Flusher
}

// NewSSEWriter 创建 SSE 写入器
func NewSSEWriter(c *gin.Context) (*SSEWriter, error) {
	// 设置 SSE 必要的响应头
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Transfer-Encoding", "chunked")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("响应写入器不支持 flushing")
	}

	return &SSEWriter{
		writer:  c.Writer,
		flusher: flusher,
	}, nil
}

// WriteEvent 写入 SSE 事件
// 格式: event: <event>\ndata: <data>\n\n
func (w *SSEWriter) WriteEvent(event, data string) error {
	fmt.Fprintf(w.writer, "event: %s\n", event)
	fmt.Fprintf(w.writer, "data: %s\n\n", data)
	w.flusher.Flush()
	return nil
}

// WriteMessage 写入消息（无事件类型）
// 格式: data: <data>\n\n
func (w *SSEWriter) WriteMessage(data string) error {
	fmt.Fprintf(w.writer, "data: %s\n\n", data)
	w.flusher.Flush()
	return nil
}

// WriteJSON 写入 JSON 数据
func (w *SSEWriter) WriteJSON(event string, data any) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return w.WriteEvent(event, string(jsonData))
}

// WriteError 写入错误事件
func (w *SSEWriter) WriteError(err error) error {
	return w.WriteJSON("error", gin.H{"error": err.Error()})
}

// WriteDone 写入完成事件
func (w *SSEWriter) WriteDone() error {
	return w.WriteEvent("done", "")
}

// KeepAlive 发送保持连接的心跳
func (w *SSEWriter) KeepAlive() error {
	return w.WriteMessage("")
}

// Stream 流式发送数据
func (w *SSEWriter) Stream(event string, ch <-chan any) error {
	for data := range ch {
		if err := w.WriteJSON(event, data); err != nil {
			return err
		}
	}
	return w.WriteDone()
}

// SSE 中间件，设置必要的响应头
func SSE() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Next()
	}
}

// StreamText 流式发送文本（适用于 AI 对话场景）
func StreamText(c *gin.Context, ch <-chan string) error {
	writer, err := NewSSEWriter(c)
	if err != nil {
		return err
	}

	for text := range ch {
		if err := writer.WriteJSON("message", gin.H{"text": text}); err != nil {
			return err
		}
	}

	return writer.WriteDone()
}

// StreamChunks 流式发送文本块（带增量标记）
func StreamChunks(c *gin.Context, ch <-chan string) error {
	writer, err := NewSSEWriter(c)
	if err != nil {
		return err
	}

	for chunk := range ch {
		if err := writer.WriteJSON("chunk", gin.H{"delta": chunk}); err != nil {
			return err
		}
	}

	return writer.WriteJSON("done", gin.H{"finished": true})
}

// StreamWithID 流式发送带消息 ID 的数据
func StreamWithID(c *gin.Context, messageID string, ch <-chan string) error {
	writer, err := NewSSEWriter(c)
	if err != nil {
		return err
	}

	// 发送开始事件
	if err := writer.WriteJSON("start", gin.H{"id": messageID}); err != nil {
		return err
	}

	// 发送内容块
	for chunk := range ch {
		if err := writer.WriteJSON("chunk", gin.H{"id": messageID, "delta": chunk}); err != nil {
			return err
		}
	}

	// 发送完成事件
	return writer.WriteJSON("done", gin.H{"id": messageID, "finished": true})
}
