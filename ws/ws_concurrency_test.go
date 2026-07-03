package ws_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EthanCodeCraft/xlgo-core/ws"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// newWSTestServer 启动一个带 ws.Handle 的 httptest server，返回 server 与 dialer。
// handler 可为 nil（用默认空处理器）。
func newWSTestServer(t *testing.T, handler ws.Handler) (*httptest.Server, *websocket.Dialer) {
	t.Helper()
	r := gin.New()
	if handler == nil {
		handler = &ws.DefaultHandler{}
	}
	r.GET("/ws", ws.Handle(handler))
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	dialer := &websocket.Dialer{HandshakeTimeout: 2 * time.Second}
	return srv, dialer
}

func wsURL(srv *httptest.Server) string {
	return "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
}

// dial 连接并返回客户端 conn。
func dial(t *testing.T, srv *httptest.Server, dialer *websocket.Dialer) *websocket.Conn {
	t.Helper()
	c, _, err := dialer.Dial(wsURL(srv), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// ===== C2b：Close 与并发 Send 不 panic =====

// 回归 C2b：并发 Close 与 Send 不能 send-on-closed panic。
// 旧实现 Close 同时 close(c.send)，并发 Send 的 select 伪随机选中 send 分支即 panic。
func TestConnectionCloseConcurrentSendNoPanic(t *testing.T) {
	srv, dialer := newWSTestServer(t, nil)
	client := dial(t, srv, dialer)

	// 服务端 conn 经 Handle 持有；通过 Hub 注册拿到服务端 Connection。
	hub := ws.NewHub()
	go hub.Run()
	t.Cleanup(func() { /* Hub.Run 无退出，靠进程结束 */ })

	// 用 HandleFunc 包装拿到服务端 Connection。
	var srvConn *ws.Connection
	var got atomic.Value // *ws.Connection
	r := gin.New()
	r.GET("/ws", ws.HandleFunc(func(conn *ws.Connection, message []byte) {
		got.Store(conn)
	}))
	srv2 := httptest.NewServer(r)
	t.Cleanup(srv2.Close)
	c2, _, err := dialer.Dial("ws"+strings.TrimPrefix(srv2.URL, "http")+"/ws", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c2.Close()
	_ = client

	// 发一条消息触发服务端 OnMessage，拿到 srvConn。
	if err := c2.WriteMessage(websocket.TextMessage, []byte("hi")); err != nil {
		t.Fatalf("write: %v", err)
	}
	// 等待服务端拿到连接。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if v := got.Load(); v != nil {
			srvConn = v.(*ws.Connection)
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if srvConn == nil {
		t.Fatal("timeout waiting for server-side Connection")
	}

	// 并发：一个 goroutine 反复 Send，主 goroutine Close。
	var panicked atomic.Value
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			if r := recover(); r != nil {
				panicked.Store(r)
			}
		}()
		for i := 0; i < 2000; i++ {
			// Send 在 Close 后会返回 error，正常；不应 panic。
			_ = srvConn.Send([]byte("x"))
		}
	}()

	// 给 Send 一点启动时间再 Close，制造最大竞态窗口。
	time.Sleep(2 * time.Millisecond)
	srvConn.Close()
	wg.Wait()

	if p := panicked.Load(); p != nil {
		t.Fatalf("concurrent Close/Send panicked (C2b send-on-closed): %v", p)
	}
}

// 回归 C2b：Close 后 Send 返回 error 而非 panic。
func TestSendAfterCloseReturnsError(t *testing.T) {
	srv, dialer := newWSTestServer(t, nil)
	c := dial(t, srv, dialer)
	defer c.Close()

	// 通过 HandleFunc 拿服务端 conn。
	var got atomic.Value
	r := gin.New()
	r.GET("/ws", ws.HandleFunc(func(conn *ws.Connection, message []byte) {
		got.Store(conn)
	}))
	srv2 := httptest.NewServer(r)
	t.Cleanup(srv2.Close)
	c2, _, err := dialer.Dial("ws"+strings.TrimPrefix(srv2.URL, "http")+"/ws", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c2.Close()
	if err := c2.WriteMessage(websocket.TextMessage, []byte("hi")); err != nil {
		t.Fatalf("write: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	var srvConn *ws.Connection
	for time.Now().Before(deadline) {
		if v := got.Load(); v != nil {
			srvConn = v.(*ws.Connection)
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if srvConn == nil {
		t.Fatal("timeout")
	}
	srvConn.Close()
	// Close 后 Send 必须返回 error（非 panic）。
	if err := srvConn.Send([]byte("after-close")); err == nil {
		t.Error("Send after Close should return error, got nil")
	}
}

// ===== C2a：Hub 广播不死锁 =====

// 回归 C2a：向含"已关闭"连接的 Hub 广播不能死锁，且失败连接被行内移除。
// 旧实现：broadcast 中 conn.Send 失败 → h.unregister <- conn（向自己发，无消费者）→ 永久阻塞。
// 这里直接 Close 服务端连接（closeChan 关闭），使 Send 走 closeChan 分支返回 error，
// 触发 Hub 行内 delete + Close。
func TestHubBroadcastDeadConnectionNoDeadlock(t *testing.T) {
	hub := ws.NewHub()
	go hub.Run()

	var got atomic.Value
	r := gin.New()
	r.GET("/ws", ws.HandleFunc(func(conn *ws.Connection, message []byte) {
		got.Store(conn)
	}))
	srv2 := httptest.NewServer(r)
	t.Cleanup(srv2.Close)
	dialer := &websocket.Dialer{HandshakeTimeout: 2 * time.Second}
	c2, _, err := dialer.Dial("ws"+strings.TrimPrefix(srv2.URL, "http")+"/ws", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c2.Close()
	if err := c2.WriteMessage(websocket.TextMessage, []byte("hi")); err != nil {
		t.Fatalf("write: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	var srvConn *ws.Connection
	for time.Now().Before(deadline) {
		if v := got.Load(); v != nil {
			srvConn = v.(*ws.Connection)
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if srvConn == nil {
		t.Fatal("timeout")
	}

	hub.Register(srvConn)
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if hub.Count() == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if hub.Count() != 1 {
		t.Fatalf("Hub Count = %d, want 1", hub.Count())
	}

	// 直接关闭服务端连接（closeChan 关闭），使后续 Send 返回 error。
	srvConn.Close()
	// 给 closeChan 关闭传播一点时间。
	time.Sleep(20 * time.Millisecond)

	// 广播必须在超时内返回（不阻塞 Hub），且失败连接被行内移除。
	done := make(chan struct{})
	go func() {
		hub.Broadcast([]byte("should-not-deadlock"))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Hub.Broadcast deadlocked (C2a: unregister self-send loop)")
	}
	// 等待 Hub 内部清理失败的连接。
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if hub.Count() == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if hub.Count() != 0 {
		t.Errorf("Hub Count = %d, want 0 (failed conn should be removed inline)", hub.Count())
	}
}

// 回归 C2a/C2b：Hub 广播到正常连接，客户端能收到。
func TestHubBroadcastReachesClients(t *testing.T) {
	hub := ws.NewHub()
	go hub.Run()

	// 用带 Hub 注册的 handler。
	handler := &ws.DefaultHandler{
		OnConnectFunc: func(conn *ws.Connection) {
			hub.Register(conn)
		},
	}
	srv, dialer := newWSTestServer(t, handler)

	// 两个客户端。
	c1 := dial(t, srv, dialer)
	c2 := dial(t, srv, dialer)
	defer c1.Close()
	defer c2.Close()

	// 等待两个连接注册。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && hub.Count() < 2 {
		time.Sleep(10 * time.Millisecond)
	}
	if hub.Count() != 2 {
		t.Fatalf("Hub Count = %d, want 2", hub.Count())
	}

	// 广播 JSON。
	if err := hub.BroadcastJSON(map[string]string{"msg": "hello"}); err != nil {
		t.Fatalf("BroadcastJSON: %v", err)
	}

	// 两个客户端都应收到。
	for i, c := range []*websocket.Conn{c1, c2} {
		c.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, msg, err := c.ReadMessage()
		if err != nil {
			t.Fatalf("client %d read: %v", i, err)
		}
		var m map[string]string
		if err := json.Unmarshal(msg, &m); err != nil {
			t.Fatalf("client %d unmarshal %q: %v", i, string(msg), err)
		}
		if m["msg"] != "hello" {
			t.Errorf("client %d got %q, want hello", i, m["msg"])
		}
	}
}

// 回归 C2a-residual：Send 非阻塞——缓冲满立即返回 ErrSendBufferFull 而非阻塞等待。
// 旧实现（阻塞 select）在 writePump 退出但 closeChan 未关、send 缓冲满时会阻塞最长 pongWait，
// 导致 Hub 广播持写锁期间 stall。非阻塞投递保证持锁期间永不阻塞。
// 用 internal test（ws_send_internal_test.go）直接测私有 send channel，此处仅验证公开行为：
// 连接 Close 后，Send 立即返回（不阻塞）。
func TestSendNonBlockingOnClosed(t *testing.T) {
	var got atomic.Value
	r := gin.New()
	r.GET("/ws", ws.HandleFunc(func(conn *ws.Connection, message []byte) {
		got.Store(conn)
	}))
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	dialer := &websocket.Dialer{HandshakeTimeout: 2 * time.Second}
	c, _, err := dialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/ws", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()
	if err := c.WriteMessage(websocket.TextMessage, []byte("hi")); err != nil {
		t.Fatalf("write: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	var srvConn *ws.Connection
	for time.Now().Before(deadline) {
		if v := got.Load(); v != nil {
			srvConn = v.(*ws.Connection)
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if srvConn == nil {
		t.Fatal("timeout")
	}
	srvConn.Close()

	// Close 后连续 Send 必须立即返回 error，不阻塞。
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			_ = srvConn.Send([]byte("x"))
		}
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Send after Close blocked (should be non-blocking)")
	}
}

// ===== C2c：半开连接超时退出（不永久泄漏 goroutine）=====

// 回归 C2c：客户端断开后，服务端读循环应在 pongWait 超时内退出（非永久阻塞）。
// 用短超时配置不便（常量包级），此处用真实连接断开 + 有限等待验证 OnClose 被调用。
func TestHalfOpenConnectionExitsOnClose(t *testing.T) {
	var closed atomic.Int32
	handler := &ws.DefaultHandler{
		OnCloseFunc: func(conn *ws.Connection) {
			closed.Add(1)
		},
	}
	srv, dialer := newWSTestServer(t, handler)
	c := dial(t, srv, dialer)

	// 客户端主动关闭（发 close 帧）。
	c.Close()

	// 服务端应在有限时间内 OnClose。pongWait=60s 太长，但客户端发的是正常 close，
	// ReadMessage 立即返回 close 错误，OnClose 应很快触发。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if closed.Load() > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("OnClose not called within timeout (C2c: read loop should exit on client close), closed=%d", closed.Load())
}

// ===== 辅助：验证 errors 导入被使用 =====
var _ = errors.New
var _ = http.StatusOK

// TestHubStopConcurrentNoPanic H-8 回归：并发 Stop 不应 double-close panic。
// 修复前 Stop 用 select{<-stop/default:close(stop)}，两个 goroutine 都走 default
// 同时 close → panic。修复后 stopOnce 保证 close 仅一次。
func TestHubStopConcurrentNoPanic(t *testing.T) {
	hub := ws.NewHub()
	go hub.Run()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			hub.Stop() // 并发 Stop
		}()
	}
	wg.Wait()

	// 再调一次 Stop（已 stop）也应安全返回
	hub.Stop()
}

// TestHubStopBeforeRunNoPanic H-9 回归：Stop 先于 Run 调用，随后 Run 不应
// 触发 wg 负计数 panic。
func TestHubStopBeforeRunNoPanic(t *testing.T) {
	hub := ws.NewHub()
	hub.Stop() // Run 尚未启动，Wait 立即返回

	// 之后启动 Run（实际场景是误用），应安全退出而非 panic
	go hub.Run()
	// 给 Run 一点时间观察到 stop 已 close 并退出
	time.Sleep(50 * time.Millisecond)
	// 再次 Stop 确保 wg 归零（Run 的 runOnce 已执行，wg.Add/Done 配对）
	hub.Stop()
}
