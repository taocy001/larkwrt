package feishu

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"larkwrt/internal/collector"
	"larkwrt/internal/events"
)

// ── Plugin display types (populated by commands/plugin.go) ───────────────────

// PluginEntry is display data for one installed plugin shown in the list card.
type PluginEntry struct {
	Name      string
	TypeLabel string // "sing-box (API)" | "通用"
	HasStatus bool
	HasConfig bool
	HasReload bool
}

// SingBoxDisplay holds data rendered in the sing-box status card.
type SingBoxDisplay struct {
	Version     string
	Groups      []SingBoxGroup
	Connections int
	UpBytes     int64
	DownBytes   int64
}

// SingBoxGroup is one Selector proxy group.
type SingBoxGroup struct {
	Name    string
	Current string
	Count   int
}

// PluginStatRow is one labelled metric row for generic plugin cards.
type PluginStatRow struct {
	Label string
	Value string
}

// ── Package list card ─────────────────────────────────────────────────────────

func BuildPackageListCard(routerName, opkgOut, filter string) *Card {
	type pkg struct {
		Name    string
		Version string
	}
	var pkgs []pkg
	for _, line := range strings.Split(strings.TrimSpace(opkgOut), "\n") {
		if line == "" {
			continue
		}
		// opkg format: "package_name - version - description"
		parts := strings.SplitN(line, " - ", 3)
		name := strings.TrimSpace(parts[0])
		version := ""
		if len(parts) >= 2 {
			version = strings.TrimSpace(parts[1])
		}
		if filter != "" && !strings.Contains(strings.ToLower(name), filter) {
			continue
		}
		pkgs = append(pkgs, pkg{name, version})
	}

	title := fmt.Sprintf("📦 %s · 已安装包 (%d)", routerName, len(pkgs))
	if filter != "" {
		title = fmt.Sprintf("📦 %s · 已安装包 · 筛选: %s (%d)", routerName, filter, len(pkgs))
	}

	cols := []tableCol{
		{Name: "name",    DisplayName: "包名", DataType: "text", Width: "auto"},
		{Name: "version", DisplayName: "版本", DataType: "text", Width: "auto"},
	}
	var rows []map[string]string
	for _, p := range pkgs {
		rows = append(rows, map[string]string{"name": p.Name, "version": p.Version})
	}

	var elems []CardElement
	if len(rows) == 0 {
		msg := "opkg 返回空结果"
		if filter != "" {
			msg = fmt.Sprintf("未找到匹配 \"%s\" 的包", filter)
		}
		elems = append(elems, div(msg))
	} else {
		elems = append(elems, tableElement(cols, rows))
	}
	return &Card{
		Schema: "2.0",
		Config: CardConfig{WideScreenMode: true},
		Header: &CardHeader{
			Title:    TextObject{Tag: "plain_text", Content: title},
			Template: "blue",
		},
		Body: CardBody{Elements: elems},
	}
}

// ── Service list card ─────────────────────────────────────────────────────────

// BuildServiceListCard shows /etc/init.d/ services and their autostart (rc.d) status.
func BuildServiceListCard(routerName, initdOut, rcdOut string) *Card {
	// collect service names from init.d
	autostart := make(map[string]bool)
	for _, name := range strings.Fields(initdOut) {
		if name == "README" {
			continue
		}
		autostart[name] = false
	}

	// mark autostart-enabled from rc.d (S<priority><name> entries)
	for _, entry := range strings.Fields(rcdOut) {
		if len(entry) < 2 || entry[0] != 'S' {
			continue
		}
		// strip leading 'S' and any numeric priority digits
		name := strings.TrimLeft(entry[1:], "0123456789")
		if _, ok := autostart[name]; ok {
			autostart[name] = true
		}
	}

	var names []string
	for name := range autostart {
		names = append(names, name)
	}
	sort.Strings(names)

	cols := []tableCol{
		{Name: "name",     DisplayName: "服务名",  DataType: "text", Width: "auto"},
		{Name: "autostart", DisplayName: "自启动", DataType: "text", Width: "auto"},
	}
	var rows []map[string]string
	for _, name := range names {
		enabled := "✗"
		if autostart[name] {
			enabled = "✓"
		}
		rows = append(rows, map[string]string{"name": name, "autostart": enabled})
	}

	var elems []CardElement
	if len(rows) == 0 {
		elems = append(elems, div("未找到服务（/etc/init.d/ 为空）"))
	} else {
		elems = append(elems, tableElement(cols, rows))
	}
	return &Card{
		Schema: "2.0",
		Config: CardConfig{WideScreenMode: true},
		Header: &CardHeader{
			Title:    TextObject{Tag: "plain_text", Content: fmt.Sprintf("🔧 %s · 服务列表 (%d)", routerName, len(rows))},
			Template: "blue",
		},
		Body: CardBody{Elements: elems},
	}
}

// ── Status card ───────────────────────────────────────────────────────────────

