# netroamer 深度调研（四）：网络连接、稳定性、加密与安全

> 调研日期：2026-09-28。方法：WebSearch/WebFetch 公开资料（优先 2024–2026）。视角：防御性产品安全评估。
> 说明：部分主题（尤其涉及中国大陆审查/合规的判决原文）检索受限或未命中权威原文，文中均如实标注「未找到」；引用为搜索摘要口径的关键数字已注明「建议复核」。

---

## 结论先行

1. **加密选型基准已收敛**：WireGuard（约 4k 行代码、Tamarin 形式化验证、2024–2025 无重大 CVE）是隧道层默认安全选择；QUIC/HTTP3 的连接迁移与 0-RTT 对弱网/移动场景有实测增益；MASQUE（RFC 9298/9484）已被 Apple iCloud Private Relay 与 Google 生态采用，是「代理即协议」的新基线。
2. **抗量子迁移已过临界点**：X25519MLKEM768（0x11EC）自 2024 年起在 Chrome/Edge/Firefox 默认启用，Cloudflare 边缘默认支持，2025 年底其 TLS 1.3 流量过半协商 PQ 混合组；剩余短板在 PQ 签名/证书与老旧中间盒（约 1.6KB ClientHello 兼容性）。
3. **加密 DNS 大陆可用性二分**：阿里 DoH（dns.alidns.com）/腾讯 DoH（doh.pub）境内直连可用、低延迟，是 netroamer DNS 国内化的合理落点；Cloudflare 1.1.1.1 DoH/DoT 在大陆被 SNI 阻断（TLS+QUIC，残余封锁可达约 360 秒，Lantern corpus 测量）。加密只解决传输安全，不解决 GFW 对敏感域名的污染。
4. **Clash/mihomo 生态安全事故模式高度一致**：未鉴权 external-controller（9090/9097）+ CORS 缺陷 → 恶意网页一键改配置 → 任意文件写 → RCE（2025 年 4–5 月 Clash Verge 1-Click RCE，波及数万台暴露设备）；恶意订阅配置投毒（rule-providers path → RCE，CFW 0.20.12）；订阅覆盖 secret 导致越权（clash-verge-rev#1783）。netroamer 凡与 mihomo 交互处都应假定「订阅不可信、API 必须鉴权、只绑 127.0.0.1」。
5. **一键脚本供应链可工程化缓解**：下载-验签-执行三步走，SHA256SUMS + GPG/Minisign 签名（校验文件本身必须签名）、HTTPS 不可替代校验、提供「先审后装」模式。
6. **看门狗必须以用户级 LaunchAgent 运行**（避免 root LaunchDaemon）；launchd `KeepAlive` 即可自愈，无需自身特权。遥测应默认本地化（SQLite/本地统计），上云前经本地差分隐私（LDP）或只回传模型更新/聚合统计。
7. **NO_PROXY 是本产品最容易被忽视的攻击/故障面**：必须显式覆盖 127.0.0.0/8、::1、RFC1918、169.254.0.0/16（含 169.254.169.254）、100.64.0.0/10（Tailscale/CGNAT）——业界已有真实漏洞（BentoML SSRF 经 100.64.0.0/10 绕过黑名单）。
8. **合规是产品形态问题而非代码问题**：自用型网络优化/本地工具 vs 分发代理二进制 vs 提供节点服务，风险量级完全不同（行政警告/罚款 → 拘留 → 刑事「提供侵入、非法控制计算机信息系统程序、工具罪」）。netroamer「本地运行、不上传、不分发节点」是风险最低形态，但需在分发与措辞上主动对齐。

---

## 1. 加密协议现状与选型（2024–2026）

### 1.1 WireGuard

- **代码与审计状态**：约 4,000 行代码、极小的密码学面（ChaCha20-Poly1305、Curve25519、BLAKE2s、SipHash），历史上通过 Cure53/Jean-Philippe Aumasson 独立审计（2020 年前后，结论为低危问题为主），并有基于 Tamarin 的协议形式化证明。**2024–2025 检索未发现新的重大协议级 CVE**。
- **性能**：2025 年 6 月的对比研究显示 WireGuard 在吞吐与 CPU 效率上优于 IPsec（ResearchGate）；与 OpenVPN 对比长期稳定在约 3 倍吞吐（ACM 性能对比论文）。真实硬件基准（Protectli，2025-09）：无风扇设备上 900 Mbps–4.15 Gbps。注意公允结论：WireGuard 优势在于击败「慢实现」，内核态优化过的 IPsec 可一战。
- **隐私注意点**：对等端公网 IP 常驻内存（UDP 端点保活），对匿名性场景是弱点；对 netroamer 的 Tailscale DERP 直连增强场景无实质影响。
- **对 netroamer 含义**：Tailscale 底层即 WireGuard；「Tailscale DERP 直连」规则增强的安全边界清晰——不触碰密钥、只优化路由，是低风险、高收益项。

