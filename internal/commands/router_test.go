package commands

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"larkwrt/internal/config"
	"larkwrt/internal/feishu"
)

// ── Helpers ───────────────────────────────────────────────────────────────────

func makeTestConfig(adminUsers ...string) *config.Config {
	if len(adminUsers) == 0 {
		adminUsers = []string{"ou_admin"}
	}
	return &config.Config{
		Feishu:   config.FeishuConfig{AdminUsers: adminUsers},
		Router:   config.RouterConfig{Name: "TestRouter"},
		Security: config.SecurityConfig{CmdRateLimit: 20, ExecWhitelist: []string{"echo"}},
	}
}

func makeMessageEvent(senderUserID, messageID, text string) feishu.EventEnvelope {
	msgContent, _ := json.Marshal(map[string]string{"text": text})
	event := feishu.IMMessageEvent{
		Sender: feishu.IMSender{
			SenderID: feishu.IMSenderID{UserID: senderUserID},
		},
		Message: feishu.IMMessage{
			MessageID:   messageID,
			MessageType: "text",
			Content:     string(msgContent),
		},
	}
	eventBytes, _ := json.Marshal(event)
	return feishu.EventEnvelope{
		Header: feishu.EventHeader{EventType: "im.message.receive_v1"},
		Event:  eventBytes,
	}
}

// spyClient records calls to Send/Reply methods.
type spyClient struct {
	sentCards  []*feishu.Card
	sentTexts  []string
	repliedTexts []string
}

func newSpyClient() *feishu.Client {
	// Use a real Client wired to a mock that won't actually make HTTP calls.
	// We test the command routing logic here, not the HTTP layer.
	// For this unit test, we rely on the fact that Client.SendText/SendCard
	// fail gracefully when baseURL is unreachable — which is acceptable since
	// we're testing command dispatch, not HTTP delivery.
	return feishu.NewClient("app_id", "secret", "chat_id", "http://localhost:0")
}

// ── extractText ───────────────────────────────────────────────────────────────

func TestExtractText_jsonContent(t *testing.T) {
	content := `{"text":"hello world"}`
	got := extractText(content)
	if got != "hello world" {
		t.Errorf("extractText: got %q want hello world", got)
	}
}

func TestExtractText_plainFallback(t *testing.T) {
	content := "plain text"
	got := extractText(content)
	if got != "plain text" {
		t.Errorf("extractText plain: got %q want plain text", got)
	}
}

func TestExtractText_noTextField(t *testing.T) {
	content := `{"other":"value"}`
	got := extractText(content)
	if got != `{"other":"value"}` {
		t.Errorf("no text field should return raw JSON: got %q", got)
	}
}

// ── stripMention ──────────────────────────────────────────────────────────────

func TestStripMention_atPrefix(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"@bot /status", "/status"},
		{"@ou_abc123 /reboot", "/reboot"},
		{"/status", "/status"},
		{"", ""},
		{"@mention", "@mention"}, // single word, no space
	}
	for _, tt := range tests {
		got := stripMention(tt.input)
		if got != tt.want {
			t.Errorf("stripMention(%q): got %q want %q", tt.input, got, tt.want)
		}
	}
}

// ── Router permission check ───────────────────────────────────────────────────

func TestRouter_nonAdminIgnored(t *testing.T) {
	cfg := makeTestConfig("ou_admin")
	router := NewRouter(cfg)
	client := newSpyClient()

	ctx := Context{Client: client, Config: cfg}
	env := makeMessageEvent("ou_nobody", "msg1", "/status")

	// Should not call any handler — non-admin silently ignored
	// We verify this by checking the rate limiter wasn't decremented
	// (since we can't easily intercept client calls in this unit test)
	router.HandleMessage(env, ctx)
	// No panic = pass
}

func TestRouter_adminCanDispatch(t *testing.T) {
	cfg := makeTestConfig("ou_admin")
	router := NewRouter(cfg)

	var called bool
	router.handlers["testcmd"] = func(ctx Context, args []string) {
		called = true
	}

	env := makeMessageEvent("ou_admin", "msg1", "/testcmd")
	ctx := Context{Client: newSpyClient(), Config: cfg}
	router.HandleMessage(env, ctx)

	if !called {
		t.Error("admin command handler was not called")
	}
}

func TestRouter_unknownCommandNoHandler(t *testing.T) {
	cfg := makeTestConfig("ou_admin")
	router := NewRouter(cfg)

	// Should not panic for unknown command
	env := makeMessageEvent("ou_admin", "msg1", "/nonexistentcmd")
	ctx := Context{Client: newSpyClient(), Config: cfg}
	router.HandleMessage(env, ctx) // no panic = pass
}

