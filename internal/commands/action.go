package commands

import (
	"fmt"
	"net"
	"strings"
	"time"

	"larkwrt/internal/executor"
)

// ── Reboot ────────────────────────────────────────────────────────────────────

func doReboot() (string, error) {
	// schedule reboot in 3s to allow the result card to be sent first
	go func() {
		time.Sleep(3 * time.Second)
		executor.RunUnchecked(5*time.Second, "reboot")
	}()
	return "重启命令已发出，3 秒后执行", nil
}

// ── WAN reconnect ─────────────────────────────────────────────────────────────

func doReconnectWAN() (string, error) {
	// Try ifup wan first; fall back to pppoe-wan
	out, _, err := executor.RunUnchecked(15*time.Second, "ifup", "wan")
	if err != nil {
		out2, errOut, err2 := executor.RunUnchecked(15*time.Second, "ifdown", "wan")
		if err2 != nil {
			return errOut, err2
		}
		out = out2
	}
	return out, nil
}

// ── WiFi control ──────────────────────────────────────────────────────────────

func doWifiControl(args []string) (string, error) {
	if len(args) < 1 {
		return "", fmt.Errorf("用法: /wifi on|off [2.4|5]")
	}
	action := strings.ToLower(args[0])
	if action != "on" && action != "off" {
		return "", fmt.Errorf("参数必须是 on 或 off")
	}

	cmd := "wifi"
	cmdArgs := []string{action}
	if len(args) >= 2 {
		// filter by band (not universally supported; try anyway)
		cmdArgs = append(cmdArgs, args[1])
	}

	out, errOut, err := executor.RunUnchecked(15*time.Second, cmd, cmdArgs...)
	if err != nil {
		return errOut, err
	}
	return out, nil
}

// ── Firewall ──────────────────────────────────────────────────────────────────

func doFirewall(args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("用法: /fw allow|block <ip>")
	}
	op := strings.ToLower(args[0])
	ip := args[1]

	// Validate: must be a plain IP or CIDR; reject anything else to prevent
	// unexpected iptables flags or malformed rules.
	if net.ParseIP(ip) == nil {
		if _, _, err := net.ParseCIDR(ip); err != nil {
			return "", fmt.Errorf("无效的 IP 地址或 CIDR: %q", ip)
		}
	}

	switch op {
	case "allow":
		// Remove any existing block rule, then add allow rule
		out, errOut, err := executor.RunUnchecked(10*time.Second,
			"iptables", "-D", "INPUT", "-s", ip, "-j", "DROP")
		_ = out // ignore "not found" errors
		_ = err
		_, errOut, err = executor.RunUnchecked(10*time.Second,
			"iptables", "-I", "INPUT", "-s", ip, "-j", "ACCEPT")
		if err != nil {
			return errOut, err
		}
		return fmt.Sprintf("✅ 已放行 IP %s", ip), nil

	case "block":
		_, errOut, err := executor.RunUnchecked(10*time.Second,
			"iptables", "-I", "INPUT", "-s", ip, "-j", "DROP")
		if err != nil {
			return errOut, err
		}
		// Also block forwarded traffic
		executor.RunUnchecked(10*time.Second,
			"iptables", "-I", "FORWARD", "-s", ip, "-j", "DROP")
		return fmt.Sprintf("✅ 已封锁 IP %s", ip), nil

	default:
		return "", fmt.Errorf("未知操作: %s（支持 allow 或 block）", op)
	}
}

// ── Service restart ───────────────────────────────────────────────────────────

var allowedServices = map[string]struct{}{
	"dnsmasq":    {},
	"firewall":   {},
	"network":    {},
	"uhttpd":     {},
	"dropbear":   {},
	"syslog":     {},
	"ntpd":       {},
	"openvpn":    {},
	"wireguard":  {},
}

func doServiceRestart(args []string) (string, error) {
	if len(args) != 1 {
		return "", fmt.Errorf("用法: /service <名称>，如 /service dnsmasq")
	}
	name := args[0]
	if _, ok := allowedServices[name]; !ok {
		return "", fmt.Errorf("服务 %q 不在允许列表中", name)
	}

	out, errOut, err := executor.RunUnchecked(30*time.Second, "/etc/init.d/"+name, "restart")
	if err != nil {
		return errOut, err
	}
	if out == "" {
		out = fmt.Sprintf("服务 %s 重启成功", name)
	}
	return out, nil
}
