# 网络诊断功能能否成为 netroamer 的护城河？

> 调研日期：2026-09-28。调研员：网络工程 + 产品战略调研。信息来源以 2024–2026 公开资料为主，全部经 WebSearch/WebFetch 核实；未找到的信息明确标注「未找到」，不编造。
> 本报告只回答一个问题：给 netroamer（开发环境网络一键优化脚本 → AI 无感网络优化产品）加「多端多节点连接稳定性/速度 + 文件传输能力」诊断功能，能否形成护城河。

---

## 0. 结论先行

1. **「诊断」本身不是护城河，是普通功能。** 个人级测速/拨测工具严重同质化（mtr、Speedtest CLI、17ce、boce.com 全免费），开发者用完即走，无留存、无数据壁垒。
2. **诊断数据确实能形成壁垒，但前提是规模 + 双边市场。** 先例：Ookla（用户测速 → Speedtest Intelligence 数据卖给 ISP，测速认证/Award 授权变成 ISP 营销资产，2025-09 还推出 Speedtest Certified）、ThousandEyes（Cisco 2020 年以约 10 亿美元收购，靠全球 vantage point 网络卖「互联网路径可见性」）、Cloudflare Radar（自有流量数据做行业报告强化品牌）。共同点：**诊断点遍布全网（别人的网络），数据聚合后对第三方有价值**。单机个人工具的数据只对用户自己有价值，构不成这类壁垒。
3. **netroamer 真正可能的壁垒不在「诊断」而在「诊断→自动修复」的闭环 + 中国大陆开发者场景的本地化规则资产。** 行业趋势支持这个方向：DEM（数字体验监控）市场的主流卖点早已从「看见问题」进化为「定位问题 + 自动动作」（ThousandEyes/Obkio 的 path visualization + alerts，AIOps 化）。诊断是修复的输入，修复才是用户价值。
4. **多端多节点领域，传输/聚合的底层技术已被大厂和标准组织吃掉**：MPTCP（Apple Siri 自 2016 年起生产部署）、MPQUIC（IETF working group draft，尚无大规模生产部署）、802.11k/v/r + OpenRoaming（WBA 2025 报告：81% Wi-Fi 高管计划 2025/2026 部署 OpenRoaming）、Speedify/Peplink 做商业聚合。小团队不要碰协议层，要碰「场景编排层」。
5. **文件传输加速已被反复验证是可收费的卖点**：IBM Aspera FASP、Signiant、MASV（~$0.25/GB）、Resilio——但付费方是媒体/影视/企业，不是个人开发者。个人侧 Syncthing/LocalSend/croc 免费且够用，别在这里找商业模式。
6. **大厂碾压风险清单**：Tailscale（2025-04 完成 $160M Series C、估值 $1.5B，正在加速吞掉 mesh 网络的可见性功能）、Apple（MPTCP/MASQUE/Continuity）、华为/小米互传联盟（2026-09 发布「碰一碰互传」跨品牌标准）、Syncthing（开源免费同步）。netroamer 应避开「通用传输/通用组网」，守「中国大陆开发环境自愈」这个大厂不服务、开源项目不管用户体验的缝隙。
7. **最终建议：值得做，但只做「诊断作为修复的传感器」，不做「诊断作为产品」。** 具体见 §5。

---

## 1. 网络诊断工具全景

### 1.1 个人 / 开发者级

