package feishu

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Client wraps the Feishu REST API for sending messages.
type Client struct {
	tokens  *TokenManager
	chatID  string
	baseURL string
	http    *http.Client
}

// NewClient creates a Client. Pass baseURL="" to use the production Feishu endpoint.
func NewClient(appID, appSecret, chatID, baseURL string) *Client {
	if baseURL == "" {
		baseURL = defaultFeishuBase
	}
	return &Client{
		tokens:  NewTokenManager(appID, appSecret, baseURL),
		chatID:  chatID,
		baseURL: baseURL,
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *Client) TokenManager() *TokenManager { return c.tokens }

// SendCard sends a rich interactive card to the configured chat.
func (c *Client) SendCard(card *Card) (string, error) {
	content, err := json.Marshal(card)
	if err != nil {
		return "", err
	}
	return c.sendMessage(SendMsgReq{
		ReceiveID: c.chatID,
		MsgType:   "interactive",
		Content:   string(content),
	})
}

// SendText sends a plain text message.
func (c *Client) SendText(text string) (string, error) {
	content, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return "", err
	}
	return c.sendMessage(SendMsgReq{
		ReceiveID: c.chatID,
		MsgType:   "text",
		Content:   string(content),
	})
}

// ReplyCard replies to a specific message with a card.
func (c *Client) ReplyCard(msgID string, card *Card) (string, error) {
	content, err := json.Marshal(card)
	if err != nil {
		return "", err
	}
	return c.replyMessage(msgID, ReplyMsgReq{
		Content: string(content),
		MsgType: "interactive",
	})
}

// ReplyText replies to a specific message with text.
func (c *Client) ReplyText(msgID, text string) (string, error) {
	content, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return "", err
	}
	return c.replyMessage(msgID, ReplyMsgReq{
		Content: string(content),
		MsgType: "text",
	})
}

// UpdateCard replaces the content of an existing card message.
func (c *Client) UpdateCard(msgID string, card *Card) error {
	content, err := json.Marshal(card)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("%s/im/v1/messages/%s", c.baseURL, msgID)
	return c.patchJSON(url, UpdateMsgReq{Content: string(content), MsgType: "interactive"})
}

// ── internal helpers ──────────────────────────────────────────────────────────

// receiveIDType returns the Feishu receive_id_type based on the ID prefix.
// ou_ → open_id (personal chat), oc_ → chat_id (group), default → open_id.
func receiveIDType(id string) string {
	if len(id) >= 3 && id[:3] == "oc_" {
		return "chat_id"
	}
	return "open_id"
}

func (c *Client) sendMessage(req SendMsgReq) (string, error) {
	token, err := c.tokens.Get()
	if err != nil {
		return "", err
	}
	url := c.baseURL + "/im/v1/messages?receive_id_type=" + receiveIDType(req.ReceiveID)
	body, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("send message: %w", err)
	}
	defer resp.Body.Close()

	var result SendMsgResp
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if result.Code != 0 {
		return "", fmt.Errorf("feishu send error %d: %s", result.Code, result.Msg)
	}
	return result.Data.MessageID, nil
}

func (c *Client) replyMessage(msgID string, req ReplyMsgReq) (string, error) {
	token, err := c.tokens.Get()
	if err != nil {
		return "", err
	}
	url := fmt.Sprintf("%s/im/v1/messages/%s/reply", c.baseURL, msgID)
	body, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("reply message: %w", err)
	}
	defer resp.Body.Close()

	var result SendMsgResp
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if result.Code != 0 {
		return "", fmt.Errorf("feishu reply error %d: %s", result.Code, result.Msg)
	}
	return result.Data.MessageID, nil
}

func (c *Client) patchJSON(url string, payload any) error {
	token, err := c.tokens.Get()
	if err != nil {
		return err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequest(http.MethodPatch, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}
	if result.Code != 0 {
		return fmt.Errorf("feishu update error %d: %s", result.Code, result.Msg)
	}
	return nil
}