来源：ACM WireGuard vs OpenVPN 对比论文（https://dl.acm.org）；Protectli 基准（https://protectli.com，2025-09）；CyberInsider "WireGuard in 2026"（https://www.cyberinsider.com）。

### 1.2 QUIC / HTTP3

- 核心增益：**连接迁移**（Connection ID 标识连接，Wi-Fi/蜂窝切换不断流）、**0-RTT** 快速重连（注意 0-RTT 数据可重放，不能承载非幂等请求）、**多流独立传输**消除 HTTP/2 队头阻塞。
- 弱网实测（国内生产案例，2025-03，Jack Jiang 随笔）：弱网+非连接复用场景核心接口成功率 +1.15%、耗时 −14ms——收益真实但非数量级，价值在尾部体验与移动场景。
- **对 netroamer 含义**：产品自身不传输遥测时可优先 DoH over HTTP/3；若未来做习惯学习型「网络体检」，探测脚本应区分 QUIC 可达性与 TCP 可达性（大陆对境外 UDP 443 的 QoS/阻断会显著影响 QUIC 体验，这是大陆特有的弱网成因）。

来源：QUIC 移动端弱网优化实践（https://blog.csdn.net，2024-04）；弱网实测数据（https://blog.csdn.net/jiangjack，2025-03，搜索摘要口径）。

### 1.3 MASQUE

- 标准族：RFC 9298（CONNECT-UDP）、RFC 9484（CONNECT-IP），构建于 HTTP/3/QUIC 之上，本质是「用标准 HTTP 语义做代理」。
- **Apple**：iCloud Private Relay 采用 HTTP/3 + MASQUE 隧道（对非 Safari 流量走 CONNECT-IP/CONNECT-UDP），叠加 OHTTP（RFC 9458，Oblivious HTTP）实现「入口知道你是谁但不知道去哪、出口知道去哪但不知道你是谁」的两跳架构；Apple 于 2024-08 在 Swift 中官方提供 OHTTP 支持（swift.org 文章 "Introducing Oblivious HTTP support in Swift"）。Cloudflare 的 Oxy（Rust）承接其出口代理（Cloudflare 博客）。
- **Google**：Chrome 支持 MASQUE 代理；VPN by Google One（2024 年停服）曾采用 MASQUE；协议主要作者 David Schinazi（先 Google 后 Apple）。
- 2025–2026 年**未检索到新的大型采用公告**（搜索质量差，多数结果无关，如实标注）。
- **对 netroamer 含义**：MASQUE 是「穿透企业防火墙友好（长得像 HTTPS）+ 弱网稳健」的远程访问演进方向；产品若未来提供自建远程开发隧道，CONNECT-IP 是比裸 WireGuard 更易穿透受限 NAT/防火墙的选择，但大陆对境外 QUIC 的干扰会削弱其优势，需留 TCP 回退。

来源：RFC 9298/9484/9458（https://www.rfc-editor.org）；TidBITS Private Relay 架构（https://tidbits.com，2021-06，架构仍有效）；swift.org OHTTP（2024-08）；Cloudflare Oxy（https://blog.cloudflare.com）。

### 1.4 TLS 1.3 与抗量子迁移（X25519MLKEM768）

- **标准化**：NIST FIPS 203（ML-KEM）2024-08 发布；TLS 混合组 X25519MLKEM768 注册码点 0x11EC（替代早期 X25519Kyber768Draft00）。
- **浏览器默认启用**：Chrome（BoringSSL，2024 年中从 Kyber draft 切到 ML-KEM，约 124/131 版本线完成默认灰度）→ 默认；Edge 随 Chromium；Firefox 132 起默认。
- **服务端/CDN**：Cloudflare 自 2022 年（Kyber 时代）起全量支持，现为默认开启；搜索摘要口径称 2025 年底其边缘 TLS 1.3 流量过半使用 PQ 混合组（建议复核 Cloudflare Radar https://radar.cloudflare.com 的 Post-Quantum 面板）。全球 TLS 1.3 连接中约 30–50%+ 协商该组（口径依测量源浮动）。
- **剩余缺口**：(1) PQ **签名/证书**仍未规模部署（当前仅密钥交换抗量子，认证仍经典）；(2) 约 1.6KB 的超大 ClientHello 使部分老旧企业防火墙/SSL 检查设备断连（Chrome 为此保留了降级路径）。
- **对 netroamer 含义**：不需要自建 PQ 服务，但探测/升级检查等自持服务端点应确认支持 X25519MLKEM768（Go 1.24+/OpenSSL 3.5+ 均已支持）；网络体检工具可把「PQ 协商是否成功」作为诊断指标之一。