| 工具 | 类型 | 定位与现状 | 商业模式 |
|---|---|---|---|
| mtr / WinMTR | 路径诊断 | ping+traceroute 持续组合，免费开源，是路径类诊断的事实基线（netadmintools.com 2025-08 的 PingPlotter 替代品榜单以其为参照） | 免费 |
| PingPlotter | 路径可视化 | 每跳延迟/丢包的时间轴可视化，个人版收费，口碑好但小众 | 订阅 |
| Ookla Speedtest CLI | 测速 | 事实标准的测速协议；Speedtest Global Index 每月发布各国固宽/移动中位数排名（speedtest.net）。**注：未检索到 Ookla 官方 2025 年中国分季度专项报告的公开全文**，仅第三方转述（维基百科「各国互联网速度列表」：中国固宽中位数约 206.91 Mbps；5G SA 下载中位数 224.82 Mbps） | 免费 + B 端数据服务 |
| Cloudflare Speed Test | 测速 | 测速 + 网络质量评分（延迟抖动），无安装、浏览器即用 | 免费，为 Cloudflare 品牌与 Radar 数据服务 |
| Cloudflare Radar / Internet Resilience | 公开数据 | Radar 2025 Year in Review：全球流量 +19%，BGP 配置错误/路由泄露仍是国家级断网主因，IPv6 采用率持续爬升（radar.cloudflare.com，2025-12） | 免费（数据资产品牌化） |
| NetSpot / WiFi Explorer | Wi-Fi 物理 | 站点勘测热力图 / 频谱信道扫描，Mac 生态口碑工具，个人付费 | 买断/订阅 |
| GlassWire | 桌面流量可视化 | Windows 桌面防火墙可视化 + 流量追踪，个人订阅约 $2.99/月起（iTechGuides 2026 数据用量监测横评），入门版历史数据仅保留 1 天 | 订阅 |
| Netdata | 服务器实时监控 | 开源、秒级高分辨率指标，面向服务器/容器；2025-09 仍有活跃评测（ittsystems.com 2025-09-04），商业版走 Netdata Cloud | 开源 + 云订阅 |
| 17ce | 中国拨测 | 国内节点实时测速（ping/dns/http/cdn）+ 服务器监控 + IDC 质量评测，2025 年仍在运营（官网 2025-08 动态），走「节点共享/路由器收益」众包模式 | 免费 + 增值 |
| boce.com（拨测） | 中国拨测 | 帝恩思（DNS.COM 旗下）免费在线评测：网站测速、DNS 测速、IPv6、海外测速；2025 年移动端改版上线 | 免费 + 增值 |
| 阿里云网站运维检测平台 | 中国拨测 | boce.aliyun.com，全球 200+ 拨测节点，http/dns/ping/tcp/udp 定时拨测，云厂商捆绑 | 云计费 |

**小结**：个人级诊断 = 完全商品化。功能可以一周写完，差异只在 UI 和中国节点覆盖。作为 netroamer 的功能可以做，作为产品方向没有价值。

### 1.2 专业级（企业为什么付费）

| 厂商 | 卖点 | 企业付费的真实原因 |
|---|---|---|
| Cisco ThousandEyes | 互联网路径可见性（hop-by-hop，含「不属于自己的网络段」：ISP/云/SaaS 路径）、全球 vantage point 代理网络 | 出故障时能证明「问题在运营商/SaaS 而不是我」，支撑 SLA 索赔与甩锅链；SD-WAN/SaaS 时代的外部依赖监控刚需。Cisco 2020 年收购后与 Meraki/Webex 深度绑定 |
| Catchpoint | 纯 DEM：DNS/CDN/API/web synthetic + last-mile + heartbeat（最快 5 秒一测） | 电商/金融对「用户侧数字体验」的实时告警。**2025-12 被 LogicMonitor 收购**，说明独立 DEM 已难支撑，平台化整合是趋势 |
| Kentik | 网络流量遥测（NetFlow/IPFIX）+ BGP + 云网络分析 | 大规模/复杂网络团队的流量分析与容量规划，比 synthetic 更深的 observability |
| Obkio | 轻量分布式 agent 的持续 NPM，定位「买得起的 ThousandEyes 替代」 | 中小企业要 80% 的功能、30% 的价格；证明该市场正在分层下探 |
| PRTG (Paessler) | 传统 SNMP 全栈监控，老牌、license 按传感器数 | 运维存量市场，与「互联网体验诊断」关系较弱 |

来源：catchpoint.com、obkio.com、PeerSpot/Slashdot 对比页、LogicMonitor 收购公告（2025-12）、Techmeme/Osler（Tailscale 融资，2025-04-08）。

**关键洞察**：企业买诊断工具买的是（a）责任界定（谁的锅）、（b）MTTR 缩短、（c）覆盖自己没有 vantage point 的网络段。这三样个人开发者一件都不需要——个人开发者要的是「别坏」「坏了自动好」。这决定了诊断功能对 netroamer 的正确形态。

### 1.3 公开诊断数据说明什么

