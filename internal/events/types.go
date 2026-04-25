package events

import "time"

type EventType uint8

const (
	EvDeviceJoin    EventType = iota // 新设备接入
	EvDeviceLeave                    // 设备离线
	EvWANIPChange                    // WAN IP 变更
	EvHighCPU                        // CPU 持续高占用
	EvHighMemory                     // 内存告警
	EvIfaceDown                      // 接口 DOWN
	EvIfaceUp                        // 接口恢复 UP
	EvRebootDetected                 // 路由重启完成
	EvServiceDown                    // 被监控服务宕机
	EvServiceUp                      // 被监控服务恢复
)

func (e EventType) String() string {
	switch e {
	case EvDeviceJoin:
		return "device_join"
	case EvDeviceLeave:
		return "device_leave"
	case EvWANIPChange:
		return "wan_ip_change"
	case EvHighCPU:
		return "high_cpu"
	case EvHighMemory:
		return "high_memory"
	case EvIfaceDown:
		return "iface_down"
	case EvIfaceUp:
		return "iface_up"
	case EvRebootDetected:
		return "reboot"
	case EvServiceDown:
		return "service_down"
	case EvServiceUp:
		return "service_up"
	default:
		return "unknown"
	}
}

type Event struct {
	Type    EventType
	Payload any
	At      time.Time
}

// ── Payload types ─────────────────────────────────────────────────────────────

type DevicePayload struct {
	MAC      string
	IP       string
	Hostname string
	Vendor   string
	Online   time.Duration // 离线时有效
}

type WANIPPayload struct {
	OldIP string
	NewIP string
	Iface string
}

type CPUPayload struct {
	Percent  float64
	Duration time.Duration
}

type MemPayload struct {
	Percent float64
	FreeMB  int
}

type IfacePayload struct {
	Name  string
	State string // "up" | "down"
}

type ServicePayload struct {
	Name string
}
