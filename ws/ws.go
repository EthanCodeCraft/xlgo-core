package ws

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// WebSocket 配置。
//
// CheckOrigin 默认采用同源校验（C7/CSWSH 修复）：仅当请求 Origin 为空（非浏览器客户端）
// 或与请求 Host 同源时放行，拒绝跨域 WebSocket 连接，防 Cross-Site WebSocket Hijacking。
// 需要允许多个可信 Origin 的场景，用 SetCheckOrigin 注入自定义校验，或用 AllowOrigins。
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     sameOriginCheck,
}

// sameOriginCheck 同源校验：Origin 为空（非浏览器/服务器内部）放行；否则要求 Origin 的
// host:port 与请求 Host 一致。防 CSWSH（C7）。
func sameOriginCheck(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		// 非浏览器客户端（如 curl/服务端拨号）不带 Origin，放行。
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return u.Host == r.Host
}

// AllowOrigins 返回一个 CheckOrigin 函数，仅放行给定 Origin 列表（含 scheme+host[:port]）。
// 用于多可信域名场景。传入空切片等价于拒绝所有带 Origin 的浏览器连接。
func AllowOrigins(origins ...string) func(r *http.Request) bool {
	allowed := make(map[string]bool, len(origins))
	for _, o := range origins {
		allowed[o] = true
	}
	return func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true // 非浏览器客户端放行
		}
		return allowed[origin]
	}
}

// 读写超时常量（C2c：防半开连接 goroutine 泄漏）。ping 周期须 < pongWait。
const (
	// pongWait 等待对端 pong 的最长时间；超时即判定连接半开、关闭。
	pongWait = 60 * time.Second
	// pingPeriod ping 发送周期，须 < pongWait 以留出重置读 deadline 的余量。
	pingPeriod = (pongWait * 9) / 10
	// writeWait 单次写操作超时。
	writeWait = 10 * time.Second
)

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

// ErrSendBufferFull 发送缓冲已满（消费者跟不上）。非阻塞投递策略下返回此错误，
// 调用方可决定重试或关闭连接。避免慢消费者阻塞发送方（含 Hub 广播持锁场景）。
var ErrSendBufferFull = errors.New("websocket send buffer full")