- Ookla 数据被 ISP 用于营销（Netvigator 六项 Speedtest Award、AT&T 2026-02「Best Home Internet」奖、Fidium 县级排名广告）；2025-09 推出 Speedtest Certified™ 网络认证项目。**诊断数据 → 评级/认证 → 向被评方收费**，是诊断数据变现的最成熟路径（speedtest.net、stocktitan.net 2026-02）。
- Cloudflare Radar 用自有边缘流量做行业叙事（outage/BGP/IPv6/AI bot 流量），直接强化「Cloudflare 看得见整个互联网」的品牌认知（blog.cloudflare.com / radar.cloudflare.com，2025-12）。
- 中国侧（17ce/boce.com）的数据变现远弱于 Ookla，主要靠增值功能和 ICP 备查等周边服务；未找到两者披露的规模化 B 端数据收入证据。

---

## 2. 多端多节点通信

### 2.1 Tailscale DERP 中继

- Tailscale 官方性能页原话：**"Direct connections nearly always result in lower latency and higher throughput"**——DERP 是 NAT 打洞失败后的兜底，不是常态路径（ssdnodes.com 2026-09 引述）。
- 实测案例：直连 WireGuard 性能≈裸 WireGuard；回退 DERP 后延迟跳到约 180ms（saaspedia.dev，2026-02）。DERP 中继普遍带来 2–3 倍以上延迟恶化与吞吐上限。
- 中国语境：Tailscale 官方 DERP 在中国大陆无节点，国内用户常态落在美/日/新 DERP 上，延迟严重劣化——这正是 netroamer「Tailscale DERP 直连增强」规则的现实价值，也是可诊断化的高价值信号源（检测 `tailscale status` 中 relay 连接并告警/引导自建 DERP）。
- **可复用件**：DERP 服务端开源（tailscale/derp），可自建；`tailscale netcheck` 命令已内置 NAT 类型/DERP 延迟探测——诊断的原材料是现成的。

### 2.2 NAT 打洞类型检测（STUN）

- 经典四型 NAT（Full/Restricted Cone/Port-Restricted/Symmetric）检测在 RFC 5389/8489 中已从「类型判定」改为「行为判定」（RFC 3489 的经典四型已被弃用），但诊断工具仍普遍实现旧四型口径。
- 开源可复用：STUNTMAN（jselbie/stunserver，含 stunclient）、coturn（WebRTC 生态标配）、**natmap（heiher/natmap）**——国内路由器社区（KoolCenter/梅林固件 2025-12 插件动态）正用它在 NAT1(Full Cone) 下打洞维持公网端口映射，说明「NAT 类型检测 + 打洞」在国内玩家群体是活跃需求。
- 结论：检测技术零门槛（一行 UDP binding request），无壁垒；价值在于把结果接进修复动作（如「Symmetric NAT → 引导开 UPnP/换网络/走 DERP」）。

### 2.3 QUIC vs TCP 弱网表现

- 2025 年文献总体一致：QUIC 靠 stream 独立（无队头阻塞）+ 更快的丢包恢复，在丢包链路上优于 TCP。但幅度高度依赖实现与场景：
  - dgway.com（2025-08-13）实测：软件实现下 TCP 与 QUIC 在 1–2% 丢包时都断崖式下跌；硬件/IP offload 的 QUIC 明显更抗丢包。
  - arXiv 2025（TCP over QUIC，2504.10054）：TCP-over-QUIC 在有损环境下吞吐显著高于原生 TCP（可达数倍）。
  - 反例：ResearchGate 研究发现 QUIC 在无线 mesh 网络中反而更差；Hacker News 基准显示内核态 QUIC vs 内核态 TLS+TCP，后者吞吐可达 3 倍（用户态 QUIC 开销）；一项视频 QoE 实证研究未发现 QUIC 带来用户可感知收益（Semantic Scholar）。
- 对 netroamer 的含义：**「QUIC 更快」不能作为无条件卖点**；诊断应按链路实测（丢包率 × RTT × 协议）给出建议，而不是贴协议信仰标签。

### 2.4 MPTCP / Multipath QUIC 现实部署

