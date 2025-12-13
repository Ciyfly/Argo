package engine

import (
	"argo/pkg/log"
	"encoding/base64"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// ============================================================================
// WebSocket Support - WebSocket 连接监控与消息捕获
// ============================================================================

// WebSocketTracker WebSocket 连接追踪器
type WebSocketTracker struct {
	mu sync.RWMutex

	// 配置
	config *WebSocketConfig

	// 连接存储
	connections map[string]*WebSocketConnection

	// 消息通道
	messageQueue chan *WebSocketMessage

	// 统计
	stats WebSocketStats

	// 控制
	stopCh chan struct{}
	wg     sync.WaitGroup
}

// WebSocketConfig WebSocket 配置
type WebSocketConfig struct {
	Enabled            bool          // 是否启用
	MaxConnections     int           // 最大追踪连接数
	MaxMessagesPerConn int           // 每个连接最大消息数
	MessageBufferSize  int           // 消息缓冲区大小
	CaptureMessages    bool          // 是否捕获消息内容
	MaxMessageSize     int           // 最大消息大小 (bytes)
	Timeout            time.Duration // 连接超时
}

// DefaultWebSocketConfig 默认 WebSocket 配置
func DefaultWebSocketConfig() *WebSocketConfig {
	return &WebSocketConfig{
		Enabled:            true,
		MaxConnections:     100,
		MaxMessagesPerConn: 50,
		MessageBufferSize:  1000,
		CaptureMessages:    true,
		MaxMessageSize:     1024 * 1024, // 1MB
		Timeout:            30 * time.Second,
	}
}

// WebSocketConnection WebSocket 连接信息
type WebSocketConnection struct {
	mu sync.RWMutex

	// 连接信息
	RequestID    string    `json:"request_id"`
	URL          string    `json:"url"`
	InitiatorURL string    `json:"initiator_url"`
	CreatedAt    time.Time `json:"created_at"`
	ClosedAt     time.Time `json:"closed_at,omitempty"`

	// 状态
	Status string `json:"status"` // "connecting", "open", "closing", "closed"

	// 消息统计
	MessagesSent     int64 `json:"messages_sent"`
	MessagesReceived int64 `json:"messages_received"`
	BytesSent        int64 `json:"bytes_sent"`
	BytesReceived    int64 `json:"bytes_received"`

	// 捕获的消息
	Messages []*WebSocketMessage `json:"messages,omitempty"`

	// 配置限制
	maxMessages int
}

// WebSocketMessage WebSocket 消息
type WebSocketMessage struct {
	ConnectionID string    `json:"connection_id"`
	URL          string    `json:"url"`
	Direction    string    `json:"direction"` // "sent" 或 "received"
	Opcode       int       `json:"opcode"`    // 1=text, 2=binary
	PayloadData  string    `json:"payload_data,omitempty"`
	PayloadSize  int       `json:"payload_size"`
	Timestamp    time.Time `json:"timestamp"`
	IsMasked     bool      `json:"is_masked,omitempty"`
}

// WebSocketStats WebSocket 统计信息
type WebSocketStats struct {
	TotalConnections  int64 `json:"total_connections"`
	ActiveConnections int64 `json:"active_connections"`
	ClosedConnections int64 `json:"closed_connections"`
	TotalMessages     int64 `json:"total_messages"`
	TotalBytesSent    int64 `json:"total_bytes_sent"`
	TotalBytesRecv    int64 `json:"total_bytes_received"`
	ErrorCount        int64 `json:"error_count"`
}

// NewWebSocketTracker 创建 WebSocket 追踪器
func NewWebSocketTracker(config *WebSocketConfig) *WebSocketTracker {
	if config == nil {
		config = DefaultWebSocketConfig()
	}

	return &WebSocketTracker{
		config:       config,
		connections:  make(map[string]*WebSocketConnection),
		messageQueue: make(chan *WebSocketMessage, config.MessageBufferSize),
		stopCh:       make(chan struct{}),
	}
}

// Start 启动 WebSocket 追踪器
func (wt *WebSocketTracker) Start() {
	if !wt.config.Enabled {
		return
	}

	wt.wg.Add(1)
	go wt.messageWorker()

	log.Logger.Info("WebSocketTracker: started")
}

// Stop 停止 WebSocket 追踪器
func (wt *WebSocketTracker) Stop() {
	close(wt.stopCh)
	wt.wg.Wait()
	log.Logger.Info("WebSocketTracker: stopped")
}

// messageWorker 消息处理工作协程
func (wt *WebSocketTracker) messageWorker() {
	defer wt.wg.Done()

	for {
		select {
		case <-wt.stopCh:
			return
		case msg := <-wt.messageQueue:
			wt.processMessage(msg)
		}
	}
}

// processMessage 处理单个消息
func (wt *WebSocketTracker) processMessage(msg *WebSocketMessage) {
	wt.mu.Lock()
	defer wt.mu.Unlock()

	conn, ok := wt.connections[msg.ConnectionID]
	if !ok {
		return
	}

	conn.mu.Lock()
	defer conn.mu.Unlock()

	// 更新统计
	if msg.Direction == "sent" {
		conn.MessagesSent++
		conn.BytesSent += int64(msg.PayloadSize)
		atomic.AddInt64(&wt.stats.TotalBytesSent, int64(msg.PayloadSize))
	} else {
		conn.MessagesReceived++
		conn.BytesReceived += int64(msg.PayloadSize)
		atomic.AddInt64(&wt.stats.TotalBytesRecv, int64(msg.PayloadSize))
	}
	atomic.AddInt64(&wt.stats.TotalMessages, 1)

	// 存储消息 (如果启用且未超限)
	if wt.config.CaptureMessages && len(conn.Messages) < conn.maxMessages {
		conn.Messages = append(conn.Messages, msg)
	}
}

// SetupPageListeners 为页面设置 WebSocket 事件监听器
func (wt *WebSocketTracker) SetupPageListeners(page *rod.Page, pageURL string) {
	if !wt.config.Enabled || page == nil {
		return
	}

	// WebSocket 连接创建
	go page.EachEvent(func(e *proto.NetworkWebSocketCreated) {
		wt.handleWebSocketCreated(e, pageURL)
	})()

	// WebSocket 关闭
	go page.EachEvent(func(e *proto.NetworkWebSocketClosed) {
		wt.handleWebSocketClosed(e)
	})()

	// WebSocket 收到帧
	go page.EachEvent(func(e *proto.NetworkWebSocketFrameReceived) {
		wt.handleFrameReceived(e)
	})()

	// WebSocket 发送帧
	go page.EachEvent(func(e *proto.NetworkWebSocketFrameSent) {
		wt.handleFrameSent(e)
	})()

	// WebSocket 错误
	go page.EachEvent(func(e *proto.NetworkWebSocketFrameError) {
		wt.handleFrameError(e)
	})()

	// WebSocket 握手响应
	go page.EachEvent(func(e *proto.NetworkWebSocketHandshakeResponseReceived) {
		wt.handleHandshakeResponse(e)
	})()

	log.Logger.Debugf("WebSocketTracker: setup listeners for %s", pageURL)
}

// handleWebSocketCreated 处理 WebSocket 连接创建
func (wt *WebSocketTracker) handleWebSocketCreated(e *proto.NetworkWebSocketCreated, pageURL string) {
	wt.mu.Lock()
	defer wt.mu.Unlock()

	// 检查连接数限制
	if len(wt.connections) >= wt.config.MaxConnections {
		log.Logger.Debugf("WebSocketTracker: max connections reached, skipping %s", e.URL)
		return
	}

	requestID := string(e.RequestID)
	conn := &WebSocketConnection{
		RequestID:    requestID,
		URL:          e.URL,
		InitiatorURL: pageURL,
		CreatedAt:    time.Now(),
		Status:       "connecting",
		Messages:     make([]*WebSocketMessage, 0),
		maxMessages:  wt.config.MaxMessagesPerConn,
	}

	// 提取发起者 URL
	if e.Initiator != nil && e.Initiator.URL != "" {
		conn.InitiatorURL = e.Initiator.URL
	}

	wt.connections[requestID] = conn
	atomic.AddInt64(&wt.stats.TotalConnections, 1)
	atomic.AddInt64(&wt.stats.ActiveConnections, 1)

	log.Logger.Debugf("WebSocketTracker: connection created %s -> %s", pageURL, e.URL)
}

// handleWebSocketClosed 处理 WebSocket 连接关闭
func (wt *WebSocketTracker) handleWebSocketClosed(e *proto.NetworkWebSocketClosed) {
	wt.mu.Lock()
	defer wt.mu.Unlock()

	requestID := string(e.RequestID)
	conn, ok := wt.connections[requestID]
	if !ok {
		return
	}

	conn.mu.Lock()
	conn.Status = "closed"
	conn.ClosedAt = time.Now()
	conn.mu.Unlock()

	atomic.AddInt64(&wt.stats.ActiveConnections, -1)
	atomic.AddInt64(&wt.stats.ClosedConnections, 1)

	log.Logger.Debugf("WebSocketTracker: connection closed %s", conn.URL)
}

// handleHandshakeResponse 处理握手响应
func (wt *WebSocketTracker) handleHandshakeResponse(e *proto.NetworkWebSocketHandshakeResponseReceived) {
	wt.mu.Lock()
	defer wt.mu.Unlock()

	requestID := string(e.RequestID)
	conn, ok := wt.connections[requestID]
	if !ok {
		return
	}

	conn.mu.Lock()
	conn.Status = "open"
	conn.mu.Unlock()

	log.Logger.Debugf("WebSocketTracker: handshake completed %s", conn.URL)
}

// handleFrameReceived 处理收到的帧
func (wt *WebSocketTracker) handleFrameReceived(e *proto.NetworkWebSocketFrameReceived) {
	if !wt.config.CaptureMessages {
		return
	}

	requestID := string(e.RequestID)
	wt.mu.RLock()
	conn, ok := wt.connections[requestID]
	wt.mu.RUnlock()
	if !ok {
		return
	}

	msg := &WebSocketMessage{
		ConnectionID: requestID,
		URL:          conn.URL,
		Direction:    "received",
		Opcode:       int(e.Response.Opcode),
		PayloadSize:  len(e.Response.PayloadData),
		Timestamp:    time.Now(),
		IsMasked:     e.Response.Mask,
	}

	// 存储 payload (如果在大小限制内)
	if len(e.Response.PayloadData) <= wt.config.MaxMessageSize {
		// 对二进制数据进行 base64 编码
		if e.Response.Opcode == 2 {
			msg.PayloadData = base64.StdEncoding.EncodeToString([]byte(e.Response.PayloadData))
		} else {
			msg.PayloadData = e.Response.PayloadData
		}
	}

	select {
	case wt.messageQueue <- msg:
	default:
		// 队列满，丢弃消息
		log.Logger.Debugf("WebSocketTracker: message queue full, dropping message")
	}
}

// handleFrameSent 处理发送的帧
func (wt *WebSocketTracker) handleFrameSent(e *proto.NetworkWebSocketFrameSent) {
	if !wt.config.CaptureMessages {
		return
	}

	requestID := string(e.RequestID)
	wt.mu.RLock()
	conn, ok := wt.connections[requestID]
	wt.mu.RUnlock()
	if !ok {
		return
	}

	msg := &WebSocketMessage{
		ConnectionID: requestID,
		URL:          conn.URL,
		Direction:    "sent",
		Opcode:       int(e.Response.Opcode),
		PayloadSize:  len(e.Response.PayloadData),
		Timestamp:    time.Now(),
		IsMasked:     e.Response.Mask,
	}

	// 存储 payload (如果在大小限制内)
	if len(e.Response.PayloadData) <= wt.config.MaxMessageSize {
		if e.Response.Opcode == 2 {
			msg.PayloadData = base64.StdEncoding.EncodeToString([]byte(e.Response.PayloadData))
		} else {
			msg.PayloadData = e.Response.PayloadData
		}
	}

	select {
	case wt.messageQueue <- msg:
	default:
		log.Logger.Debugf("WebSocketTracker: message queue full, dropping message")
	}
}

// handleFrameError 处理帧错误
func (wt *WebSocketTracker) handleFrameError(e *proto.NetworkWebSocketFrameError) {
	requestID := string(e.RequestID)
	atomic.AddInt64(&wt.stats.ErrorCount, 1)
	log.Logger.Debugf("WebSocketTracker: frame error on %s: %s", requestID, e.ErrorMessage)
}

// GetConnection 获取连接信息
func (wt *WebSocketTracker) GetConnection(requestID string) *WebSocketConnection {
	wt.mu.RLock()
	defer wt.mu.RUnlock()
	return wt.connections[requestID]
}

// GetAllConnections 获取所有连接
func (wt *WebSocketTracker) GetAllConnections() []*WebSocketConnection {
	wt.mu.RLock()
	defer wt.mu.RUnlock()

	result := make([]*WebSocketConnection, 0, len(wt.connections))
	for _, conn := range wt.connections {
		result = append(result, conn)
	}
	return result
}

// GetStats 获取统计信息
func (wt *WebSocketTracker) GetStats() WebSocketStats {
	return WebSocketStats{
		TotalConnections:  atomic.LoadInt64(&wt.stats.TotalConnections),
		ActiveConnections: atomic.LoadInt64(&wt.stats.ActiveConnections),
		ClosedConnections: atomic.LoadInt64(&wt.stats.ClosedConnections),
		TotalMessages:     atomic.LoadInt64(&wt.stats.TotalMessages),
		TotalBytesSent:    atomic.LoadInt64(&wt.stats.TotalBytesSent),
		TotalBytesRecv:    atomic.LoadInt64(&wt.stats.TotalBytesRecv),
		ErrorCount:        atomic.LoadInt64(&wt.stats.ErrorCount),
	}
}

// GetWebSocketURLs 获取所有 WebSocket URL 列表
func (wt *WebSocketTracker) GetWebSocketURLs() []string {
	wt.mu.RLock()
	defer wt.mu.RUnlock()

	urls := make([]string, 0, len(wt.connections))
	seen := make(map[string]bool)

	for _, conn := range wt.connections {
		if !seen[conn.URL] {
			seen[conn.URL] = true
			urls = append(urls, conn.URL)
		}
	}
	return urls
}

// ExtractEndpoints 从 WebSocket URL 提取 API 端点信息
func (wt *WebSocketTracker) ExtractEndpoints() []*WebSocketEndpoint {
	wt.mu.RLock()
	defer wt.mu.RUnlock()

	endpoints := make(map[string]*WebSocketEndpoint)

	for _, conn := range wt.connections {
		parsed, err := url.Parse(conn.URL)
		if err != nil {
			continue
		}

		// 生成端点 key
		key := parsed.Host + parsed.Path

		endpoint, ok := endpoints[key]
		if !ok {
			endpoint = &WebSocketEndpoint{
				URL:           conn.URL,
				Host:          parsed.Host,
				Path:          parsed.Path,
				Protocol:      parsed.Scheme,
				QueryParams:   parsed.Query(),
				ConnectionIDs: make([]string, 0),
			}
			endpoints[key] = endpoint
		}

		endpoint.ConnectionIDs = append(endpoint.ConnectionIDs, conn.RequestID)
		endpoint.TotalConnections++
		endpoint.TotalMessages += conn.MessagesSent + conn.MessagesReceived
	}

	result := make([]*WebSocketEndpoint, 0, len(endpoints))
	for _, ep := range endpoints {
		result = append(result, ep)
	}
	return result
}

// WebSocketEndpoint WebSocket 端点信息
type WebSocketEndpoint struct {
	URL              string              `json:"url"`
	Host             string              `json:"host"`
	Path             string              `json:"path"`
	Protocol         string              `json:"protocol"` // ws 或 wss
	QueryParams      map[string][]string `json:"query_params,omitempty"`
	ConnectionIDs    []string            `json:"connection_ids"`
	TotalConnections int                 `json:"total_connections"`
	TotalMessages    int64               `json:"total_messages"`
}

// WebSocketResult WebSocket 结果摘要 (用于输出)
type WebSocketResult struct {
	Endpoints   []*WebSocketEndpoint   `json:"endpoints"`
	Connections []*WebSocketConnection `json:"connections"`
	Stats       WebSocketStats         `json:"stats"`
}

// GetResult 获取 WebSocket 结果
func (wt *WebSocketTracker) GetResult() *WebSocketResult {
	return &WebSocketResult{
		Endpoints:   wt.ExtractEndpoints(),
		Connections: wt.GetAllConnections(),
		Stats:       wt.GetStats(),
	}
}

// Clear 清空所有数据
func (wt *WebSocketTracker) Clear() {
	wt.mu.Lock()
	defer wt.mu.Unlock()

	wt.connections = make(map[string]*WebSocketConnection)
	wt.stats = WebSocketStats{}
}
