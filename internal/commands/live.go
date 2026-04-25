package commands

import (
	"context"
	"strings"
	"time"

	"larkwrt/internal/executor"
	"larkwrt/internal/feishu"

	"github.com/rs/zerolog/log"
)

// sendLiveCard sends the initial live card and returns its message ID.
// Uses ReplyCard when ctx.MessageID is set, otherwise SendCard to the group.
func sendLiveCard(ctx Context, card *feishu.Card) (string, error) {
	if ctx.MessageID != "" {
		return ctx.Client.ReplyCard(ctx.MessageID, card)
	}
	return ctx.Client.SendCard(card)
}

// runLive sends an initial "running" card, then streams command output into it,
// updating every 800 ms until the process exits. A final update sets the
// success/failure header colour.
func runLive(ctx Context, title string, timeout time.Duration, name string, args ...string) {
	card := feishu.BuildLiveCard(ctx.Config.Router.Name, title, "", false, false)
	cardMsgID, err := sendLiveCard(ctx, card)
	if err != nil {
		log.Error().Err(err).Msg("send live card")
		ctx.Client.ReplyText(ctx.MessageID, title+" 启动失败: "+err.Error())
		return
	}

	execCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	lines, errc := executor.StreamLines(execCtx, name, args...)

	var buf strings.Builder
	ticker := time.NewTicker(800 * time.Millisecond)
	defer ticker.Stop()

	update := func(done, success bool) {
		c := feishu.BuildLiveCard(ctx.Config.Router.Name, title, buf.String(), done, success)
		if err := ctx.Client.UpdateCard(cardMsgID, c); err != nil {
			log.Error().Err(err).Msg("update live card")
		}
	}

	for {
		select {
		case line, ok := <-lines:
			if !ok {
				// lines closed — process exited; read final error
				runErr := <-errc
				update(true, runErr == nil)
				return
			}
			buf.WriteString(line + "\n")
		case <-ticker.C:
			if buf.Len() > 0 {
				update(false, false)
			}
		}
	}
}

// runLiveShell is like runLive but goes through the executor whitelist.
func runLiveShell(ctx Context, title string, timeout time.Duration, name string, args ...string) {
	card := feishu.BuildLiveCard(ctx.Config.Router.Name, title, "", false, false)
	cardMsgID, err := sendLiveCard(ctx, card)
	if err != nil {
		log.Error().Err(err).Msg("send live card")
		ctx.Client.ReplyText(ctx.MessageID, title+" 启动失败: "+err.Error())
		return
	}

	execCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	lines, errc, err := ctx.Executor.Stream(execCtx, name, args...)
	if err != nil {
		ctx.Client.UpdateCard(cardMsgID, feishu.BuildLiveCard(ctx.Config.Router.Name, title, err.Error(), true, false))
		return
	}

	var buf strings.Builder
	ticker := time.NewTicker(800 * time.Millisecond)
	defer ticker.Stop()

	update := func(done, success bool) {
		c := feishu.BuildLiveCard(ctx.Config.Router.Name, title, buf.String(), done, success)
		if err := ctx.Client.UpdateCard(cardMsgID, c); err != nil {
			log.Error().Err(err).Msg("update live card")
		}
	}

	for {
		select {
		case line, ok := <-lines:
			if !ok {
				runErr := <-errc
				update(true, runErr == nil)
				return
			}
			buf.WriteString(line + "\n")
		case <-ticker.C:
			if buf.Len() > 0 {
				update(false, false)
			}
		}
	}
}