- **MPTCP（RFC 8684）是唯一大规模生产级多路径协议**：Apple 自 2016 年（CoNEXT 论文 "An In-depth Understanding of Multipath TCP on Mobile Devices"）在 Siri/Apple Maps 等场景部署，2024–2025 未检索到新的专项跟进研究；3GPP ATSSS（R16+）用 MPTCP + ATSSS-LL 做 5G/Wi-Fi 流量引导切换。
- **Multipath QUIC**：IETF QUIC WG 已采纳为 working group draft（draft-ietf-quic-multipath），2025-03 IETF 122 仍在激辩 packet number space 设计；生产部署「尚无大规模案例」，Cloudflare 公开表达兴趣但定位为 future work；学术实现活跃（UCLouvain、Media-over-MPQUIC、Flexicast QUIC）。
- Apple 的另一条线：iCloud Private Relay 用 MASQUE + HTTP/3/QUIC 做 QUIC-over-QUIC 双跳中继（apple.com 官方 Overview PDF，2021-12；Cloudflare/Fastly 技术博客）——证明大厂在做「应用层中继网络」，个人项目不可比。

### 2.5 多链路聚合与无缝切换

- **802.11k/v/r**：k（邻居报告）+ v（BSS Transition）+ r（Fast BSS Transition，把 802.1X 漫游从约 800ms 压到 <30ms）是 Wi-Fi 内无缝漫游的成熟三件套（Cisco/Meraki/Mist 官方文档）；Wi-Fi 8（802.11bn）把「无缝漫游」列为核心特性。
- **OpenRoaming**（WBA 联盟，基于 Passpoint/Hotspot 2.0）：WBA 2025 年度报告——**81% 的 Wi-Fi 行业高管计划在 2025/2026 部署 OpenRoaming**；但 Wayfi Wireless（2025-07）指出客户端工具链、文档、锁定问题仍拖慢落地。
- **商业聚合**：Speedify（软件 bonding，实时控制信道测丢包并重传，最容易部署，覆盖手机/电脑/路由器）、Peplink SpeedFusion、Bondix（waveform.com WAN bonding 横评）；开源侧有 OpenMPTCP 路由器方案。
- **成熟可复用件清单**：tailscale/derp、tailscale netcheck、stunclient/coturn/natmap、iperf3、OpenWrt mptcpd、OpenMPTCP；MPQUIC 可参考（mptcpanalyzer、mpquic 参考实现）但勿用于生产承诺。

**小结**：多端多节点的「协议层」已被 MPTCP/QUIC/标准组织解决或在解决；「基础设施层」（OpenRoaming、运营商）个人项目无法参与；剩下的可做空间是**「检测与编排」——告诉你现在该用哪条路、为什么慢、并自动切**。这恰是 netroamer 的形态。

---

## 3. 文件传输能力

### 3.1 高性能传输协议与产品

| 产品 | 技术 | 定位 | 付费方 |
|---|---|---|---|
| IBM Aspera (fasp) | UDP 大窗口 + 速率自适应拥塞控制（与 TCP 公平共存） | 企业级大文件远距传输 | 媒体/影视/医疗/企业，license 订阅 |
| Signiant (Media Shuttle / SDCX) | 私有 UDP 加速协议 | 广电/影视公司级传输平台 | 企业订阅（第三方估计典型配置约 $8,500/年，signiant.com 对比页） |
| MASV | 云中转 + TCP 优化 | 自助式按量付费 | 约 $0.25/GB（约 $250/TB），freelancer/后期团队；高量用户年成本可到约 $30,000（signiant.com 对比页） |
| Resilio Sync | P2P + 自有 uTP 类协议，1:N 分发极快（官方称 1:10 场景最高 500% 提速） | 企业同步/分发（Connect）+ 个人版 | 企业订阅；个人版免费层 |
| Syncthing | 开源 P2P 持续同步，BEP 协议 | 自托管同步 | 免费（性能受 CPU 加解密限制，官方 FAQ 明示） |
| LocalSend | LAN 内 AirDrop 式（HTTPS + mDNS 发现） | 一次性本地互传 | 免费开源；MakeUseOf 横评中快于 Blip/PairDrop |
| croc / rsync / rclone / tus / UDT / bbcp | 开源传输件 | 单发直传/同步/断点续传 | 免费（HN「Aspera 的自由软件替代品」社区共识清单） |

### 3.2 技术原理与增益证据