// Send 非阻塞发送消息。不直接写底层 conn，而是投递到 send channel 由 writePump 统一写出。
//
// 并发安全说明（C2b 修复）：Close() 仅 close closeChan，不再 close send channel，
// 因此 select 选中 send 分支也只是向未关闭 channel 投递（不会 panic）。
//
// 非阻塞策略（C2a-residual 修复）：投递用 default 分支，缓冲满立即返回 ErrSendBufferFull，
// 而非阻塞等待。这避免 Hub 广播持锁期间因慢消费者/已死连接（writePump 退出但 closeChan
// 未关、send 缓冲满）阻塞最长 pongWait，导致整个 Hub stall。调用方对缓冲满可重试或关闭连接。
func (c *Connection) Send(data []byte) error {
	// 快速路径：连接已关闭则立即失败。
	if c.IsClosed() {
		return errors.New("connection closed")
	}
	select {
	case c.send <- data:
		return nil
	default:
		// 缓冲满。再检查一次 closeChan，避免对刚关闭的连接报"缓冲满"而非"已关闭"。
		select {
		case <-c.closeChan:
			return errors.New("connection closed")
		default:
			return ErrSendBufferFull
		}
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

// Close 关闭连接。仅 close closeChan 作关闭信号，不 close send channel（C2b 修复），
// 避免 Close 与并发 Send 之间的 send-on-closed panic。send channel 由 GC 回收。
func (c *Connection) Close() {
	c.once.Do(func() {
		close(c.closeChan)
		// #nosec G104 -- 关闭底层连接的错误无意义（重复关闭返错属正常），忽略
		_ = c.conn.Close()
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

		// 读循环前置：设置初始读 deadline 与 pong handler（C2c：防半开连接永久阻塞）。
		// 每收到 pong 重置读 deadline；超时未收到 pong 则 ReadMessage 返回错误退出。
		_ = conn.conn.SetReadDeadline(time.Now().Add(pongWait))
		conn.conn.SetPongHandler(func(string) error {
			_ = conn.conn.SetReadDeadline(time.Now().Add(pongWait))
			return nil
		})

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

// writePump 写入泵。每次写前设置写超时，按 pingPeriod 周期发 ping（须 < pongWait）。
// 写失败时主动 Close 连接，触发 closeChan 关闭 → 读循环 ReadMessage 因底层 conn 关闭
// 返回错误而退出，避免半开连接残留最长 pongWait 才回收（C2c 残留优化）。
func writePump(conn *Connection) {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-conn.closeChan:
			return
		case message, ok := <-conn.send:
			// send channel 不再被 Close（C2b 修复），ok 永远为 true；
			// 此分支保留 ok 检查仅为兼容历史与防御。
			if !ok {
				_ = conn.conn.WriteControl(websocket.CloseMessage, []byte{}, time.Now().Add(writeWait))
				conn.Close()
				return
			}
			conn.mu.Lock()
			_ = conn.conn.SetWriteDeadline(time.Now().Add(writeWait))
			err := conn.conn.WriteMessage(websocket.TextMessage, message)
			conn.mu.Unlock()
			if err != nil {
				conn.Close()
				return
			}
		case <-ticker.C:
			conn.mu.Lock()
			_ = conn.conn.SetWriteDeadline(time.Now().Add(writeWait))
			err := conn.conn.WriteMessage(websocket.PingMessage, nil)
			conn.mu.Unlock()
			if err != nil {
				conn.Close()
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

// SetCheckOrigin 设置 Origin 检查函数。
//
// L-H：直接写包级 upgrader.CheckOrigin 无锁，与并发 Upgrade（读 upgrader.CheckOrigin）
// 存在数据竞争。约定仅在 HTTP 服务启动前（init/main 阶段、路由注册前）调用一次，
// 不要在服务运行期与请求处理并发切换。运行期动态切换 Origin 策略请改用自有 upgrader 实例。
func SetCheckOrigin(fn func(r *http.Request) bool) {
	upgrader.CheckOrigin = fn
}

// Hub 连接管理中心（用于广播）。
// W1 修复：添加 stop channel + Stop() 方法，支持优雅退出。
type Hub struct {
	connections map[*Connection]bool
	register    chan *Connection
	unregister  chan *Connection
	broadcast   chan []byte
	mu          sync.RWMutex
	stop        chan struct{}
	stopOnce    sync.Once       // H-8: 保证 close(stop) 仅一次，并发 Stop 不 double-close panic
	runOnce     sync.Once       // H-9: 保证 Run 仅执行一次
	runStarted  atomic.Bool     // H-9: 标记 Run 是否已启动，Stop 据此决定是否等待
	runDone     chan struct{}   // H-9: Run 退出时 close，替代 WaitGroup 避免 Add/Wait 竞态
}

// NewHub 创建 Hub
func NewHub() *Hub {
	return &Hub{
		connections: make(map[*Connection]bool),
		register:    make(chan *Connection),
		unregister:  make(chan *Connection),
		broadcast:   make(chan []byte, 256),
		stop:        make(chan struct{}),
		runDone:     make(chan struct{}),
	}
}

// Run 运行 Hub。消费 register/unregister/broadcast/stop 四个 channel。
//
// W1 修复：新增 stop 退出分支，Stop() 方法 close(stop) 通知退出。
// 死锁修复（C2a）：广播分支不再向自身 unregister channel 回环。
//
// H-9 修复：用 runOnce 守卫 Run 仅执行一次；用 runDone channel 替代 WaitGroup。
// 原实现 wg.Add(1) 在 Run 内、wg.Wait() 在 Stop 内，二者并发时违反 WaitGroup
// "Add(正delta且计数器为0) 必须 happens-before Wait" 的约束，-race 必采。
// 改为 Run 启动时 Store runStarted=true 并在退出时 close(runDone)；Stop 仅在
// runStarted 为 true 时 <-runDone 等待，未启动则直接返回，无竞态。
func (h *Hub) Run() {
	h.runOnce.Do(func() {
		h.runStarted.Store(true)
		defer close(h.runDone)
		for {
			select {
			case <-h.stop:
				// 关闭所有连接后退出
				h.mu.Lock()
				for conn := range h.connections {
					delete(h.connections, conn)
					conn.Close()
				}
				h.mu.Unlock()
				return

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
				// 持写锁单次遍历，失败的连接行内移除并关闭，不回环 unregister channel（C2a 修复）。
				h.mu.Lock()
				for conn := range h.connections {
					if err := conn.Send(message); err != nil {
						delete(h.connections, conn)
						conn.Close()
					}
				}
				h.mu.Unlock()
			}
		}
	})
}

// Stop 停止 Hub（W1 修复：优雅退出机制）。
// close(stop) 通知 Run() 退出；若 Run 已启动则等待其完全结束（<-runDone）。
//
// H-8 修复：用 stopOnce 保证 close(stop) 仅执行一次。原实现 select{<-stop/default:close(stop)}
// 在并发 Stop 时两个调用方都可能走 default 分支同时 close → double-close panic；
// 原注释声称的"stopped 标志保护"实际不存在（文档撒谎）。stopOnce 彻底消除该竞态。
//
// H-9 修复：仅在 runStarted 为 true 时等待 runDone；Run 未启动时直接返回，
// 避免 WaitGroup Add/Wait 竞态，且 Stop 先于 Run 调用后误再调 Run 也不会 panic。
// 幂等：重复/并发调用安全。
func (h *Hub) Stop() {
	h.stopOnce.Do(func() {
		close(h.stop)
	})
	if h.runStarted.Load() {
		<-h.runDone
	}
}

// Register 注册连接（W1 修复：Hub 已 stop 时 non-blocking 返回，避免永久阻塞）。
func (h *Hub) Register(conn *Connection) {
	select {
	case h.register <- conn:
	case <-h.stop:
		conn.Close()
	}
}

// Unregister 注销连接（W1 修复：Hub 已 stop 时 non-blocking 返回）。
func (h *Hub) Unregister(conn *Connection) {
	select {
	case h.unregister <- conn:
	case <-h.stop:
	}
}

// Broadcast 广播消息（W1 修复：Hub 已 stop 时 non-blocking 返回）。
func (h *Hub) Broadcast(message []byte) {
	select {
	case h.broadcast <- message:
	case <-h.stop:
	}
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
