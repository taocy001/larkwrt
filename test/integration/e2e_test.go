package integration

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"larkwrt/internal/collector"
	"larkwrt/internal/commands"
	"larkwrt/internal/config"
	"larkwrt/internal/events"
	"larkwrt/internal/executor"
	"larkwrt/internal/feishu"
)

const (
	testAdminID = "ou_test_admin"
	testChatID  = "oc_test_chat"
	testAppID   = "cli_test_app"
	testSecret  = "test_secret"
)

func setupTest(t *testing.T) (*MockFeishu, *feishu.Client, *feishu.WSClient, *commands.Router) {
	t.Helper()
	mock := NewMockFeishu()
	t.Cleanup(mock.Close)

	client := feishu.NewClient(testAppID, testSecret, testChatID, mock.BaseURL())
	// Inject token so tests don't need the real auth endpoint
	client.TokenManager().ForceSet(testToken, time.Hour)

	ws := feishu.NewWSClient(testAppID, testSecret, mock.BaseURL())

	cfg := &config.Config{
		Feishu:   config.FeishuConfig{AdminUsers: []string{testAdminID}},
		Router:   config.RouterConfig{Name: "TestRouter"},
		Security: config.SecurityConfig{CmdRateLimit: 20, ExecWhitelist: []string{"echo"}},
	}
	router := commands.NewRouter(cfg)
	return mock, client, ws, router
}

func makeTestCollector() *collector.Collector {
	bus := events.NewBus(0)
	return collector.New(5*time.Second, 30*time.Second, bus, "br-lan", nil)
}

// ── TC-01: Status card push on WS event ──────────────────────────────────────

