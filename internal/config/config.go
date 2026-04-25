package config

import (
	"fmt"
	"os"
	"time"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Feishu   FeishuConfig      `toml:"feishu"`
	Router   RouterConfig      `toml:"router"`
	Monitor  MonitorConfig     `toml:"monitor"`
	Alert    AlertConfig       `toml:"alert"`
	Security SecurityConfig    `toml:"security"`
	Devices  map[string]string `toml:"devices"`  // MAC (lowercase) → friendly name
	Plugins  []PluginConfig    `toml:"plugins"`  // optional plugin profiles
}

// PluginConfig describes how to detect, query, and manage one OpenWrt plugin.
type PluginConfig struct {
	Name       string          `toml:"name"`
	Type       string          `toml:"type"`        // "" (generic) | "singbox"
	Detect     string          `toml:"detect"`      // file/binary path to check install
	ConfigFile string          `toml:"config_file"`
	StatusCmd  string          `toml:"status_cmd"`  // shell command for generic status
	ReloadCmd  string          `toml:"reload_cmd"`  // shell command for reload/restart
	Stats      []PluginStatDef `toml:"stats"`       // named metric commands
	APIURL     string          `toml:"api_url"`     // REST API base URL (singbox)
	APISecret  string          `toml:"api_secret"`  // Bearer token for API auth
}

// PluginStatDef is one named metric scraped via a shell command.
type PluginStatDef struct {
	Label string `toml:"label"`
	Cmd   string `toml:"cmd"`
}

type FeishuConfig struct {
	AppID      string   `toml:"app_id"`
	AppSecret  string   `toml:"app_secret"`
	ChatID     string   `toml:"chat_id"`
	AdminUsers []string `toml:"admin_users"`
}

type RouterConfig struct {
	Name     string `toml:"name"`
	LanIface string `toml:"lan_iface"`
}

type MonitorConfig struct {
	CollectFast     duration `toml:"collect_interval_fast"`
	CollectSlow     duration `toml:"collect_interval_slow"`
	WatchedServices []string `toml:"watched_services"` // services to alert on crash/recovery
}

type AlertConfig struct {
	CPUThresholdPct int `toml:"cpu_threshold_pct"`
	CPUDurationSecs int `toml:"cpu_duration_secs"`
	MemThresholdPct int `toml:"memory_threshold_pct"`
	CooldownSecs    int `toml:"cooldown_secs"`
}

type SecurityConfig struct {
	CmdRateLimit     int      `toml:"cmd_rate_limit"`
	ExecWhitelist    []string `toml:"exec_whitelist"`
	ServiceWhitelist []string `toml:"service_whitelist"` // services allowed for start/stop/restart
}

// duration is a TOML-friendly wrapper for time.Duration.
type duration struct{ time.Duration }

func (d *duration) UnmarshalText(text []byte) error {
	v, err := time.ParseDuration(string(text))
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}

func Load(path string) (*Config, error) {
	cfg := &Config{}
	setDefaults(cfg)

	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open config %s: %w", path, err)
	}
	defer f.Close()

	if _, err := toml.NewDecoder(f).Decode(cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	return cfg, validate(cfg)
}

func setDefaults(c *Config) {
	c.Router.Name = "OpenWrt"
	c.Router.LanIface = "br-lan"
	c.Monitor.CollectFast = duration{5 * time.Second}
	c.Monitor.CollectSlow = duration{30 * time.Second}
	c.Alert.CPUThresholdPct = 85
	c.Alert.CPUDurationSecs = 60
	c.Alert.MemThresholdPct = 90
	c.Alert.CooldownSecs = 300
	c.Security.CmdRateLimit = 20
	c.Security.ExecWhitelist = []string{"ping", "ping6", "traceroute", "nslookup", "logread"}
	c.Security.ServiceWhitelist = []string{
		"dnsmasq", "firewall", "network", "uhttpd",
		"dropbear", "syslog", "ntpd", "openvpn", "wireguard",
	}
}

func validate(c *Config) error {
	if c.Feishu.AppID == "" {
		return fmt.Errorf("feishu.app_id is required")
	}
	if c.Feishu.AppSecret == "" {
		return fmt.Errorf("feishu.app_secret is required")
	}
	if c.Feishu.ChatID == "" {
		return fmt.Errorf("feishu.chat_id is required")
	}
	if len(c.Feishu.AdminUsers) == 0 {
		return fmt.Errorf("feishu.admin_users must have at least one entry")
	}
	return nil
}

func (c *Config) IsAdmin(userID string) bool {
	for _, uid := range c.Feishu.AdminUsers {
		if uid == userID {
			return true
		}
	}
	return false
}