来源：LogicWeb "Post-Quantum TLS 2026: ML-KEM vs Your Certificate"（https://www.logicweb.com，2026）；Cloudflare Radar；NIST FIPS 203（https://csrc.nist.gov，2024-08）。

### 1.5 加密 DNS（DoH/DoT/DoQ）与大陆可用性

| 服务 | DoH | DoT | 大陆可用性 |
|---|---|---|---|
| 阿里 AliDNS | `https://dns.alidns.com/dns-query`（223.5.5.5/223.6.6.6） | `dns.alidns.com:853` | 境内备案，直连可用，低延迟 |
| 腾讯 DNSPod | `https://doh.pub/dns-query`（119.29.29.29） | `dot.pub:853` | 境内备案，直连可用，低延迟 |
| 360 | `https://doh.360.cn/dns-query` | — | 境内可用 |
| Cloudflare 1.1.1.1 | `https://cloudflare-dns.com/dns-query` | `one.one.one.one:853` | **不可用**：SNI 阻断（TLS+QUIC），UDP 53 被污染；触发后残余封锁可达约 360s |

- **关键定性**（AdGuard Home 自建教程与《DNS加密、认证与隐私保护》2024-11 一致结论）：DoH/DoT 解决的是**链路劫持/篡改**（ISP 明文劫持），不解决上游污染——境内 DoH 对被封锁域名同样返回污染结果；防污染需分流（境内域名走阿里/腾讯，境外域名经代理解析）或本地 dnscrypt-proxy/mosdns。
- **对 netroamer 含义**：把系统 DNS 改为 223.5.5.5/119.29.29.29 时，建议直接写 DoH/DoT 端点（macOS 加密 DNS profile / mihomo `nameserver`），并在文档中明确「加密 DNS ≠ 防污染」；不要推荐 1.1.1.1 等境外加密 DNS 作为默认值。

来源：DNS 加密与隐私保护（https://blog.vsar.site，2024-11）；AdGuard Home 自建教程（https://jared.top 等多个教程，搜索摘要）；Lantern circumvention-corpus（https://corpus.lantern.io，SNI 阻断与 360s 残余封锁测量）；OONI DoH/DoT 阻断测量论文（https://ooni.org）。

---

## 2. 本地代理链路安全（Clash/mihomo 生态事故簿）

### 2.1 事故与漏洞时间线

| 时间 | 事件 | 根因 | 来源 |
|---|---|---|---|
| 2023-01 | Clash for Windows ≤0.20.12 RCE | 恶意订阅中 `rule-providers.path` 不安全处理 → 任意路径写文件 → RCE | https://cn-sec.com（漏洞速递 + POC） |
| 2024-07 | CFW 开放代理端口（7890）滥用分析 | 代理监听 0.0.0.0 / API 无鉴权 → 被局域网/公网当免费跳板 | https://aajax.top/2024/07/24/ExploitingCFW |
| 2024-12 | clash_for_windows_pkg 订阅 RCE 复现 | 同上：攻击者托管恶意订阅，更新订阅即触发 | https://cn-sec.com（复现文） |
| 2025-04-27/29 | Clash Verge Rev 本地提权 | 服务组件缺陷，Mac/Linux 可提权至 root | https://www.nodeloc.com；https://v2ex.com（kasusa 帖） |
| 2025-04/05 | **Clash Verge 1-Click RCE**（Goby 团队 4-28 与 5-19 两次披露） | 默认在 `127.0.0.1:9097` 开 RESTful API 且 CORS 配置缺陷 → 恶意网页跨源调用 API 改写 Mihomo 配置 → 绕过路径检查任意文件写 → 借插件/扩展加载机制 RCE；FOFA 测绘显示数万台暴露 | https://nosec.org/m/share/5885.html；https://bbs.kanxue.com/thread-286909.htm；https://www.ctfiot.com/247262.html |
| 2025 | 订阅覆盖 external-controller secret（clash-verge-rev#1783） | 导入订阅时未遵守用户设定的 secret → 越权访问 API；有公开扫描工具 ClashScan | https://github.com/clash-verge-rev/clash-verge-rev/issues/1783 |
| 持续 | clash-verge-service 设计问题 | service 启动目标不校验（可拉起任意程序）、GUI↔service 无鉴权、端口暴露局域网 | https://github.com/clash-verge-rev/clash-verge-service/issues/10 |
| 2023-11 | Clash for Windows 作者 Fndroom/Fndroid 删库（11-02「江湖再见」），随后 Dreamacro 删除 clash/clash premium 核心 | 社区普遍归因于监管压力（见 §4）；生态由 mihomo（Clash.Meta）等 fork 延续 | https://bbs.imoutolove.me（2023-11-02）；Telegram 频道存档 |

