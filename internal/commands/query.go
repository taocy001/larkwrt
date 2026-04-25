package commands

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"larkwrt/internal/executor"
	"larkwrt/internal/feishu"

	"github.com/rs/zerolog/log"
)

func HandleStatus(ctx Context, _ []string) {
	snap := ctx.Collector.Current()
	card := feishu.BuildStatusCard(ctx.Config.Router.Name, snap)
	if err := sendOrUpdate(ctx, card); err != nil {
		ctx.Client.ReplyText(ctx.MessageID, "获取状态失败: "+err.Error())
	}
}

func HandleDevices(ctx Context, _ []string) {
	snap := ctx.Collector.Current()
	devices := snap.Devices
	if ctx.DevDB != nil {
		devices = ctx.DevDB.EnrichDevices(devices)
	}
	card := feishu.BuildDeviceListCard(ctx.Config.Router.Name, devices, ctx.Config.Devices)
	if err := sendOrUpdate(ctx, card); err != nil {
		ctx.Client.ReplyText(ctx.MessageID, "获取设备列表失败: "+err.Error())
	}
}

// sendOrUpdate updates the card in place if ctx.CardMessageID is set, otherwise sends a new card.
func sendOrUpdate(ctx Context, card *feishu.Card) error {
	if ctx.CardMessageID != "" {
		return ctx.Client.UpdateCard(ctx.CardMessageID, card)
	}
	_, err := ctx.Client.SendCard(card)
	return err
}

func HandleTraffic(ctx Context, _ []string) {
	snap := ctx.Collector.Current()
	card := feishu.BuildTrafficCard(ctx.Config.Router.Name, snap.NetRates)
	if _, err := ctx.Client.ReplyCard(ctx.MessageID, card); err != nil {
		log.Error().Err(err).Msg("send traffic card")
		ctx.Client.ReplyText(ctx.MessageID, "获取流量数据失败: "+err.Error())
	}
}

func HandleWifi(ctx Context, _ []string) {
	snap := ctx.Collector.Current()
	card := feishu.BuildWifiCard(ctx.Config.Router.Name, snap.Wifi)
	if _, err := ctx.Client.ReplyCard(ctx.MessageID, card); err != nil {
		log.Error().Err(err).Msg("send wifi card")
		ctx.Client.ReplyText(ctx.MessageID, "获取无线信息失败: "+err.Error())
	}
}

func HandleLog(ctx Context, args []string) {
	n := 20
	if len(args) > 0 {
		v, err := strconv.Atoi(args[0])
		if err != nil || v <= 0 || v > 9999 {
			ctx.Client.ReplyText(ctx.MessageID, "用法: /log [n]，n 必须为 1-9999 的整数")
			return
		}
		n = v
	}
	out, errOut, err := executor.RunUnchecked(10*time.Second, "logread", "-l", strconv.Itoa(n))
	if err != nil {
		ctx.Client.ReplyText(ctx.MessageID, "logread 失败: "+errOut)
		return
	}
	ctx.Client.ReplyText(ctx.MessageID, truncateLog(out, 3000))
}

func HandlePing(ctx Context, args []string) {
	if len(args) == 0 {
		ctx.Client.ReplyText(ctx.MessageID, "用法: /ping <host>")
		return
	}
	host := args[0]
	runLive(ctx, "ping "+host, 20*time.Second, "ping", "-c", "4", host)
}

func HandleDNS(ctx Context, args []string) {
	if len(args) == 0 {
		ctx.Client.ReplyText(ctx.MessageID, "用法: /dns <domain>")
		return
	}
	domain := args[0]
	runLive(ctx, "nslookup "+domain, 10*time.Second, "nslookup", domain)
}

func HandleRoute(ctx Context, _ []string) {
	snap := ctx.Collector.Current()
	card := feishu.BuildRouteCard(ctx.Config.Router.Name, snap.Routes)
	if _, err := ctx.Client.ReplyCard(ctx.MessageID, card); err != nil {
		log.Error().Err(err).Msg("send route card")
		ctx.Client.ReplyText(ctx.MessageID, "获取路由表失败: "+err.Error())
	}
}