func BuildStatusCard(routerName string, snap collector.Snapshot) *Card {
	card := &Card{
		Schema: "2.0",
		Config: CardConfig{WideScreenMode: true},
		Header: &CardHeader{
			Title:    TextObject{Tag: "plain_text", Content: fmt.Sprintf("🖥 %s · 状态概览", routerName)},
			Template: "blue",
		},
	}

	// ── Resource bar row ──
	cpuBar := progressBar(snap.CPU, 100, 8)
	memBar := progressBar(snap.Mem.UsedPct(), 100, 8)
	diskBar := progressBar(snap.Disk.UsedPct(), 100, 8)

	tempStr := "N/A"
	if len(snap.Temps) > 0 {
		tempStr = fmt.Sprintf("%.0f°C", snap.Temps[0].TempC)
	}

	resourceMD := fmt.Sprintf(
		"**CPU**  %s %.0f%%   **温度** %s\n"+
			"**内存** %s %.0f%%   %d/%d MB\n"+
			"**存储** %s %.0f%%   %d/%d MB",
		cpuBar, snap.CPU, tempStr,
		memBar, snap.Mem.UsedPct(), snap.Mem.UsedMB(), snap.Mem.TotalMB(),
		diskBar, snap.Disk.UsedPct(), snap.Disk.UsedMB, snap.Disk.TotalMB,
	)
	card.Body.Elements = append(card.Body.Elements, div(resourceMD))
	card.Body.Elements = append(card.Body.Elements, hr())

	// ── Network row ──
	wanIP := getWANIP(snap.Addrs)
	lanIP := getLANIP(snap.Addrs)
	wanRate := getIfaceRate(snap.NetRates, wanIfaceNames(snap.Addrs))
	netMD := fmt.Sprintf(
		"**WAN**  🌐 %s   ↑%s ↓%s\n"+
			"**LAN**  %s   已连接 **%d** 台设备\n"+
			"**在线** %s   负载 %.2f %.2f %.2f",
		wanIP,
		formatRate(wanRate.TxBps), formatRate(wanRate.RxBps),
		lanIP,
		len(snap.Devices),
		formatUptime(snap.Uptime),
		snap.Load.One, snap.Load.Five, snap.Load.Fifteen,
	)
	card.Body.Elements = append(card.Body.Elements, div(netMD))

	// ── Wi-Fi row ──
	if len(snap.Wifi) > 0 {
		card.Body.Elements = append(card.Body.Elements, hr())
		var wifiLines []string
		for _, w := range snap.Wifi {
			freq := w.Frequency
			if freq == "" {
				freq = "?"
			}
			wifiLines = append(wifiLines, fmt.Sprintf("**%s** ch%s (%s GHz)   %s",
				w.SSID, w.Channel, freq, w.Encryption))
		}
		card.Body.Elements = append(card.Body.Elements, div("📶 "+strings.Join(wifiLines, "\n")))
	}

	// ── Action buttons ──
	card.Body.Elements = append(card.Body.Elements, hr())
	card.Body.Elements = append(card.Body.Elements, actions([]map[string]any{
		button("刷新", "default", map[string]any{"action": "refresh_status"}),
		button("设备列表", "default", map[string]any{"action": "list_devices"}),
		button("重启路由", "danger", map[string]any{"action": "reboot_confirm"}),
	}))

	return card
}

// ── Device list card ──────────────────────────────────────────────────────────

// BuildDeviceListCard renders connected devices as a typed table.
// deviceNames maps MAC (lowercase) to custom friendly names from config.
func BuildDeviceListCard(routerName string, devices []collector.Device, deviceNames map[string]string) *Card {
	card := &Card{
		Schema: "2.0",
		Config: CardConfig{WideScreenMode: true},
		Header: &CardHeader{
			Title:    TextObject{Tag: "plain_text", Content: fmt.Sprintf("📱 %s · 已连接设备 (%d)", routerName, len(devices))},
			Template: "blue",
		},
	}

	if len(devices) == 0 {
		card.Body.Elements = append(card.Body.Elements, div("当前无设备在线"))
		return card
	}

	cols := []tableCol{
		{Name: "icon",   DisplayName: "类型", DataType: "text", Width: "auto"},
		{Name: "name",   DisplayName: "名称", DataType: "text", Width: "auto"},
		{Name: "ip",     DisplayName: "IP",   DataType: "text", Width: "auto"},
		{Name: "mac",    DisplayName: "MAC",  DataType: "text", Width: "auto"},
		{Name: "vendor", DisplayName: "厂商", DataType: "text", Width: "auto"},
	}

	var rows []map[string]string
	for _, d := range devices {
		name := d.DisplayName() // Note > Hostname > Vendor > MAC
		if n, ok := deviceNames[strings.ToLower(d.MAC)]; ok && n != "" {
			name = n
		}
		icon := d.Type.Icon()
		if d.RandomMAC {
			icon += "🔀"
		}
		rows = append(rows, map[string]string{
			"icon": icon, "name": name, "ip": d.IP, "mac": d.MAC, "vendor": d.Vendor,
		})
	}

	card.Body.Elements = append(card.Body.Elements, tableElement(cols, rows))
	return card
}