func TestE2E_WSConnect_and_handshake(t *testing.T) {
	mock, _, ws, _ := setupTest(t) //nolint:dogsled
	stop := make(chan struct{})
	defer close(stop)

	go ws.Run()

	// Wait for the WS client to connect
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if mock.ConnCount() > 0 {
			return // connected
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("WS client did not connect within 3s")
}

// TC-01: /status command → status card sent to chat
func TestE2E_StatusCommand(t *testing.T) {
	mock, client, ws, _ := setupTest(t)
	col := makeTestCollector()

	stop := make(chan struct{})
	go ws.Run()
	go col.Start(stop)
	defer close(stop)

	// Wait for WS connect
	time.Sleep(200 * time.Millisecond)

	ctx := commands.Context{
		Client:    client,
		Collector: col,
		Config: &config.Config{
			Router: config.RouterConfig{Name: "TestRouter"},
		},
	}

	// Dispatch /status directly (simulates receiving the message)
	commands.HandleStatus(ctx, nil)

	found := mock.WaitForMessage(2*time.Second, func(msg SentMessage) bool {
		return msg.ReceiveID == testChatID && msg.MsgType == "interactive"
	})
	if !found {
		t.Fatal("no interactive card sent after /status command")
	}
}

// TC-02: Device join event → alert card pushed
func TestE2E_DeviceJoinAlert(t *testing.T) {
	mock, client, ws, _ := setupTest(t)

	bus := events.NewBus(0)
	bus.Subscribe(func(ev events.Event) {
		card := feishu.BuildAlertCard("TestRouter", ev)
		client.SendCard(card)
	})

	stop := make(chan struct{})
	go ws.Run()
	defer close(stop)

	time.Sleep(200 * time.Millisecond)

	// Publish a device join event
	bus.Publish(events.Event{
		Type: events.EvDeviceJoin,
		Payload: events.DevicePayload{
			MAC: "aa:bb:cc:dd:ee:01", IP: "192.168.1.100", Hostname: "iPhone-15",
		},
		At: time.Now(),
	})

	found := mock.WaitForMessage(2*time.Second, func(msg SentMessage) bool {
		return strings.Contains(msg.Content, "iPhone-15")
	})
	if !found {
		t.Fatal("device join alert card not sent within 2s")
	}
}

// TC-03: WAN IP change → alert card
func TestE2E_WANIPChangeAlert(t *testing.T) {
	mock, client, ws, _ := setupTest(t)
	bus := events.NewBus(0)
	bus.Subscribe(func(ev events.Event) {
		client.SendCard(feishu.BuildAlertCard("TestRouter", ev))
	})

	stop := make(chan struct{})
	go ws.Run()
	defer close(stop)
	time.Sleep(100 * time.Millisecond)

	bus.Publish(events.Event{
		Type: events.EvWANIPChange,
		Payload: events.WANIPPayload{OldIP: "1.2.3.4", NewIP: "5.6.7.8", Iface: "eth0"},
		At:      time.Now(),
	})

	found := mock.WaitForMessage(2*time.Second, func(msg SentMessage) bool {
		return strings.Contains(msg.Content, "1.2.3.4") && strings.Contains(msg.Content, "5.6.7.8")
	})
	if !found {
		t.Fatal("WAN IP change alert not sent")
	}
}

// TC-04: High CPU → alert card (deduplication test)
func TestE2E_HighCPUAlert_deduplication(t *testing.T) {
	mock, client, ws, _ := setupTest(t)
	bus := events.NewBus(300) // 300s cooldown
	bus.Subscribe(func(ev events.Event) {
		client.SendCard(feishu.BuildAlertCard("TestRouter", ev))
	})

	stop := make(chan struct{})
	go ws.Run()
	defer close(stop)
	time.Sleep(100 * time.Millisecond)

	for i := 0; i < 5; i++ {
		bus.Publish(events.Event{
			Type:    events.EvHighCPU,
			Payload: events.CPUPayload{Percent: 91.0, Duration: 65 * time.Second},
			At:      time.Now(),
		})
	}

	time.Sleep(300 * time.Millisecond)
	msgs := mock.SentMessages()
	cpuAlerts := 0
	for _, m := range msgs {
		if strings.Contains(m.Content, "CPU") {
			cpuAlerts++
		}
	}
	if cpuAlerts != 1 {
		t.Errorf("high CPU alert should fire exactly once (dedup), got %d", cpuAlerts)
	}
}

// TC-05: /reboot → confirm card → execute
func TestE2E_RebootConfirmFlow(t *testing.T) {
	mock, client, ws, router := setupTest(t)

	cfg := &config.Config{
		Feishu:   config.FeishuConfig{AdminUsers: []string{testAdminID}},
		Router:   config.RouterConfig{Name: "TestRouter"},
		Security: config.SecurityConfig{CmdRateLimit: 20},
	}
	ctx := commands.Context{
		Client: client,
		Config: cfg,
	}

	stop := make(chan struct{})
	go ws.Run()
	defer close(stop)
	time.Sleep(100 * time.Millisecond)

	// Initiate reboot via the router
	msgContent, _ := json.Marshal(map[string]string{"text": "/reboot"})
	event := feishu.IMMessageEvent{
		Sender:  feishu.IMSender{SenderID: feishu.IMSenderID{UserID: testAdminID}},
		Message: feishu.IMMessage{MessageID: "msg1", MessageType: "text", Content: string(msgContent)},
	}
	eventBytes, _ := json.Marshal(event)
	env := feishu.EventEnvelope{
		Header: feishu.EventHeader{EventType: "im.message.receive_v1"},
		Event:  eventBytes,
	}
	router.HandleMessage(env, ctx)

	// Should have sent a confirmation card
	found := mock.WaitForMessage(2*time.Second, func(msg SentMessage) bool {
		return strings.Contains(msg.Content, "confirm") ||
			strings.Contains(msg.Content, "确认")
	})
	if !found {
		t.Fatal("confirm card not sent after /reboot")
	}
	_ = ctx
}

// TC-06: Permission denied — non-admin silently ignored
func TestE2E_NonAdminIgnored(t *testing.T) {
	mock, client, ws, router := setupTest(t)

	cfg := &config.Config{
		Feishu: config.FeishuConfig{AdminUsers: []string{testAdminID}},
		Router: config.RouterConfig{Name: "TestRouter"},
	}
	ctx := commands.Context{Client: client, Config: cfg}

	stop := make(chan struct{})
	go ws.Run()
	defer close(stop)
	time.Sleep(100 * time.Millisecond)

	initial := len(mock.SentMessages())

	msgContent, _ := json.Marshal(map[string]string{"text": "/status"})
	event := feishu.IMMessageEvent{
		Sender:  feishu.IMSender{SenderID: feishu.IMSenderID{UserID: "ou_hacker"}},
		Message: feishu.IMMessage{MessageID: "msg1", MessageType: "text", Content: string(msgContent)},
	}
	eventBytes, _ := json.Marshal(event)
	env := feishu.EventEnvelope{
		Header: feishu.EventHeader{EventType: "im.message.receive_v1"},
		Event:  eventBytes,
	}
	router.HandleMessage(env, ctx)

	time.Sleep(200 * time.Millisecond)
	if len(mock.SentMessages()) != initial {
		t.Error("non-admin should not trigger any message sending")
	}
}

// TC-07: WS reconnect on disconnect
func TestE2E_WSReconnect(t *testing.T) {
	mock, _, ws, _ := setupTest(t)
	stop := make(chan struct{})
	defer close(stop)

	go ws.Run()

	// Wait for initial connection
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if mock.ConnCount() > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if mock.ConnCount() == 0 {
		t.Fatal("initial WS connection not established")
	}

	// Force-close all connections from server side
	mock.mu.Lock()
	for _, c := range mock.conns {
		c.Close()
	}
	mock.conns = nil
	mock.mu.Unlock()

	// WSClient should reconnect within the backoff (1s + margin)
	time.Sleep(2500 * time.Millisecond)
	if mock.ConnCount() == 0 {
		t.Error("WS client did not reconnect after server-side disconnect")
	}
}

// TC-08: Rate limiting (20 req/min)
func TestE2E_RateLimiting(t *testing.T) {
	_, client, ws, router := setupTest(t)

	cfg := &config.Config{
		Feishu:   config.FeishuConfig{AdminUsers: []string{testAdminID}},
		Router:   config.RouterConfig{Name: "TestRouter"},
		Security: config.SecurityConfig{CmdRateLimit: 5},
	}

	stop := make(chan struct{})
	go ws.Run()
	defer close(stop)
	time.Sleep(100 * time.Millisecond)

	// Temporarily replace the router's rate limiter with a tight one
	originalLimiter := router.GetLimiter()
	tightLimiter := commands.NewTestLimiter(3, time.Minute)
	router.SetLimiter(tightLimiter)
	defer router.SetLimiter(originalLimiter)

	var handledCount int
	router.SetHandler("testecho", func(ctx commands.Context, args []string) {
		handledCount++
	})

	ctx := commands.Context{Client: client, Config: cfg}
	for i := 0; i < 6; i++ {
		msgContent, _ := json.Marshal(map[string]string{"text": "/testecho"})
		event := feishu.IMMessageEvent{
			Sender:  feishu.IMSender{SenderID: feishu.IMSenderID{UserID: testAdminID}},
			Message: feishu.IMMessage{MessageID: fmt.Sprintf("msg%d", i), MessageType: "text", Content: string(msgContent)},
		}
		eventBytes, _ := json.Marshal(event)
		env := feishu.EventEnvelope{
			Header: feishu.EventHeader{EventType: "im.message.receive_v1"},
			Event:  eventBytes,
		}
		router.HandleMessage(env, ctx)
	}

	if handledCount != 3 {
		t.Errorf("rate limiter: got %d handled (want 3 of 6 allowed)", handledCount)
	}
}

// TC-09: Command injection prevention
func TestE2E_CommandInjectionPrevention(t *testing.T) {
	shell := executor.New([]string{"echo"})

	// Injection attempt: the semicolon and subsequent rm should not be executed
	stdout, _, err := shell.Run("echo", "safe; rm -rf /tmp/should-not-exist")
	if err != nil {
		t.Fatalf("echo with metachar arg: %v", err)
	}
	// The argument is passed literally — no shell expansion
	if !strings.Contains(stdout, "safe; rm -rf /tmp/should-not-exist") {
		t.Errorf("injection metacharacters were not treated as literal: %q", stdout)
	}
}

// TC-10: Token auto-refresh
func TestE2E_TokenRefresh(t *testing.T) {
	mock := NewMockFeishu()
	defer mock.Close()

	// Start with an expired token
	client := feishu.NewClient(testAppID, testSecret, testChatID, mock.BaseURL())
	// Don't ForceSet — let it go through the real token refresh path

	// The mock token endpoint returns a valid token
	// Sending a message should trigger token fetch + send
	_, err := client.SendText("test")
	if err != nil {
		t.Fatalf("SendText with auto-refresh: %v", err)
	}

	msgs := mock.SentMessages()
	if len(msgs) == 0 {
		t.Error("message not sent after token refresh")
	}
}

// TC-11: Confirmation timeout
func TestE2E_ConfirmationTimeout(t *testing.T) {
	// Injection a pending action with a very short expiry
	cfg := &config.Config{
		Feishu: config.FeishuConfig{AdminUsers: []string{testAdminID}},
		Router: config.RouterConfig{Name: "TestRouter"},
	}
	mock := NewMockFeishu()
	defer mock.Close()
	client := feishu.NewClient(testAppID, testSecret, testChatID, mock.BaseURL())
	client.TokenManager().ForceSet(testToken, time.Hour)

	router := commands.NewRouter(cfg)
	ctx := commands.Context{SenderID: testAdminID, Client: client, Config: cfg}

	var executed bool
	router.InitiateConfirmForTest(ctx, "test action", func() (string, error) {
		executed = true
		return "", nil
	})

	// Manually expire all pending tokens
	router.ExpireAllPendingForTest()

	// Try to execute after expiry
	router.ExecuteFirstPendingForTest(ctx)

	if executed {
		t.Error("expired confirmation should not execute")
	}
}