func HandleARP(ctx Context, _ []string) {
	out, _, err := executor.RunUnchecked(5*time.Second, "ip", "neigh")
	snap := ctx.Collector.Current()
	devices := snap.Devices
	if ctx.DevDB != nil {
		devices = ctx.DevDB.EnrichDevices(devices)
	}
	neighOut := ""
	if err == nil {
		neighOut = out
	}
	card := feishu.BuildARPCard(ctx.Config.Router.Name, neighOut, devices, ctx.DevDB)
	if _, err := ctx.Client.ReplyCard(ctx.MessageID, card); err != nil {
		log.Error().Err(err).Msg("send arp card")
		ctx.Client.ReplyText(ctx.MessageID, "获取 ARP 表失败: "+err.Error())
	}
}

func HandleTop(ctx Context, args []string) {
	n := parseN("", 15)
	if len(args) > 0 {
		n = parseN(args[0], 15)
	}
	out, _, err := executor.RunUnchecked(10*time.Second, "top", "-b", "-n", "1")
	if err != nil {
		out, _, err = executor.RunUnchecked(5*time.Second, "ps", "-o", "pid,vsz,comm")
		if err != nil {
			ctx.Client.ReplyText(ctx.MessageID, "获取进程列表失败")
			return
		}
	}
	card := feishu.BuildTopCard(ctx.Config.Router.Name, out, n)
	if _, err := ctx.Client.ReplyCard(ctx.MessageID, card); err != nil {
		log.Error().Err(err).Msg("send top card")
		ctx.Client.ReplyText(ctx.MessageID, "获取进程列表失败: "+err.Error())
	}
}

func HandleDisk(ctx Context, _ []string) {
	out, _, err := executor.RunUnchecked(5*time.Second, "df", "-h")
	if err != nil {
		ctx.Client.ReplyText(ctx.MessageID, "获取磁盘信息失败")
		return
	}
	card := feishu.BuildDiskCard(ctx.Config.Router.Name, out)
	if _, err := ctx.Client.ReplyCard(ctx.MessageID, card); err != nil {
		log.Error().Err(err).Msg("send disk card")
		ctx.Client.ReplyText(ctx.MessageID, "💾 磁盘使用\n\n"+out)
	}
}

func HandleNote(ctx Context, args []string) {
	if ctx.DevDB == nil {
		ctx.Client.ReplyText(ctx.MessageID, "设备数据库未初始化")
		return
	}
	if len(args) < 2 {
		ctx.Client.ReplyText(ctx.MessageID, "用法: /note <MAC或IP> <备注>\n示例: /note aa:bb:cc:dd:ee:ff 我的NAS\n清除备注: /note aa:bb:cc:dd:ee:ff -")
		return
	}
	target := args[0]
	note := strings.Join(args[1:], " ")
	if note == "-" {
		note = ""
	}
	mac := ctx.DevDB.SetNote(target, note)
	if mac == "" {
		ctx.Client.ReplyText(ctx.MessageID, fmt.Sprintf("未找到设备: %s\n请先用 /devices 或 /arp 确认 MAC/IP", target))
		return
	}
	if note == "" {
		ctx.Client.ReplyText(ctx.MessageID, fmt.Sprintf("✅ 已清除 %s 的备注", mac))
	} else {
		ctx.Client.ReplyText(ctx.MessageID, fmt.Sprintf("✅ 已设置 %s 的备注: %s", mac, note))
	}
}

func HandleHelp(ctx Context, _ []string) {
	help := `**🛠 OpenWrt 管理命令**

**查询**
/status (s)       状态概览
/devices (d)      已连接设备（含厂商识别）
/traffic (t)      实时流量
/wifi             无线网络信息
/top [n]          进程列表（默认15行）
/disk             磁盘使用
/log [n]          最近 n 条日志（默认20）
/ping <host>      连通性测试
/dns <domain>     DNS 查询
/route            路由表
/arp              ARP 邻居表

**设备管理**
/note <MAC|IP> <备注>   添加设备备注（- 清除）

**操作（需二次确认）**
/reboot           重启路由器
/reconnect        重拨 WAN
/wifi on|off      开关无线
/service <name>   重启服务（如 dnsmasq）
/exec <cmd>       执行白名单命令
/fw allow|block <ip>  防火墙规则`

	ctx.Client.ReplyText(ctx.MessageID, help)
}

// ── local helpers ─────────────────────────────────────────────────────────────

func parseN(s string, def int) int {
	var v int
	if _, err := fmt.Sscanf(s, "%d", &v); err == nil && v > 0 {
		return v
	}
	return def
}

func truncateLog(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	// keep last n runes (newest log entries)
	return "…\n" + string(runes[len(runes)-n:])
}