// ── Alert cards ───────────────────────────────────────────────────────────────

func BuildAlertCard(routerName string, ev events.Event) *Card {
	var title, body, template string

	switch ev.Type {
	case events.EvDeviceJoin:
		p := ev.Payload.(events.DevicePayload)
		name := alertDeviceName(p)
		title = "📱 新设备接入"
		body = fmt.Sprintf("**%s** 已连接\nIP: %s   MAC: %s", name, p.IP, p.MAC)
		template = "green"

	case events.EvDeviceLeave:
		p := ev.Payload.(events.DevicePayload)
		name := alertDeviceName(p)
		title = "👋 设备离线"
		body = fmt.Sprintf("**%s** 已断开\nIP: %s   MAC: %s", name, p.IP, p.MAC)
		template = "grey"

	case events.EvWANIPChange:
		p := ev.Payload.(events.WANIPPayload)
		title = "🌐 WAN IP 变更"
		body = fmt.Sprintf("接口: %s\n旧 IP: %s\n新 IP: %s", p.Iface, p.OldIP, p.NewIP)
		template = "yellow"

	case events.EvHighCPU:
		p := ev.Payload.(events.CPUPayload)
		title = "⚠️ CPU 高占用"
		body = fmt.Sprintf("CPU 使用率 **%.0f%%**，已持续 %s", p.Percent, formatDuration(p.Duration))
		template = "red"

	case events.EvHighMemory:
		p := ev.Payload.(events.MemPayload)
		title = "🔴 内存告警"
		body = fmt.Sprintf("内存使用率 **%.0f%%**，剩余 **%d MB**", p.Percent, p.FreeMB)
		template = "red"

	case events.EvIfaceDown:
		p := ev.Payload.(events.IfacePayload)
		title = "🔌 接口 DOWN"
		body = fmt.Sprintf("接口 %s 链路断开", p.Name)
		template = "red"

	case events.EvIfaceUp:
		p := ev.Payload.(events.IfacePayload)
		title = "✅ 接口恢复"
		body = fmt.Sprintf("接口 %s 链路已恢复", p.Name)
		template = "green"

	case events.EvRebootDetected:
		title = "✅ 路由已重启"
		body = "检测到路由器重启完成"
		template = "green"

	case events.EvServiceDown:
		p := ev.Payload.(events.ServicePayload)
		title = "🔴 服务宕机"
		body = fmt.Sprintf("服务 **%s** 已停止运行", p.Name)
		template = "red"

	case events.EvServiceUp:
		p := ev.Payload.(events.ServicePayload)
		title = "✅ 服务恢复"
		body = fmt.Sprintf("服务 **%s** 已恢复运行", p.Name)
		template = "green"

	default:
		title = "📢 系统事件"
		body = ev.Type.String()
		template = "grey"
	}

	return &Card{
		Schema: "2.0",
		Config: CardConfig{WideScreenMode: true},
		Header: &CardHeader{
			Title:    TextObject{Tag: "plain_text", Content: fmt.Sprintf("%s · %s", routerName, title)},
			Template: template,
		},
		Body: CardBody{Elements: []CardElement{
			div(body),
			div(fmt.Sprintf("🕐 %s", ev.At.Format("15:04:05"))),
		}},
	}
}

// ── Confirmation card ─────────────────────────────────────────────────────────

func BuildConfirmCard(routerName, actionLabel, confirmToken string) *Card {
	return &Card{
		Schema: "2.0",
		Config: CardConfig{WideScreenMode: true},
		Header: &CardHeader{
			Title:    TextObject{Tag: "plain_text", Content: fmt.Sprintf("⚠️ %s · 操作确认", routerName)},
			Template: "yellow",
		},
		Body: CardBody{Elements: []CardElement{
			div(fmt.Sprintf("即将执行：**%s**\n\n请在 60 秒内确认，超时自动取消。", actionLabel)),
			actions([]map[string]any{
				button("✅ 确认执行", "danger", map[string]any{
					"action": "confirm",
					"token":  confirmToken,
				}),
				button("❌ 取消", "default", map[string]any{
					"action": "cancel",
					"token":  confirmToken,
				}),
			}),
		}},
	}
}

// BuildResultCard shows the outcome of an executed command.
func BuildResultCard(routerName, cmdLabel, output string, success bool) *Card {
	tpl := "green"
	icon := "✅"
	if !success {
		tpl = "red"
		icon = "❌"
	}
	body := fmt.Sprintf("命令: **%s**\n\n%s", cmdLabel, truncate(output, 800))
	return &Card{
		Schema: "2.0",
		Config: CardConfig{WideScreenMode: true},
		Header: &CardHeader{
			Title:    TextObject{Tag: "plain_text", Content: fmt.Sprintf("%s %s · 执行结果", icon, routerName)},
			Template: tpl,
		},
		Body: CardBody{Elements: []CardElement{div(body)}},
	}
}

// ── ARP card ──────────────────────────────────────────────────────────────────

