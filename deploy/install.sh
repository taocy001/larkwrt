#!/bin/sh
# One-shot installer for larkwrt on OpenWrt
#
# Usage:
#   BINARY_URL=https://github.com/.../releases/download/v1.0.0 \
#   sh install.sh \
#     --app-id    cli_xxx \
#     --app-secret xxx \
#     --chat-id   oc_xxx \
#     --admin     ou_xxx \
#     --admin     ou_yyy \
#     --name      "Home Router"
#
# Flags:
#   --app-id      Feishu App ID (required)
#   --app-secret  Feishu App Secret (required)
#   --chat-id     Target group chat ID, starts with oc_ (required)
#   --admin       Admin user open_id, starts with ou_ (required; repeat for multiple admins)
#   --name        Router display name (default: hostname)
#   --lan-iface   LAN bridge interface for device discovery (default: br-lan)
#
# Set BINARY_URL to the directory containing release binaries.
# If unset, the binary must be copied manually before running this script.

set -e

BINARY_URL="${BINARY_URL:-}"
ARCH=$(uname -m)
CONFIG_DIR=/etc/larkwrt
BINARY=/usr/bin/larkwrt-agent

# ── Argument parsing ──────────────────────────────────────────────────────────
APP_ID=""
APP_SECRET=""
CHAT_ID=""
ADMIN_USERS=""   # comma-separated, built up from multiple --admin flags
ROUTER_NAME="$(uname -n)"
LAN_IFACE="br-lan"

while [ $# -gt 0 ]; do
    case "$1" in
        --app-id)     APP_ID="$2";     shift 2 ;;
        --app-secret) APP_SECRET="$2"; shift 2 ;;
        --chat-id)    CHAT_ID="$2";    shift 2 ;;
        --admin)
            if [ -z "$ADMIN_USERS" ]; then
                ADMIN_USERS="\"$2\""
            else
                ADMIN_USERS="$ADMIN_USERS, \"$2\""
            fi
            shift 2 ;;
        --name)       ROUTER_NAME="$2"; shift 2 ;;
        --lan-iface)  LAN_IFACE="$2";  shift 2 ;;
        *) echo "Unknown argument: $1"; exit 1 ;;
    esac
done

# ── Validate required fields ──────────────────────────────────────────────────
MISSING=""
[ -z "$APP_ID" ]      && MISSING="$MISSING --app-id"
[ -z "$APP_SECRET" ]  && MISSING="$MISSING --app-secret"
[ -z "$CHAT_ID" ]     && MISSING="$MISSING --chat-id"
[ -z "$ADMIN_USERS" ] && MISSING="$MISSING --admin"
if [ -n "$MISSING" ]; then
    echo "Error: missing required arguments:$MISSING"
    exit 1
fi

# ── Detect arch and pick binary ───────────────────────────────────────────────
case "$ARCH" in
    mips)    SUFFIX="mips"   ;;
    mipsel)  SUFFIX="mipsle" ;;
    armv7*)  SUFFIX="arm"    ;;
    aarch64) SUFFIX="arm64"  ;;
    x86_64)  SUFFIX="amd64"  ;;
    *)       echo "Unsupported arch: $ARCH"; exit 1 ;;
esac

echo "[+] Detected arch: $ARCH → binary suffix: $SUFFIX"

# ── Install binary ────────────────────────────────────────────────────────────
if [ -n "$BINARY_URL" ]; then
    echo "[+] Downloading binary from $BINARY_URL ..."
    wget -q -O "$BINARY" "${BINARY_URL}/larkwrt-agent-${SUFFIX}"
    chmod 755 "$BINARY"
    echo "[+] Binary installed to $BINARY"
else
    if [ ! -x "$BINARY" ]; then
        echo "[!] BINARY_URL is not set and $BINARY does not exist."
        echo "    Copy dist/larkwrt-agent-${SUFFIX} to $BINARY and re-run."
        exit 1
    fi
    echo "[!] BINARY_URL not set; using existing binary at $BINARY"
fi

# ── Write config ──────────────────────────────────────────────────────────────
mkdir -p "$CONFIG_DIR"

if [ -f "$CONFIG_DIR/config.toml" ]; then
    echo "[!] Config already exists at $CONFIG_DIR/config.toml — skipping"
else
    cat > "$CONFIG_DIR/config.toml" << EOF
[feishu]
app_id     = "${APP_ID}"
app_secret = "${APP_SECRET}"
chat_id    = "${CHAT_ID}"
admin_users = [${ADMIN_USERS}]

[router]
name      = "${ROUTER_NAME}"
lan_iface = "${LAN_IFACE}"

[monitor]
collect_interval_fast = "5s"
collect_interval_slow = "30s"

[alert]
cpu_threshold_pct    = 85
cpu_duration_secs    = 60
memory_threshold_pct = 90
cooldown_secs        = 300

[security]
cmd_rate_limit = 20
exec_whitelist = ["ping", "ping6", "traceroute", "nslookup", "logread", "cat"]
EOF
    chmod 600 "$CONFIG_DIR/config.toml"
    echo "[+] Config written to $CONFIG_DIR/config.toml"
fi

# ── Install init script ───────────────────────────────────────────────────────
cat > /etc/init.d/larkwrt << 'INITEOF'
#!/bin/sh /etc/rc.common
START=99
STOP=10
USE_PROCD=1
PROG=/usr/bin/larkwrt-agent
CONFIG=/etc/larkwrt/config.toml
start_service() {
    [ -f "$PROG" ]   || { logger -t larkwrt "binary not found: $PROG"; return 1; }
    [ -f "$CONFIG" ] || { logger -t larkwrt "config not found: $CONFIG"; return 1; }
    procd_open_instance
    procd_set_param command "$PROG" -config "$CONFIG"
    procd_set_param respawn 30 5 5
    procd_set_param stdout 1
    procd_set_param stderr 1
    procd_set_param pidfile /var/run/larkwrt.pid
    procd_close_instance
}
stop_service() {
    rm -f /var/run/larkwrt.pid
}
reload_service() { stop; start; }
INITEOF

chmod 755 /etc/init.d/larkwrt
/etc/init.d/larkwrt enable
/etc/init.d/larkwrt start

# ── Verify startup ────────────────────────────────────────────────────────────
echo "[+] Waiting for agent to start..."
sleep 3
if logread 2>/dev/null | grep -q "larkwrt-agent starting"; then
    echo "[+] Agent started successfully."
else
    echo "[!] Agent may not have started — check logs with: logread -e larkwrt"
fi

echo "[+] Done. To monitor: logread -f -e larkwrt"