- 原理：高带宽时延积（BDP）链路上，TCP 吞吐受 Mathis 公式约束（吞吐 ∝ MSS/(RTT·√loss)），**0.01% 丢包 + 100ms RTT 就能把单条 TCP 压到几十 Mbps 以下**（Silver Peak 独立基准：1 Gbps 链路、150GB FTP 传输爬行）。UDP 型协议（fasp/Resilio）用速率控制绕开 TCP 拥塞窗口，因此「传输提速」的物理基础是真实的（Wikipedia: Fast and Secure Protocol；GTGI 渠道商声称 fasp 在 500ms RTT / 30% 丢包下跑满带宽）。
- **证据强度的诚实结论**：多 Gbps、几十倍提速的证据几乎全部来自厂商白皮书与营销（IBM/GTGI/Signiant），**独立同行评审的 fasp 对比研究未检索到**；IBM 官方社区论坛亦有 fasp 速率策略挤压其他业务流量的真实投诉。方向可信，数字打折。
- QUIC 路线：见 §2.3，QUIC 在有损链路上对 TCP 的优势真实但依赖实现；QUIC-based 传输（如 MASV 底层、rclone 的 quic backend）是渐进改良而非数量级跃升。

### 3.3 「传输提速」作为卖点被验证过吗？

**验证过，但付费方是 B 端媒体/企业，不是个人开发者。** Signiant 与 Aspera 存活并盈利多年、MASV 按 TB 收费有真实客户（影视后期、广告、广播）、Resilio Connect 卖给企业分发场景。个人侧：Syncthing/LocalSend/croc 免费、够用、口碑好——个人传输市场不存在「愿意为速度付费」的群体证据。**netroamer 若把传输提速当独立卖点，面向的人群（开发者）恰是不付费的人群。**

---

## 4. 战略判断

### 4.1 「诊断」对个人开发者工具：护城河还是普通功能？

**普通功能。** 论证：

- 供给侧零门槛：mtr/Speedtest CLI/netcheck/stunclient 全是现成开源件，任何竞品（Clash Verge、Mihomo Party 分支、镜像加速脚本）两周内可复制同款诊断面板。
- 需求侧低频浅层：个人开发者只在「坏了」时打开诊断，用完即走，不产生留存和付费。17ce/boce.com 免费且在国内节点覆盖上碾压任何新入者。
- 诊断数据积累能否转化为壁垒？**分两种：**
  - 「我的机器的数据」→ 对别人无价值，不构成数据壁垒。单机诊断数据没有聚合外部性。
  - 「全网 vantage point 的数据」→ 构成壁垒，先例确凿：Ookla（众包测速 → Intelligence 数据 + Award/Certified 认证向 ISP 收费，2025-09 推 Speedtest Certified）、ThousandEyes（全球探针 → Cisco 约 $1B 收购，2020）、Catchpoint（同路线，独立路线走不通后 2025-12 并入 LogicMonitor）、Cloudflare Radar（自有边缘流量 → 行业话语权）。**共同前提：诊断点覆盖「别人的网络」+ 数据聚合后产生第三方愿意付费的行业真相。** netroamer 理论上可以往这个方向长（每个国内开发者的机器都是中国 GFW 语境下的探测节点——这是一个全球厂商做不了、Ookla 不细做的数据集），但要清醒：这需要数万级装机量、用户授权与隐私设计、以及一个愿意为此付费的买方（国内云厂商/CDN/跨境 SaaS？目前未见公开先例证明此市场在国内成立）。**结论：长期可作为想象空间与 PR 叙事（如《中国开发者网络体验报告》），不能作为近期押注。**

### 4.2 哪些点会被碾过？哪些小团队反而能做深？

**会被碾过（不要做）：**
1. 通用 mesh 组网/中继网络——Tailscale 2025-04 完成 $160M Series C、估值 $1.5B（Techmeme/Osler），正把 netcheck/DERP/可见性全部产品化；自建中继网络在成本与运维上必败。
2. 端到端传输协议层（自研 UDP 加速协议）——Aspera/Signiant/Resilio 砍了多年，Apple 的 MPTCP、IETF 的 MPQUIC 在定义标准；个人项目造不出可维护的协议栈。
3. 手机间/生态内互传——互传联盟（小米/OPPO/vivo/荣耀）2026-09 发布「碰一碰互传」跨品牌标准（NFC+Wi-Fi Direct，10 月起推送），华为走鸿蒙自有体系；系统级入口不可撼动。
4. Wi-Fi 物理层勘测（NetSpot/WiFi Explorer 已垄断 Mac 口碑）。