// BuildARPCard renders the ARP/neighbour table (IPv4 only; IPv6 link-local skipped).
// devices and db are used to enrich entries with known hostnames, types, and notes.
func BuildARPCard(routerName, neighOut string, devices []collector.Device, db *collector.DevDB) *Card {
	// Build lookup: IP → Device
	devByIP := make(map[string]collector.Device, len(devices))
	for _, d := range devices {
		devByIP[d.IP] = d
	}

	cols := []tableCol{
		{Name: "name",  DisplayName: "名称",    DataType: "text", Width: "auto"},
		{Name: "ip",    DisplayName: "IP 地址", DataType: "text", Width: "auto"},
		{Name: "mac",   DisplayName: "MAC",     DataType: "text", Width: "auto"},
		{Name: "state", DisplayName: "状态",    DataType: "text", Width: "auto"},
	}

	var rows []map[string]string
	if neighOut != "" {
		for _, line := range strings.Split(strings.TrimSpace(neighOut), "\n") {
			if line == "" {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			ip := fields[0]
			if strings.HasPrefix(ip, "fe80:") || (strings.Contains(ip, ":") && !strings.Contains(line, "lladdr")) {
				continue
			}
			mac, state := "-", fields[len(fields)-1]
			for i, f := range fields {
				if f == "lladdr" && i+1 < len(fields) {
					mac = fields[i+1]
				}
			}
			name := ip
			if d, ok := devByIP[ip]; ok {
				name = d.DisplayName() // Note > Hostname > Vendor > MAC
			} else if mac != "-" {
				if db != nil {
					if rec, ok := db.Get(mac); ok {
						if rec.Note != "" {
							name = rec.Note
						} else if rec.Hostname != "" {
							name = rec.Hostname
						}
					}
				}
				if name == ip {
					if vendor := collector.OUILookup(mac); vendor != "" {
						name = vendor
					}
				}
			}
			rows = append(rows, map[string]string{
				"name": name, "ip": ip, "mac": mac, "state": state,
			})
		}
	} else {
		for _, d := range devices {
			rows = append(rows, map[string]string{
				"name": d.DisplayName(), "ip": d.IP, "mac": d.MAC, "state": "-",
			})
		}
	}

	var elems []CardElement
	if len(rows) == 0 {
		elems = append(elems, div("ARP 表为空"))
	} else {
		elems = append(elems, tableElement(cols, rows))
	}
	return &Card{
		Schema: "2.0",
		Config: CardConfig{WideScreenMode: true},
		Header: &CardHeader{
			Title:    TextObject{Tag: "plain_text", Content: fmt.Sprintf("📡 %s · ARP 邻居表 (%d)", routerName, len(rows))},
			Template: "blue",
		},
		Body: CardBody{Elements: elems},
	}
}

// ── Route card ────────────────────────────────────────────────────────────────

func BuildRouteCard(routerName string, routes []collector.RouteEntry) *Card {
	cols := []tableCol{
		{Name: "dst",   DisplayName: "目标", DataType: "text", Width: "auto"},
		{Name: "gw",    DisplayName: "网关", DataType: "text", Width: "auto"},
		{Name: "dev",   DisplayName: "接口", DataType: "text", Width: "auto"},
		{Name: "proto", DisplayName: "协议", DataType: "text", Width: "auto"},
	}
	var rows []map[string]string
	for _, r := range routes {
		gw := r.Gateway
		if gw == "" {
			gw = "直连"
		}
		proto := r.Proto
		if proto == "" {
			proto = "-"
		}
		rows = append(rows, map[string]string{"dst": r.Dst, "gw": gw, "dev": r.Dev, "proto": proto})
	}

	var elems []CardElement
	if len(rows) == 0 {
		elems = append(elems, div("路由表为空"))
	} else {
		elems = append(elems, tableElement(cols, rows))
	}
	return &Card{
		Schema: "2.0",
		Config: CardConfig{WideScreenMode: true},
		Header: &CardHeader{
			Title:    TextObject{Tag: "plain_text", Content: fmt.Sprintf("🗺 %s · 路由表 (%d)", routerName, len(rows))},
			Template: "blue",
		},
		Body: CardBody{Elements: elems},
	}
}

// ── Card element helpers ──────────────────────────────────────────────────────

func div(mdContent string) CardElement {
	return CardElement{
		"tag":  "div",
		"text": textObj("lark_md", mdContent),
	}
}

func hr() CardElement {
	return CardElement{"tag": "hr"}
}

func actions(btns []map[string]any) CardElement {
	acts := make([]any, len(btns))
	for i, b := range btns {
		acts[i] = b
	}
	return CardElement{
		"tag":     "action",
		"actions": acts,
	}
}

func button(label, btype string, value map[string]any) map[string]any {
	return map[string]any{
		"tag":  "button",
		"text": textObj("plain_text", label),
		"type": btype,
		"behaviors": []map[string]any{
			{"type": "callback", "value": value},
		},
	}
}

// ── Formatting helpers ────────────────────────────────────────────────────────

// tableCol defines a column in the Card 2.0 native table component.
type tableCol struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	DataType    string `json:"data_type"`
	Width       string `json:"width"`
}

