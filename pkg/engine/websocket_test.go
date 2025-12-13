package engine

import (
	"argo/pkg/log"
	"testing"
	"time"
)

func init() {
	// 初始化日志以避免测试中的 nil pointer
	log.Init(false, true) // quiet mode
}

func TestWebSocketTracker_Basic(t *testing.T) {
	config := &WebSocketConfig{
		Enabled:            true,
		MaxConnections:     10,
		MaxMessagesPerConn: 5,
		MessageBufferSize:  100,
		CaptureMessages:    true,
		MaxMessageSize:     1024,
		Timeout:            5 * time.Second,
	}
	tracker := NewWebSocketTracker(config)
	tracker.Start()
	defer tracker.Stop()

	// 验证初始状态
	stats := tracker.GetStats()
	if stats.TotalConnections != 0 {
		t.Errorf("Expected 0 total connections, got %d", stats.TotalConnections)
	}

	t.Logf("WebSocketTracker basic test passed")
}

func TestWebSocketTracker_Disabled(t *testing.T) {
	config := &WebSocketConfig{
		Enabled: false,
	}
	tracker := NewWebSocketTracker(config)

	// 当禁用时，不应该追踪任何东西
	connections := tracker.GetAllConnections()
	if len(connections) != 0 {
		t.Errorf("Expected 0 connections when disabled, got %d", len(connections))
	}
}

func TestWebSocketConfig_Default(t *testing.T) {
	config := DefaultWebSocketConfig()

	if !config.Enabled {
		t.Error("Expected Enabled to be true by default")
	}
	if config.MaxConnections != 100 {
		t.Errorf("Expected MaxConnections=100, got %d", config.MaxConnections)
	}
	if config.MaxMessagesPerConn != 50 {
		t.Errorf("Expected MaxMessagesPerConn=50, got %d", config.MaxMessagesPerConn)
	}
	if config.MessageBufferSize != 1000 {
		t.Errorf("Expected MessageBufferSize=1000, got %d", config.MessageBufferSize)
	}
	if !config.CaptureMessages {
		t.Error("Expected CaptureMessages to be true by default")
	}
	if config.MaxMessageSize != 1024*1024 {
		t.Errorf("Expected MaxMessageSize=1MB, got %d", config.MaxMessageSize)
	}
}

func TestWebSocketConnection(t *testing.T) {
	conn := &WebSocketConnection{
		RequestID:    "test-123",
		URL:          "wss://example.com/socket",
		InitiatorURL: "https://example.com",
		CreatedAt:    time.Now(),
		Status:       "connecting",
		Messages:     make([]*WebSocketMessage, 0),
		maxMessages:  10,
	}

	// 验证基本字段
	if conn.RequestID != "test-123" {
		t.Errorf("Expected RequestID='test-123', got '%s'", conn.RequestID)
	}
	if conn.URL != "wss://example.com/socket" {
		t.Errorf("Expected URL='wss://example.com/socket', got '%s'", conn.URL)
	}
	if conn.Status != "connecting" {
		t.Errorf("Expected Status='connecting', got '%s'", conn.Status)
	}
}

func TestWebSocketMessage(t *testing.T) {
	msg := &WebSocketMessage{
		ConnectionID: "test-123",
		URL:          "wss://example.com/socket",
		Direction:    "received",
		Opcode:       1, // text
		PayloadData:  "Hello, WebSocket!",
		PayloadSize:  17,
		Timestamp:    time.Now(),
		IsMasked:     false,
	}

	if msg.Direction != "received" {
		t.Errorf("Expected Direction='received', got '%s'", msg.Direction)
	}
	if msg.Opcode != 1 {
		t.Errorf("Expected Opcode=1 (text), got %d", msg.Opcode)
	}
	if msg.PayloadSize != 17 {
		t.Errorf("Expected PayloadSize=17, got %d", msg.PayloadSize)
	}
}

func TestWebSocketTracker_GetResult(t *testing.T) {
	config := DefaultWebSocketConfig()
	tracker := NewWebSocketTracker(config)

	result := tracker.GetResult()

	if result == nil {
		t.Fatal("GetResult returned nil")
	}
	if result.Endpoints == nil {
		t.Error("Expected Endpoints to be initialized")
	}
	if result.Connections == nil {
		t.Error("Expected Connections to be initialized")
	}
}

func TestWebSocketTracker_Clear(t *testing.T) {
	config := DefaultWebSocketConfig()
	tracker := NewWebSocketTracker(config)

	// 模拟添加一些数据
	tracker.mu.Lock()
	tracker.connections["test-1"] = &WebSocketConnection{
		RequestID: "test-1",
		URL:       "wss://example.com/socket1",
	}
	tracker.connections["test-2"] = &WebSocketConnection{
		RequestID: "test-2",
		URL:       "wss://example.com/socket2",
	}
	tracker.mu.Unlock()

	// 清空
	tracker.Clear()

	// 验证已清空
	connections := tracker.GetAllConnections()
	if len(connections) != 0 {
		t.Errorf("Expected 0 connections after Clear, got %d", len(connections))
	}
}

