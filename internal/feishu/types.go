package feishu

import "encoding/json"

// ── App Access Token ──────────────────────────────────────────────────────────

type tokenReq struct {
	AppID     string `json:"app_id"`
	AppSecret string `json:"app_secret"`
}

type tokenResp struct {
	Code           int    `json:"code"`
	Msg            string `json:"msg"`
	AppAccessToken string `json:"app_access_token"`
	Expire         int    `json:"expire"` // seconds
}

// ── WebSocket endpoint ────────────────────────────────────────────────────────

type wsEndpointResp struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data *struct {
		URL          string `json:"URL"` // capital URL per official SDK
		ClientConfig *struct {
			ReconnectCount    int `json:"ReconnectCount"`
			ReconnectInterval int `json:"ReconnectInterval"`
			PingInterval      int `json:"PingInterval"` // seconds
		} `json:"ClientConfig"`
	} `json:"data"`
}

// ── WebSocket frame (protobuf) ────────────────────────────────────────────────

// Frame matches the protobuf message used by the official Feishu WS SDK.
type Frame struct {
	SeqID           uint64
	LogID           uint64
	Service         int32
	Method          int32 // 0 = Control, 1 = Data
	Headers         []FrameHeader
	PayloadEncoding string
	PayloadType     string
	Payload         []byte
	LogIDNew        string
}

type FrameHeader struct {
	Key   string
	Value string
}

func (f Frame) GetHeader(key string) string {
	for _, h := range f.Headers {
		if h.Key == key {
			return h.Value
		}
	}
	return ""
}

const (
	FrameMethodControl = int32(0)
	FrameMethodData    = int32(1)

	FrameTypeEvent = "event"
	FrameTypePing  = "ping"
	FrameTypePong  = "pong"
)

// ── Feishu Event Schema 2.0 ───────────────────────────────────────────────────

type EventEnvelope struct {
	Schema string          `json:"schema"`
	Header EventHeader     `json:"header"`
	Event  json.RawMessage `json:"event"`
}

type EventHeader struct {
	EventID   string `json:"event_id"`
	EventType string `json:"event_type"`
	CreateTime string `json:"create_time"`
	Token     string `json:"token"`
	AppID     string `json:"app_id"`
}

// im.message.receive_v1
type IMMessageEvent struct {
	Sender  IMSender  `json:"sender"`
	Message IMMessage `json:"message"`
}

type IMSender struct {
	SenderID   IMSenderID `json:"sender_id"`
	SenderType string     `json:"sender_type"`
}

type IMSenderID struct {
	UserID  string `json:"user_id"`
	OpenID  string `json:"open_id"`
	UnionID string `json:"union_id"`
}

type IMMessage struct {
	MessageID   string `json:"message_id"`
	ChatID      string `json:"chat_id"`
	ChatType    string `json:"chat_type"`
	MessageType string `json:"message_type"`
	Content     string `json:"content"` // JSON string
	CreateTime  string `json:"create_time"`
}

// card.action.trigger
type CardActionEvent struct {
	Operator CardOperator       `json:"operator"`
	Action   CardActionDetail   `json:"action"`
	Token    string             `json:"token"`
	Context  CardActionContext  `json:"context"`
}

type CardActionContext struct {
	OpenMessageID string `json:"open_message_id"`
	OpenChatID    string `json:"open_chat_id"`
}

type CardOperator struct {
	UserID string `json:"user_id"`
	OpenID string `json:"open_id"`
}

type CardActionDetail struct {
	Tag   string         `json:"tag"`
	Value map[string]any `json:"value"`
}

// ── Message Send ──────────────────────────────────────────────────────────────

type SendMsgReq struct {
	ReceiveID string `json:"receive_id"`
	MsgType   string `json:"msg_type"`
	Content   string `json:"content"` // JSON-encoded card or text
	UUID      string `json:"uuid,omitempty"`
}

type SendMsgResp struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		MessageID string `json:"message_id"`
	} `json:"data"`
}

type UpdateMsgReq struct {
	Content string `json:"content"`
	MsgType string `json:"msg_type"`
}

// ── Reply message ─────────────────────────────────────────────────────────────

type ReplyMsgReq struct {
	Content   string `json:"content"`
	MsgType   string `json:"msg_type"`
	ReplyInThread bool `json:"reply_in_thread,omitempty"`
}

// ── Card ──────────────────────────────────────────────────────────────────────

// Card is the top-level Feishu interactive card (JSON 2.0).
type Card struct {
	Schema string      `json:"schema"`
	Config CardConfig  `json:"config"`
	Header *CardHeader `json:"header,omitempty"`
	Body   CardBody    `json:"body"`
}

type CardBody struct {
	Elements []CardElement `json:"elements"`
}

type CardConfig struct {
	WideScreenMode bool `json:"wide_screen_mode"`
}

type CardHeader struct {
	Title    TextObject `json:"title"`
	Template string     `json:"template"` // "blue"|"red"|"yellow"|"green"|"grey"
}

// CardElement is intentionally untyped at the top level to support
// different element structures (div, action, column_set, hr).
type CardElement = map[string]any

func textObj(tag, content string) map[string]any {
	return map[string]any{"tag": tag, "content": content}
}

// TextObject for use in card header only.
type TextObject struct {
	Tag     string `json:"tag"`
	Content string `json:"content"`
}