**未找到**：mihomo 就上述问题被分配的正式 CVE 编号（检索 NVD 无命中）；相关漏洞主要走 GitHub issue/社区披露。mihomo 官方 wiki 已注明：Unix socket / Windows named pipe 方式的 API **不校验 secret**，且 `/configs` API 的 `path` 参数已限制在 workdir/SAFE_PATHS 内（https://wiki.metacubex.one/config/general）。

### 2.2 攻击面模型（对 netroamer 直接适用）

1. **external-controller 三宗罪**：绑 `0.0.0.0` 无 secret（公网/局域网直取配置、白嫖流量、改规则）；CORS 缺陷使「访问恶意网页」即等同本机 API 调用（1-Click RCE 的本质是浏览器跨源 + 无 token 的本地 REST API）；订阅可覆写 secret/绑定地址（配置投毒）。
2. **恶意订阅 = 不可信输入**：字段可含 rule-providers path、external-controller、脚本（script）等危险面；订阅链接本身就是高价值追踪目标。
3. **mixed-port 明文 HTTP 代理被本机滥用**：7897/7890 是无认证 HTTP/SOCKS 混合端口——本机**任意**进程（包括恶意软件）可把流量塞进来借道出网（回联、绕过防火墙出站规则），或把系统代理指向自身做流量记录/改写；若监听非环回地址则整个局域网可用。缓解：只绑 127.0.0.1、`authentication` 用户名密码、`skip-auth-prefixes` 限定环回、`lan` 关闭、防火墙限制端口属主。
4. **API 鉴权被偷跑流量的现实案例**：机场用户 API 未鉴权被扫描改配置「偷跑」流量（https://bulianglin.com/archives/clashapi.html）。

### 2.3 系统代理 vs TUN 的安全/稳定性权衡

- **系统代理**：应用层、侵入性小；只覆盖认代理设置的程序（浏览器、认 `http_proxy` 的 CLI）；不触碰路由表与 DNS，出错面小。**安全上更优**：明文 HTTP 代理的暴露面被限制在「主动遵从代理设置的应用」。
- **TUN**：虚拟网卡接管默认路由，覆盖终端/游戏/UDP 等全部流量，配 fake-ip 做 DNS 劫持；代价：异常退出可致默认网关丢失断网（需路由恢复自愈——正是 netroamer 看门狗的价值点）、与零信任客户端（aTrust 等）虚拟网卡路由冲突、LAN 打印机/NAS 误入代理需绕过规则、Windows 依赖 Wintun 驱动。
- **对 netroamer 含义**：产品以「增强既有 Clash/mihomo 配置」存在，应**默认不动用户的 TUN 决策**；写入的规则须保证 `LAN/CGNAT/环回` 段 DIRECT（对应 §3.5）；看门狗自愈逻辑须包含「代理进程死亡 → 恢复系统代理为空」而非只重启代理，否则用户断网。

来源：Clash Verge Rev TUN/系统代理教程（https://haiwaijichang.online，2026-09）；SkyPick Wintun 故障指引；jichang 系教程（搜索摘要汇总）。

---

## 3. netroamer 自身安全设计

### 3.1 一键脚本（curl | bash）供应链

风险：脚本服务器被攻破、域名过期被抢注、下载链路 MITM、非交互式管道的部分写入被盲执行；同类「安装时投毒」已有大量先例（AUR eBPF rootkit 供应链事件等）。

缓解（按优先级）：
1. **两段式安装为默认**：`curl -fsSLO` → `sha256sum -c` → `bash`，而不是直接 `curl | bash`；保留 `curl | bash` 快捷模式但在脚本内打印即将执行的动作摘要（可审计）。
2. **校验链必须完整**：`SHA256SUMS` 文件本身用 GPG 或 Minisign 签名，公钥指纹放在 README/Release 页/（理想）独立域名——只校验和、不校验 checksums 文件的来源等于没校验。
3. **版本锁定**：安装器默认拉固定版本 tag，`latest` 仅作显式 opt-in；CI 中 pin 工件哈希。
4. **先审后装**：提供 `--dry-run` / 「打印脚本并退出」模式（参考 Nullify CLI 安装器模式）。
5. **HTTPS 必要但不充分**：传输加密不等于内容可信。
6. **镜像（gh-proxy）只用于加速、永不用于信任**：gh-proxy 类前缀代理是**不可信第三方**，可任意篡改 Release 二进制（已有 ghproxy 劫持 + 自签证书 MITM 案例）；凡经镜像下载的文件必须以**来自官方 HTTPS 渠道**的哈希/签名校验，校验值不得经同一镜像获取；优先清华 TUNA/中科大 USTC 等权威镜像或直接经用户既有代理端到端 TLS 访问 GitHub。

