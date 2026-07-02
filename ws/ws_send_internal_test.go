package ws

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// 回归 C2a-residual：Send 非阻塞——send 缓冲满后立即返回 ErrSendBufferFull，
// 不阻塞调用方。旧实现（阻塞 select + 缓冲满）会阻塞，Hub 广播持写锁期间 stall。
//
// 用 internal test 直接构造 Connection（绕过 Handle），不启动 writePump 消费 send，
// 填满缓冲后 Send 必须立即返回 ErrSendBufferFull。
func TestSendNonBlockingBufferFullInternal(t *testing.T) {
	// 建一对真实 websocket conn 作为底层。
	var srvConn *websocket.Conn
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		c, err := u.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		mu.Lock()
		srvConn = c
		mu.Unlock()
		// 阻塞读保持连接存活。
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer srv.Close()
	dialer := &websocket.Dialer{HandshakeTimeout: 2 * time.Second}
	client, _, err := dialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	// 等待服务端 conn 就绪。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		if srvConn != nil {
			mu.Unlock()
			break
		}
		mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	if srvConn == nil {
		t.Fatal("server-side conn not ready")
	}
	defer srvConn.Close()

	conn := NewConnection(srvConn)
	defer conn.Close()

	// 不启动 writePump，send 无消费者。填满缓冲（256）。
	for i := 0; i < 256; i++ {
		if err := conn.Send([]byte("x")); err != nil {
			t.Fatalf("filling buffer[%d] err = %v", i, err)
		}
	}
	// 第 257 个必须立即返回 ErrSendBufferFull（非阻塞）。
	done := make(chan error, 1)
	go func() {
		done <- conn.Send([]byte("overflow"))
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrSendBufferFull) {
			t.Errorf("overflow Send err = %v, want ErrSendBufferFull", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Send on full buffer blocked (should be non-blocking)")
	}
}
