// Package sse 提供 Server-Sent Events 流式响应支持，典型用于 AI 对话/LLM 流式输出。
//
// 断连契约（C3 收尾，重要）：
// 框架侧的消费循环（Stream/StreamText/StreamChunks/StreamWithID）已监听 c.Request.Context()，
// 客户端断连即退出，消费端不泄漏 goroutine。但框架无法单方面停止上游生产者（LLM 流）——
// 生产者（向 ch 发送数据的一方）必须自行监听同一 ctx 并在取消时停止生产，否则：
//   - 上游 LLM 流在客户端已断开后仍持续运行（浪费算力/费用）；
//   - 生产者向已无人消费的 ch 发送可能阻塞（无界 ch）或丢弃（有界 ch）。
//
// 推荐写法：生产者 goroutine 内 select { case <-ctx.Done(): return; case ...: ch <- token }，
// 其中 ctx 取自请求的 c.Request.Context()（Stream 系列内部用的就是它）。
package sse

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
)

// SSEWriter SSE 写入器
type SSEWriter struct {
	writer  gin.ResponseWriter
	flusher http.Flusher
	ctx     context.Context // 请求上下文，用于 Stream 系列监听断连（C3a）
}

// NewSSEWriter 创建 SSE 写入器
func NewSSEWriter(c *gin.Context) (*SSEWriter, error) {
	// 设置 SSE 必要的响应头
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	// 不手设 Transfer-Encoding: chunked——HTTP/1.1 下由 server 自动分帧，
	// HTTP/2 下该头非法会致协议错误（C3c 修复）。

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("响应写入器不支持 flushing")
	}

	return &SSEWriter{
		writer:  c.Writer,
		flusher: flusher,
		ctx:     c.Request.Context(),
	}, nil
}

// WriteEvent 写入 SSE 事件
// 格式: event: <event>\ndata: <data>\n\n
//
// 写错误向上传播（C3b 修复）：旧实现丢弃 fmt.Fprintf 错误且恒 return nil，
// 导致客户端断连后 Stream 守卫永不触发、消费循环不退出、上游 LLM 流持续运行。
func (w *SSEWriter) WriteEvent(event, data string) error {
	if _, err := fmt.Fprintf(w.writer, "event: %s\n", event); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w.writer, "data: %s\n\n", data); err != nil {
		return err
	}
	w.flusher.Flush()
	return nil
}

// WriteMessage 写入消息（无事件类型）
// 格式: data: <data>\n\n
func (w *SSEWriter) WriteMessage(data string) error {
	if _, err := fmt.Fprintf(w.writer, "data: %s\n\n", data); err != nil {
		return err
	}
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

// KeepAlive 发送保持连接的心跳。
//
// 用 SSE 注释行（": ping\n\n"）而非 data 行（N6 修复）：data 行（含空 data）会触发
// 客户端 onmessage 回调，注释行仅维持连接不产生消息事件，更符合心跳语义。
func (w *SSEWriter) KeepAlive() error {
	if _, err := fmt.Fprintf(w.writer, ": ping\n\n"); err != nil {
		return err
	}
	w.flusher.Flush()
	return nil
}

// Stream 流式发送数据
//
// 消费循环含 ctx.Done 分支（C3a 修复）：客户端断连（c.Request.Context 取消）时立即退出，
// 不再仅靠 channel 关闭或写错误退出。配合 WriteJSON 的写错误传播（C3b），断连即停。
// ctx 为 nil 时回退到 context.Background()（防御外部未走 NewSSEWriter 构造的 nil panic）。
func (w *SSEWriter) Stream(event string, ch <-chan any) error {
	ctx := w.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case data, ok := <-ch:
			if !ok {
				return w.WriteDone()
			}
			if err := w.WriteJSON(event, data); err != nil {
				return err
			}
		}
	}
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
//
// 消费循环含 ctx.Done 分支（C3a 修复）：客户端断连即退出。
// 生产者（ch 的发送方）也应监听 c.Request.Context()，以便消费端早退后停止上游 LLM 流；
// 若生产者忽略 ctx，本函数仍会因 ctx.Done 退出（消费端不泄漏），但上游生产者可能继续运行
// 直到其自身完成或 channel 阻塞——调用方有责任在 ctx 取消时停止生产。
func StreamText(c *gin.Context, ch <-chan string) error {
	writer, err := NewSSEWriter(c)
	if err != nil {
		return err
	}

	ctx := c.Request.Context()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case text, ok := <-ch:
			if !ok {
				return writer.WriteDone()
			}
			if err := writer.WriteJSON("message", gin.H{"text": text}); err != nil {
				return err
			}
		}
	}
}

// StreamChunks 流式发送文本块（带增量标记）
func StreamChunks(c *gin.Context, ch <-chan string) error {
	writer, err := NewSSEWriter(c)
	if err != nil {
		return err
	}

	ctx := c.Request.Context()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case chunk, ok := <-ch:
			if !ok {
				return writer.WriteJSON("done", gin.H{"finished": true})
			}
			if err := writer.WriteJSON("chunk", gin.H{"delta": chunk}); err != nil {
				return err
			}
		}
	}
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

	ctx := c.Request.Context()
	// 发送内容块
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case chunk, ok := <-ch:
			if !ok {
				// 发送完成事件
				return writer.WriteJSON("done", gin.H{"id": messageID, "finished": true})
			}
			if err := writer.WriteJSON("chunk", gin.H{"id": messageID, "delta": chunk}); err != nil {
				return err
			}
		}
	}
}
