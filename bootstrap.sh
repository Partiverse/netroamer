#!/usr/bin/env bash
# netroamer —— macOS / Linux 中国网络开发环境一键配置
# 原则见 README.md：TUN 永不开启、镜像优先、ZCode 三层直连、DNS 国内化、监控自愈
# 幂等：可重复执行；被改文件均带时间戳备份
# 用法：bash bootstrap.sh [--diagnose] [--no-dns] [--no-watchdog] [--dns] [--ci]
set -euo pipefail

NO_DNS=false; NO_WATCHDOG=false; FORCE_DNS=false; CI=false; DIAGNOSE=false
for arg in "$@"; do
  case "$arg" in
    --diagnose) DIAGNOSE=true ;;
    --no-dns) NO_DNS=true ;;
    --no-watchdog) NO_WATCHDOG=true ;;
    --dns) FORCE_DNS=true ;;          # Linux 上显式要求写 DNS（需 sudo）
    --ci) CI=true; NO_DNS=true; NO_WATCHDOG=true ;;   # CI/测试环境：只配 shell 块与镜像
    *) echo "未知参数: $arg"; exit 1 ;;
  esac
done

# ============================================================
# 交互式诊断模式（诊断段禁用 set -eu，避免 bash 3.2 误退出）
# ============================================================
if [ "$DIAGNOSE" = true ]; then
  # bash 3.2: set -u 对未定义变量报 unbound variable；
  # 大量 curl/nc 命令会返回非零，set -e 频繁触发退出。
  # 在整个 DIAGNOSE 段统一禁用严格模式，结束后恢复。
  set +eu

  # 显式定义诊断段依赖的变量（原写在脚本后部，bash 不报错但显式更安全）
  OS="$(uname)"
  PROXY_PORT="${PROXY_PORT:-7897}"
  CVR="$HOME/Library/Application Support/io.github.clash-verge-rev.clash-verge-rev"
  # set -u 时HOME/SHLVL/PROMPT_COMMAND 等内置变量始终存在，这里用 || true 双重保险
  HOME="${HOME:-/Users/$(whoami)}"
  SHELL="${SHELL:-/bin/zsh}"
  # set -u 时数组索引展开不会触发报错，但变量展开会；
  # 因此所有可能失败的 curl/nc 用 || true 垫底。
  # ---- 彩色输出（TERM 不是 dumb 时才启用，避免 CI 日志乱码）----
  if command -v tput >/dev/null 2>&1 && [ -n "${TERM:-}" ] && [ "${TERM:-dumb}" != "dumb" ] && [ -z "${CI:-}" ]; then
    R=$(tput setaf 1 2>/dev/null)   # 红色
    G=$(tput setaf 2 2>/dev/null)   # 绿色
    Y=$(tput setaf 3 2>/dev/null)   # 黄色
    B=$(tput setaf 4 2>/dev/null)   # 蓝色
    C=$(tput setaf 6 2>/dev/null)   # 青色
    M=$(tput setaf 5 2>/dev/null)   # 紫色
    D=$(tput sgr0 2>/dev/null)      # 默认
    BOLD=$(tput bold 2>/dev/null)
    DIM=$(tput dim 2>/dev/null)
    BAR_BG=$(tput setaf 8 2>/dev/null)  # 灰色（进度条背景）
  else
    R="[ERR]"; G="[OK]"; Y="[WARN]"; B="[INFO]"; C="[STEP]"; M=""; D=""; BOLD=""; DIM=""; BAR_BG=""
  fi
  PASS=0; FAIL=0; WARN=0; FIX_COUNT=0
  TOTAL_STAGES=9
  CURRENT_STAGE=0

  # ---- 进度条 ----
  draw_bar() {
    # $1=当前 $2=总数 $3=宽度
    local cur=$1 total=$2 width=${3:-28}
    local filled=$((cur * width / total))
    local empty=$((width - filled))
    printf "%s[%s%s%s] %d/%d%s" \
      "$DIM" "$G" "$(printf '#%.0s' $(seq 1 $filled 2>/dev/null) 2>/dev/null || echo "")" \
      "$BAR_BG" "$(printf '.%.0s' $(seq 1 $empty 2>/dev/null) 2>/dev/null || echo "")" \
      "$D" "$cur" "$total"
  }

  # ---- 旋转等待动画 ----
  spin_pid=""
  spin_char() { printf "%s" "$DIM"/; }
  spin() {
    # $1=PID $2=消息
    local pid=$1 msg=$2
    local chars="⠋⠙⠹⠸⠼⠴⠦⠧⠇�"
    local i=0
    while kill -0 "$pid" 2>/dev/null; do
      local ch="${chars:$((i % ${#chars})):1}"
      printf "\r  ${C}%s${D} %s %s  " "$ch" "$msg" "$(spin_char)"
      i=$((i+1))
      sleep 0.12
    done
    printf "\r  ${G}✓${D} %-40s\n" "$msg"
  }

  info()  { echo "  ${C}▸ $1${D}"; }
  ok()    { echo "  ${G}✔ $1${D}"; ((PASS++)) || true; }
  fail()  { echo "  ${R}✖ $1${D}"; ((FAIL++)) || true; }
  warn()  { echo "  ${Y}⚠ $1${D}"; ((WARN++)) || true; }
  rule()  {
    CURRENT_STAGE=$((CURRENT_STAGE + 1))
    local stage=$CURRENT_STAGE
    local title="$1"
    local bar=$(draw_bar $stage $TOTAL_STAGES)
    echo ""
    echo "${BOLD}${C}┌──────────────────────────────────────────────┐${D}"
    printf "  ${BOLD}${C}│ %-44s │${D}\n" "$(draw_bar $stage $TOTAL_STAGES) ${BOLD}$title${D}"
    echo "${BOLD}${C}└──────────────────────────────────────────────┘${D}"
  }

  # ---- 交互确认（CI 模式下自动跳过）----
  ask_fix() {
    # $1=检查名 $2=修复命令 $3=说明
    local check_name="$1" fix_cmd="$2" explanation="$3"
    echo ""
    echo "  ${BAR_BG}┄${D} ${Y}⚡ 发现问题${D} ${BAR_BG}┄${D}"
    echo ""
    echo "  ${BOLD}${R}✖ $check_name${D}"
    [ -n "$explanation" ] && echo "  ${DIM}说明：$explanation${D}"
    echo "  ${DIM}命令：$fix_cmd${D}"
    echo ""
    if [ "${CI:-false}" = "true" ]; then
      echo "  ${M}⬡ CI 模式：自动跳过${D}"
      return 1
    fi
    echo -n "  ${BOLD}${G}▶ 是否自动修复？${D} [${G}Y${D}/enter=修复 ${R}n${D}=跳过] "
    local answer=""
    read -r answer 2>/dev/null || true
    case "$answer" in
      n|n|N)  echo "  ${DIM}已跳过${D}"; return 1 ;;
      *)       echo "  ${G}▶ 执行修复...${D}"; return 0 ;;
    esac
  }

  # ---- 工具函数 ----
  cmd_ok() { "$@" >/dev/null 2>&1; return 0; }
  cmd_out() { "$@" 2>/dev/null; }
  port_open() {
    (exec 3<>/dev/tcp/127.0.0.1/$1) 2>/dev/null && { exec 3>&- 3<&-; return 0; }
    nc -z -w 2 127.0.0.1 $1 2>/dev/null
  }
  http_code() {
    local code
    code=$(curl -s --noproxy '*' -o /dev/null -w "%{http_code}" --max-time "${2:-8}" "$1" 2>/dev/null) || true
    echo "${code:-000}"
  }
  proxy_http_code() {
    local code
    code=$(curl -s -x "http://127.0.0.1:${PROXY_PORT:-7897}" -o /dev/null -w "%{http_code}" --max-time "${2:-10}" "$1" 2>/dev/null) || true
    echo "${code:-000}"
  }
  resolve_time() {
    local ms
    ms=$(curl -s -o /dev/null -w "%{time_namelookup}" --max-time "${2:-5}" "$1" 2>/dev/null) || true
    echo "${ms:-0}"
  }
  # 动态获取 macOS 活跃网络服务名（CI runner 环境下可能不是 "Wi-Fi"）
  get_network_service() {
    if [ "$(uname)" != "Darwin" ]; then return 1; fi
    local svc
    svc=$(networksetup -listallnetworkservices 2>/dev/null | grep -v '*' | grep -v '^$' | \
          while read -r line; do
            [ -z "$line" ] && continue
            local dev
            dev=$(networksetup -listpreferwirelessnetworks 2>/dev/null | grep -v '^$' | head -1 || true)
            echo "$line"
          done | head -1) || true
    # 降级：取第一个网络服务
    [ -z "$svc" ] && svc=$(networksetup -listallnetworkservices 2>/dev/null | tail -n +2 | grep -v '*' | head -1 || true)
    echo "${svc:-Wi-Fi}"
  }

  PROXY_PORT="${PROXY_PORT:-7897}"
  CVR="$HOME/Library/Application Support/io.github.clash-verge-rev.clash-verge-rev"

  START_TIME=${START_TIME:-$(date +%s)}
  echo ""
  rule "netroamer 交互式网络诊断"
  echo "  系统: $(uname)  |  Shell: ${SHELL##*/}  |  代理端口: $PROXY_PORT"
  echo "  ${DIM}开始时间: $(date '+%H:%M:%S')${D}"
  echo ""

  # ============================================================
  # 阶段一：核心进程与端口
  # ============================================================
  rule "阶段一：核心进程与端口"

  info "检查代理端口 (:$PROXY_PORT)..."
  if port_open "$PROXY_PORT"; then
    ok "端口 $PROXY_PORT 已在监听"
  else
    fail "端口 $PROXY_PORT 未监听——Clash Verge / mihomo 是否在运行？"
    echo "  建议：打开 Clash Verge，或在 Linux 上手动启动 mihomo"
  fi

  info "检查核心进程..."
  CORE_PROC=$(pgrep -fl 'verge-mihomo|mihomo' 2>/dev/null | grep -v pgrep | head -1 || true)
  if [ -n "$CORE_PROC" ]; then
    ok "核心进程运行中"
    echo "  PID: $(echo "$CORE_PROC" | awk '{print $1}')"
  else
    fail "未检测到 verge-mihomo / mihomo 进程"
    if cmd_ok grep -q "Clash" /Applications 2>/dev/null; then
      echo "  Clash Verge 已安装但未运行，尝试启动："
      echo "    open -a 'Clash Verge'"
    fi
  fi

  # ============================================================
  # 阶段二：系统代理状态
  # ============================================================
  rule "阶段二：系统代理（PAC）"

  if [ "$(uname)" = "Darwin" ]; then
    NET_SVC=$(get_network_service)
    info "检查 macOS 系统 Web/HTTPS 代理 (服务: $NET_SVC)..."
    WEB_PROXY=$(networksetup -getwebproxy "$NET_SVC" 2>/dev/null | awk '/^Enabled:/{print $2}' || echo "")
    HTTPS_PROXY=$(networksetup -getproxyhttps "$NET_SVC" 2>/dev/null | awk '/^Enabled:/{print $2}' || echo "")
    if [ "$WEB_PROXY" = "Yes" ] && [ "$HTTPS_PROXY" = "Yes" ]; then
      ok "Web + HTTPS 系统代理已开启"
    elif [ "$WEB_PROXY" = "Yes" ]; then
      warn "仅 Web 代理开启，HTTPS 代理未开"
      if ask_fix "HTTPS 代理未开启" \
        "networksetup -setproxyhttps \"$NET_SVC\" 127.0.0.1 $PROXY_PORT" \
        "HTTPS 代理未开，浏览器访问 HTTPS 站可能不走代理"; then
        networksetup -setproxyhttps "$NET_SVC" "127.0.0.1" "$PROXY_PORT" off \
          && ok "HTTPS 代理已开启" || fail "HTTPS 代理开启失败"
      fi
    else
      fail "系统代理未开启——浏览器流量直连，不受 Clash 规则保护"
      echo "  后果：PAC 规则不生效，国内外流量全部直连"
      if ask_fix "系统代理未开启" \
        "networksetup -setwebproxy \"$NET_SVC\" 127.0.0.1 $PROXY_PORT && networksetup -setproxyhttps \"$NET_SVC\" 127.0.0.1 $PROXY_PORT" \
        "开启系统代理使浏览器流量经 Clash 分流"; then
        networksetup -setwebproxy "$NET_SVC" "127.0.0.1" "$PROXY_PORT" off \
          && networksetup -setproxyhttps "$NET_SVC" "127.0.0.1" "$PROXY_PORT" off \
          && ok "系统代理已开启" || fail "系统代理开启失败"
      fi
    fi

    info "检查 PAC bypass 绕过域名..."
    BYPASS=$(networksetup -getproxybypassdomains "$NET_SVC" 2>/dev/null | tr '\n' ' ')
    echo "  当前绕过: ${BYPASS:-（空）}"
    # PAC bypass 和 NO_PROXY 是不同层级，两者都存在是正常的

  else  # Linux
    info "检查 shell 代理环境变量..."
    if [ -n "$HTTP_PROXY" ] || [ -n "$http_proxy" ]; then
      PROXY_CHAIN="${HTTP_PROXY:-$http_proxy}"
      ok "HTTP_PROXY 已设置: $PROXY_CHAIN"
      if [ -n "$NO_PROXY" ]; then
        echo "  NO_PROXY 包含 ${#NO_PROXY} 个域名/网段"
      fi
    else
      warn "当前 shell 未检测到 HTTP_PROXY（需先 source rc 文件后生效）"
      echo "  如果是在新终端中运行，这是正常的——自动探测块在 rc 文件中"
      echo "  诊断命令：bash netroamer/bootstrap.sh（会重新写入 rc 文件）"
    fi
  fi

  # ============================================================
  # 阶段三：TUN 模式深度检测
  # ============================================================
  rule "阶段三：TUN 模式"

  info "检查 Clash Verge TUN 配置..."
  if [ -f "$CVR/verge.yaml" ]; then
    TUN_LINE=$(grep -n 'enable_tun_mode' "$CVR/verge.yaml" 2>/dev/null | grep -v '^#' | head -1 || true)
    TUN_STACK=$(grep -n '^\s\+stack:' "$CVR/verge.yaml" 2>/dev/null | head -1 || true)
    DNS_HIJACK=$(grep -n 'dns-hijack\|dns_hijack' "$CVR/verge.yaml" 2>/dev/null | grep -v '^#' | head -3 || true)

    if echo "$TUN_LINE" | grep -q 'true'; then
      fail "TUN 模式已开启！"
      echo "  文件: $CVR/verge.yaml"
      echo "  行: ${TUN_LINE}"
      echo ""
      echo "  ${R}这是 ZCode / AI 工具断网的根本原因！${D}"
      echo "  TUN 劫持全系统路由 + fake-ip DNS，把本应直连的国内流量"
      echo "  （bigmodel.cn / vectide.cn 等）全部送入代理隧道，"
      echo "  导致 AI 开发工具连接超时。"
      echo ""
      if ask_fix "TUN 模式已开启" \
        "sed -i '' 's/enable_tun_mode: true/enable_tun_mode: false/' $CVR/verge.yaml" \
        "关闭 TUN 后需重启 Clash Verge 生效"; then
        sed -i '' 's/enable_tun_mode: true/enable_tun_mode: false/' "$CVR/verge.yaml" 2>/dev/null \
          && ok "已关闭 TUN（enable_tun_mode: false），请重启 Clash Verge" \
          || fail "修改 verge.yaml 失败（权限？）"
      fi
    else
      ok "TUN 模式未开启（正常）"
      [ -n "$TUN_STACK" ] && echo "  当前 stack: $(echo "$TUN_STACK" | awk '{print $2}')"
    fi

    [ -n "$DNS_HIJACK" ] && echo "  DNS 劫持配置: $(echo "$DNS_HIJACK" | head -1 | cut -d: -f2- | tr -d ' ')"
  else
    warn "未找到 Clash Verge 配置文件（Clash Verge Rev 是否已安装？）"
  fi

  # ============================================================
  # 阶段四：DNS 解析链深度分析
  # ============================================================
  rule "阶段四：DNS 解析链"

  if [ "$(uname)" = "Darwin" ]; then
    NET_SVC=$(get_network_service)
    DNS_RAW=$(networksetup -getdnsservers "$NET_SVC" 2>/dev/null || true)
    # CI 环境 networksetup 可能因权限报错，跳过 DNS 检查
    if echo "$DNS_RAW" | grep -qi "not a recognized\|error"; then
      warn "无法读取 DNS 配置（权限或 CI 环境）"
    else
      DNS_SERVERS=$(echo "$DNS_RAW" | grep -v "There aren't" | grep -v "^$" | head -5)
      info "当前 DNS 服务器:"
      echo "$DNS_SERVERS" | while read -r dns; do
        [ -n "$dns" ] && echo "    $dns"
      done
      DOMESTIC_DNS=false
      if echo "$DNS_SERVERS" | grep -q "223.5.5.5\|119.29.29.29\|100.100.100.100"; then
        ok "DNS 已国内化，解析速度快且不被污染"
        DOMESTIC_DNS=true
      else
        fail "DNS 未国内化！"
        echo "  当前 DNS: $DNS_SERVERS"
        echo "  后果：国内域名解析慢或被污染，ZCode 等工具可能解析失败"
        if ask_fix "DNS 未国内化" \
          "networksetup -setdnsservers \"$NET_SVC\" 223.5.5.5 119.29.29.29" \
          "改为阿里 223.5.5.5 + DNSPod 119.29.29.29，解析快且不污染"; then
          networksetup -setdnsservers "$NET_SVC" 223.5.5.5 119.29.29.29 \
            && ok "DNS 已改为 223.5.5.5 / 119.29.29.29" \
            || fail "DNS 修改失败（检查网络接口名是否正确）"
        fi
      fi
    fi
  fi

  info "DNS 解析时间测试（国内域名，不走代理）..."
  DOMAIN_TEST="open.bigmodel.cn"
  RESOLVE_MS=$(resolve_time "https://$DOMAIN_TEST" 5)
  if [ -n "$RESOLVE_MS" ]; then
    MS_INT=$(printf "%.0f" "$RESOLVE_MS" 2>/dev/null || echo "0")
    if [ "$MS_INT" -lt 50 ]; then
      ok "DNS 解析时间 ${RESOLVE_MS}s（优秀）"
    elif [ "$MS_INT" -lt 200 ]; then
      warn "DNS 解析时间 ${RESOLVE_MS}s（一般，建议检查 DNS 配置）"
    else
      fail "DNS 解析时间 ${RESOLVE_MS}s（过慢，可能 DNS 污染或服务器慢）"
    fi
  else
    warn "DNS 解析测试超时（网络可能已断开）"
  fi

  # ============================================================
  # 阶段五：三层直连实际连通性验证
  # ============================================================
  rule "阶段五：三层直连保护——实际连通性验证"

  info "第1层：PAC 直连——curl 直连测试（不走代理）"
  ZCODE_DOMAINS=(
    "open.bigmodel.cn"
    "bigmodel.cn"
    "vectide.cn"
    "www.zhipuai.cn"
    "hf-mirror.com"
  )
  LAYER1_FAIL=0
  for dom in "${ZCODE_DOMAINS[@]}"; do
    CODE=$(http_code "https://$dom" 8)
    if [[ "$CODE" == 2* ]] || [[ "$CODE" == 3* ]]; then
      ok "$dom → HTTP $CODE（直连正常）"
    else
      fail "$dom → HTTP ${CODE:-超时}（直连失败！）"
      ((LAYER1_FAIL++))
    fi
  done
  if [ $LAYER1_FAIL -gt 0 ]; then
    echo ""
    warn "第1层（PAC 直连）有 $LAYER1_FAIL 个域名直连失败"
    echo "  排查步骤："
    echo "    1. 检查 DNS 是否被污染：bash bootstrap.sh --diagnose（阶段四）"
    echo "    2. 检查 TUN 是否开启（阶段三）"
    echo "    3. 检查路由器/网关是否正常"
  else
    ok "第1层：全部 ZCode 域名直连正常"
  fi

  info "第2层：Clash 规则——通过代理访问 ZCode（验证代理链完整）"
  LAYER2_FAIL=0
  for dom in "${ZCODE_DOMAINS[@]}"; do
    CODE=$(proxy_http_code "https://$dom" 10)
    if [[ "$CODE" == 2* ]] || [[ "$CODE" == 3* ]]; then
      ok "$dom → HTTP $CODE（经代理可达）"
    else
      warn "$dom → HTTP ${CODE:-超时}（代理链失败，但不影响直连）"
      ((LAYER2_FAIL++))
    fi
  done
  [ $LAYER2_FAIL -eq 0 ] && ok "第2层：所有域名经代理也可达"

  info "第3层：NO_PROXY 环境变量"
  NO_PROXY_VAL=""
  if [ -n "${NO_PROXY:-}" ]; then
    NO_PROXY_VAL="$NO_PROXY"
  elif [ -f ~/.zshrc ]; then
    NO_PROXY_VAL=$(grep -m1 'NO_PROXY=' ~/.zshrc 2>/dev/null \
      | sed 's/.*NO_PROXY=//; s/"//g' | awk '{print $1}')
  fi
  ZCODE_MISSING=""
  for dom in bigmodel.cn vectide.cn zhipuai.cn "z.ai" hf-mirror.com; do
    if [ -n "$NO_PROXY_VAL" ] && echo "$NO_PROXY_VAL" | grep -q "$dom"; then
      ok "NO_PROXY 包含 $dom"
    else
      ZCODE_MISSING="$ZCODE_MISSING $dom"
    fi
  done
  if [ -n "$ZCODE_MISSING" ]; then
    warn "NO_PROXY 缺少以下域名：$ZCODE_MISSING"
    echo "  这些域名可能经代理绕行（不影响直连，但不如直连快）"
    if ask_fix "NO_PROXY 域名缺失" \
      "重新运行 bash bootstrap.sh（会重建带完整域名的 rc 文件）" \
      "bootstrap.sh 会写入包含全部 ZCode 域名的 NO_PROXY"; then
      bash "$0" --no-dns --no-watchdog >/dev/null 2>&1 \
        && ok "rc 文件已更新（需新终端生效）" \
        || fail "bootstrap.sh 执行失败"
    fi
  else
    ok "第3层：NO_PROXY 包含全部关键域名"
  fi

  # ============================================================
  # 阶段六：代理出口与国际连通性
  # ============================================================
  rule "阶段六：代理出口与国际连通性"

  info "测试代理出口（Apple 测试页）..."
  FOREIGN_CODE=$(proxy_http_code "https://www.apple.com/library/test/success.html" 10)
  if [ "${FOREIGN_CODE:-000}" = "200" ]; then
    ok "代理出口正常（apple.com HTTP $FOREIGN_CODE）"
  else
    warn "代理出口异常（apple.com HTTP ${FOREIGN_CODE:-未响应}）"
    echo "  可能原因：代理节点故障 / 订阅过期 / 节点被墙"
    echo "  建议：到 Clash Verge 手动切换节点"
  fi

  info "测速基准（延迟）"
  SPEEDS=(
    "国内:https://open.bigmodel.cn"
    "AI服务:https://api.zhipuai.cn"
    "国际:https://www.google.com"
  )
  for entry in "${SPEEDS[@]}"; do
    IFS=':' read -r label url <<< "$entry"
    LATENCY=$(curl -s -o /dev/null -w "%{time_total}" --max-time 10 "$url" 2>/dev/null || echo "超时")
    echo "    $label: ${LATENCY}s"
  done

  # ============================================================
  # 阶段七：路由表与接口分析
  # ============================================================
  rule "阶段七：路由表与网络接口"

  info "默认路由"
  if [ "$(uname)" = "Darwin" ]; then
    GATEWAY=$(netstat -nr 2>/dev/null | awk '/^default/ {print $2}' | head -1)
    echo "    网关: ${GATEWAY:-未知}"
    GATEWAY_LATENCY=$(nc -z -w 2 "$GATEWAY" 443 2>/dev/null && echo "可达" || echo "不可达")
    echo "    网关连通: $GATEWAY_LATENCY"
  else
    ip route 2>/dev/null | awk '/^default/{print "    "$0}' || true
  fi

  info "虚拟网卡（TUN/TAP/utun）"
  if [ "$(uname)" = "Darwin" ]; then
    IFACES=$(ifconfig 2>/dev/null | awk '/^utun[0-9]/ {print $1}' | tr '\n' ' ')
    if [ -n "$IFACES" ]; then
      echo "    发现 utun 设备: $IFACES"
      echo "    （可能为 Tailscale 或其他 VPN 软件）"
    else
      echo "    未发现 utun 设备（TUN 模式未激活，正常）"
    fi
  else
    ip link 2>/dev/null | awk '/^[0-9]+: (tun|tap)/ {print "    "$2}' | sed 's/:$//' || true
  fi

  # ============================================================
  # 阶段八：包管理器镜像
  # ============================================================
  rule "阶段八：包管理器镜像"

  check_mirror() {
    # $1=工具名 $2=检测命令 $3=期望值 $4=正确时的说明
    local val; val=$($2 2>/dev/null)
    if echo "$val" | grep -q "$3"; then
      ok "$1 → $val（$4）"
    else
      warn "$1 → $val（不是最优，当前速度可能很慢）"
      echo "    期望包含: $3"
    fi
  }

  if command -v pip3 >/dev/null 2>&1 || command -v pip >/dev/null 2>&1; then
    PIP_REG=$(pip config get global.index-url 2>/dev/null || pip3 config get global.index-url 2>/dev/null || true)
    PIP_REG=${PIP_REG:-$(grep -m1 'index-url' "$HOME/.config/pip/pip.conf" 2>/dev/null | sed 's/.*=//' | tr -d ' ' || true)}
    if echo "$PIP_REG" | grep -q "tuna\|tsinghua\|npmmirror\|aliyun"; then
      ok "pip → $PIP_REG"
    else
      fail "pip → ${PIP_REG:-（未配置）}"
      if ask_fix "pip 未配置国内镜像" \
        "pip config set global.index-url https://pypi.tuna.tsinghua.edu.cn/simple" \
        "当前源在国外，pip install 可能极慢（10-20 KB/s）"; then
        pip config set global.index-url https://pypi.tuna.tsinghua.edu.cn/simple 2>/dev/null \
          && ok "pip → 清华 TUNA" \
          || fail "pip 镜像配置失败"
      fi
    fi
  fi

  if command -v npm >/dev/null 2>&1; then
    NPM_REG=$(npm config get registry 2>/dev/null)
    if echo "$NPM_REG" | grep -q "npmmirror\|cnpm\|taobao"; then
      ok "npm → $NPM_REG"
    else
      fail "npm → ${NPM_REG:-（未配置）}"
      if ask_fix "npm 未配置国内镜像" \
        "npm config set registry https://registry.npmmirror.com" \
        "当前源在国外，npm install 可能极慢"; then
        npm config set registry https://registry.npmmirror.com \
          && ok "npm → npmmirror" || fail "npm 镜像配置失败"
      fi
    fi
  fi

  if command -v cargo >/dev/null 2>&1 || [ -d "$HOME/.cargo" ]; then
    if [ -f "$HOME/.cargo/config.toml" ] && grep -q "rsproxy\|ustc\|tuna" "$HOME/.cargo/config.toml" 2>/dev/null; then
      ok "cargo → $(grep 'registry' "$HOME/.cargo/config.toml" 2>/dev/null | head -1 | sed 's/.*=//;s/ //g')"
    else
      fail "cargo → $(grep 'registry' "$HOME/.cargo/config.toml" 2>/dev/null | head -1 | sed 's/.*=//' || echo '（未配置）')"
      if ask_fix "cargo 未配置国内镜像" \
        "mkdir -p \$HOME/.cargo && cat > \$HOME/.cargo/config.toml <<'TOML'