func tableElement(cols []tableCol, rows []map[string]string) CardElement {
	anyRows := make([]map[string]any, len(rows))
	for i, r := range rows {
		m := make(map[string]any, len(r))
		for k, v := range r {
			m[k] = v
		}
		anyRows[i] = m
	}
	return CardElement{
		"tag":       "table",
		"page_size": 50,
		"row_height": "low",
		"header_style": map[string]any{
			"text_align":       "left",
			"bold":             true,
			"background_style": "grey",
		},
		"columns": cols,
		"rows":    anyRows,
	}
}

func progressBar(val, max float64, width int) string {
	pct := val / max
	if pct > 1 {
		pct = 1
	}
	filled := int(math.Round(pct * float64(width)))
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

func formatRate(bps float64) string {
	switch {
	case bps >= 1e9:
		return fmt.Sprintf("%.1fG", bps/1e9)
	case bps >= 1e6:
		return fmt.Sprintf("%.1fM", bps/1e6)
	case bps >= 1e3:
		return fmt.Sprintf("%.0fK", bps/1e3)
	default:
		return fmt.Sprintf("%.0fB", bps)
	}
}

func formatUptime(d time.Duration) string {
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h >= 24 {
		return fmt.Sprintf("%dd%dh", h/24, h%24)
	}
	return fmt.Sprintf("%dh%dm", h, m)
}

func formatDuration(d time.Duration) string {
	return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
}

func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}

// BuildLiveCard renders a streaming command output card.
// done=false → still running (blue); done=true,success=true → green; done=true,success=false → red.
func BuildLiveCard(routerName, title, output string, done, success bool) *Card {
	icon, tpl := "⏳", "blue"
	if done {
		if success {
			icon, tpl = "✅", "green"
		} else {
			icon, tpl = "❌", "red"
		}
	}
	body := "_执行中，请稍候…_"
	if output != "" {
		// Keep at most the last 2000 chars so the card stays within limits
		runes := []rune(output)
		if len(runes) > 2000 {
			output = "…\n" + string(runes[len(runes)-2000:])
		}
		body = "```\n" + output + "```"
	}
	return &Card{
		Schema: "2.0",
		Config: CardConfig{WideScreenMode: true},
		Header: &CardHeader{
			Title:    TextObject{Tag: "plain_text", Content: fmt.Sprintf("%s %s · %s", icon, routerName, title)},
			Template: tpl,
		},
		Body: CardBody{Elements: []CardElement{div(body)}},
	}
}

func alertDeviceName(p events.DevicePayload) string {
	if p.Hostname != "" {
		return p.Hostname
	}
	if p.Vendor != "" {
		parts := strings.Split(p.MAC, ":")
		if len(parts) == 6 {
			return p.Vendor + " (" + strings.Join(parts[3:], ":") + ")"
		}
		return p.Vendor
	}
	return p.MAC
}

func getLANIP(addrs []collector.AddrInfo) string {
	for _, ai := range addrs {
		if isWANIfaceName(ai.Iface) || ai.Iface == "lo" {
			continue
		}
		if len(ai.Addrs) > 0 {
			ip := ai.Addrs[0]
			if idx := strings.Index(ip, "/"); idx >= 0 {
				ip = ip[:idx]
			}
			return ip
		}
	}
	return "N/A"
}

func getWANIP(addrs []collector.AddrInfo) string {
	for _, ai := range addrs {
		if isWANIfaceName(ai.Iface) && len(ai.Addrs) > 0 {
			ip := ai.Addrs[0]
			if idx := strings.Index(ip, "/"); idx >= 0 {
				ip = ip[:idx]
			}
			return ip
		}
	}
	return "N/A"
}

func wanIfaceNames(addrs []collector.AddrInfo) []string {
	var names []string
	for _, ai := range addrs {
		if isWANIfaceName(ai.Iface) {
			names = append(names, ai.Iface)
		}
	}
	return names
}

func isWANIfaceName(name string) bool {
	for _, w := range []string{"eth0", "eth1", "pppoe-wan", "wan"} {
		if name == w {
			return true
		}
	}
	return strings.HasPrefix(name, "ppp")
}

func getIfaceRate(rates map[string]collector.NetRate, ifaces []string) collector.NetRate {
	for _, name := range ifaces {
		if r, ok := rates[name]; ok {
			return r
		}
	}
	return collector.NetRate{}
}

// ── Traffic card ──────────────────────────────────────────────────────────────

