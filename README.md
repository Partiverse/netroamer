# netroamer

把本机（macOS）验证过的中国网络开发环境架构，一键落到新 macOS / Linux / Windows 上。
[![CI](https://github.com/OWNER/netroamer/actions/workflows/ci.yml/badge.svg)](https://github.com/OWNER/netroamer/actions/workflows/ci.yml)
姊妹项目：[`kubuntu-cn-setup/`](../kubuntu-cn-setup/)（Kubuntu 裸机 APT/DNS/Docker 初始化，Linux 上建议先跑它再跑本脚本）。

> 脚本会自动检测并安装自身依赖（curl/git/nc，经 brew/apt/dnf/pacman/zypper/apk）；
> 工具链（pip/cargo/go/npm）不代装——检测到哪个就配哪个的国内镜像。
> 仓库不含任何订阅地址、token 或私人域名凭证；`ci.yml` 里有私密信息扫描兜底。

## 六条原则（一切配置由此推导）

1. **TUN 永不开启。** TUN 劫持全系统路由 + fake-ip DNS，曾两次弄断 ZCode 的国内直连。
   一律用 PAC 系统代理（macOS/Windows）或环境变量（Linux/CLI），让直连域名在**进代理之前**就分流。
2. **镜像优先，代理兜底。** 国内包管理器一律走国内镜像（清华/rsproxy/goproxy.cn/npmmirror，实测 11–27 MB/s）；
   走海外节点只有 10–20 KB/s，慢千倍。只有镜像覆盖不了的（Google、GitHub API 等）才走代理。
3. **ZCode / AI 域名三层直连保护。** ① PAC 域名 DIRECT ② Clash 规则链 prepend（含 PROCESS-NAME-REGEX）
   ③ shell `NO_PROXY`。任何一层失效，另两层仍保住 ZCode 连通。
4. **DNS 本机国内化。** 223.5.5.5（阿里）+ 119.29.29.29（DNSPod），解析快且不被污染。
5. **监控自愈 + 通知克制。** 看门狗每 5 分钟检查：核心死亡自动拉起；链路异常发 macOS/Linux 通知；
   同类告警 90 分钟冷却，检查目标用 Apple 测试页（Cloudflare 探针会因出口节点误报）。
6. **安全基线。** Clash 只监听 127.0.0.1、控制面只留 unix socket；订阅 token 文件 chmod 600；FileVault/磁盘加密开启。

## 目录结构

```
netroamer/
├── bootstrap.sh            # macOS + Linux 入口（幂等，可重复执行）
├── bootstrap.ps1           # Windows 入口（管理员 PowerShell）
├── clash/
│   ├── pac.js              # PAC 模板（粘贴进 Clash Verge「PAC 模式」设置）
│   ├── rules-prepend.yaml  # 粘进 Clash Verge「Rules 增强」的 prepend 块
│   └── rules-merge.yaml    # 粘进 Clash Verge「Merge」的 rule-providers 补充
├── share/
│   └── net-watchdog.sh     # 看门狗（macOS LaunchAgent / Linux cron 调用）
└── .github/workflows/ci.yml # 三平台 CI（macOS/Linux/Windows）+ 私密信息扫描
```

## 使用方法

### macOS（本机架构的原样复刻）

```bash
bash bootstrap.sh              # shell 代理块 + 镜像 + DNS + 看门狗
bash bootstrap.sh --diagnose   # 只读诊断：检查端口/进程/代理状态/DNS/镜像/连通性
bash bootstrap.sh --no-dns     # 不动 DNS（如公司机）
bash bootstrap.sh --no-watchdog
```

Clash Verge Rev 需手动安装（brew install --cask clash-verge-rev 或官网），装完后：
- 开 PAC 模式 → 把 `clash/pac.js` 内容粘贴进 PAC 设置
- 订阅右键「编辑规则」→ 粘贴 `clash/rules-prepend.yaml`
- 订阅右键「编辑 Merge」→ 粘贴 `clash/rules-merge.yaml`
- 设置里确认：TUN 关、系统代理开、开机自启、静默启动

### Linux

```bash
bash bootstrap.sh              # shell 代理块 + 镜像（pip/cargo/go/npm）
# sudo bash bootstrap.sh --dns  # 可选：写 systemd-resolved DNS
```
Linux 上代理由环境变量驱动（脚本写入 `~/.zshrc`/`~/.bashrc` 自动探测块 + `proxy_on/off`），
配合 mihomo/clash 内核手动运行即可，无系统级 PAC。

### Windows（管理员 PowerShell）

```powershell
Set-ExecutionPolicy -Scope Process Bypass; .\bootstrap.ps1              # 环境变量 + 镜像 + DNS
.\bootstrap.ps1 -SkipDNS
```
Windows 上系统代理同样交给 Clash Verge Rev 的 PAC 模式（粘贴 `clash/pac.js`）。

## CI（GitHub Actions）

`.github/workflows/ci.yml` 在每次 push 时：
1. **私密信息扫描**——仓库中出现订阅 token 样式、私有主机名等即失败；
2. **三平台真实执行**——macOS / Ubuntu 跑 `bootstrap.sh --ci`，Windows 跑 `bootstrap.ps1 -CI`
   （外加 git-bash 交叉跑一遍 `bootstrap.sh --ci`），并验证代理函数、镜像配置落盘。
`--ci` 模式跳过 DNS 与看门狗等本机专属步骤，只配 shell 块 + 镜像，可在任何环境安全试跑。

## 关键域名清单（直连白名单，改任何一层时保持三处同步）

`bigmodel.cn`（ZCode API）、`vectide.cn`（API 中转）、`zhipuai.cn`、`z.ai`、`hf-mirror.com`、
`npmmirror.com`、`.cn`、Tailscale 三域（tailscale.com / tailscale.io / ts.net）、`localhost/.local/.lan`、RFC1918、100.64.0.0/10。

## 排障速查

- 国内站打不开 → 先查 Clash 是否被开了 TUN（必须关）；再 `nc -z 127.0.0.1 7897`
- ZCode 超时 → 三层直连是否都在（PAC / prepend / `echo $NO_PROXY`）
- 通知轰炸 → `tail /tmp/net-watchdog.log`；临时停用 `touch /tmp/net-watchdog.off`（Windows/Linux：环境变量 `NET_WATCHDOG_OFF=1`）
- Docker 拉镜像慢 → Docker Desktop 手动代理 `http://127.0.0.1:7897`，排除名单加国内域名
  （注意 settings-store.json 键名是全大写 `OverrideProxyHTTP`，混合大小写会被静默丢弃）
