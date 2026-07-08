package ws

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func TestM11SetCheckOriginConcurrentWithUpgradeNoRace(t *testing.T) {
	SetCheckOrigin(nil)
	t.Cleanup(func() { SetCheckOrigin(nil) })

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/ws", Handle(nil))
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		allowA := func(*http.Request) bool { return true }
		allowB := func(*http.Request) bool { return true }
		for {
			select {
			case <-done:
				return
			default:
				SetCheckOrigin(allowA)
				SetCheckOrigin(allowB)
			}
		}
	}()

	dialer := &websocket.Dialer{HandshakeTimeout: 2 * time.Second}
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	for i := 0; i < 50; i++ {
		conn, _, err := dialer.Dial(url, nil)
		if err != nil {
			t.Fatalf("dial[%d]: %v", i, err)
		}
		_ = conn.Close()
	}
	close(done)
	wg.Wait()
}

func TestM11HandleNilUsesDefaultHandler(t *testing.T) {
	SetCheckOrigin(func(*http.Request) bool { return true })
	t.Cleanup(func() { SetCheckOrigin(nil) })

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/ws", Handle(nil))
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/ws", nil)
	if err != nil {
		t.Fatalf("dial Handle(nil): %v", err)
	}
	_ = conn.Close()
}

func TestM11HubNotRunFastFail(t *testing.T) {
	hub := NewHub()
	conn := NewConnection(nil)

	if err := hub.TryRegister(conn); !errors.Is(err, ErrHubNotRunning) {
		t.Fatalf("TryRegister before Run error = %v, want ErrHubNotRunning", err)
	}
	if !conn.IsClosed() {
		t.Fatal("TryRegister before Run should close rejected connection")
	}
	if err := hub.TryUnregister(NewConnection(nil)); !errors.Is(err, ErrHubNotRunning) {
		t.Fatalf("TryUnregister before Run error = %v, want ErrHubNotRunning", err)
	}
	if err := hub.TryBroadcast([]byte("x")); !errors.Is(err, ErrHubNotRunning) {
		t.Fatalf("TryBroadcast before Run error = %v, want ErrHubNotRunning", err)
	}
	if err := hub.BroadcastJSON(map[string]string{"x": "y"}); !errors.Is(err, ErrHubNotRunning) {
		t.Fatalf("BroadcastJSON before Run error = %v, want ErrHubNotRunning", err)
	}
}

func TestM11PublicRegisterToleratesRunStartupWindow(t *testing.T) {
	hub := NewHub()
	conn := NewConnection(nil)

	hub.Register(conn)
	go hub.Run()
	t.Cleanup(hub.Stop)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if hub.Count() == 1 {
			if conn.IsClosed() {
				t.Fatal("public Register should not close connection during Run startup window")
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("Hub Count = %d, want 1", hub.Count())
}

func TestM11HubStopDrainsAndCloses(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	for !hub.runStarted.Load() {
		time.Sleep(time.Millisecond)
	}

	conn := NewConnection(nil)
	hub.register <- conn
	hub.broadcast <- []byte("discarded")
	hub.Stop()

	if !conn.IsClosed() {
		t.Fatal("Stop should close pending registered connection")
	}
	if got := hub.Count(); got != 0 {
		t.Fatalf("Hub Count after Stop = %d, want 0", got)
	}
	if got := len(hub.register); got != 0 {
		t.Fatalf("register queue length after Stop = %d, want 0", got)
	}
	if got := len(hub.broadcast); got != 0 {
		t.Fatalf("broadcast queue length after Stop = %d, want 0", got)
	}
	if err := hub.TryRegister(NewConnection(nil)); !errors.Is(err, ErrHubStopped) {
		t.Fatalf("TryRegister after Stop error = %v, want ErrHubStopped", err)
	}
	if err := hub.TryBroadcast([]byte("x")); !errors.Is(err, ErrHubStopped) {
		t.Fatalf("TryBroadcast after Stop error = %v, want ErrHubStopped", err)
	}
}

func TestM11HubStopConcurrentTryRegisterClosesAll(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	for !hub.runStarted.Load() {
		time.Sleep(time.Millisecond)
	}

	var connsMu sync.Mutex
	var conns []*Connection
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn := NewConnection(nil)
			connsMu.Lock()
			conns = append(conns, conn)
			connsMu.Unlock()
			_ = hub.TryRegister(conn)
		}()
	}
	time.Sleep(time.Millisecond)
	hub.Stop()
	wg.Wait()

	connsMu.Lock()
	defer connsMu.Unlock()
	for i, conn := range conns {
		if !conn.IsClosed() {
			t.Fatalf("connection %d left open after concurrent Stop/TryRegister", i)
		}
	}
	if got := hub.Count(); got != 0 {
		t.Fatalf("Hub Count after concurrent Stop/TryRegister = %d, want 0", got)
	}
	if got := len(hub.register); got != 0 {
		t.Fatalf("register queue length after concurrent Stop/TryRegister = %d, want 0", got)
	}
}

func TestM11HubNilGuards(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	t.Cleanup(hub.Stop)

	if err := hub.TryRegister(nil); !errors.Is(err, ErrNilConnection) {
		t.Fatalf("TryRegister(nil) error = %v, want ErrNilConnection", err)
	}
	if err := hub.TryUnregister(nil); !errors.Is(err, ErrNilConnection) {
		t.Fatalf("TryUnregister(nil) error = %v, want ErrNilConnection", err)
	}
}
