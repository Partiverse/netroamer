#!/bin/zsh
# netroamer 看门狗（macOS/Linux 通用）
# 每 5 分钟由 LaunchAgent(Linux: cron) 调用：
#   - Clash/mihomo 核心死亡 → 自动拉起（应用死了不代拉，尊重手动退出）
#   - 国外代理链路异常 → 通知（Apple 测试页为探针，Cloudflare 易被出口节点误报）
#   - 国内直连异常 → 高优先级通知
# 同类告警 90 分钟冷却；touch /tmp/net-watchdog.off 或 export NET_WATCHDOG_OFF=1 停用
[ -f /tmp/net-watchdog.off ] && exit 0
[ -n "$NET_WATCHDOG_OFF" ] && exit 0

NOTIFY_LOG="${NET_WATCHDOG_LOG:-/tmp/net-watchdog.log}"
COOLDOWN="${NET_WATCHDOG_COOLDOWN:-5400}"   # 90 分钟

notify() {
  if command -v osascript >/dev/null 2>&1; then
    osascript -e "display notification \"$1\" with title \"网络监控\" sound name \"Ping\"" 2>/dev/null
  elif command -v notify-send >/dev/null 2>&1; then
    notify-send "网络监控" "$1" 2>/dev/null
  fi
  echo "[$(date '+%F %T')] $1" >> "$NOTIFY_LOG"
}
alert_once() {  # $1=类型 $2=消息
  local stamp="/tmp/net-watchdog.$1"
  local now=$(date +%s)
  if [ ! -f "$stamp" ] || [ $(( now - $(cat "$stamp" 2>/dev/null || echo 0) )) -gt "$COOLDOWN" ]; then
    echo "$now" > "$stamp"
    notify "$2"
  fi
}

# 核心进程名：mihomo 兼容内核（verge-mihomo / mihomo / clash）
CORE_RUNNING=$(pgrep -f '(verge-)?(mihomo|clash)' 2>/dev/null | head -1)

# 场景1：核心死亡
if [ -z "$CORE_RUNNING" ]; then
  if command -v osascript >/dev/null 2>&1 && pgrep -f "Clash Verge.app" >/dev/null 2>&1; then
    alert_once "coredead" "Clash 应用在运行但核心已死，正在自动重启..."
    open -a "Clash Verge" 2>/dev/null
    sleep 25
    if pgrep -f '(verge-)?(mihomo|clash)' >/dev/null 2>&1; then
      notify "Clash 核心已自动恢复 ✅"
    else
      alert_once "recoverfail" "Clash 核心重启失败，请手动打开 Clash Verge"
    fi
  else
    alert_once "coredown" "Clash/mihomo 核心未运行。国内直连不受影响。"
  fi
  exit 0
fi

# 场景2：核心活着，检查两条链路（端口可由 NET_WATCHDOG_PORT 覆盖，默认 7897）
PORT="${NET_WATCHDOG_PORT:-7897}"
# 国外代理：Apple 官方测试页（任何出口节点均可达）
FOREIGN=$(curl -s -x "http://127.0.0.1:$PORT" -o /dev/null -w "%{http_code}" --max-time 10 \
  "https://www.apple.com/library/test/success.html" 2>/dev/null)
# 国内直连：先探默认网关 443，失败再兜底 baidu.com
DOMESTIC_OK=false
if [ "$(uname)" = "Darwin" ]; then
  GATEWAY=$(route -n get default 2>/dev/null | awk '/gateway/{print $2}')
else
  GATEWAY=$(ip route 2>/dev/null | awk '/^default/{print $3; exit}')
fi
if [ -n "$GATEWAY" ] && nc -z -w 3 "$GATEWAY" 443 2>/dev/null; then
  DOMESTIC_OK=true
else
  DOMESTIC=$(curl -s --noproxy '*' -o /dev/null -w "%{http_code}" --max-time 8 "https://www.baidu.com" 2>/dev/null)
  [[ "$DOMESTIC" == 2* || "$DOMESTIC" == 3* ]] && DOMESTIC_OK=true
fi

if [ "$DOMESTIC_OK" = false ]; then
  alert_once "domestic" "严重：国内直连异常，请检查 Wi-Fi/路由器"
elif [ "$FOREIGN" != "200" ]; then
  alert_once "foreign" "代理出口异常（apple.com HTTP=${FOREIGN}），国内流量不受影响；如需外网请换节点"
fi