**小团队反而能做深（缝隙在系统交界处）：**
1. **中国大陆开发环境这个「脏活场景」**：GFW 波动、镜像失效、代理端口漂移、DERP 绕美日——大厂不碰（合规），国际开源不懂（无国内语境），现有国内开源（mihomo 内核、ghproxy 类镜像，2025 年仍活跃更新，见 github.com/WJQSERVER-STUDIO/ghproxy-touka）只做单点不做闭环。Mihomo-Party 2025-02 因开发者失联归档的事件说明该生态持续缺「有维护承诺的整合层」。
2. **诊断→修复的闭环编排**：检测「DERP 中继→引导直连/自建」「Symmetric NAT→打洞方案」「镜像超时→自动切换源」「DNS 污染→换 DoH」——单点诊断人人会写，把诊断信号映射到修复动作的**规则库**（且随用户环境自学习）才是复利资产。
3. **开发者工作流的网络 SLA**：不是「网速多少 Mbps」而是「`npm install`/`git clone`/`docker pull`/AI API 首 token 延迟快不快」——以开发者任务为单位的诊断指标，全球无成熟玩家，与 netroamer 的包管理器镜像/代理能力天然咬合。
4. 多端场景下的**「我的代理拓扑体检」**：多设备间（手机-家宽-VPS）链路质量矩阵、哪条链路该跑什么流量——Speedify/Peplink 面向直播/商旅人群收费，面向中国开发者的免费+可解释版本是空位。

### 4.3 netroamer 诊断功能：最终建议

**判定：值得做——但定位必须是「自愈引擎的传感器 + 用户信任的证明」，而不是「功能卖点」。** 理由与边界：

- **做（按优先级）**：
  1. **修复前/后的对照诊断（before/after diff）**：每次 netroamer 自动修复前后各跑一次轻量探测（目标站 RTT/丢包/下载小样本），把「修好了：github.com 克隆 34s→2.1s」变成可展示证据。这是留存与口碑引擎，且零竞品在做。
  2. **以开发者任务为单位的健康分**：git/npm/pip/docker/AI API 各一个探测器，输出「任务级网络体检」，而非通用 Mbps。数据结构与修复规则库（已有：AI 工具进程直连、DERP 直连、镜像切换）对齐。
  3. **多端链路矩阵（轻量版）**：复用 tailscale netcheck + STUN binding + DERP 延迟，标注 NAT 类型与 recommended path；不做 bonding，只做「建议+一键切换」。
  4. **匿名聚合遥测（远期可选，需 opt-in 与隐私设计）**：如果装机量上到万级，输出《中国开发者网络体验报告》——对标 Cloudflare Radar 的品牌打法，为未来「诊断数据壁垒」留接口。不承诺、不依赖。
- **不做**：自建中继网络、传输协议自研、通用传输产品（对标 Syncthing/LocalSend/互传联盟）、Wi-Fi 勘测、企业 DEM 功能（告警系统/SNMP）。
- **壁垒的真实构成**（按强度排序）：国内修复规则库的持续维护（脏活+时间）> 修复闭环的用户习惯与默认安装位 > 任务级诊断指标的口径定义权 > 匿名遥测数据（仅当规模足够）。诊断功能对前三项是放大器：**没有诊断，「自愈」无法自证；有了诊断，「自动修复闭环」才可被感知——这才是诊断在 netroamer 里的战略角色。**

---

## 附：关键来源清单（URL + 日期）

