// Package integration provides a mock Feishu server for end-to-end tests.
package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	"larkwrt/internal/feishu"

	"github.com/gorilla/websocket"
)

const testToken = "test_app_access_token_xyz"

// SentMessage records a message sent to the mock Feishu API.
type SentMessage struct {
	ReceiveID string
	MsgType   string
	Content   string
}

// MockFeishu runs a minimal Feishu HTTP + WebSocket server for testing.
type MockFeishu struct {
	server   *httptest.Server
	upgrader websocket.Upgrader

	mu       sync.Mutex
	conns    []*websocket.Conn
	sent     []SentMessage
	msgIDSeq int
}

// NewMockFeishu starts the mock server and returns it.
func NewMockFeishu() *MockFeishu {
	m := &MockFeishu{
		upgrader: websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }},
	}

	mux := http.NewServeMux()
	// Token endpoint (REST client still uses tokens)
	mux.HandleFunc("/open-apis/auth/v3/app_access_token/internal", m.handleToken)
	// WS endpoint discovery — POST /callback/ws/endpoint (no /open-apis prefix)
	mux.HandleFunc("/callback/ws/endpoint", m.handleWSEndpoint)
	// Message send
	mux.HandleFunc("/open-apis/im/v1/messages", m.handleSendMessage)
	// Message reply + update
	mux.HandleFunc("/open-apis/im/v1/messages/", m.handleMessageOp)
	// WebSocket connection
	mux.HandleFunc("/ws", m.handleWebSocket)

	m.server = httptest.NewServer(mux)
	return m
}

// BaseURL returns the HTTP base URL (to pass to feishu.NewClient / NewWSClient).
func (m *MockFeishu) BaseURL() string {
	return m.server.URL + "/open-apis"
}

// Close shuts down the mock server.
func (m *MockFeishu) Close() {
	m.mu.Lock()
	for _, c := range m.conns {
		c.Close()
	}
	m.mu.Unlock()
	m.server.Close()
}

// SentMessages returns a copy of all messages sent to the mock.
func (m *MockFeishu) SentMessages() []SentMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]SentMessage, len(m.sent))
	copy(out, m.sent)
	return out
}

// SendEvent pushes a Feishu event to all connected WS clients via protobuf frame.
func (m *MockFeishu) SendEvent(eventType string, eventPayload any) error {
	payloadBytes, err := json.Marshal(eventPayload)
	if err != nil {
		return err
	}
	env := feishu.EventEnvelope{
		Schema: "2.0",
		Header: feishu.EventHeader{
			EventID:    fmt.Sprintf("ev_%d", time.Now().UnixNano()),
			EventType:  eventType,
			CreateTime: fmt.Sprintf("%d", time.Now().UnixMilli()),
		},
		Event: payloadBytes,
	}
	envBytes, _ := json.Marshal(env)

	frame := encodeTestFrame(envBytes, "msg-test")

	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.conns {
		if err := c.WriteMessage(websocket.BinaryMessage, frame); err != nil {
			return err
		}
	}
	return nil
}

// WaitForMessage waits up to timeout for a message matching pred to be sent.
func (m *MockFeishu) WaitForMessage(timeout time.Duration, pred func(SentMessage) bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, msg := range m.SentMessages() {
			if pred(msg) {
				return true
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// ConnCount returns the number of active WebSocket connections.
func (m *MockFeishu) ConnCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.conns)
}

// ── HTTP handlers ─────────────────────────────────────────────────────────────

func (m *MockFeishu) handleToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"code":             0,
		"app_access_token": testToken,
		"expire":           7200,
	})
}

func (m *MockFeishu) handleWSEndpoint(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	wsURL := "ws" + m.server.URL[4:] + "/ws?service_id=1&device_id=mock-device"
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"code": 0,
		"data": map[string]any{
			"URL": wsURL, // capital URL per official SDK
			"ClientConfig": map[string]any{
				"PingInterval":      30,
				"ReconnectCount":    3,
				"ReconnectInterval": 1,
			},
		},
	})
}

func (m *MockFeishu) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ReceiveID string `json:"receive_id"`
		MsgType   string `json:"msg_type"`
		Content   string `json:"content"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	m.mu.Lock()
	m.msgIDSeq++
	msgID := fmt.Sprintf("om_test_%d", m.msgIDSeq)
	m.sent = append(m.sent, SentMessage{
		ReceiveID: req.ReceiveID,
		MsgType:   req.MsgType,
		Content:   req.Content,
	})
	m.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"code": 0,
		"data": map[string]any{"message_id": msgID},
	})
}

func (m *MockFeishu) handleMessageOp(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost || r.Method == http.MethodPatch {
		var req struct {
			Content string `json:"content"`
			MsgType string `json:"msg_type"`
		}
		json.NewDecoder(r.Body).Decode(&req)

		m.mu.Lock()
		m.msgIDSeq++
		msgID := fmt.Sprintf("om_test_%d", m.msgIDSeq)
		m.sent = append(m.sent, SentMessage{
			MsgType: req.MsgType,
			Content: req.Content,
		})
		m.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"code": 0,
			"data": map[string]any{"message_id": msgID},
		})
		return
	}
	http.Error(w, "not found", http.StatusNotFound)
}

// ── WebSocket handler ─────────────────────────────────────────────────────────

func (m *MockFeishu) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := m.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	m.mu.Lock()
	m.conns = append(m.conns, conn)
	m.mu.Unlock()

	// Read frames from client (pings, acks) — respond to pings with pongs
	go func() {
		defer func() {
			m.mu.Lock()
			for i, c := range m.conns {
				if c == conn {
					m.conns = append(m.conns[:i], m.conns[i+1:]...)
					break
				}
			}
			m.mu.Unlock()
			conn.Close()
		}()
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			f, err := feishu.UnmarshalFrameForTest(msg)
			if err != nil {
				continue
			}
			if f.Method == feishu.FrameMethodControl && f.GetHeader("type") == feishu.FrameTypePing {
				pong := feishu.Frame{
					SeqID:   f.SeqID,
					Method:  feishu.FrameMethodControl,
					Headers: []feishu.FrameHeader{{Key: "type", Value: feishu.FrameTypePong}},
				}
				conn.WriteMessage(websocket.BinaryMessage, feishu.MarshalFrameForTest(pong))
			}
		}
	}()
}

// encodeTestFrame creates a protobuf data frame containing eventPayload.
func encodeTestFrame(eventPayload []byte, msgID string) []byte {
	f := feishu.Frame{
		SeqID:  1,
		Method: feishu.FrameMethodData,
		Headers: []feishu.FrameHeader{
			{Key: "type", Value: feishu.FrameTypeEvent},
			{Key: "message_id", Value: msgID},
			{Key: "sum", Value: "1"},
			{Key: "seq", Value: "0"},
		},
		Payload: eventPayload,
	}
	return feishu.MarshalFrameForTest(f)
}