[source.crates-io]
replace-with = 'rsproxy-sparse'
[source.rsproxy-sparse]
registry = 'sparse+https://rsproxy.cn/index/'
[net]
git-fetch-with-cli = true
TOML" \
        "当前源在国外，cargo build 可能极慢"; then
        mkdir -p "$HOME/.cargo"
        cat > "$HOME/.cargo/config.toml" <<'TOML'
[source.crates-io]
replace-with = "rsproxy-sparse"
[source.rsproxy-sparse]
registry = "sparse+https://rsproxy.cn/index/"
[net]
git-fetch-with-cli = true
TOML
        ok "cargo → rsproxy-sparse" || fail "cargo 镜像配置失败"
      fi
    fi
  fi

  # ============================================================
  # 阶段九：DNS 泄露测试
  # ============================================================
  rule "阶段九：DNS 泄露检测"

  info "检测 DNS 泄露（查询 icanhazip.com 的实际出口 IP）..."
  # 这个测试原理：在代理环境下查询 IP，Clash DNS 泄露会显示本机真实 IP 而非代理出口 IP
  PROXY_IP=$(curl -s -x "http://127.0.0.1:$PROXY_PORT" --max-time 10 \
    "https://api.ipify.org?format=text" 2>/dev/null || true)
  DIRECT_IP=$(curl -s --noproxy '*' --max-time 10 \
    "https://api.ipify.org?format=text" 2>/dev/null || true)
  if [ -n "$PROXY_IP" ]; then
    ok "代理出口 IP: $PROXY_IP"
    [ -n "$DIRECT_IP" ] && echo "    直连 IP: $DIRECT_IP"
    if [ "$PROXY_IP" = "$DIRECT_IP" ]; then
      warn "代理 IP 与直连 IP 相同——代理可能未生效，或出口节点与本机同 IP 段"
    fi
  else
    warn "无法获取代理出口 IP（节点可能故障）"
  fi

  # ============================================================
  # 汇总报告
  # ============================================================
  ELAPSED=$(( $(date +%s) - START_TIME ))
  echo ""
  echo "${BOLD}${C}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${D}"
  echo "  ${BOLD}诊 断 汇 总${D}    ${dim}耗时: ${ELAPSED:-未知}s${D}"
  echo "${BOLD}${C}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${D}"
  echo ""

  # 分数条
  local total=$((PASS + FAIL + WARN))
  local ok_w=$((PASS * 28 / (total > 0 ? total : 1)))
  local fail_w=$((FAIL * 28 / (total > 0 ? total : 1)))
  local warn_w=$((WARN * 28 / (total > 0 ? total : 1)))
  local bar=$(printf "%${ok_w}s" | tr ' ' '█')
  local warn_bar=$(printf "%${warn_w}s" | tr ' ' '▄')
  local fail_bar=$(printf "%${fail_w}s" | tr ' ' '▪')

  echo "  ${G}█${D} ${G}通过  $PASS${D}    ${Y}▄${D} ${Y}警告  $WARN${D}    ${R}▪${D} ${R}失败  $FAIL${D}"
  echo "  ${BAR_BG}┄${D}${G}${bar}${Y}${warn_bar}${R}${fail_bar}${D}${BAR_BG}                                                              ${D}"
  echo ""

  # 网络健康评分
  local denom=$(( total > 0 ? total : 1 ))
  local score=$(( (PASS * 100) / denom ))
  if   [ "$score" -ge 90 ]; then
    echo "  ${G}▶ 网络健康评分：$score/100${D}  ${G}优秀${D}  — 运行流畅，无需操作"
  elif [ "$score" -ge 70 ]; then
    echo "  ${Y}▶ 网络健康评分：$score/100${D}  ${Y}良好${D}  — 有 ${WARN} 项可优化，运行无碍"
  elif [ "$score" -ge 40 ]; then
    echo "  ${Y}▶ 网络健康评分：$score/100${D}  ${R}一般${D}  — 有 ${FAIL} 项需修复，可能影响 AI 工具"
  else
    echo "  ${R}▶ 网络健康评分：$score/100${D}  ${R}异常${D}  — 建议运行 ${BOLD}bash bootstrap.sh${D} 全面修复"
  fi
  echo ""

  if [ "$FAIL" -gt 0 ]; then
    echo "  ${R}✖ 有 $FAIL 项检查失败：${D}"
    echo ""
    echo "  ${R}1.${D} ${R}阶段三（TUN 模式）${D}  — TUN 开启会导致 ZCode 等国内域名断连"
    echo "      修复：关掉 Clash Verge 设置里的 TUN，重启 Clash"
    echo ""
    echo "  ${R}2.${D} ${R}阶段二（系统代理）${D}  — 浏览器流量直连，不受 Clash 规则保护"
    echo "      修复：系统偏好设置 → 网络 → 高级 → 代理 → 开启 Web/HTTPS 代理"
    echo ""
    echo "  ${R}3.${D} ${R}阶段四（DNS）${D}  — DNS 污染/过慢导致域名解析失败"
    echo "      修复：sudo networksetup -setdnsservers Wi-Fi 223.5.5.5 119.29.29.29"
    echo ""
    echo "  ${DIM}按任意键退出...${D}"
    read -rsn1 2>/dev/null || true
    exit 1

  elif [ "$WARN" -gt 0 ]; then
    echo "  ${Y}⚠ 有 $WARN 项警告，部分配置可进一步优化：${D}"
    echo ""
    echo "  ${Y}1.${D} ${Y}阶段五（三层直连）${D}  — ZCode 域名可能未完全直连"
    echo "      修复：bash bootstrap.sh（会重新写入 NO_PROXY / PAC）"
    echo ""
    echo "  ${Y}2.${D} ${Y}阶段七（路由表）${D}  — 路由表有残留 TUN 条目"
    echo "      修复：重启 Clash Verge 可清除残留路由"
    echo ""
    echo "  运行 ${BOLD}bash bootstrap.sh${D} 可自动优化警告项"
    echo ""
    exit 0
  else
    echo "${G}  ✔ 全部检查通过，网络配置状态优秀 ✔${D}"
    echo ""
    echo "  ${DIM}恭喜！你的网络环境已就绪，可以直接使用 ZCode / AI 工具。${D}"
    echo "  ${DIM}提示：如有异常，运行 ${BOLD}bash bootstrap.sh --diagnose${D}${DIM} 重新诊断${D}"
    echo ""
    exit 0
  fi
  # 恢复严格模式
  set -euo pipefail