**诊断工具与市场**
- Obkio 网络诊断工具页：https://www.obkio.com（2025-10-16）
- PingPlotter 替代品横评（netadmintools.com）：2025-08
- LogicMonitor 收购 Catchpoint：2025-12（LogicMonitor/catchpoint.com 公告）
- Tailscale $160M Series C、估值 $1.5B：https://www.techmeme.com/250408/p12 、https://www.osler.com（2025-04-08）
- Speedtest Certified™ 发布：The Globe and Mail 转述 Ookla，2025-09；AT&T 获 Ookla Best Home Internet 奖：stocktitan.net，2026-02
- Cloudflare Radar 2025 Year in Review：https://radar.cloudflare.com / blog.cloudflare.com（2025-12-15，Help Net Security 转述）
- 17ce：https://17ce.com（2025-08 官网动态）；boce.com：https://www.boce.com；阿里云拨测：https://boce.aliyun.com
- GlassWire 定价（约 $2.99/月，入门版 1 天历史）：iTechGuides 2026 横评；Netdata 2025 评测：ittsystems.com（2025-09-04）
- Speedtest Global Index：https://www.speedtest.net（注：未找到 Ookla 官方 2025 中国分季度专项报告公开全文；中国固宽中位数约 206.91 Mbps 见维基百科各国网速列表，5G SA 224.82 Mbps 见 Ookla 5G SA 报告转述）

**多端多节点**
- Tailscale 性能说明（"Direct connections nearly always…"）：ssdnodes.com（2026-09-08）；DERP 回退延迟约 180ms 实测：saaspedia.dev（2026-02）
- tailscale/derp、tailscale netcheck：Tailscale 官方文档/开源仓库
- STUNTMAN/stunclient：https://github.com/jselbie/stunserver；coturn；natmap：https://github.com/heiher/natmap（KoolCenter 梅林插件动态 2025-12-23）
- QUIC vs TCP 丢包实测：dgway.com（2025-08-13）；TCP over QUIC：arXiv 2504.10054v2（2025-04）；QUIC 无线 mesh 反例：ResearchGate；QoE 无增益实证：Semantic Scholar
- Apple MPTCP（Siri）部署研究：CoNEXT 2016 "An In-depth Understanding of Multipath TCP on Mobile Devices"（2024–2025 未找到新专项研究）
- Multipath QUIC 状态：draft-ietf-quic-multipath（IETF Datatracker，IETF122 纪要 2025-03-20）；无大规模生产部署；3GPP ATSSS 用 MPTCP
- iCloud Private Relay（MASQUE/QUIC）：Apple 官方 Overview PDF（2021-12）；Cloudflare/Fastly 技术博客
- WBA 2025 年度报告（OpenRoaming 81% 部署意愿）：wballiance.com（2024-12-10）；OpenRoaming 落地阻力：wayfiwireless.com（2025-07）
- 802.11k/v/r 快漫游（r：<30ms）：Cisco 9800 官方指南、Meraki/Mist 文档
- Speedify bonding/failover：speedify.com、waveform.com WAN bonding 横评

**文件传输**
- Aspera fasp 原理与厂商声称：Wikipedia "Fast and Secure Protocol"；GTGI（500ms RTT/30% loss 跑满带宽，厂商渠道口径）；IBM 社区论坛真实投诉（fasp 挤压其他流量）
- TCP 在低丢包高延迟下崩溃的独立基准：Silver Peak performance brief（0.01% loss + 100ms）
- Signiant vs MASV 定价对比：https://www.signiant.com/comparison/signiant-media-shuttle-vs-masv 、masv.io（$0.25/GB；2025–2026 在线页面）
- Resilio（1:10 场景 500% 提速为厂商口径）：resilio.com；Syncthing FAQ（CPU 敏感）：docs.syncthing.net；LocalSend 横评：MakeUseOf、Zorin 论坛（2025-10-28）
- HN「Aspera 自由软件替代」讨论：UDT/bbcp/rclone/tus 等开源替代清单

**中国生态**
- 互传联盟「碰一碰互传」标准（小米/OPPO/vivo/荣耀，NFC+Wi-Fi Direct，2026-10 起推送；华为未参与）：pconline（2026-09-15/17 报道）
- ghproxy 类镜像加速项目（2025 活跃）：https://github.com/WJQSERVER-STUDIO/ghproxy-touka；CSDN 综述
- Mihomo-Party 归档事件：2025-02-06（社区公告转述）

**未找到的信息（明确声明）**
- Ookla 官方 2025 年中国分季度/分运营商专项报告公开全文：未找到
- Aspera/Signiant 独立同行评审对比研究：未找到（仅厂商白皮书与渠道材料）
- 中国「个人开发者网络诊断数据」被 B 端付费的公开案例：未找到
- Speedify SDK 独立评测细节：未找到（仅官方与横评定位信息）