来源：boot.dev 数据完整性密码学指南；Checkmarx 安装时恶意脚本分析；CSDN ghproxy 劫持案例（搜索摘要）。

### 3.2 看门狗 / LaunchAgent 最小权限

- **用户级 LaunchAgent 优先于 root LaunchDaemon**：netroamer 操作的对象（用户 shell 配置、用户目录内 mihomo 配置、通知）全部无需 root。注意一个 macOS 陷阱：新版 launchd **不拒绝** LaunchAgent 里写 `UserName: root`（不像 systemd），容易误写——绝不设置。
- **自愈用 launchd 原生能力**：`RunAtLoad: true` + `KeepAlive`（或 `KeepAlive.SuccessfulExit`），不要自己写 while-true 循环——launchd 以 root 运行且更可靠，看门狗本体无需特权。cron 仅作为 Linux 侧回退，写入用户 crontab（非 /etc）。
- **文件权限**：plist（`~/Library/LaunchAgents/`）600/644；脚本与所在目录禁止组/其他用户写（防 plist/二进制劫持）；重定向脚本若自更新，更新链路走 §3.1 校验。
- **防劫持纵深**：二进制 codesign + `launchctl` 加载时校验（有条件用 SMAppService + LaunchConstraints）；脚本内不 eval 远程内容；日志落 `~/Library/Logs/` 并轮转。
- **可卸载性是安全特性**：提供 `netroamer uninstall` 一键移除 LaunchAgent/cron/环境变量/DNS 改动并回滚备份——用户可干净退出，也降低「软件残留」投诉与安全软件误报（LaunchAgent 持久化是 macOS 恶意软件最常见持久化向量，MITRE T1543，会被安全软件盯上，签名与清晰命名很重要）。

来源：MITRE ATT&CK T1543（launchd 持久化）；all-smi 文档（launchd 对 root UserName 的行为，lib.rs）；openclaw-scheduler plist 模式（GitHub）；Apple launchd.plist man page。

### 3.3 遥测最小化与习惯数据隐私（AI 习惯学习演进的前提）

**原则：能不出设备就不出设备；出设备必先匿名化/加噪。**

- **不采集（黑名单）**：完整 URL/域名级浏览历史、代理节点身份/订阅内容、Wi-Fi SSID/位置、硬件序列号、Apple ID/账号、可回溯的原始时间线。
- **本地允许（默认）**：端口连通性/延迟时间序列、DNS 解析成败与耗时、包管理器镜像下载速率、直连/代理判定命中率——全部落在本地 SQLite/JSONL，仅本进程可读（用户目录 700）。
- **匿名化（如未来上云）**：域名截断为主域 + 一次性哈希盐（盐不落盘）、时间戳粗化到小时、IP 只保留 ASN/国家、k-匿名聚合（桶内 <k 台设备不出数）。
- **LDP/联邦学习适用性**：学术共识模式是「设备端本地差分隐私（LDP）加噪后才出设备」或「只上传模型更新/聚合梯度」（arXiv LDP 综述；LDP-AIMD：代理间无需通信即可获得 DP 保证，2023；联邦学习用于 DNS 遥测恶意域名检测，ScienceDirect 2026）。**对 netroamer 的现实建议**：习惯学习模型（如「该进程该时段走直连还是代理」）完全可以在设备端训练（数据量小、模型轻），上云学习在当前阶段不必要也不值得承担的隐私/合规成本；若必须，走 LDP 噪声 + 聚合，而非原始遥测上传。
- **用户控制权**：遥测默认关或首次显式 opt-in；一键导出/删除本地数据；隐私声明明确列出「不回传清单」。

### 3.4 泄漏检测点与 kill switch

netroamer 作为「网络体检」功能应内置检测（用户可直接一键自检）：

1. **DNS 泄漏**：本机解析是否绕过预期 DNS（比对 `dig +short whoami.ds.akahelp.net` 类回显解析器结果与预期出口）；mihomo 场景确认 `dns.enhanced-mode`/劫持生效；**加密 DNS ≠ 防污染**要写进诊断输出。
2. **WebRTC 泄漏**：VPN/代理下浏览器 STUN 仍可暴露真实 IP——VPN 类工具无法根治，须浏览器层禁用/限制 WebRTC；检测页给出浏览器修复指引（wizcase/Surfshark 指南一致结论）。
3. **IPv6 泄漏**：代理栈不承载 IPv6 时，`test-ipv6.com`/Comparitech 检测会暴露 v6 真实地址；最可靠处置是在工具内提供「禁用/封堵 IPv6」选项（系统级或防火墙级），且每次配置变更后复测。
4. **kill switch 实现层级**：应用级 toggle 最弱；**防火墙级最强**——macOS 用 pf（anchor 规则：仅允许 mihomo 进程出站、block all else）、Linux 用 iptables/nftables/ufw（输出链按 uid/cgroup 限流）。对 netroamer：不默认替用户装 kill switch（侵入性高），但提供「检查系统代理与进程存活一致性」的自愈项：代理进程死亡时清空系统代理设置（这本身就是一种应用级 kill switch，防止「无代理却仍指向 127.0.0.1:7897」的全断网态）。
5. 验证闭环：参考 Comparitech/ipleak.net 多合一检测（IPv4/IPv6/WebRTC/DNS），任何配置变更后自动复测。

