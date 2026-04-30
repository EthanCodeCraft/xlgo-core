package ws_test

import (
	"testing"

	"github.com/EthanCodeCraft/xlgo-core/ws"
)

func TestMessageType(t *testing.T) {
	if ws.TypeText != "text" {
		t.Errorf("TypeText = %s, want text", ws.TypeText)
	}
	if ws.TypeBinary != "binary" {
		t.Errorf("TypeBinary = %s, want binary", ws.TypeBinary)
	}
	if ws.TypePing != "ping" {
		t.Errorf("TypePing = %s, want ping", ws.TypePing)
	}
	if ws.TypePong != "pong" {
		t.Errorf("TypePong = %s, want pong", ws.TypePong)
	}
	if ws.TypeClose != "close" {
		t.Errorf("TypeClose = %s, want close", ws.TypeClose)
	}
}

func TestMessage(t *testing.T) {
	msg := ws.Message{
		Type:    ws.TypeText,
		Content: "hello world",
	}

	if msg.Type != ws.TypeText {
		t.Error("Message Type failed")
	}
	if msg.Content != "hello world" {
		t.Error("Message Content failed")
	}
}

func TestNewConnection(t *testing.T) {
	// NewConnection 需要 websocket.Conn，无法直接测试
	// 仅验证结构体定义存在
}

func TestNewHub(t *testing.T) {
	hub := ws.NewHub()
	if hub == nil {
		t.Error("NewHub should not return nil")
	}
}

func TestHubCount(t *testing.T) {
	hub := ws.NewHub()

	// 无连接时计数应为 0
	count := hub.Count()
	if count != 0 {
		t.Errorf("Hub Count = %d, want 0", count)
	}
}

func TestHubBroadcast(t *testing.T) {
	hub := ws.NewHub()

	// 广播消息（无连接时也应正常）
	hub.Broadcast([]byte("test message"))
}

func TestHubBroadcastJSON(t *testing.T) {
	hub := ws.NewHub()

	err := hub.BroadcastJSON(map[string]string{"key": "value"})
	if err != nil {
		t.Errorf("BroadcastJSON error: %v", err)
	}
}

func TestDefaultHandler(t *testing.T) {
	handler := ws.DefaultHandler{}

	// 测试空处理器不会 panic
	handler.OnConnect(nil)
	handler.OnMessage(nil, nil)
	handler.OnClose(nil)
	handler.OnError(nil, nil)
}

func TestDefaultHandlerWithFuncs(t *testing.T) {
	connectCalled := false
	messageCalled := false

	handler := ws.DefaultHandler{
		OnConnectFunc: func(conn *ws.Connection) {
			connectCalled = true
		},
		OnMessageFunc: func(conn *ws.Connection, message []byte) {
			messageCalled = true
		},
	}

	handler.OnConnect(nil)
	handler.OnMessage(nil, nil)

	if !connectCalled {
		t.Error("OnConnectFunc should be called")
	}
	if !messageCalled {
		t.Error("OnMessageFunc should be called")
	}
}

func TestHandlerFunc(t *testing.T) {
	var handlerFunc ws.HandlerFunc = func(conn *ws.Connection, message []byte) {
		// 处理消息
	}

	if handlerFunc == nil {
		t.Error("HandlerFunc should not be nil")
	}
}