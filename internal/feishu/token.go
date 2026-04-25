package feishu

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

const defaultFeishuBase = "https://open.feishu.cn/open-apis"

// TokenManager fetches and auto-refreshes the app_access_token.
type TokenManager struct {
	appID     string
	appSecret string
	baseURL   string // e.g. "https://open.feishu.cn/open-apis"
	client    *http.Client

	mu        sync.RWMutex
	token     string
	expiresAt time.Time
}

func NewTokenManager(appID, appSecret, baseURL string) *TokenManager {
	if baseURL == "" {
		baseURL = defaultFeishuBase
	}
	return &TokenManager{
		appID:     appID,
		appSecret: appSecret,
		baseURL:   baseURL,
		client:    &http.Client{Timeout: 10 * time.Second},
	}
}

// Get returns a valid token, refreshing if within 60 s of expiry.
func (t *TokenManager) Get() (string, error) {
	t.mu.RLock()
	if t.token != "" && time.Until(t.expiresAt) > 60*time.Second {
		tok := t.token
		t.mu.RUnlock()
		return tok, nil
	}
	t.mu.RUnlock()
	return t.refresh()
}

// ForceSet injects a token directly — used in tests to skip the auth API call.
func (t *TokenManager) ForceSet(token string, ttl time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.token = token
	t.expiresAt = time.Now().Add(ttl)
}

func (t *TokenManager) refresh() (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.token != "" && time.Until(t.expiresAt) > 60*time.Second {
		return t.token, nil
	}

	url := t.baseURL + "/auth/v3/app_access_token/internal"
	body, err := json.Marshal(tokenReq{AppID: t.appID, AppSecret: t.appSecret})
	if err != nil {
		return "", err
	}
	resp, err := t.client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	var result tokenResp
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("token decode: %w", err)
	}
	if result.Code != 0 {
		return "", fmt.Errorf("token error %d: %s", result.Code, result.Msg)
	}
	t.token = result.AppAccessToken
	t.expiresAt = time.Now().Add(time.Duration(result.Expire) * time.Second)
	return t.token, nil
}
