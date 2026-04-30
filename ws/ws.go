package ws

import (
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// WebSocket 配置
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // 生产环境应检查 Origin
	},
}

// MessageType 消息类型
type MessageType string

const (
	TypeText   MessageType = "text"
	TypeBinary MessageType = "binary"
	TypePing   MessageType = "ping"
	TypePong   MessageType = "pong"
	TypeClose  MessageType = "close"
)

// Message WebSocket 消息
type Message struct {
	Type    MessageType `json:"type"`
	Content any         `json:"content"`
}

// Connection WebSocket 连接
type Connection struct {
	conn      *websocket.Conn
	send      chan []byte
	closeChan chan struct{}
	once      sync.Once
	mu        sync.Mutex
}

// NewConnection 创建 WebSocket 连接
func NewConnection(conn *websocket.Conn) *Connection {
	return &Connection{
		conn:      conn,
		send:      make(chan []byte, 256),
		closeChan: make(chan struct{}),
	}
}

// Send 发送消息
func (c *Connection) Send(data []byte) error {
	select {
	case c.send <- data:
		return nil
	case <-c.closeChan:
		return errors.New("connection closed")
	}
}

// SendJSON 发送 JSON 消息
func (c *Connection) SendJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.Send(data)
}

// SendText 发送文本消息
func (c *Connection) SendText(text string) error {
	return c.SendJSON(Message{Type: TypeText, Content: text})
}

// Close 关闭连接
func (c *Connection) Close() {
	c.once.Do(func() {
		close(c.closeChan)
		close(c.send)
		c.conn.Close()
	})
}

// IsClosed 检查连接是否已关闭
func (c *Connection) IsClosed() bool {
	select {
	case <-c.closeChan:
		return true
	default:
		return false
	}
}

// SetReadDeadline 设置读取超时
func (c *Connection) SetReadDeadline(t time.Time) error {
	return c.conn.SetReadDeadline(t)
}

// SetWriteDeadline 设置写入超时
func (c *Connection) SetWriteDeadline(t time.Time) error {
	return c.conn.SetWriteDeadline(t)
}

// Handler WebSocket 处理器接口
type Handler interface {
	OnConnect(conn *Connection)
	OnMessage(conn *Connection, message []byte)
	OnClose(conn *Connection)
	OnError(conn *Connection, err error)
}

// HandlerFunc 处理函数类型
type HandlerFunc func(conn *Connection, message []byte)

// DefaultHandler 默认处理器
type DefaultHandler struct {
	OnConnectFunc func(conn *Connection)
	OnMessageFunc func(conn *Connection, message []byte)
	OnCloseFunc   func(conn *Connection)
	OnErrorFunc   func(conn *Connection, err error)
}

func (h *DefaultHandler) OnConnect(conn *Connection) {
	if h.OnConnectFunc != nil {
		h.OnConnectFunc(conn)
	}
}

func (h *DefaultHandler) OnMessage(conn *Connection, message []byte) {
	if h.OnMessageFunc != nil {
		h.OnMessageFunc(conn, message)
	}
}

func (h *DefaultHandler) OnClose(conn *Connection) {
	if h.OnCloseFunc != nil {
		h.OnCloseFunc(conn)
	}
}

func (h *DefaultHandler) OnError(conn *Connection, err error) {
	if h.OnErrorFunc != nil {
		h.OnErrorFunc(conn, err)
	}
}

// Upgrade 升级 HTTP 连接为 WebSocket
func Upgrade(c *gin.Context) (*Connection, error) {
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return nil, err
	}
	return NewConnection(conn), nil
}

// Handle WebSocket 处理中间件
func Handle(handler Handler) gin.HandlerFunc {
	return func(c *gin.Context) {
		conn, err := Upgrade(c)
		if err != nil {
			return
		}
		defer conn.Close()

		// 触发连接事件
		handler.OnConnect(conn)

		// 启动写入协程
		go writePump(conn)

		// 读取消息
		for {
			_, message, err := conn.conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					handler.OnError(conn, err)
				}
				handler.OnClose(conn)
				break
			}
			handler.OnMessage(conn, message)
		}
	}
}

// writePump 写入泵
func writePump(conn *Connection) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-conn.closeChan:
			return
		case message, ok := <-conn.send:
			if !ok {
				conn.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			conn.mu.Lock()
			err := conn.conn.WriteMessage(websocket.TextMessage, message)
			conn.mu.Unlock()
			if err != nil {
				return
			}
		case <-ticker.C:
			conn.mu.Lock()
			err := conn.conn.WriteMessage(websocket.PingMessage, nil)
			conn.mu.Unlock()
			if err != nil {
				return
			}
		}
	}
}

// HandleFunc 使用函数处理 WebSocket
func HandleFunc(fn HandlerFunc) gin.HandlerFunc {
	return Handle(&DefaultHandler{
		OnMessageFunc: fn,
	})
}

// SetCheckOrigin 设置 Origin 检查函数
func SetCheckOrigin(fn func(r *http.Request) bool) {
	upgrader.CheckOrigin = fn
}

// Hub 连接管理中心（用于广播）
type Hub struct {
	connections map[*Connection]bool
	register    chan *Connection
	unregister  chan *Connection
	broadcast   chan []byte
	mu          sync.RWMutex
}

// NewHub 创建 Hub
func NewHub() *Hub {
	return &Hub{
		connections: make(map[*Connection]bool),
		register:    make(chan *Connection),
		unregister:  make(chan *Connection),
		broadcast:   make(chan []byte, 256),
	}
}

// Run 运行 Hub
func (h *Hub) Run() {
	for {
		select {
		case conn := <-h.register:
			h.mu.Lock()
			h.connections[conn] = true
			h.mu.Unlock()

		case conn := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.connections[conn]; ok {
				delete(h.connections, conn)
				conn.Close()
			}
			h.mu.Unlock()

		case message := <-h.broadcast:
			h.mu.RLock()
			for conn := range h.connections {
				if err := conn.Send(message); err != nil {
					h.mu.RUnlock()
					h.unregister <- conn
					h.mu.RLock()
				}
			}
			h.mu.RUnlock()
		}
	}
}

// Register 注册连接
func (h *Hub) Register(conn *Connection) {
	h.register <- conn
}

// Unregister 注销连接
func (h *Hub) Unregister(conn *Connection) {
	h.unregister <- conn
}

// Broadcast 广播消息
func (h *Hub) Broadcast(message []byte) {
	h.broadcast <- message
}

// BroadcastJSON 广播 JSON 消息
func (h *Hub) BroadcastJSON(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	h.Broadcast(data)
	return nil
}

// Count 获取连接数
func (h *Hub) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.connections)
}