来源：Comparitech DNS leak test；PulseVPN kill switch 指南（https://pulsevpn.dev）；wizcase/Surfshark WebRTC 指南（2025-01）；Reddit r/netsec 70 家 VPN 实测（IPv6 泄漏普遍）。

### 3.5 NO_PROXY / 代理环境变量与内网防护（重点）

风险链：netroamer 给用户写入 `http_proxy/https_proxy=127.0.0.1:7897` 后，**所有**认环境变量的工具（git、go、npm、curl、各语言 SDK、AI CLI）的出站流量都过代理；若 `NO_PROXY` 不完整，本应直连的内部流量会被送进 mihomo，按规则引擎判定——被送出公网节点即等于**内网流量泄漏给代理出口**，同时引入可用性故障（代理挂了内网也断）。

必须显式覆盖的 NO_PROXY 段（业界漏洞教训）：

```
NO_PROXY=localhost,127.0.0.0/8,::1,.local
        10.0.0.0/8,172.16.0.0/12,192.168.0.0/16      # RFC1918
        169.254.0.0/16                                # 链路本地，含云元数据 169.254.169.254
        100.64.0.0/10                                 # CGNAT/Tailscale（*.ts.net 通常可解析为 100.x）
        198.18.0.0/15                                 # mihomo fake-ip 段（若 TUN 开启）
        [::ffff:127.0.0.0]/104                        # IPv6-mapped 环回
```

- **100.64.0.0/10 是最大盲区**：大量实现只认 RFC1918。真实先例：BentoML 的 SSRF 修复被 100.64.0.0/10 绕过（GitHub issue，2026-06）。netroamer 主打 Tailscale 场景，CGNAT 段必须一等公民。
- **工具侧差异要写文档**：各语言对 NO_PROXY 的解析不一致（有的只支持域名后缀、有的支持 CIDR、Go 支持有限通配），**不能只靠环境变量**——mihomo 侧规则兜底：`IP-CIDR,127.0.0.0/8,DIRECT`、`IP-CIDR,100.64.0.0/10,DIRECT`、`IP-CIDR,10.0.0.0/8,DIRECT` 等内置为不可删规则；对内网域名（`.internal`、`.lan`、`*.ts.net`）直接 DIRECT。
- **变体覆盖**：`localhost`、`LOCALHOST`、裸 `127.0.0.1`、机器主机名各工具解析行为不同，NO_PROXY 同时写 `localhost` 与 `127.0.0.1`。
- 深层防护：工具（git/go）校验目标时先解析 IP 再比对 CIDR，而非字符串匹配主机名——这也是 SSRF 防护的标准结论（TSRC SSRF 指北）。

来源：BentoML CGNAT SSRF bypass（https://github.com，2026-06-29）；TSRC《SSRF 安全指北》（https://security.tencent.com）；OneUptime CGNAT 说明。

---

## 4. 合规风险评估（如实陈述）

### 4.1 法律框架

- 《计算机信息网络国际联网管理暂行规定》（国务院令第 195 号，1996，仍有效）：单位/个人必须使用国家公用电信网提供的国际出入口信道；第六条/第十四条——「擅自建立、使用非法信道进行国际联网」由公安机关责令停止联网、**警告、可并处罚款（一般不超过 15000 元）**。
- 《电信条例》：经营性网络服务需许可；向他人提供翻墙服务/共享账号可依治安管理处罚升至**行政拘留（最高 15 日）+ 罚款**（htstack 等汇总口径）。
- 刑事层面：开发、销售代理/翻墙工具或提供服务，司法实践中有以**提供侵入、非法控制计算机信息系统程序、工具罪**追责的判例（典型如「刘冰洋案」：搭建 55 个境外代理服务器、发展 4091 客户、获利约 34.26 万元获刑；williamlong 2018 年报道）。学界 2025 年讨论（西南政法大学学报，2025-03《网络有组织犯罪中帮助行为的规范归责》）主张对**单纯提供软件者**限制刑事打击、以行政处罚为主——但这是学理观点，非司法承诺。

### 4.2 先例与生态事实

