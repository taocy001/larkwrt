package commands

import (
	"encoding/json"
	"strings"
	"sync"
	"time"

	"larkwrt/internal/collector"
	"larkwrt/internal/config"
	"larkwrt/internal/executor"
	"larkwrt/internal/feishu"

	"github.com/rs/zerolog/log"
)

// Context carries per-request context into command handlers.
type Context struct {
	SenderID      string
	MessageID     string // message to reply to (text commands)
	CardMessageID string // if non-empty, update this card in place instead of sending new
	Client        *feishu.Client
	Collector     *collector.Collector
	DevDB         *collector.DevDB
	Executor      *executor.Shell
	Config        *config.Config
}

// Handler is a command handler function.
type Handler func(ctx Context, args []string)

// Router parses incoming Feishu messages and dispatches to registered handlers.
type Router struct {
	cfg     *config.Config
	limiter *limiter
	handlers map[string]Handler

	// pending confirmations: token → PendingAction
	pendingMu sync.Mutex
	pending   map[string]*PendingAction

	// message-level dedup: msg_id → first seen; evicted after 30 min
	seenMsgMu  sync.Mutex
	seenMsgIDs map[string]time.Time
}

// PendingAction is a dangerous operation waiting for user confirmation.
type PendingAction struct {
	Token       string
	UserID      string
	Label       string
	Execute     func() (string, error) // one-shot result
	LiveExecute func(ctx Context)      // streaming; set instead of Execute for long commands
	ExpiresAt   time.Time
}

func NewRouter(cfg *config.Config) *Router {
	limit := cfg.Security.CmdRateLimit
	if limit <= 0 {
		limit = 20
	}
	r := &Router{
		cfg:        cfg,
		limiter:    newLimiter(limit, time.Minute),
		handlers:   make(map[string]Handler),
		pending:    make(map[string]*PendingAction),
		seenMsgIDs: make(map[string]time.Time),
	}
	r.registerAll()
	return r
}

// HandleMessage processes an im.message.receive_v1 event.
func (r *Router) HandleMessage(env feishu.EventEnvelope, ctx Context) {
	var ev feishu.IMMessageEvent
	if err := json.Unmarshal(env.Event, &ev); err != nil {
		log.Warn().Err(err).Msg("parse message event")
		return
	}

	// Accept either user_id or open_id in admin_users config.
	ids := ev.Sender.SenderID
	senderID := ids.UserID
	if !r.cfg.IsAdmin(senderID) {
		senderID = ids.OpenID
		if !r.cfg.IsAdmin(senderID) {
			log.Warn().Str("user_id", ids.UserID).Str("open_id", ids.OpenID).Msg("non-admin ignored")
			return
		}
	}
	ctx.SenderID = senderID
	ctx.MessageID = ev.Message.MessageID

	if r.isDuplicateMsg(ev.Message.MessageID) {
		log.Warn().Str("msg_id", ev.Message.MessageID).Msg("duplicate message dropped")
		return
	}

	text := extractText(ev.Message.Content)
	text = stripMention(text)
	text = strings.TrimSpace(text)

	if text == "" {
		return
	}

	// apply rate limiting
	if !r.limiter.Allow(senderID) {
		ctx.Client.ReplyText(ctx.MessageID, "⚠️ 操作过于频繁，请稍后再试")
		return
	}

	parts := strings.Fields(text)
	cmd := strings.ToLower(parts[0])
	args := parts[1:]

	// strip leading slash
	cmd = strings.TrimPrefix(cmd, "/")

	h, ok := r.handlers[cmd]
	if !ok {
		ctx.Client.ReplyText(ctx.MessageID, "❓ 未知命令，发送 /help 查看帮助")
		return
	}
	log.Info().Str("user", senderID).Str("cmd", cmd).Strs("args", args).Msg("cmd")
	h(ctx, args)
}

// HandleCardAction processes a card.action.trigger event (button click).
func (r *Router) HandleCardAction(env feishu.EventEnvelope, ctx Context) {
	var ev feishu.CardActionEvent
	if err := json.Unmarshal(env.Event, &ev); err != nil {
		log.Warn().Err(err).Msg("parse card action event")
		return
	}

	senderID := ev.Operator.UserID
	if !r.cfg.IsAdmin(senderID) {
		senderID = ev.Operator.OpenID
		if !r.cfg.IsAdmin(senderID) {
			return
		}
	}
	ctx.SenderID = senderID
	ctx.CardMessageID = ev.Context.OpenMessageID

	action, _ := ev.Action.Value["action"].(string)
	token, _ := ev.Action.Value["token"].(string)

	switch action {
	case "refresh_status":
		HandleStatus(ctx, nil)
	case "list_devices":
		HandleDevices(ctx, nil)
	case "reboot_confirm":
		r.initiateConfirm(ctx, "重启路由器", func() (string, error) {
			return doReboot()
		})
	case "confirm":
		r.executeConfirm(ctx, token)
	case "cancel":
		r.cancelConfirm(ctx, token)
	}
}

// ── Confirmation flow ─────────────────────────────────────────────────────────

func (r *Router) initiateConfirmLive(ctx Context, label string, fn func(ctx Context)) {
	token := newToken()
	action := &PendingAction{
		Token:       token,
		UserID:      ctx.SenderID,
		Label:       label,
		LiveExecute: fn,
		ExpiresAt:   time.Now().Add(60 * time.Second),
	}
	r.pendingMu.Lock()
	r.pending[token] = action
	r.pendingMu.Unlock()

	card := feishu.BuildConfirmCard(r.cfg.Router.Name, label, token)
	if _, err := ctx.Client.SendCard(card); err != nil {
		log.Error().Err(err).Msg("send confirm card")
	}

	time.AfterFunc(60*time.Second, func() {
		r.pendingMu.Lock()
		delete(r.pending, token)
		r.pendingMu.Unlock()
	})
}

