package feishu

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
)

const (
	wsEndpointPath      = "/callback/ws/endpoint"
	defaultPingInterval = 120 * time.Second
	maxReconnectWait    = 60 * time.Second
)

// WSClient manages the Feishu WebSocket long connection.
type WSClient struct {
	appID     string
	appSecret string
	wsBase    string // base URL for WS endpoint (no /open-apis suffix)
	events    chan EventEnvelope
	stop      chan struct{}
	seqID     atomic.Uint64

	writeMu sync.Mutex     // guards all conn.WriteMessage calls
	seenMu  sync.Mutex
	seenIDs map[string]time.Time // event_id → first seen; evicted after 1 hour
}

// NewWSClient creates a WSClient. Pass baseURL="" to use the production Feishu endpoint.
// baseURL follows the same convention as NewClient (e.g. "https://open.feishu.cn/open-apis").
// The WS endpoint lives at the root (no /open-apis), so the suffix is stripped internally.
func NewWSClient(appID, appSecret, baseURL string) *WSClient {
	if baseURL == "" {
		baseURL = defaultFeishuBase
	}
	// WS endpoint does not use the /open-apis path prefix
	wsBase := strings.TrimSuffix(baseURL, "/open-apis")
	return &WSClient{
		appID:     appID,
		appSecret: appSecret,
		wsBase:    wsBase,
		events:    make(chan EventEnvelope, 64),
		stop:      make(chan struct{}),
		seenIDs:   make(map[string]time.Time),
	}
}

// Events returns the channel on which received Feishu events are published.
func (w *WSClient) Events() <-chan EventEnvelope { return w.events }

// Run starts the connection loop. Blocks until Stop is called.
func (w *WSClient) Run() {
	backoff := time.Second
	for {
		if err := w.connect(); err != nil {
			log.Error().Err(err).Dur("retry_in", backoff).Msg("ws connect failed")
		}
		select {
		case <-w.stop:
			return
		case <-time.After(backoff):
			backoff = minDur(backoff*2, maxReconnectWait)
		}
	}
}

func (w *WSClient) Stop() { close(w.stop) }

// connect establishes one WebSocket session until it errors or stop is called.
func (w *WSClient) connect() error {
	wsURL, pingInterval, err := w.getEndpoint()
	if err != nil {
		return fmt.Errorf("get endpoint: %w", err)
	}

	// Extract service_id from WS URL query params (used in ping frames)
	var serviceID int32
	if u, err := url.Parse(wsURL); err == nil {
		if sid := u.Query().Get("service_id"); sid != "" {
			fmt.Sscanf(sid, "%d", &serviceID)
		}
	}

	dialer := websocket.Dialer{HandshakeTimeout: 15 * time.Second}
	conn, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		return fmt.Errorf("ws dial: %w", err)
	}
	defer conn.Close()

	log.Info().Str("url", wsURL).Msg("ws connected")

	pingStop := make(chan struct{})
	go w.pinger(conn, pingInterval, serviceID, pingStop)
	defer close(pingStop)

	for {
		select {
		case <-w.stop:
			return nil
		default:
		}

		conn.SetReadDeadline(time.Now().Add(pingInterval + 30*time.Second))
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("ws read: %w", err)
		}

		frame, err := unmarshalFrame(msg)
		if err != nil {
			log.Warn().Err(err).Msg("decode frame")
			continue
		}

		log.Debug().Int32("method", frame.Method).Str("type", frame.GetHeader("type")).
			Str("msg_id", frame.GetHeader("message_id")).Int("payload_len", len(frame.Payload)).Msg("ws frame")
		if frame.Method == FrameMethodData {
			log.Info().Str("type", frame.GetHeader("type")).Str("msg_id", frame.GetHeader("message_id")).Msg("ws data frame")
		}

		switch frame.Method {
		case FrameMethodControl:
			if frame.GetHeader("type") == FrameTypePong && len(frame.Payload) > 0 {
				var conf struct {
					PingInterval int `json:"PingInterval"`
				}
				if json.Unmarshal(frame.Payload, &conf) == nil && conf.PingInterval > 0 {
					pingInterval = time.Duration(conf.PingInterval) * time.Second
				}
			}
		case FrameMethodData:
			w.handleDataFrame(conn, frame)
		}
	}
}

