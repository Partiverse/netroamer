# netroamer —— Windows 中国网络开发环境一键配置
# 用法（管理员 PowerShell）：
#   Set-ExecutionPolicy -Scope Process Bypass; .\bootstrap.ps1
#   .\bootstrap.ps1 -SkipDNS
#   .\bootstrap.ps1 -CI        # GitHub Actions / 测试环境：跳过 DNS 与本机专属步骤
# 原则见 README.md：TUN 永不开启、镜像优先、ZCode 三层直连、DNS 国内化
param(
  [switch]$SkipDNS,
  [switch]$SkipEnv,
  [switch]$CI
)
$ErrorActionPreference = "Stop"
$Port = if ($env:PROXY_PORT) { $env:PROXY_PORT } else { "7897" }

Write-Host "=== netroamer (Windows) ==="

# ---------------------------------------------------------------
# 1. 用户级环境变量（三层直连的第 3 层；系统代理交给 Clash Verge 的 PAC）
# ---------------------------------------------------------------
if (-not $SkipEnv) {
  Write-Host "--- 1/3 环境变量 ---"
  $noProxy = "localhost,127.0.0.1,::1,.local,.cn,npmmirror.com,hf-mirror.com,bigmodel.cn,vectide.cn,zhipuai.cn,z.ai,100.64.0.0/10"
  [Environment]::SetEnvironmentVariable("HTTP_PROXY",  "http://127.0.0.1:$Port", "User")
  [Environment]::SetEnvironmentVariable("HTTPS_PROXY", "http://127.0.0.1:$Port", "User")
  [Environment]::SetEnvironmentVariable("ALL_PROXY",   "socks5://127.0.0.1:$Port", "User")
  [Environment]::SetEnvironmentVariable("NO_PROXY",    $noProxy, "User")
  [Environment]::SetEnvironmentVariable("no_proxy",    $noProxy, "User")
  Write-Host "  HTTP(S)_PROXY/ALL_PROXY/NO_PROXY 已写入用户环境变量（重启终端生效）"
  Write-Host "  注意：NO_PROXY 需程序自行遵守；若某工具直连失效，在其自己的配置里补白名单"
}

# ---------------------------------------------------------------
# 2. 包管理器镜像（镜像优先原则）
# ---------------------------------------------------------------
Write-Host "--- 2/3 包管理器镜像 ---"
if (Get-Command pip -ErrorAction SilentlyContinue) {
  $appdata = $env:APPDATA
  New-Item -ItemType Directory -Force -Path "$appdata\pip" | Out-Null
  $bak = "$appdata\pip\pip.ini.bak.$(Get-Date -Format yyyyMMdd-HHmmss)"
  if (Test-Path "$appdata\pip\pip.ini") { Copy-Item "$appdata\pip\pip.ini" $bak; Write-Host "  备份: $bak" }
  @"
[global]
index-url = https://pypi.tuna.tsinghua.edu.cn/simple
"@ | Set-Content -Encoding ASCII "$appdata\pip\pip.ini"
  Write-Host "  pip → 清华 TUNA"
}
if (Test-Path "$env:USERPROFILE\.cargo") {
  $cfg = "$env:USERPROFILE\.cargo\config.toml"
  if (Test-Path $cfg) { Copy-Item $cfg "$cfg.bak.$(Get-Date -Format yyyyMMdd-HHmmss)" }
  @"
[source.crates-io]
replace-with = "rsproxy-sparse"
[source.rsproxy-sparse]
registry = "sparse+https://rsproxy.cn/index/"
[net]
git-fetch-with-cli = true
"@ | Set-Content -Encoding UTF8 $cfg
  Write-Host "  cargo → 字节 rsproxy (sparse)"
}
if (Get-Command go -ErrorAction SilentlyContinue) {
  go env -w GOPROXY=https://goproxy.cn,direct
  Write-Host "  go → goproxy.cn"
}
if (Get-Command npm -ErrorAction SilentlyContinue) {
  npm config set registry https://registry.npmmirror.com
  Write-Host "  npm → npmmirror"
}

# ---------------------------------------------------------------
# 3. DNS 国内化（对所有联网适配器）
# ---------------------------------------------------------------
Write-Host "--- 3/3 DNS ---"
if ($SkipDNS -or $CI) {
  Write-Host "  跳过（-SkipDNS 或 -CI）"
} else {
  Get-NetAdapter | Where-Object { $_.Status -eq "Up" } | ForEach-Object {
    Set-DnsClientServerAddress -InterfaceIndex $_.ifIndex -ServerAddresses "223.5.5.5","119.29.29.29"
    Write-Host "  $($_.Name) → 223.5.5.5 119.29.29.29"
  }
} else {
  Write-Host "  跳过 (-SkipDNS)"
}

Write-Host ""
Write-Host "=== Clash Verge Rev 手动步骤 ==="
Write-Host "  1. 安装: winget install clash-verge-rev（或 GitHub release）"
Write-Host "  2. 设置: TUN 关 / PAC 模式开 / 开机自启 / 静默启动"
Write-Host "  3. PAC 内容粘贴: clash/pac.js"
Write-Host "  4. 规则 prepend 粘贴: clash/rules-prepend.yaml；Merge 粘贴: clash/rules-merge.yaml"
Write-Host "  5. Docker Desktop 手动代理 http://127.0.0.1:$Port（排除名单加国内域名；键名必须全大写 OverrideProxyHTTP）"
Write-Host "=== 完成 ==="