- 2023-09-30 起中国区 App Store 强制 ICP 备案，代理类应用集中下架；2023-11-02 Clash for Windows 作者删库、随后 Dreamacro 删除 Clash 核心与 Premium 仓库（bbs.imoutolove.me 2023-11-02；Telegram 频道存档），社区普遍归因于监管压力（作者本人未公开说明原因——**未找到**作者官方声明，如实标注）。
- 生态以 fork 延续：mihomo（Clash.Meta）、FlClash 等；2025–2026 年 Clash 类客户端经非大陆区 App Store/非官方渠道仍可获得。
- 企业现实：企业经审批使用国际出入口信道/租用专线（跨境专线备案）是合法通道；企业内开发者自用代理工具是普遍现实但处于灰色地带（**未找到** 2024–2025 年公开的新判决原文：裁判文书网检索受限且部分搜索查询被内容过滤拦截，以下量级判断基于上述既有框架与历史案例）。

### 4.3 对 netroamer 产品形态的含义

风险量级排序（低 → 高）：**本地开源脚本（不改网络出口本质）< 分发代理内核/二进制 < 运营节点/订阅服务**。netroamer 当前与规划形态落在最低档，但有三条红线要在工程与措辞上主动守住：

1. **不分发代理内核与节点**：不捆绑 mihomo 二进制、不内置任何订阅源/节点/机场信息、不提供「一键接入境外节点」类功能。产品定位严格收敛为「用户**既有**本地网络环境（用户自装的 Clash/mihomo/Tailscale）的检测、镜像加速与直连规则优化」——netroamer 写入的规则（AI 工具进程直连、DERP 直连、国内镜像、国内 DNS）方向是**直连/境内优化**，不涉及构造翻墙信道，这是合规叙述的关键事实。
2. **措辞纪律**：README/官网/发布文案避免「科学上网」「突破封锁」「永久免费节点」类表述与任何机场/节点推广（含返利链接）；文档明确「本工具不提供网络接入服务、不代理任何流量、不收集上传用户数据」。
3. **分发形态**：源码 + 可审计脚本分发优于编译二进制分发（前者与「工具开发」的距离更远、且 §3.1 的验签链路本身就是尽调证明）；不上架大陆应用商店；如接受捐赠/商业化，避免与代理服务经营绑定（经营性指控会把风险从「工具」抬到「服务」）。
4. **残余风险如实告知**：即便守住上述边界，「主动探测并增强 Clash/mihomo 配置」仍与代理生态强关联，存在被连带波及的不确定性（2023 删库潮证明了「未违法但有压力即消失」的现实可能）；产品应假设「仓库可能随时消失」：镜像发布渠道（自建、Codeberg 等）、用户本地可完整卸载、无服务端依赖——netroamer 的本地化架构恰好天然满足。

---

## 5. netroamer 安全加固 Checklist

### 供应链与分发
- [ ] 安装脚本默认两段式：下载 → `sha256sum -c`（SHA256SUMS 经 GPG/Minisign 签名）→ 执行；保留 `curl | bash` 但内嵌动作摘要打印
- [ ] 版本锁定安装（`netroamer install vX.Y.Z`），`latest` 需显式 opt-in
- [ ] `--dry-run` / `--print-script` 先审后装模式
- [ ] gh-proxy 镜像仅加速用途；凡经镜像的文件必须用来自官方 HTTPS 渠道的哈希校验，校验值不经同一镜像分发
- [ ] 发布物存多渠道镜像（假设仓库可能消失）；文档不含任何节点/机场/翻墙措辞

### 代理链路（写 Clash/mihomo 配置时）
- [ ] 强制 external-controller 仅 `127.0.0.1`（或改用 Unix socket 并知晓其不校验 secret 的前提）+ 随机强 secret；检测并告警用户现存 `0.0.0.0` 配置
- [ ] 导入/合并用户订阅前做字段审查：拒绝/告警 `rule-providers.path` 越界、external-controller 改写、script 段
- [ ] mixed-port 仅绑 127.0.0.1，`lan: false`，考虑 `authentication`；检测监听面并告警
- [ ] 内置不可删 DIRECT 规则：127.0.0.0/8、10/8、172.16/12、192.168/16、169.254/16、**100.64.0.0/10**、198.18.0.0/15、`.lan/.local/.internal/*.ts.net`
- [ ] 不擅自开启/关闭 TUN；TUN 相关变更前备份路由与配置，失败可回滚

### 环境变量
- [ ] NO_PROXY 按 §3.5 全量段写入（含 100.64.0.0/10 与 169.254.169.254），同时写 `localhost` 与 `127.0.0.1`
- [ ] 文档说明各工具 NO_PROXY 解析差异；提供 `netroamer doctor` 校验当前环境变量与预期一致