fi

OS="$(uname)"
TS="$(date +%Y%m%d-%H%M%S)"
PROXY_PORT="${PROXY_PORT:-7897}"
bak() { [ -f "$1" ] && cp "$1" "$1.bak.$TS" && echo "  备份: $1.bak.$TS" || true; }

echo "=== netroamer ($OS) ==="

# ---------------------------------------------------------------
# 0. 依赖自检：缺什么装什么（curl/git/nc）；装不了给出明确指引
# ---------------------------------------------------------------
echo "--- 0/6 依赖自检 ---"
PKG=""
if command -v brew >/dev/null 2>&1; then PKG="brew"
elif command -v apt-get >/dev/null 2>&1; then PKG="apt"
elif command -v dnf >/dev/null 2>&1; then PKG="dnf"
elif command -v pacman >/dev/null 2>&1; then PKG="pacman"
elif command -v zypper >/dev/null 2>&1; then PKG="zypper"
elif command -v apk >/dev/null 2>&1; then PKG="apk"
fi

pkg_install() {  # $@=包名（按当前包管理器映射后安装）
  [ -n "$PKG" ] || { echo "  [跳过] 无包管理器，请手动安装: $*"; return 1; }
  local sudo=""
  [ "$(id -u)" != "0" ] && command -v sudo >/dev/null 2>&1 && sudo="sudo"
  echo "  通过 $PKG 安装: $*"
  case "$PKG" in
    brew)   brew install "$@" ;;
    apt)    $sudo apt-get update -qq && $sudo apt-get install -y "$@" ;;
    dnf)    $sudo dnf install -y "$@" ;;
    pacman) $sudo pacman -S --noconfirm --needed "$@" ;;
    zypper) $sudo zypper install -y "$@" ;;
    apk)    $sudo apk add "$@" ;;
  esac
}