func TestWebSocketTracker_GetWebSocketURLs(t *testing.T) {
	config := DefaultWebSocketConfig()
	tracker := NewWebSocketTracker(config)

	// 添加一些连接（相同 URL 只应该返回一次）
	tracker.mu.Lock()
	tracker.connections["test-1"] = &WebSocketConnection{
		RequestID: "test-1",
		URL:       "wss://example.com/socket",
	}
	tracker.connections["test-2"] = &WebSocketConnection{
		RequestID: "test-2",
		URL:       "wss://example.com/socket", // 相同 URL
	}
	tracker.connections["test-3"] = &WebSocketConnection{
		RequestID: "test-3",
		URL:       "wss://example.com/other",
	}
	tracker.mu.Unlock()

	urls := tracker.GetWebSocketURLs()

	if len(urls) != 2 {
		t.Errorf("Expected 2 unique URLs, got %d", len(urls))
	}

	t.Logf("WebSocket URLs: %v", urls)
}

func TestWebSocketTracker_ExtractEndpoints(t *testing.T) {
	config := DefaultWebSocketConfig()
	tracker := NewWebSocketTracker(config)

	// 添加连接
	tracker.mu.Lock()
	tracker.connections["test-1"] = &WebSocketConnection{
		RequestID:        "test-1",
		URL:              "wss://api.example.com/v1/stream?token=abc",
		MessagesSent:     10,
		MessagesReceived: 20,
	}
	tracker.connections["test-2"] = &WebSocketConnection{
		RequestID:        "test-2",
		URL:              "wss://api.example.com/v1/stream?token=xyz", // 相同端点
		MessagesSent:     5,
		MessagesReceived: 15,
	}
	tracker.mu.Unlock()

	endpoints := tracker.ExtractEndpoints()

	if len(endpoints) != 1 {
		t.Errorf("Expected 1 endpoint (same path), got %d", len(endpoints))
	}

	if len(endpoints) > 0 {
		ep := endpoints[0]
		if ep.TotalConnections != 2 {
			t.Errorf("Expected TotalConnections=2, got %d", ep.TotalConnections)
		}
		if ep.TotalMessages != 50 { // 10+20+5+15
			t.Errorf("Expected TotalMessages=50, got %d", ep.TotalMessages)
		}
		t.Logf("Endpoint: host=%s, path=%s, connections=%d, messages=%d",
			ep.Host, ep.Path, ep.TotalConnections, ep.TotalMessages)
	}
}

func TestWebSocketStats(t *testing.T) {
	stats := WebSocketStats{
		TotalConnections:  10,
		ActiveConnections: 3,
		ClosedConnections: 7,
		TotalMessages:     100,
		TotalBytesSent:    5000,
		TotalBytesRecv:    15000,
		ErrorCount:        2,
	}

	if stats.TotalConnections != 10 {
		t.Errorf("Expected TotalConnections=10, got %d", stats.TotalConnections)
	}
	if stats.ActiveConnections != 3 {
		t.Errorf("Expected ActiveConnections=3, got %d", stats.ActiveConnections)
	}
	if stats.TotalMessages != 100 {
		t.Errorf("Expected TotalMessages=100, got %d", stats.TotalMessages)
	}
}

func TestWebSocketEndpoint(t *testing.T) {
	endpoint := &WebSocketEndpoint{
		URL:              "wss://api.example.com/v1/stream",
		Host:             "api.example.com",
		Path:             "/v1/stream",
		Protocol:         "wss",
		QueryParams:      map[string][]string{"token": {"abc"}},
		ConnectionIDs:    []string{"conn-1", "conn-2"},
		TotalConnections: 2,
		TotalMessages:    150,
	}

	if endpoint.Protocol != "wss" {
		t.Errorf("Expected Protocol='wss', got '%s'", endpoint.Protocol)
	}
	if len(endpoint.ConnectionIDs) != 2 {
		t.Errorf("Expected 2 connection IDs, got %d", len(endpoint.ConnectionIDs))
	}
}

func BenchmarkWebSocketTracker_AddConnection(b *testing.B) {
	config := &WebSocketConfig{
		Enabled:            true,
		MaxConnections:     10000,
		MaxMessagesPerConn: 100,
		MessageBufferSize:  10000,
		CaptureMessages:    true,
		MaxMessageSize:     1024,
	}
	tracker := NewWebSocketTracker(config)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tracker.mu.Lock()
		tracker.connections["test"] = &WebSocketConnection{
			RequestID: "test",
			URL:       "wss://example.com/socket",
		}
		delete(tracker.connections, "test")
		tracker.mu.Unlock()
	}
}

func BenchmarkWebSocketTracker_GetStats(b *testing.B) {
	config := DefaultWebSocketConfig()
	tracker := NewWebSocketTracker(config)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tracker.GetStats()
	}
}