### 看门狗/LaunchAgent
- [ ] 用户级 LaunchAgent（绝不设 root UserName），cron 仅 Linux 用户级回退
- [ ] 用 launchd `KeepAlive` 自愈，不写特权守护循环；脚本/目录权限最小化（非本用户不可写）
- [ ] 自愈包含「代理死亡 → 清空系统代理设置」分支（应用级 kill switch）
- [ ] `netroamer uninstall` 一键完整回滚（LaunchAgent/cron/env/DNS/配置备份）
- [ ] 脚本 codesign/哈希固定，防 plist 指向被篡改目标

### 遥测与习惯数据（AI 演进前置条件）
- [ ] 遥测默认本地化（本地 SQLite/JSONL，700 权限）；黑名单：URL/全域名、节点身份、SSID/位置、账号标识、原始时间线
- [ ] 上云（若做）：opt-in + 主域截断/一次性盐哈希 + 小时级粗化 + k-匿名聚合；习惯模型优先设备端训练；确需跨设备学习走 LDP 加噪/仅模型更新，不上传原始遥测
- [ ] 隐私声明含「不回传清单」；提供一键导出/删除本地数据

### 泄漏检测与诊断
- [ ] `netroamer doctor` 覆盖：DNS 泄漏（回显解析器比对）、系统代理与进程存活一致性、NO_PROXY 完整性、mihomo 配置危险字段扫描（secret 缺失/绑 0.0.0.0/路径越界）、PQ 协商探测（加分项）
- [ ] IPv6/WebRTC 泄漏：提供检测指引与（可选）IPv6 封堵选项；每次变更后引导复测（ipleak.net/Comparitech 类）
- [ ] DNS 建议默认写阿里/腾讯 DoH 端点；文档明确「加密 DNS ≠ 防污染」，不推荐境外加密 DNS 作默认值

---

## 主要来源索引（含日期）

- Cloudflare Radar Post-Quantum 面板：https://radar.cloudflare.com（持续更新；2025 年底过半口径见搜索摘要，建议复核）
- LogicWeb，Post-Quantum TLS 2026: ML-KEM vs Your Certificate：https://www.logicweb.com（2026）
- NIST FIPS 203（ML-KEM）：https://csrc.nist.gov（2024-08）
- ACM，A Performance Comparison of WireGuard and OpenVPN：https://dl.acm.org
- Lantern circumvention-corpus（中国 SNI 阻断 Cloudflare DoH/DoT、残余封锁 ~360s）：https://corpus.lantern.io
- DNS 加密、认证与隐私保护：https://blog.vsar.site（2024-11-18）
- nosec 漏洞预警，Clash Verge 1-Click RCE（数万设备）：https://nosec.org/m/share/5885.html（2025-05）
- 看雪，Clash Verge 1-Click RCE 逆向：https://bbs.kanxue.com/thread-286909.htm（2025）
- clash-verge-rev#1783（订阅覆盖 secret）：https://github.com/clash-verge-rev/clash-verge-rev/issues/1783
- clash-verge-service#10（服务启动不校验目标/无鉴权）：https://github.com/clash-verge-rev/clash-verge-service/issues/10
- cn-sec，CFW 订阅 rule-providers path RCE（含 POC）：https://cn-sec.com（2023-01-16；2024-12-05 复现文）
- Exploiting Clash for Windows – 开放代理有多危险：https://aajax.top/2024/07/24/ExploitingCFW（2024-07-24）
- bulianglin，Clash API 鉴权与流量偷跑：https://bulianglin.com/archives/clashapi.html
- BentoML SSRF bypass via 100.64.0.0/10：https://github.com（2026-06-29）
- TSRC《SSRF 安全指北》：https://security.tencent.com
- boot.dev，Achieving Data Integrity Using Cryptography（校验和须配合签名）：https://www.boot.dev（2020-05，方法论仍有效）
- RFC 9298（CONNECT-UDP）/ RFC 9484（CONNECT-IP）/ RFC 9458（OHTTP）：https://www.rfc-editor.org（2022–2023）
- TidBITS，iCloud Private Relay 架构：https://tidbits.com（2021-06）
- swift.org，Introducing Oblivious HTTP support in Swift：https://www.swift.org（2024-08）
- arXiv，A Comprehensive Survey on Local Differential Privacy：https://arxiv.org
- LDP-AIMD（联邦优化中的本地差分隐私）：https://www.researchgate.net（2023）
- Comparitech DNS Leak Test；PulseVPN kill switch 指南 https://pulsevpn.dev；wizcase 泄漏防护（2025-01-13）
- 《计算机信息网络国际联网管理暂行规定》（1996，国务院令第 195 号）；htstack 自建服务器风险汇总：https://www.htstack.com
- 西南政法大学学报《网络有组织犯罪中帮助行为的规范归责》（2025-03，搜索摘要）
- Clash 删库事件记录：https://bbs.imoutolove.me（2023-11-02）；Telegram 频道存档（2023-11）