func BuildTrafficCard(routerName string, rates map[string]collector.NetRate) *Card {
	type row struct {
		name string
		tx   float64
		rx   float64
	}
	var rs []row
	for name, r := range rates {
		if name == "lo" {
			continue
		}
		rs = append(rs, row{name, r.TxBps, r.RxBps})
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].name < rs[j].name })

	cols := []tableCol{
		{Name: "iface", DisplayName: "接口",     DataType: "text", Width: "auto"},
		{Name: "tx",    DisplayName: "↑ 上传/s", DataType: "text", Width: "auto"},
		{Name: "rx",    DisplayName: "↓ 下载/s", DataType: "text", Width: "auto"},
	}
	var rows []map[string]string
	for _, r := range rs {
		rows = append(rows, map[string]string{
			"iface": r.name,
			"tx":    formatRate(r.tx),
			"rx":    formatRate(r.rx),
		})
	}

	var elems []CardElement
	if len(rows) == 0 {
		elems = append(elems, div("暂无流量数据"))
	} else {
		elems = append(elems, tableElement(cols, rows))
	}
	return &Card{
		Schema: "2.0",
		Config: CardConfig{WideScreenMode: true},
		Header: &CardHeader{
			Title:    TextObject{Tag: "plain_text", Content: fmt.Sprintf("📊 %s · 实时流量", routerName)},
			Template: "blue",
		},
		Body: CardBody{Elements: elems},
	}
}

// ── Wifi card ─────────────────────────────────────────────────────────────────

func BuildWifiCard(routerName string, wifis []collector.WifiInfo) *Card {
	cols := []tableCol{
		{Name: "iface", DisplayName: "接口", DataType: "text", Width: "auto"},
		{Name: "ssid",  DisplayName: "SSID", DataType: "text", Width: "auto"},
		{Name: "ch",    DisplayName: "信道", DataType: "text", Width: "auto"},
		{Name: "freq",  DisplayName: "频率", DataType: "text", Width: "auto"},
		{Name: "enc",   DisplayName: "加密", DataType: "text", Width: "auto"},
	}
	var rows []map[string]string
	for _, w := range wifis {
		rows = append(rows, map[string]string{
			"iface": w.Iface,
			"ssid":  w.SSID,
			"ch":    w.Channel,
			"freq":  w.Frequency,
			"enc":   w.Encryption,
		})
	}

	var elems []CardElement
	if len(rows) == 0 {
		elems = append(elems, div("未检测到无线接口（需安装 iwinfo）"))
	} else {
		elems = append(elems, tableElement(cols, rows))
	}
	return &Card{
		Schema: "2.0",
		Config: CardConfig{WideScreenMode: true},
		Header: &CardHeader{
			Title:    TextObject{Tag: "plain_text", Content: fmt.Sprintf("📶 %s · 无线网络", routerName)},
			Template: "blue",
		},
		Body: CardBody{Elements: elems},
	}
}

// ── Disk card ─────────────────────────────────────────────────────────────────

func BuildDiskCard(routerName string, dfOutput string) *Card {
	cols := []tableCol{
		{Name: "fs",    DisplayName: "文件系统", DataType: "text", Width: "auto"},
		{Name: "size",  DisplayName: "大小",     DataType: "text", Width: "auto"},
		{Name: "used",  DisplayName: "已用",     DataType: "text", Width: "auto"},
		{Name: "avail", DisplayName: "可用",     DataType: "text", Width: "auto"},
		{Name: "pct",   DisplayName: "使用率",   DataType: "text", Width: "auto"},
		{Name: "mount", DisplayName: "挂载点",   DataType: "text", Width: "auto"},
	}
	var rows []map[string]string
	for _, line := range strings.Split(dfOutput, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 || fields[0] == "Filesystem" {
			continue
		}
		rows = append(rows, map[string]string{
			"fs":    fields[0],
			"size":  fields[1],
			"used":  fields[2],
			"avail": fields[3],
			"pct":   fields[4],
			"mount": fields[5],
		})
	}

	var elems []CardElement
	if len(rows) == 0 {
		elems = append(elems, div("获取磁盘信息失败"))
	} else {
		elems = append(elems, tableElement(cols, rows))
	}
	return &Card{
		Schema: "2.0",
		Config: CardConfig{WideScreenMode: true},
		Header: &CardHeader{
			Title:    TextObject{Tag: "plain_text", Content: fmt.Sprintf("💾 %s · 磁盘使用", routerName)},
			Template: "blue",
		},
		Body: CardBody{Elements: elems},
	}
}

// ── Top/Process card ──────────────────────────────────────────────────────────

func BuildTopCard(routerName string, procOutput string, limit int) *Card {
	rows := parseProcessRows(procOutput, limit)

	hasCPU := len(rows) > 0 && rows[0]["cpu"] != ""
	var cols []tableCol
	if hasCPU {
		cols = []tableCol{
			{Name: "pid", DisplayName: "PID",  DataType: "text", Width: "auto"},
			{Name: "cpu", DisplayName: "%CPU", DataType: "text", Width: "auto"},
			{Name: "mem", DisplayName: "内存", DataType: "text", Width: "auto"},
			{Name: "cmd", DisplayName: "进程", DataType: "text", Width: "auto"},
		}
	} else {
		cols = []tableCol{
			{Name: "pid", DisplayName: "PID",  DataType: "text", Width: "auto"},
			{Name: "mem", DisplayName: "内存", DataType: "text", Width: "auto"},
			{Name: "cmd", DisplayName: "进程", DataType: "text", Width: "auto"},
		}
	}

	var elems []CardElement
	if len(rows) == 0 {
		elems = append(elems, div("获取进程列表失败"))
	} else {
		elems = append(elems, tableElement(cols, rows))
	}
	title := fmt.Sprintf("📋 %s · 进程列表", routerName)
	if limit > 0 {
		title = fmt.Sprintf("📋 %s · 进程列表 (top %d)", routerName, limit)
	}
	return &Card{
		Schema: "2.0",
		Config: CardConfig{WideScreenMode: true},
		Header: &CardHeader{
			Title:    TextObject{Tag: "plain_text", Content: title},
			Template: "blue",
		},
		Body: CardBody{Elements: elems},
	}
}