// getEndpoint discovers the WS URL by POSTing AppID/AppSecret to the endpoint API.
func (w *WSClient) getEndpoint() (wsURL string, pingInterval time.Duration, err error) {
	body, err := json.Marshal(map[string]string{
		"AppID":     w.appID,
		"AppSecret": w.appSecret,
	})
	if err != nil {
		return "", 0, err
	}
	req, err := http.NewRequest(http.MethodPost, w.wsBase+wsEndpointPath, bytes.NewReader(body))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("locale", "zh")

	cl := &http.Client{Timeout: 10 * time.Second}
	resp, err := cl.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("endpoint api: HTTP %d", resp.StatusCode)
	}

	var result wsEndpointResp
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", 0, err
	}
	if result.Code != 0 {
		return "", 0, fmt.Errorf("endpoint api: code=%d msg=%s", result.Code, result.Msg)
	}
	if result.Data == nil || result.Data.URL == "" {
		return "", 0, fmt.Errorf("endpoint api: empty URL in response")
	}
	if !strings.HasPrefix(result.Data.URL, "wss://") && !strings.HasPrefix(result.Data.URL, "ws://") {
		return "", 0, fmt.Errorf("endpoint api: invalid ws URL %q", result.Data.URL)
	}
	if strings.HasPrefix(result.Data.URL, "ws://") {
		log.Warn().Str("url", result.Data.URL).Msg("ws endpoint is not using TLS")
	}

	pi := defaultPingInterval
	if result.Data.ClientConfig != nil && result.Data.ClientConfig.PingInterval > 0 {
		pi = time.Duration(result.Data.ClientConfig.PingInterval) * time.Second
	}
	return result.Data.URL, pi, nil
}

func (w *WSClient) writeFrame(conn *websocket.Conn, frame Frame) error {
	w.writeMu.Lock()
	defer w.writeMu.Unlock()
	return conn.WriteMessage(websocket.BinaryMessage, marshalFrame(frame))
}

func (w *WSClient) pinger(conn *websocket.Conn, interval time.Duration, serviceID int32, stop <-chan struct{}) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			seq := w.seqID.Add(1)
			frame := Frame{
				SeqID:   seq,
				Method:  FrameMethodControl,
				Service: serviceID,
				Headers: []FrameHeader{{Key: "type", Value: FrameTypePing}},
			}
			if err := w.writeFrame(conn, frame); err != nil {
				log.Warn().Err(err).Msg("ws ping write")
				return
			}
		}
	}
}

func (w *WSClient) handleDataFrame(conn *websocket.Conn, frame Frame) {
	msgType := frame.GetHeader("type")
	msgID := frame.GetHeader("message_id")

	// Always ACK the data frame back to server
	defer func() {
		p, err := json.Marshal(map[string]int{"code": 0})
		if err != nil {
			p = []byte(`{"code":0}`)
		}
		ack := Frame{
			SeqID:   frame.SeqID,
			LogID:   frame.LogID,
			Service: frame.Service,
			Method:  FrameMethodData,
			Headers: append(frame.Headers, FrameHeader{Key: "biz_rt", Value: "0"}),
			Payload: p,
		}
		if err := w.writeFrame(conn, ack); err != nil {
			log.Warn().Err(err).Msg("ws ack write")
		}
	}()

	if msgType != FrameTypeEvent {
		return
	}

	var env EventEnvelope
	if err := json.Unmarshal(frame.Payload, &env); err != nil {
		log.Warn().Err(err).Str("msg_id", msgID).Msg("unmarshal event")
		return
	}
	if w.isDuplicate(env.Header.EventID) {
		log.Warn().Str("event_id", env.Header.EventID).Msg("duplicate event dropped")
		return
	}
	select {
	case w.events <- env:
	case <-w.stop:
	default:
		log.Warn().Msg("event channel full, dropping event")
	}
}

func (w *WSClient) isDuplicate(eventID string) bool {
	if eventID == "" {
		return false
	}
	w.seenMu.Lock()
	defer w.seenMu.Unlock()
	cutoff := time.Now().Add(-30 * time.Minute)
	for id, t := range w.seenIDs {
		if t.Before(cutoff) {
			delete(w.seenIDs, id)
		}
	}
	if _, seen := w.seenIDs[eventID]; seen {
		return true
	}
	w.seenIDs[eventID] = time.Now()
	return false
}

func minDur(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
