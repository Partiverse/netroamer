#!/usr/bin/env bash
# netroamer —— macOS / Linux 中国网络开发环境一键配置
# 原则见 README.md：TUN 永不开启、镜像优先、ZCode 三层直连、DNS 国内化、监控自愈
# 幂等：可重复执行；被改文件均带时间戳备份
# 用法：bash bootstrap.sh [--no-dns] [--no-watchdog] [--dns] [--ci]
set -euo pipefail

NO_DNS=false; NO_WATCHDOG=false; FORCE_DNS=false; CI=false
for arg in "$@"; do
  case "$arg" in
    --no-dns) NO_DNS=true ;;
    --no-watchdog) NO_WATCHDOG=true ;;
    --dns) FORCE_DNS=true ;;          # Linux 上显式要求写 DNS（需 sudo）
    --ci) CI=true; NO_DNS=true; NO_WATCHDOG=true ;;   # CI/测试环境：只配 shell 块与镜像
    *) echo "未知参数: $arg"; exit 1 ;;
  esac
done

OS="$(uname)"
TS="$(date +%Y%m%d-%H%M%S)"
PROXY_PORT="${PROXY_PORT:-7897}"
bak() { [ -f "$1" ] && cp "$1" "$1.bak.$TS" && echo "  备份: $1.bak.$TS" || true; }

echo "=== netroamer ($OS) ==="

# ---------------------------------------------------------------
# 0. 依赖自检：缺什么装什么（curl/git/nc）；装不了给出明确指引
# ---------------------------------------------------------------
echo "--- 0/5 依赖自检 ---"
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
proxy_off() { unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy NO_PROXY no_proxy; echo "proxy off"; }
# git clone GitHub 走 gh-proxy.com URL 转发镜像（影响 push，push 前 gh_mirror_off）
gh_mirror_on()  { git config --global url."https://gh-proxy.com/https://github.com/".insteadOf "https://github.com/"; echo "github via gh-proxy.com"; }
gh_mirror_off() { git config --global --unset-all url."https://gh-proxy.com/https://github.com/".insteadOf 2>/dev/null; echo "github direct"; }
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
echo "--- 1/5 shell 代理块 ---"
apply_block "$HOME/.zshrc"
apply_block "$HOME/.bashrc"

# ---------------------------------------------------------------
# 2. 包管理器镜像（镜像优先原则；实测 11-27 MB/s vs 节点 10-20 KB/s）
#    工具链本身不代装（体积大），检测到哪个配哪个；都没有也会在末尾提示
# ---------------------------------------------------------------
echo "--- 2/5 包管理器镜像 ---"
TOOLCHAIN_SEEN=0
if command -v pip3 >/dev/null 2>&1 || command -v pip >/dev/null 2>&1; then
  mkdir -p "$HOME/.config/pip"
  bak "$HOME/.config/pip/pip.conf"
  cat > "$HOME/.config/pip/pip.conf" <<'EOF'
[global]
index-url = https://pypi.tuna.tsinghua.edu.cn/simple
EOF
  echo "  pip → 清华 TUNA"; TOOLCHAIN_SEEN=1
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
if command -v go >/dev/null 2>&1; then
  go env -w GOPROXY=https://goproxy.cn,direct
  echo "  go → goproxy.cn"; TOOLCHAIN_SEEN=1
fi
if command -v npm >/dev/null 2>&1; then
  npm config set registry https://registry.npmmirror.com
  echo "  npm → npmmirror"; TOOLCHAIN_SEEN=1
fi
[ "$TOOLCHAIN_SEEN" = 0 ] && echo "  未检测到 pip/cargo/go/npm，跳过（装好工具链后重跑本脚本即可自动配置镜像）"

# ---------------------------------------------------------------
# 3. DNS 国内化（223.5.5.5 + 119.29.29.29）
# ---------------------------------------------------------------
echo "--- 3/5 DNS ---"
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
echo "--- 4/5 看门狗 ---"
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
echo "--- 5/5 Clash 手动步骤 ---"
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