// parseProcessRows parses `top -b -n 1` or `ps` output into table rows.
// It finds the header line containing "PID" and "COMMAND"/"CMD"/"COMM",
// then extracts pid, %cpu (if present), vsz→mem, and command.
func parseProcessRows(output string, limit int) []map[string]string {
	lines := strings.Split(output, "\n")

	// Find header line
	headerIdx := -1
	var headers []string
	for i, line := range lines {
		upper := strings.ToUpper(line)
		if strings.Contains(upper, "PID") &&
			(strings.Contains(upper, "COMMAND") || strings.Contains(upper, " CMD") || strings.Contains(upper, "COMM")) {
			headers = strings.Fields(line)
			headerIdx = i
			break
		}
	}
	if headerIdx < 0 || len(headers) == 0 {
		return nil
	}

	pidIdx, cpuIdx, vszIdx, cmdIdx := -1, -1, -1, -1
	for i, h := range headers {
		switch strings.ToUpper(h) {
		case "PID":
			pidIdx = i
		case "%CPU", "CPU%":
			cpuIdx = i
		case "VSZ":
			vszIdx = i
		case "COMMAND", "CMD", "COMM":
			if cmdIdx < 0 { // take the first match
				cmdIdx = i
			}
		}
	}
	if pidIdx < 0 || cmdIdx < 0 {
		return nil
	}

	var rows []map[string]string
	for _, line := range lines[headerIdx+1:] {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) <= cmdIdx {
			continue
		}
		if _, err := strconv.Atoi(fields[pidIdx]); err != nil {
			continue
		}
		row := map[string]string{
			"pid": fields[pidIdx],
			"cmd": strings.Join(fields[cmdIdx:], " "),
		}
		if cpuIdx >= 0 && cpuIdx < len(fields) {
			row["cpu"] = fields[cpuIdx] + "%"
		}
		if vszIdx >= 0 && vszIdx < len(fields) {
			if kb, err := strconv.Atoi(fields[vszIdx]); err == nil {
				row["mem"] = formatSizeKB(kb)
			} else {
				row["mem"] = fields[vszIdx]
			}
		}
		rows = append(rows, row)
		if limit > 0 && len(rows) >= limit {
			break
		}
	}
	return rows
}

func formatSizeKB(kb int) string {
	switch {
	case kb >= 1024*1024:
		return fmt.Sprintf("%.1fG", float64(kb)/1024/1024)
	case kb >= 1024:
		return fmt.Sprintf("%.0fM", float64(kb)/1024)
	default:
		return fmt.Sprintf("%dK", kb)
	}
}

// ── Plugin list card ──────────────────────────────────────────────────────────

func BuildPluginListCard(routerName string, plugins []PluginEntry) *Card {
	var elems []CardElement
	if len(plugins) == 0 {
		elems = append(elems, div("未检测到已安装的插件\n\n请在 config.toml 中配置 **[[plugins]]** 并设置 **detect** 路径"))
	} else {
		cols := []tableCol{
			{Name: "name",   DisplayName: "插件名", DataType: "text", Width: "auto"},
			{Name: "type",   DisplayName: "类型",   DataType: "text", Width: "auto"},
			{Name: "status", DisplayName: "状态查询", DataType: "text", Width: "auto"},
			{Name: "config", DisplayName: "配置文件", DataType: "text", Width: "auto"},
			{Name: "reload", DisplayName: "重载",   DataType: "text", Width: "auto"},
		}
		var rows []map[string]string
		for _, p := range plugins {
			yn := func(b bool) string {
				if b {
					return "✓"
				}
				return "✗"
			}
			rows = append(rows, map[string]string{
				"name":   p.Name,
				"type":   p.TypeLabel,
				"status": yn(p.HasStatus),
				"config": yn(p.HasConfig),
				"reload": yn(p.HasReload),
			})
		}
		elems = append(elems, tableElement(cols, rows))
		elems = append(elems, hr())
		elems = append(elems, div("**查看状态:** /plugin status <名称>\n**查看配置:** /plugin config <名称>\n**重载插件:** /plugin reload <名称>\n**切换节点:** /plugin switch <名称> <代理组> <节点>"))
	}
	return &Card{
		Schema: "2.0",
		Config: CardConfig{WideScreenMode: true},
		Header: &CardHeader{
			Title:    TextObject{Tag: "plain_text", Content: fmt.Sprintf("🧩 %s · 已安装插件 (%d)", routerName, len(plugins))},
			Template: "blue",
		},
		Body: CardBody{Elements: elems},
	}
}