map_install() {  # $1=通用名，映射到各发行版包名
  case "$PKG:$1" in
    *:curl)                pkg_install curl ;;
    *:git)                 pkg_install git ;;
    apt:nc)                pkg_install netcat-openbsd ;;
    dnf:nc)                pkg_install nmap-ncat ;;
    pacman:nc)             pkg_install openbsd-netcat ;;
    apk:nc)                pkg_install netcat-openbsd ;;
    *:nc)                  pkg_install netcat ;;
  esac
}

MISSING=()
for cmd in curl git nc; do
  if ! command -v "$cmd" >/dev/null 2>&1; then
    if map_install "$cmd"; then
      command -v "$cmd" >/dev/null 2>&1 || MISSING+=("$cmd(安装后仍不可用)")
    else
      MISSING+=("$cmd")
    fi
  fi
done
[ ${#MISSING[@]} -gt 0 ] \
  && echo "  警告：以下依赖不可用（脚本会降级运行）：${MISSING[*]}" \
  || echo "  curl / git / nc 均可用"
# curl/nc 缺失时 shell 探测块自动降级为 /dev/tcp 探测，功能不受影响

# ---------------------------------------------------------------
# 1. shell 代理块：写入 ~/.zshrc 与 ~/.bashrc（幂等标记块）
#    自动探测 7897 端口活着才 export；含 proxy_on/off、GitHub 镜像开关
# ---------------------------------------------------------------
PROXY_BLOCK=$(cat <<EOF
# >>> netroamer proxy >>>
# 自动探测 Clash/mihomo 混合端口，活着才启用代理环境变量（国内域名三层直连的第 3 层）
netroamer_port_open() {
  (exec 3<>/dev/tcp/127.0.0.1/${PROXY_PORT}) 2>/dev/null && { exec 3>&- 3<&-; return 0; }
  command -v nc >/dev/null 2>&1 && nc -z -w 1 127.0.0.1 ${PROXY_PORT} 2>/dev/null
}
if netroamer_port_open; then
  export HTTP_PROXY="http://127.0.0.1:${PROXY_PORT}" HTTPS_PROXY="http://127.0.0.1:${PROXY_PORT}"
  export http_proxy="\$HTTP_PROXY" https_proxy="\$HTTPS_PROXY"
  export ALL_PROXY="socks5://127.0.0.1:${PROXY_PORT}" all_proxy="\$ALL_PROXY"
  export NO_PROXY="localhost,127.0.0.1,::1,.local,.cn,npmmirror.com,hf-mirror.com,bigmodel.cn,vectide.cn,zhipuai.cn,z.ai,100.64.0.0/10"
  export no_proxy="\$NO_PROXY"
fi
proxy_on() {
  export HTTP_PROXY="http://127.0.0.1:${PROXY_PORT}" HTTPS_PROXY="http://127.0.0.1:${PROXY_PORT}"
  export http_proxy="\$HTTP_PROXY" https_proxy="\$HTTPS_PROXY"
  export ALL_PROXY="socks5://127.0.0.1:${PROXY_PORT}" all_proxy="\$ALL_PROXY"
  export NO_PROXY="localhost,127.0.0.1,::1,.local,.cn,npmmirror.com,hf-mirror.com,bigmodel.cn,vectide.cn,zhipuai.cn,z.ai,100.64.0.0/10"
  export no_proxy="\$NO_PROXY"; echo "proxy on (:${PROXY_PORT})"
}
proxy_off() {
  unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy NO_PROXY no_proxy
  echo "proxy off"
}
# GitHub 读写全部走本地代理（push 前无需关闭，url.rewrite 仅影响 clone/fetch）
gh_proxy_on() {
  git config --global http.https://github.com.proxy "http://127.0.0.1:${PROXY_PORT}"
  echo "github push via proxy (:${PROXY_PORT})"
}
gh_proxy_off() {
  git config --global --unset http.https://github.com.proxy 2>/dev/null
  echo "github push direct"
}
# git clone 读操作走 gh-proxy.com URL 镜像（可与 gh_proxy_on 共存）
gh_mirror_on() {
  git config --global url."https://gh-proxy.com/https://github.com/".insteadOf "https://github.com/"
  echo "github clone via gh-proxy.com"
}
gh_mirror_off() {
  git config --global --unset-all url."https://gh-proxy.com/https://github.com/".insteadOf 2>/dev/null
  echo "github clone direct"
}
# <<< netroamer proxy <<<
EOF
)

apply_block() {  # $1=rc 文件
  local rc="$1"; [ -f "$rc" ] || touch "$rc"
  # 幂等：先删本脚本管理的标记块（含旧版 network-bootstrap 标记，升级用）
  if grep -qE "^# >>> (netroamer|network-bootstrap) proxy >>>" "$rc"; then
    awk '/^# >>> (netroamer|network-bootstrap) proxy >>>/{skip=1}
         /^# <<< (netroamer|network-bootstrap) proxy <<</{skip=0; next}
         !skip' "$rc" > "$rc.tmp"
    mv "$rc.tmp" "$rc"
  fi
  # 迁移：删除更早版本无标记的「Clash Verge CLI 代理」块（从其标题行到 proxy_off 行）
  if grep -q "===== Clash Verge CLI 代理" "$rc"; then
    bak "$rc"
    sed -e '/===== Clash Verge CLI 代理/,/^proxy_off() /d' "$rc" > "$rc.tmp" && mv "$rc.tmp" "$rc"
    echo "  已迁移旧代理块: $rc"
  fi
  printf '\n%s\n' "$PROXY_BLOCK" >> "$rc"
  echo "  已写入: $rc"
}
echo "--- 1/6 shell 代理块 ---"
apply_block "$HOME/.zshrc"
apply_block "$HOME/.bashrc"

# ---------------------------------------------------------------
# 2. 包管理器镜像（镜像优先原则；实测 11-27 MB/s vs 节点 10-20 KB/s）
#    工具链本身不代装（体积大），检测到哪个配哪个；都没有也会在末尾提示
# ---------------------------------------------------------------
echo "--- 2/6 包管理器镜像 ---"
TOOLCHAIN_SEEN=0
if command -v pip3 >/dev/null 2>&1 || command -v pip >/dev/null 2>&1; then
  mkdir -p "$HOME/.config/pip"
  bak "$HOME/.config/pip/pip.conf"
  cat > "$HOME/.config/pip/pip.conf" <<'EOF'
[global]
index-url = https://pypi.tuna.tsinghua.edu.cn/simple
extra-index-url = https://mirrors.aliyun.com/pypi/simple
EOF
  echo "  pip → 清华 TUNA + 阿里云备用"; TOOLCHAIN_SEEN=1
fi
if command -v cargo >/dev/null 2>&1 || [ -d "$HOME/.cargo" ]; then
  bak "$HOME/.cargo/config.toml"
  cat > "$HOME/.cargo/config.toml" <<'EOF'
[source.crates-io]
replace-with = "rsproxy-sparse"
[source.rsproxy-sparse]
registry = "sparse+https://rsproxy.cn/index/"
[net]
git-fetch-with-cli = true
EOF
  echo "  cargo → 字节 rsproxy (sparse)"; TOOLCHAIN_SEEN=1
fi
# rustup 自身（独立于 cargo）走字节镜像，避免 rustup update 卡在 static.rust-lang.org
if command -v rustup >/dev/null 2>&1; then
  export RUSTUP_DIST_SERVER=https://rsproxy.cn
  export RUSTUP_UPDATE_ROOT=https://rsproxy.cn/rustup
  rustup set RUSTUP_DIST_SERVER https://rsproxy.cn 2>/dev/null || true
  echo "  rustup → 字节 rsproxy.cn"
fi
if command -v go >/dev/null 2>&1; then
  go env -w GOPROXY=https://goproxy.cn,direct
  echo "  go → goproxy.cn"; TOOLCHAIN_SEEN=1
fi
if command -v npm >/dev/null 2>&1; then
  npm config set registry https://registry.npmmirror.com
  echo "  npm → npmmirror"; TOOLCHAIN_SEEN=1
fi
# Homebrew bottles 走 gh-proxy.com，避免 --cask 从 GitHub 直连下载极慢
if [ "$OS" = "Darwin" ] && command -v brew >/dev/null 2>&1; then
  # 写入 zshrc（brew 启动时会 source）
  HB_MARKER="# netroamer: homebrew via gh-proxy"
  if ! grep -q "$HB_MARKER" "$HOME/.zshrc" 2>/dev/null; then
    cat >> "$HOME/.zshrc" <<'HBMARKER'

# netroamer: homebrew via gh-proxy
export HOMEBREW_BREW_GIT_REMOTE="https://gh-proxy.com/https://github.com/Homebrew/brew.git"
export HOMEBREW_CORE_GIT_REMOTE="https://gh-proxy.com/https://github.com/Homebrew/homebrew-core.git"
export HOMEBREW_API_DOMAIN="https://gh-proxy.com/https://formulae.brew.sh/api"
export HOMEBREW_BOTTLE_DOMAIN="https://ghfast.cloud"
HBMARKER
  fi
  # 当前 shell 也设（本次 session 生效）
  export HOMEBREW_BREW_GIT_REMOTE="https://gh-proxy.com/https://github.com/Homebrew/brew.git"
  export HOMEBREW_CORE_GIT_REMOTE="https://gh-proxy.com/https://github.com/Homebrew/homebrew-core.git"
  export HOMEBREW_API_DOMAIN="https://gh-proxy.com/https://formulae.brew.sh/api"
  export HOMEBREW_BOTTLE_DOMAIN="https://ghfast.cloud"
  echo "  homebrew → gh-proxy.com (brew/git/api/bottles)"
fi
[ "$TOOLCHAIN_SEEN" = 0 ] && echo "  未检测到 pip/cargo/go/npm，跳过（装好工具链后重跑本脚本即可自动配置镜像）"

# ---------------------------------------------------------------
# 2.5 Docker Hub 镜像（DaoCloud 公开免费，无需注册；Docker Desktop 用户在 GUI 设置里配）
# ---------------------------------------------------------------
echo "--- 2b/6 Docker Hub 镜像 ---"
DOCKER_CONF="/etc/docker/daemon.json"
if [ "$OS" = "Darwin" ]; then
  # macOS Docker Desktop：不改系统 daemon.json，在 GUI 里配
  if command -v docker >/dev/null 2>&1; then
    echo "  Docker Desktop macOS：请在 Docker Desktop → Settings → Docker Engine 中配置："
    echo '  { "registry-mirrors": ["https://docker.m.daocloud.io"] }'
  else
    echo "  未检测到 docker，跳过（GUI 配置路径同上）"
  fi
elif [ "$OS" = "Linux" ]; then
  # Linux systemd Docker daemon
  if command -v docker >/dev/null 2>&1; then
    mkdir -p "$(dirname "$DOCKER_CONF")"
    bak "$DOCKER_CONF" 2>/dev/null || true
    # 保留现有配置（如果有），只追加 mirrors
    if [ -f "$DOCKER_CONF" ] && grep -q "registry-mirrors" "$DOCKER_CONF" 2>/dev/null; then
      echo "  registry-mirrors 已存在，跳过（手动检查 $DOCKER_CONF）"
    else
      cat > "$DOCKER_CONF" <<'EOF'
{
  "registry-mirrors": ["https://docker.m.daocloud.io"]
}
EOF
      systemctl restart docker 2>/dev/null && echo "  Linux docker → DaoCloud" \
        || echo "  docker daemon.json 已写入，需 sudo systemctl restart docker 生效"
    fi
  else
    echo "  未检测到 docker，跳过"
  fi
else
  echo "  跳过（非 macOS/Linux）"
fi

# ---------------------------------------------------------------
# 3. DNS 国内化（223.5.5.5 + 119.29.29.29）
# ---------------------------------------------------------------
echo "--- 3/6 DNS ---"
if [ "$OS" = "Darwin" ] && [ "$NO_DNS" = false ]; then
  for svc in $(networksetup -listallnetworkservices 2>/dev/null | tail -n +2); do
    networksetup -setdnsservers "$svc" 223.5.5.5 119.29.29.29 >/dev/null 2>&1 \
      && echo "  $svc → 223.5.5.5 119.29.29.29"
  done
elif [ "$OS" = "Linux" ] && { [ "$FORCE_DNS" = true ]; }; then
  if command -v resolvectl >/dev/null 2>&1; then
    mkdir -p /etc/systemd/resolved.conf.d
    bak /etc/systemd/resolved.conf.d/netroamer.conf 2>/dev/null || true
    printf '[Resolve]\nDNS=223.5.5.5 119.29.29.29\nDomains=~.\n' > /etc/systemd/resolved.conf.d/netroamer.conf
    systemctl restart systemd-resolved
    echo "  systemd-resolved → 223.5.5.5 119.29.29.29"
  else
    echo "  跳过：未找到 systemd-resolved（可手动写 /etc/resolv.conf）"
  fi
else
  echo "  跳过（--no-dns/--ci 或 Linux 未加 --dns）"
fi

# ---------------------------------------------------------------
# 4. 看门狗（LaunchAgent / cron，每 5 分钟）
# ---------------------------------------------------------------
echo "--- 4/6 看门狗 ---"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [ "$NO_WATCHDOG" = false ]; then
  mkdir -p "$HOME/.local/bin"
  cp "$SCRIPT_DIR/share/net-watchdog.sh" "$HOME/.local/bin/net-watchdog.sh"
  chmod +x "$HOME/.local/bin/net-watchdog.sh"
  if [ "$OS" = "Darwin" ]; then
    # 清理旧版看门狗 LaunchAgent（任意旧 Label，不含本版 com.netroamer.watchdog）
    for OLD_PLIST in "$HOME/Library/LaunchAgents/"*netwatchdog*.plist; do
      [ -f "$OLD_PLIST" ] || continue
      [[ "$OLD_PLIST" == *"com.netroamer.watchdog.plist" ]] && continue
      launchctl unload "$OLD_PLIST" 2>/dev/null || true
      rm -f "$OLD_PLIST"
      echo "  已移除旧 LaunchAgent: $(basename "$OLD_PLIST")"
    done
    PLIST="$HOME/Library/LaunchAgents/com.netroamer.watchdog.plist"
    bak "$PLIST"
    cat > "$PLIST" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>com.netroamer.watchdog</string>
  <key>ProgramArguments</key><array>
    <string>/bin/zsh</string><string>$HOME/.local/bin/net-watchdog.sh</string>
  </array>
  <key>StartInterval</key><integer>300</integer>
  <key>RunAtLoad</key><true/>
</dict></plist>
EOF
    launchctl unload "$PLIST" 2>/dev/null || true
    launchctl load "$PLIST"
    echo "  LaunchAgent 已加载（每 5 分钟；日志 /tmp/net-watchdog.log）"
  else
    if command -v crontab >/dev/null 2>&1; then
      (crontab -l 2>/dev/null | grep -v net-watchdog.sh; echo "*/5 * * * * $HOME/.local/bin/net-watchdog.sh") | crontab -
      echo "  cron 已配置（每 5 分钟）"
    else
      echo "  警告：无 crontab，请手动定时执行 $HOME/.local/bin/net-watchdog.sh"
    fi
  fi
fi

# ---------------------------------------------------------------
# 5. Clash 配置提示（PAC/规则无法安全自动写入，给出粘贴路径）+ 自检
# ---------------------------------------------------------------
echo "--- 5/6 Clash 手动步骤 ---"
if [ "$OS" = "Darwin" ]; then
  CVR="$HOME/Library/Application Support/io.github.clash-verge-rev.clash-verge-rev"
  echo "  1. 安装 Clash Verge Rev: brew install --cask clash-verge-rev"
  echo "  2. 设置: TUN 关 / PAC 模式开 / 开机自启 / 静默启动（verge.yaml: enable_tun_mode=false, proxy_auto_config=true）"
  echo "  3. PAC 内容粘贴: $SCRIPT_DIR/clash/pac.js"
  echo "  4. 规则 prepend 粘贴: $SCRIPT_DIR/clash/rules-prepend.yaml"
  echo "  5. Merge 粘贴: $SCRIPT_DIR/clash/rules-merge.yaml"
  echo "  6. 订阅 token 文件 chmod 600；FileVault 开启"
  [ -d "$CVR" ] && echo "  （检测到已有配置目录: ${CVR}）" || true
else
  echo "  Linux: 运行 mihomo/clash 内核（配置中 TUN 关、external-controller 仅 unix socket）"
  echo "  规则模板同: $SCRIPT_DIR/clash/（PAC 由环境变量层替代）"
fi

# --- 收尾自检：rc 文件语法必须能过，否则立即报错退出 ---
FAIL=0
for rc in "$HOME/.zshrc" "$HOME/.bashrc"; do
  if command -v zsh >/dev/null 2>&1 && [[ "$rc" == *.zshrc ]]; then
    zsh -n "$rc" || FAIL=1
  fi
  bash -n "$rc" || FAIL=1
done
if [ "$FAIL" = 1 ]; then
  echo "!! rc 文件语法检查未通过，请检查备份文件 *.bak.$TS"; exit 1
fi
echo "=== 完成。新开终端使 shell 块生效；排障速查见 README.md ==="