func (r *Router) initiateConfirm(ctx Context, label string, fn func() (string, error)) {
	token := newToken()
	action := &PendingAction{
		Token:     token,
		UserID:    ctx.SenderID,
		Label:     label,
		Execute:   fn,
		ExpiresAt: time.Now().Add(60 * time.Second),
	}

	r.pendingMu.Lock()
	r.pending[token] = action
	r.pendingMu.Unlock()

	card := feishu.BuildConfirmCard(r.cfg.Router.Name, label, token)
	if _, err := ctx.Client.SendCard(card); err != nil {
		log.Error().Err(err).Msg("send confirm card")
	}

	time.AfterFunc(60*time.Second, func() {
		r.pendingMu.Lock()
		delete(r.pending, token)
		r.pendingMu.Unlock()
	})
}

func (r *Router) executeConfirm(ctx Context, token string) {
	r.pendingMu.Lock()
	action, ok := r.pending[token]
	if ok {
		delete(r.pending, token)
	}
	r.pendingMu.Unlock()

	if !ok {
		ctx.Client.SendText("⚠️ 操作已过期或不存在")
		return
	}
	if action.UserID != ctx.SenderID {
		ctx.Client.SendText("❌ 无权限确认此操作")
		return
	}
	if time.Now().After(action.ExpiresAt) {
		ctx.Client.SendText("⏰ 确认已超时，请重新发起")
		return
	}

	log.Info().Str("user", ctx.SenderID).Str("action", action.Label).Msg("confirmed action")

	if action.LiveExecute != nil {
		action.LiveExecute(ctx)
		return
	}
	out, err := action.Execute()
	card := feishu.BuildResultCard(r.cfg.Router.Name, action.Label, out, err == nil)
	ctx.Client.SendCard(card)
}

func (r *Router) cancelConfirm(ctx Context, token string) {
	r.pendingMu.Lock()
	delete(r.pending, token)
	r.pendingMu.Unlock()
	ctx.Client.SendText("✅ 操作已取消")
}

// ── Handler registration ──────────────────────────────────────────────────────

func (r *Router) registerAll() {
	// query (read-only)
	r.handlers["status"] = HandleStatus
	r.handlers["s"] = HandleStatus
	r.handlers["devices"] = HandleDevices
	r.handlers["d"] = HandleDevices
	r.handlers["traffic"] = HandleTraffic
	r.handlers["t"] = HandleTraffic
	r.handlers["top"] = HandleTop
	r.handlers["disk"] = HandleDisk
	r.handlers["log"] = HandleLog
	r.handlers["ping"] = HandlePing
	r.handlers["dns"] = HandleDNS
	r.handlers["route"] = HandleRoute
	r.handlers["arp"] = HandleARP
	r.handlers["help"] = HandleHelp
	r.handlers["note"] = HandleNote

	// wifi: read if no args, control if args provided
	r.handlers["wifi"] = func(ctx Context, args []string) {
		if len(args) >= 1 {
			r.initiateConfirm(ctx, "WiFi 控制: "+strings.Join(args, " "), func() (string, error) {
				return doWifiControl(args)
			})
		} else {
			HandleWifi(ctx, args)
		}
	}

	// action (wrapped with confirm)
	r.handlers["reboot"] = func(ctx Context, args []string) {
		r.initiateConfirm(ctx, "重启路由器", doReboot)
	}
	r.handlers["service"] = func(ctx Context, args []string) {
		label := "重启服务: " + strings.Join(args, " ")
		r.initiateConfirm(ctx, label, func() (string, error) {
			return doServiceRestart(args)
		})
	}
	r.handlers["reconnect"] = func(ctx Context, args []string) {
		r.initiateConfirm(ctx, "重拨 WAN", doReconnectWAN)
	}
	r.handlers["fw"] = func(ctx Context, args []string) {
		label := "防火墙: " + strings.Join(args, " ")
		r.initiateConfirm(ctx, label, func() (string, error) {
			return doFirewall(args)
		})
	}
	r.handlers["exec"] = func(ctx Context, args []string) {
		if len(args) == 0 {
			ctx.Client.ReplyText(ctx.MessageID, "用法: /exec <cmd> [args...]")
			return
		}
		label := "exec: " + strings.Join(args, " ")
		r.initiateConfirmLive(ctx, label, func(execCtx Context) {
			runLiveShell(execCtx, label, 60*time.Second, args[0], args[1:]...)
		})
	}
}

func (r *Router) isDuplicateMsg(msgID string) bool {
	if msgID == "" {
		return false
	}
	r.seenMsgMu.Lock()
	defer r.seenMsgMu.Unlock()
	cutoff := time.Now().Add(-30 * time.Minute)
	for id, t := range r.seenMsgIDs {
		if t.Before(cutoff) {
			delete(r.seenMsgIDs, id)
		}
	}
	if _, seen := r.seenMsgIDs[msgID]; seen {
		return true
	}
	r.seenMsgIDs[msgID] = time.Now()
	return false
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func extractText(content string) string {
	var m map[string]string
	if err := json.Unmarshal([]byte(content), &m); err != nil {
		return content
	}
	if t, ok := m["text"]; ok {
		return t
	}
	return content
}

// stripMention removes "@bot " prefixes that Feishu adds in group chats.
func stripMention(text string) string {
	// Feishu format: "@_user_<open_id> text" or just the raw text
	// Remove anything that looks like @mention
	parts := strings.SplitN(text, " ", 2)
	if len(parts) == 2 && strings.HasPrefix(parts[0], "@") {
		return strings.TrimSpace(parts[1])
	}
	return text
}