// ── sing-box status card ──────────────────────────────────────────────────────

func BuildSingBoxCard(routerName, pluginName string, s SingBoxDisplay) *Card {
	var elems []CardElement

	// header stats line
	statsLine := fmt.Sprintf(
		"**版本** %s   **连接数** %d   **↑累计** %s   **↓累计** %s",
		orNA(s.Version),
		s.Connections,
		formatBytes(s.UpBytes),
		formatBytes(s.DownBytes),
	)
	elems = append(elems, div(statsLine))

	if len(s.Groups) > 0 {
		elems = append(elems, hr())
		cols := []tableCol{
			{Name: "group",   DisplayName: "代理组", DataType: "text", Width: "auto"},
			{Name: "current", DisplayName: "当前节点", DataType: "text", Width: "auto"},
			{Name: "count",   DisplayName: "节点数",  DataType: "text", Width: "auto"},
		}
		var rows []map[string]string
		for _, g := range s.Groups {
			rows = append(rows, map[string]string{
				"group":   g.Name,
				"current": g.Current,
				"count":   fmt.Sprintf("%d", g.Count),
			})
		}
		elems = append(elems, tableElement(cols, rows))
		elems = append(elems, hr())
		elems = append(elems, div("切换节点: **/plugin switch** "+pluginName+" **<代理组>** **<节点名>**"))
	} else {
		elems = append(elems, hr())
		elems = append(elems, div("_未获取到代理组（检查 api_url 是否正确，或 sing-box 未运行）_"))
	}

	return &Card{
		Schema: "2.0",
		Config: CardConfig{WideScreenMode: true},
		Header: &CardHeader{
			Title:    TextObject{Tag: "plain_text", Content: fmt.Sprintf("🔀 %s · %s 代理状态", routerName, pluginName)},
			Template: "blue",
		},
		Body: CardBody{Elements: elems},
	}
}

// ── Generic plugin status card ────────────────────────────────────────────────

func BuildGenericPluginCard(routerName, pluginName, statusOut string, statusErr error, stats []PluginStatRow) *Card {
	var elems []CardElement

	if statusErr != nil {
		elems = append(elems, div(fmt.Sprintf("**状态查询失败:** %s", statusErr.Error())))
	} else if statusOut != "" {
		body := truncate(statusOut, 1500)
		elems = append(elems, div("```\n"+body+"\n```"))
	} else {
		elems = append(elems, div("_未配置 status_cmd_"))
	}

	if len(stats) > 0 {
		elems = append(elems, hr())
		cols := []tableCol{
			{Name: "label", DisplayName: "指标", DataType: "text", Width: "auto"},
			{Name: "value", DisplayName: "值",   DataType: "text", Width: "auto"},
		}
		var rows []map[string]string
		for _, s := range stats {
			rows = append(rows, map[string]string{"label": s.Label, "value": s.Value})
		}
		elems = append(elems, tableElement(cols, rows))
	}

	tpl := "blue"
	if statusErr != nil {
		tpl = "red"
	}
	return &Card{
		Schema: "2.0",
		Config: CardConfig{WideScreenMode: true},
		Header: &CardHeader{
			Title:    TextObject{Tag: "plain_text", Content: fmt.Sprintf("🔧 %s · %s 状态", routerName, pluginName)},
			Template: tpl,
		},
		Body: CardBody{Elements: elems},
	}
}

// ── Plugin config file card ───────────────────────────────────────────────────

func BuildPluginConfigCard(routerName, pluginName, filePath, content string) *Card {
	body := truncate(content, 2000)
	elems := []CardElement{
		div(fmt.Sprintf("**文件路径:** `%s`", filePath)),
		hr(),
		div("```\n" + body + "\n```"),
	}
	if len([]rune(content)) > 2000 {
		elems = append(elems, div("_（内容已截断，完整文件请直接查看路由器）_"))
	}
	return &Card{
		Schema: "2.0",
		Config: CardConfig{WideScreenMode: true},
		Header: &CardHeader{
			Title:    TextObject{Tag: "plain_text", Content: fmt.Sprintf("📄 %s · %s 配置", routerName, pluginName)},
			Template: "blue",
		},
		Body: CardBody{Elements: elems},
	}
}

// ── formatting helpers ────────────────────────────────────────────────────────

func formatBytes(b int64) string {
	const (
		GB = 1 << 30
		MB = 1 << 20
		KB = 1 << 10
	)
	switch {
	case b >= GB:
		return fmt.Sprintf("%.2f GB", float64(b)/GB)
	case b >= MB:
		return fmt.Sprintf("%.1f MB", float64(b)/MB)
	case b >= KB:
		return fmt.Sprintf("%.0f KB", float64(b)/KB)
	default:
		return fmt.Sprintf("%d B", b)
	}
}

func orNA(s string) string {
	if s == "" {
		return "N/A"
	}
	return s
}