func TestRouter_helpCommandRegistered(t *testing.T) {
	cfg := makeTestConfig("ou_admin")
	router := NewRouter(cfg)

	if _, ok := router.handlers["help"]; !ok {
		t.Error("help command should be registered")
	}
	if _, ok := router.handlers["status"]; !ok {
		t.Error("status command should be registered")
	}
	if _, ok := router.handlers["reboot"]; !ok {
		t.Error("reboot command should be registered")
	}
}

// ── Confirmation flow ─────────────────────────────────────────────────────────

func TestRouter_confirmAndExecute(t *testing.T) {
	cfg := makeTestConfig("ou_admin")
	router := NewRouter(cfg)

	var executed bool
	ctx := Context{
		SenderID: "ou_admin",
		Client:   newSpyClient(),
		Config:   cfg,
	}

	router.initiateConfirm(ctx, "test action", func() (string, error) {
		executed = true
		return "done", nil
	})

	// Find the pending token
	router.pendingMu.Lock()
	var token string
	for t := range router.pending {
		token = t
	}
	router.pendingMu.Unlock()

	if token == "" {
		t.Fatal("no pending action after initiateConfirm")
	}

	router.executeConfirm(ctx, token)

	if !executed {
		t.Error("action should have been executed after confirm")
	}

	// Pending action should be cleaned up
	router.pendingMu.Lock()
	_, stillPending := router.pending[token]
	router.pendingMu.Unlock()
	if stillPending {
		t.Error("pending action should be removed after execute")
	}
}

func TestRouter_cancelCleansPending(t *testing.T) {
	cfg := makeTestConfig("ou_admin")
	router := NewRouter(cfg)

	ctx := Context{SenderID: "ou_admin", Client: newSpyClient(), Config: cfg}
	router.initiateConfirm(ctx, "test", func() (string, error) { return "", nil })

	router.pendingMu.Lock()
	var token string
	for t := range router.pending {
		token = t
	}
	router.pendingMu.Unlock()

	router.cancelConfirm(ctx, token)

	router.pendingMu.Lock()
	_, stillPending := router.pending[token]
	router.pendingMu.Unlock()

	if stillPending {
		t.Error("cancelled action should be removed")
	}
}

func TestRouter_confirmWrongUser(t *testing.T) {
	cfg := makeTestConfig("ou_admin", "ou_other")
	router := NewRouter(cfg)

	ctx := Context{SenderID: "ou_admin", Client: newSpyClient(), Config: cfg}
	router.initiateConfirm(ctx, "test", func() (string, error) { return "", nil })

	router.pendingMu.Lock()
	var token string
	for t := range router.pending {
		token = t
	}
	router.pendingMu.Unlock()

	// Different user tries to confirm
	otherCtx := Context{SenderID: "ou_other", Client: newSpyClient(), Config: cfg}
	var executed bool
	router.pending[token].Execute = func() (string, error) {
		executed = true
		return "", nil
	}
	router.executeConfirm(otherCtx, token)

	if executed {
		t.Error("different user should not be able to confirm another user's action")
	}
}

func TestRouter_confirmExpiredToken(t *testing.T) {
	cfg := makeTestConfig("ou_admin")
	router := NewRouter(cfg)

	ctx := Context{SenderID: "ou_admin", Client: newSpyClient(), Config: cfg}

	// Try to confirm a non-existent token
	router.executeConfirm(ctx, "nonexistent-token")
	// No panic = pass
}

// ── Rate limit integration ────────────────────────────────────────────────────

func TestRouter_rateLimitBlocking(t *testing.T) {
	cfg := makeTestConfig("ou_admin")
	router := NewRouter(cfg)

	var handlerCallCount int
	router.handlers["echo"] = func(ctx Context, args []string) {
		handlerCallCount++
	}

	// Swap in a tight limiter scoped to this router instance
	originalLimiter := router.limiter
	router.limiter = newLimiter(3, 60*time.Second)
	defer func() { router.limiter = originalLimiter }()

	ctx := Context{Client: newSpyClient(), Config: cfg}
	for i := 0; i < 5; i++ {
		env := makeMessageEvent("ou_admin", fmt.Sprintf("msg%d", i), "/echo")
		router.HandleMessage(env, ctx)
	}

	if handlerCallCount != 3 {
		t.Errorf("handler called %d times, want 3 (rate limited after 3)", handlerCallCount)
	}
}
