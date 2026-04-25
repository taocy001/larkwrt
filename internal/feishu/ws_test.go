package feishu

import (
	"encoding/json"
	"testing"
	"time"
)

// ── Protobuf frame encode / decode ────────────────────────────────────────────

func TestMarshalUnmarshalFrame_roundtrip(t *testing.T) {
	original := Frame{
		SeqID:   42,
		LogID:   99,
		Service: 7,
		Method:  FrameMethodData,
		Headers: []FrameHeader{
			{Key: "type", Value: "event"},
			{Key: "message_id", Value: "msg-123"},
		},
		Payload: []byte(`{"key":"value"}`),
	}

	data := marshalFrame(original)
	decoded, err := unmarshalFrame(data)
	if err != nil {
		t.Fatalf("unmarshalFrame: %v", err)
	}

	if decoded.SeqID != 42 {
		t.Errorf("SeqID: got %d want 42", decoded.SeqID)
	}
	if decoded.Method != FrameMethodData {
		t.Errorf("Method: got %d want %d", decoded.Method, FrameMethodData)
	}
	if decoded.GetHeader("type") != "event" {
		t.Errorf("header type: got %q want event", decoded.GetHeader("type"))
	}
	if decoded.GetHeader("message_id") != "msg-123" {
		t.Errorf("header message_id: got %q", decoded.GetHeader("message_id"))
	}
	if string(decoded.Payload) != `{"key":"value"}` {
		t.Errorf("Payload: got %q", string(decoded.Payload))
	}
}

func TestMarshalUnmarshalFrame_controlPing(t *testing.T) {
	f := Frame{
		SeqID:   1,
		Method:  FrameMethodControl,
		Service: 3,
		Headers: []FrameHeader{{Key: "type", Value: FrameTypePing}},
	}
	data := marshalFrame(f)
	got, err := unmarshalFrame(data)
	if err != nil {
		t.Fatalf("unmarshalFrame: %v", err)
	}
	if got.Method != FrameMethodControl {
		t.Errorf("Method: got %d want %d", got.Method, FrameMethodControl)
	}
	if got.GetHeader("type") != FrameTypePing {
		t.Errorf("type header: got %q want %q", got.GetHeader("type"), FrameTypePing)
	}
}

func TestMarshalUnmarshalFrame_emptyPayload(t *testing.T) {
	f := Frame{SeqID: 5, Method: FrameMethodControl}
	got, err := unmarshalFrame(marshalFrame(f))
	if err != nil {
		t.Fatalf("unmarshalFrame: %v", err)
	}
	if len(got.Payload) != 0 {
		t.Errorf("expected empty payload, got %d bytes", len(got.Payload))
	}
}

func TestMarshalFrame_multipleHeaders(t *testing.T) {
	f := Frame{
		SeqID:  1,
		Method: FrameMethodData,
		Headers: []FrameHeader{
			{Key: "type", Value: "event"},
			{Key: "trace_id", Value: "trace-abc"},
			{Key: "sum", Value: "1"},
			{Key: "seq", Value: "0"},
		},
		Payload: []byte("{}"),
	}
	got, err := unmarshalFrame(marshalFrame(f))
	if err != nil {
		t.Fatalf("unmarshalFrame: %v", err)
	}
	if len(got.Headers) != 4 {
		t.Errorf("headers count: got %d want 4", len(got.Headers))
	}
	if got.GetHeader("trace_id") != "trace-abc" {
		t.Errorf("trace_id: got %q", got.GetHeader("trace_id"))
	}
}

func TestUnmarshalFrame_truncated(t *testing.T) {
	// Even a partially corrupted frame should not panic
	_, err := unmarshalFrame([]byte{0x08, 0x80, 0x80}) // incomplete varint
	if err == nil {
		t.Log("handled gracefully") // error or empty frame both acceptable
	}
}

func TestUnmarshalFrame_empty(t *testing.T) {
	f, err := unmarshalFrame([]byte{})
	if err != nil {
		t.Fatalf("empty buf: %v", err)
	}
	// All fields zero
	if f.SeqID != 0 || f.Method != 0 {
		t.Errorf("expected zero frame, got %+v", f)
	}
}

func TestFrame_GetHeader_missing(t *testing.T) {
	f := Frame{Headers: []FrameHeader{{Key: "type", Value: "event"}}}
	if v := f.GetHeader("nonexistent"); v != "" {
		t.Errorf("expected empty string, got %q", v)
	}
}

func TestMarshalFrame_pongWithPayload(t *testing.T) {
	payload, _ := json.Marshal(map[string]int{"PingInterval": 30})
	f := Frame{
		SeqID:   10,
		Method:  FrameMethodControl,
		Headers: []FrameHeader{{Key: "type", Value: FrameTypePong}},
		Payload: payload,
	}
	got, err := unmarshalFrame(marshalFrame(f))
	if err != nil {
		t.Fatalf("unmarshalFrame: %v", err)
	}
	if got.GetHeader("type") != FrameTypePong {
		t.Errorf("type: got %q", got.GetHeader("type"))
	}
	var conf struct{ PingInterval int }
	if err := json.Unmarshal(got.Payload, &conf); err != nil {
		t.Fatalf("payload json: %v", err)
	}
	if conf.PingInterval != 30 {
		t.Errorf("PingInterval: got %d want 30", conf.PingInterval)
	}
}

// ── Token Manager ─────────────────────────────────────────────────────────────

func TestTokenManager_ForceSet(t *testing.T) {
	tm := NewTokenManager("app_id", "app_secret", "http://localhost:9999")
	tm.ForceSet("test-token-xyz", time.Hour)

	tok, err := tm.Get()
	if err != nil {
		t.Fatalf("Get after ForceSet: %v", err)
	}
	if tok != "test-token-xyz" {
		t.Errorf("token: got %q want test-token-xyz", tok)
	}
}
