package commands

import (
	"fmt"
	"strings"
	"time"

	"larkwrt/internal/config"
	"larkwrt/internal/executor"
	"larkwrt/internal/feishu"
	"larkwrt/internal/plugin"

	"github.com/rs/zerolog/log"
)

// handlePlugin is the entry point for /plugin and /pl commands.
// Subcommand dispatch:
//
//	/plugin [list]                         — list installed plugins
//	/plugin <name>                         — show plugin status (shorthand)
//	/plugin status  <name>                 — show plugin status
//	/plugin config  <name>                 — show config file (read-only)
//	/plugin reload  <name>                 — reload plugin (confirm required)
//	/plugin switch  <name> <group> <node>  — switch sing-box proxy (confirm required)
func (r *Router) handlePlugin(ctx Context, args []string) {
	if len(args) == 0 || strings.ToLower(args[0]) == "list" {
		pluginList(ctx)
		return
	}

	sub := strings.ToLower(args[0])

	switch sub {
	case "status", "config", "reload", "switch":
		if len(args) < 2 {
			ctx.Client.ReplyText(ctx.MessageID, fmt.Sprintf("用法: /plugin %s <插件名>", sub))
			return
		}
		name := args[1]
		p, ok := plugin.Find(ctx.Config.Plugins, name)
		if !ok {
			ctx.Client.ReplyText(ctx.MessageID, fmt.Sprintf("插件 %q 未安装或未在配置中", name))
			return
		}
		switch sub {
		case "status":
			pluginStatus(ctx, p)
		case "config":
			pluginConfig(ctx, p)
		case "reload":
			pluginReload(r, ctx, p)
		case "switch":
			pluginSwitch(r, ctx, p, args[2:])
		}
	default:
		// treat first arg as plugin name → show status
		name := args[0]
		p, ok := plugin.Find(ctx.Config.Plugins, name)
		if !ok {
			ctx.Client.ReplyText(ctx.MessageID, fmt.Sprintf("插件 %q 未安装或未在配置中\n发送 /plugin list 查看已安装插件", name))
			return
		}
		pluginStatus(ctx, p)
	}
}

// ── sub-handlers ──────────────────────────────────────────────────────────────

func pluginList(ctx Context) {
	installed := plugin.Installed(ctx.Config.Plugins)
	card := feishu.BuildPluginListCard(ctx.Config.Router.Name, installedToEntries(installed))
	if _, err := ctx.Client.ReplyCard(ctx.MessageID, card); err != nil {
		log.Error().Err(err).Msg("send plugin list card")
		ctx.Client.ReplyText(ctx.MessageID, "获取插件列表失败")
	}
}

func pluginStatus(ctx Context, p config.PluginConfig) {
	if p.Type == "singbox" {
		if p.APIURL == "" {
			ctx.Client.ReplyText(ctx.MessageID, fmt.Sprintf("插件 %s 未配置 api_url", p.Name))
			return
		}
		status := plugin.FetchSingBoxStatus(p.APIURL, p.APISecret)
		card := feishu.BuildSingBoxCard(ctx.Config.Router.Name, p.Name, toSingBoxDisplay(status))
		if _, err := ctx.Client.ReplyCard(ctx.MessageID, card); err != nil {
			log.Error().Err(err).Msg("send singbox card")
		}
		return
	}
	// generic plugin
	out, statusErr := plugin.RunStatus(p)
	stats := plugin.RunStats(p)
	rows := make([]feishu.PluginStatRow, len(stats))
	for i, s := range stats {
		rows[i] = feishu.PluginStatRow{Label: s.Label, Value: s.Value}
	}
	card := feishu.BuildGenericPluginCard(ctx.Config.Router.Name, p.Name, out, statusErr, rows)
	if _, err := ctx.Client.ReplyCard(ctx.MessageID, card); err != nil {
		log.Error().Err(err).Msg("send plugin status card")
	}
}

func pluginConfig(ctx Context, p config.PluginConfig) {
	content, err := plugin.ReadConfig(p)
	if err != nil {
		ctx.Client.ReplyText(ctx.MessageID, fmt.Sprintf("读取 %s 配置失败: %v", p.Name, err))
		return
	}
	card := feishu.BuildPluginConfigCard(ctx.Config.Router.Name, p.Name, p.ConfigFile, content)
	if _, err := ctx.Client.ReplyCard(ctx.MessageID, card); err != nil {
		log.Error().Err(err).Msg("send plugin config card")
	}
}

func pluginReload(r *Router, ctx Context, p config.PluginConfig) {
	if p.ReloadCmd == "" {
		ctx.Client.ReplyText(ctx.MessageID, fmt.Sprintf("插件 %s 未配置 reload_cmd", p.Name))
		return
	}
	label := fmt.Sprintf("重载插件 %s", p.Name)
	reloadCmd, pluginName := p.ReloadCmd, p.Name
	r.initiateConfirm(ctx, label, func() (string, error) {
		out, errOut, err := executor.RunUnchecked(30*time.Second, "sh", "-c", reloadCmd)
		if err != nil {
			return errOut, err
		}
		if out == "" {
			return fmt.Sprintf("插件 %s 重载成功", pluginName), nil
		}
		return out, nil
	})
}

func pluginSwitch(r *Router, ctx Context, p config.PluginConfig, args []string) {
	if p.Type != "singbox" {
		ctx.Client.ReplyText(ctx.MessageID, "switch 仅支持 type=singbox 的插件")
		return
	}
	if len(args) < 2 {
		ctx.Client.ReplyText(ctx.MessageID, "用法: /plugin switch <插件名> <代理组> <节点名>")
		return
	}
	if p.APIURL == "" {
		ctx.Client.ReplyText(ctx.MessageID, fmt.Sprintf("插件 %s 未配置 api_url", p.Name))
		return
	}
	group := args[0]
	node := strings.Join(args[1:], " ")
	label := fmt.Sprintf("%s: %s → %s", p.Name, group, node)
	apiURL, secret := p.APIURL, p.APISecret
	r.initiateConfirm(ctx, label, func() (string, error) {
		if err := plugin.SwitchSingBoxProxy(apiURL, secret, group, node); err != nil {
			return "", err
		}
		return fmt.Sprintf("✅ %s 已切换至 %s", group, node), nil
	})
}

// ── conversion helpers ────────────────────────────────────────────────────────

func installedToEntries(plugins []config.PluginConfig) []feishu.PluginEntry {
	entries := make([]feishu.PluginEntry, len(plugins))
	for i, p := range plugins {
		typeLabel := "通用"
		if p.Type == "singbox" {
			typeLabel = "sing-box (API)"
		}
		entries[i] = feishu.PluginEntry{
			Name:      p.Name,
			TypeLabel: typeLabel,
			HasStatus: p.StatusCmd != "" || p.Type == "singbox",
			HasConfig: p.ConfigFile != "",
			HasReload: p.ReloadCmd != "",
		}
	}
	return entries
}

func toSingBoxDisplay(s *plugin.SingBoxStatus) feishu.SingBoxDisplay {
	d := feishu.SingBoxDisplay{
		Version:     s.Version,
		Connections: s.Connections,
		UpBytes:     s.UpTotal,
		DownBytes:   s.DownTotal,
	}
	for _, g := range s.Groups {
		d.Groups = append(d.Groups, feishu.SingBoxGroup{
			Name:    g.Name,
			Current: g.Current,
			Count:   len(g.Options),
		})
	}
	return d
}
