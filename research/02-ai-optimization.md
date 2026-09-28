# netroamer 研究报告 02：AI 学习用户习惯做网络优化——技术可行性与产品形态

> 调研日期：2026-09-28。方法：WebSearch/WebFetch（优先 2024–2026 信息源）。检索受限说明：部分一手论文与 App Store 页面无法直接抓取，价格等以搜索结果中的二手引用为准并已标注。

---

## 结论先行（TL;DR）

1. **「AI 学习用户习惯优化网络」在 2024–2026 已从论文走向商业产品**，但全都做在云端/系统层（Juniper Mist、Plume、Google Pixel Adaptive Connectivity、Clash Party Smart Core），**没有一个开源项目在「本地 agent + Clash/mihomo 生态」里做这件事——netroamer 的差异化空间真实存在**。
2. 最直接可抄的先例是 **vernesong/mihomo Smart 分支 + Clash Party "Smart Core"**：用 LightGBM 本地训练节点选择模型，数据明确"仅在本地收集和存储，不上传"（clashparty.org 官方文档），证明「本地、轻量、隐私友好」这条路线已被验证可行且被用户接受。
3. **不需要训练大模型**。用户习惯学习的可用特征（工作时段、常用域名/进程、SSID、延迟曲线）用 EWMA / 分位数统计 / 决策树级模型即可覆盖；LightGBM 级别已属"重型"，学术 DRL 拥塞控制（Aurora/Orca）反而短期不可落地——它们改的是内核/传输层，netroamer 应在**应用层策略（规则、选路、预热）**闭环，不动内核。
4. 低成本闭环在 mihomo 上是**现成的**：external controller RESTful API 提供 `/connections`（WebSocket 每连接实时流量）、`PUT /configs`（热重载）、`PUT /proxies/{name}`（切节点）、`/storage`（≤1MB 键值存储）等端点（wiki.metacubex.one，2026 抓取）。「观测→生成规则→热重载」无需改内核、无需 root。
5. 未发现「自动把慢域名加直连」的独立开源先例——这是空白点，也是 netroamer 最好讲的故事。
6. 付费意愿证据：开发者愿为**一次性 license** 付费（Surge Mac $49.99、Quantumult X $7.99、Shadowrocket $2.99），对订阅制敏感；Tailscale 证明了「网络体验即服务」的订阅模型（$6–18/user/月）在基础设施侧成立。netroamer 短期应坚持免费开源 + 可选云端增值，不碰"卖节点"。

---

## 1. 学术 / 工业现状

### 1.1 Learning-based congestion control（学习型拥塞控制）

| 方案 | 机制 | 成熟度 | 开源可用性 |
|---|---|---|---|
| **Aurora** (SIGCOMM'21) | DRL agent 在 Cubic 之上做增量调整（模仿 BBR 的 observation-action 空间） | 学术原型，有公平性/效率缺陷（Liao et al. 2024 系统分析其 unfairness） | 开源（github.com/SMAT-Lab/Aurora） |
| **Orca** (SIGCOMM'23) | "Pragmatic"学习型 CC：RL agent 叠加在 Cubic 上，模拟环境训练，面向真实 Internet 部署 | 学术原型 + 少量真实网络实验，无大规模商用证据 | 开源；QMUL 综述确认其 hybrid 设计 |
| **PCC Vivace** | 在线学习（无训练），实时根据效用函数调速率 | 学术原型；蜂窝场景常作为 baseline | 开源（UCL 谱系 repo，维护不活跃） |
| **COPA** | 延迟目标型（AIMD on delay），非 ML 但常与 learning-based 同列 | 学术原型，移动网络低时延表现好 | 开源（MIT swift 栈） |
| **Spine** (CoNEXT'22) | 分层 DRL CC，宣称超低 overhead | 学术原型 | 开源 |
| **ORC** (APNet 2025) | 在线 RL + 启发式结合 | 新工作 | ACM DL 有论文/幻灯 |

来源：
- Orca 视频/论文页与 QMUL 综述：https://coseners.qmul.ac.uk/wp-content/uploads/2023/08/Reinforcement-Learning-based-Congestion-Control.pdf （抓取 2026-09-28）
- "Towards Fair and Efficient Learning-based Congestion Control"（Aurora 公平性分析，2024-03）：https://arxiv.org/pdf/2403.01798
- ORC (APNet 2025)：https://dl.acm.org/doi/10.1145/3735358.3735381

**判断**：learning-based CC 学术活跃（ORC、TCP-RLA 等 2024–2025 新工作持续出现），但**全部工作在传输层/内核层**，需要改 TCP 栈或用用户态传输库，与 netroamer「shell 脚本/本地 agent + mihomo」的形态不在一个层面。netroamer 可**选择内核算法**（如 Linux 启用 BBR）而非发明算法。

### 1.2 BBR 演进（唯一已大规模落地的"非学习但模型驱动"CC）

- **BBRv1**：Linux 4.9（2016）upstream，是唯一默认可用的 BBR 版本。
- **BBRv2**：修复对 Cubic 的不公平与队列膨胀，未进 Linux 主线。
- **BBRv3**：修复 BBRv2 两个 bug（带宽探测过早结束、带宽收敛），2023 起部署在 Google 服务器，代码在 https://github.com/google/bbr （2024-11 更新文档化带宽收敛改动）；2024 有公开评测论文 "BBRv3 in the public Internet: a boon or a bane?"（https://www.researchgate.net/publication/382422167_BBRv3_in_the_public_Internet_a_boon_or_a_bane）。
- 关键工程事实：**BBRv2/v3 至今未进 Linux 主线**（google/bbr repo 仍以 patch 形式分发），服务器端可用 BBRv1 + `fq`，或自行编译。

### 1.3 MPTCP / Multipath QUIC 的 ML 路径选择

- **MPTCP（RFC 8684）**：Linux 内核 5.6（2020）upstream，2024–2025 持续硬化（CVE 修复活跃），mptcp.dev 跟踪 path manager/scheduler 生态。**成熟度：生产可用**，但客户端侧（macOS/Windows 应用层）几乎无人启用，Apple Siri 是著名用户态用例（iOS 内部）。
- **Multipath QUIC**：IETF draft 拖了 7 年、21 个版本仍未收敛（Huitema, "The long slow path to QUIC multipath", 2024-02, https://www.privateoctopus.com）；mainline quic-go/quinn **均不支持**，只有研究 fork。**成熟度：实验室**。
- **ML 路径调度学术工作**（2024–2025）：TA2LS（two-level optimal-path evaluation, IEEE 2024）、ISM（RL 调度 for 多路视频, ACM）、OLAPS（在线学习预测拥塞）、DaMPS（deadline-aware）、MPCC（PCC 式在线学习扩展到多路径, HUJI, NORNET testbed 验证）。共同点：**论文多、无产品**。
  - 来源：https://www.computer.org （TA2LS）、https://dl.acm.org （ISM）、http://www.cs.cuhk.hk （DaMPS）、https://cs.huji.ac.il （MPCC），检索 2026-09-28。

**判断**：多路径 ML 选路对 netroamer 是**远期选项**——mihomo 本身有 url-test/fallback/load-balance 组，等价于"启发式路径选择"；ML 化的正确姿势不是上 MPTCP，而是**在 mihomo 策略组层面做智能选节点**（Smart Core 已证明此路可通）。

### 1.4 带宽预测

- 学术主流是 LSTM/attention 模型预测 5G/LTE 吞吐（Kim 2023 attention-LSTM；Eldeeb 2025 real-time throughput prediction for MNO；Clemson 5G NSA 非侵入式预测）。
- 应用落点集中在 **video streaming ABR** 与 **运营商网络规划**，没有消费级工具把它产品化。
- 来源：https://www.sciencedirect.com/science/article/pii/S2090447925003296 （2025）；https://dr.lib.iastate.edu （Kim 2023）。检索 2026-09-28。

**判断**：带宽预测对「代理选节点」场景可简化为**吞吐探测 + 时间序列统计**（分时段 P50/P95），不需要深度模型。

### 1.5 802.11k/v/r 无缝漫游、WBA OpenRoaming、Wi-Fi SON

- **802.11k/v/r**：hostapd/wpa_supplicant/OpenWrt 支持完备（OpenWrt 官方漫游文档 https://openwrt.org/docs/guide-user/network/wifi/roaming）；社区共识是**瓶颈在客户端实现**——"few clients actually act on 802.11k/v information"（OpenWrt forum 长帖 https://forum.openwrt.org/t/proper-configuration-of-802-11k-and-802-11v/152994）。辅助守护进程 DAWN / usteer 成熟度"够用但不精致"（https://openwrt.org/docs/guide-user/network/wifi/dawn）。
- **WBA OpenRoaming**：基于 Passpoint（Hotspot 2.0）的联邦漫游；热点规模从 ~1M（Release 3 发布时）→ 3M（2025-01）→ 5M+（2025 下半年 Purple ConneX 宣称）；Google Orion Wifi、Purple、Roam 为认证 Identity Provider；Cambium Wi-Fi 7 AP（2025-03）、Westermo、Extreme 获认证。**对 netroamer 的含义**：这是运营商侧基础设施，客户端 agent 无直接动作空间，但值得作为「未来多端协同的产品叙事素材」。
- **Wi-Fi SON（企业级）**：Juniper Mist 是标杆——Wi-Fi Assurance 云服务 + Mist AI + Marvis 虚拟网络助手，2024-01 发布 "AI-Native Networking Platform"（vmblog.com 报道），ML 做 SLE（service level expectation）异常检测与自愈。**消费级**：Plume Adaptive WiFi（云端 AI 控制 SuperPods，learning 用户模式，https://www.plume.com，2016 起即"first self-optimizing Wi-Fi"）、eero TrueMesh。共同模式：**云端 ML + 网格硬件 + 订阅**，均非本地 agent。

**判断**：漫游/SON 的成熟件都在 AP 侧与云侧；netroamer 作为**客户端 agent** 唯一可做的是「感知 Wi-Fi 环境（SSID/信号/质量）并联动上层策略」——即 Wi-Fi 指纹作为特征输入，而非自建 SON。

---

## 2. 用户习惯学习：谁能学、怎么学、有没有人在做

### 2.1 系统级厂商（产品实证）

| 厂商 | 做法 | 证据 | 局限 |
|---|---|---|---|
| **Apple** | iOS `wifid` 内含 **Adaptive Roaming**（设备端自学习漫游决策，诊断日志可见 `wifid Adaptive Roaming Not Supported` 字样）；iCloud 同步 Wi-Fi 凭据；iOS 26 新增 Wi-Fi Aware；Siri 长期使用 MPTCP | Apple 社区诊断日志（iOS 14.6+ 时代即存在）；iOS 26 功能报道（skymobile.ir，2025-06） | 全部黑盒，无公开文档 |
| **Google** | Pixel **Adaptive Connectivity**：on-device ML 决定何时离开弱 Wi-Fi 回落蜂窝、网络切换；AOSP 有 `WifiNetworkScoreCache`/network rating 打分框架 | Pixel 论坛与官方设置项；AOSP framework/base 源码（android.googlesource.com） | 算法闭源，仅 Pixel 系列 |
| **Microsoft** | **未找到** 2024–2026 任何 "AI 网络智能" 产品化公告；仅有 Windows Connection Manager (Wcmsvc) 传统连接管理与 TroubleshootingSvc 智能排障 | learn.microsoft.com（检索 2026-09-28，明确说明：找不到） | — |
| **小米/华为** | 澎湃OS 2（2024-10）HyperAI 端侧模型 + 微架构调度器；HarmonyOS 6（2025-10）"灵犀通信" 体系（通信感知一体化） | 官方发布会/官网；**两家均未公开「AI 选网」具体算法** | 黑盒 |

**结论**：系统级厂商验证了「on-device 学习用户模式 → 主动优化网络」的价值，但全部闭源，且没有一个对「代理/分流」场景做任何处理——这恰是中国开发者的核心痛点，netroamer 的立足点。

### 2.2 客户端/本地 agent 如何从 telemetry 学习用户模式（技术配方）

可学的模式与对应特征（全部可本地采集）：
- **工作时段模式**：连接事件的时间直方图（hour-of-week 向量），识别「9–18 点工作 Wi-Fi / 晚间游戏 / 周末编程」。
- **常用应用/域名**：mihomo `/connections` 流给出 `metadata.host/processPath/rule`（含进程路径），统计每进程/每域名频次 × 时段 → 「此人 9 点必开 Slack+IDE，22 点打 Steam」。
- **Wi-Fi 指纹**：SSID/BSSID/信号强度/网关 RTT，构建「每个网络环境的性能基线」。
- **网络质量曲线**：RTT/丢包/DNS 时延的时序，按（SSID × 时段）分桶。
- **失败模式**：连接失败/重置/超时事件（Smart Core 收集的字段即此类：握手延迟、成功率、连续失败次数、最后使用时间戳——clashparty.org/docs/guide/smart-core-principles，抓取 2026-09-28）。

### 2.3 On-device 学习（TinyML / 联邦学习 / 本地统计）的隐私与成本优势

- 学术证据（2024–2025）：FL on TinyML 设备相比集中式训练**能耗降 33%**，配合差分隐私可给形式化保证（IEEE Access 2024: https://ieeexplore.ieee.org/iel8/6287639/10380310/10758420.pdf）；FL+TinyML 在资源受限边缘设备综述（ScienceDirect 2025, Ramadan et al.: https://www.sciencedirect.com/science/article/pii/S2405959525000839，被引 ~60）。
- **工程判断**：对 netroamer，TinyML/FL 属于「叙事加分、技术 unnecessary」——特征维度 <50、样本为本机数据，用统计模型（EWMA、分位数、频次矩阵）或 LightGBM 即可，FL 只有在「多用户联合建模」阶段才有意义（且需云端，违背本地优先叙事）。
- **隐私叙事是真实卖点**：Smart Core 明文承诺"数据仅在本地收集和存储，不会上传到外部服务器"——国内用户对流量元数据上云高度敏感，本地学习 = 差异化。

---

## 3. 低成本实现路径（重点）

### 3.1 采集指标：各平台现成方案

**Linux（软路由/WSL/服务器）——最丰富**
- **eBPF**：采集 RTT/重传/丢包/每进程流量的开源件全部现成——DeepFlow（eBPF+cBPF 零侵扰采集 TCP RTT/重传，https://www.deepflow.io/blog/037-ebpf-the-key-technology-to-observability-en/index.html）、Coroot（网络指标×进程关联）、Microsoft Retina、Red Hat NetObserv **FlowRTT**（eBPF `fentry` hook `tcp_rcv_established()` 提取 SRTT，2024-02 官方文章）+ eBPF 丢包追踪。均为开源。
- **getsockopt(TCP_INFO)**：每 socket 的 `tcpi_rtt/tcpi_rttvar/tcpi_lost`，零依赖（Mozilla Bugzilla 与 TU Wien 移动宽带测量平台均用此法）。对 Go/mihomo 场景：mihomo 自身已暴露等效数据，通常无需自写 eBPF。
- **注意**：eBPF 方案（DeepFlow 等）是容器云形态，直接套用在个人桌面是杀鸡用牛刀；netroamer 只需借鉴 `tc`/`fentry` hook 点位，或干脆用 mihomo API。

**macOS（netroamer 主力场景之一）**
- **NetworkExtension**：`NETransparentProxy`/`NEAppProxy` 提供 per-flow 元数据（app、host、流量）；开源实现先例少（agentjail 是少数 NETransparentProxy 系统扩展开源案例；developer.apple.com 论坛确认其限制：只管出站）。App Store 分发需 entitlement 审核。
- **轻量替代**：`nettop`/`lsof`/procfs 类轮询做每进程统计（apple.stackexchange.com 经典方案）；exelban/stats 开源菜单栏监控可参考。
- **最省路线**：mihomo 以 TUN/系统代理运行时，其 `/connections` API 已含 `processPath`（macOS 上 mihomo 通过 libproc 取进程），**零额外采集组件**。

**Windows**
- **WFP (Windows Filtering Platform)**：精确每进程归属的正道（GlassWire/NetLimiter 路线），`FWPM_CONDITION_ALE_APP_ID`；纯监控可用 `GetExtendedTcpTable`/`GetExtendedUdpTable` 轮询（PID→端口→连接）+ `GetPerTcpConnectionEStats`（每连接字节/RTT 计数器）；注意轮询开销被安全研究提示过（EDRPrison 分析）。
- **WinTUN**（wintun.net，WireGuard 官方）：只提供 L3 TUN 网卡，**不含每进程信息**，需与上述 API 关联；Tailscale/mihomo tun 模式都基于它。

**通用最省方案（netroamer 现状即可扩展）**：mihomo external controller 的 **`/connections` (WebSocket)** 每 1s 推送全部活跃连接：`up/down/rxSpeed/txSpeed/metadata{host,dstIP,processPath,rule,proxyChain,sniffHost}`——一个 WS 客户端就能拿到 90% 所需 telemetry，跨三平台一致，无需 eBPF/NE/WFP。这是「低成本」的核心事实。

### 3.2 「观测 → 自动生成/调优分流规则」闭环先例与可行性

**先例（检索到的全部）**：
1. **vernesong/mihomo Smart 内核（最强先例）**：`proxy-group type: smart` + LightGBM 3.3.5 模型（`lgbm-url` 可配、`lgbm-auto-update` 每 72h），`collectdata: true` + `sample-rate` 本地收集连接性能；选节点逻辑分四层：域名历史性能 → ASN 权重表 → 缓存序列 → 回退轮询；含故障切换（握手超时 > 历史均值 1.5× 触发）、分阶段恢复（5/10/15/30 分钟）、指数惩罚与时间衰减、LRU+14 天持久化、随机探索避免局部最优、Sticky Sessions（来源：https://clashparty.org/docs/guide/smart-core-principles ，抓取 2026-09-28）。**这就是「AI 学习用户习惯选节点」的现成实现**，GPL 开源。
2. **OpenClash 智能调度**：按延迟/丢包/带宽/负载加权评分自动选优，正则筛选分组（lobehub skills 页）。
3. **Clash Verge Rev 全局扩展脚本 / 各懒人配置 gist**：静态重写规则，无学习。
4. **未发现**任何「自动检测慢域名→生成 DIRECT 规则」的开源项目（多轮中英文检索均无，2026-09-28）。

**mihomo 能否被外部驱动？能，API 足够（wiki.metacubex.one/api/，抓取 2026-09-28）**：

| 能力 | 端点 | 说明 |
|---|---|---|
| 实时观测 | `GET /connections` (WS)、`/logs`、`/traffic`、`/memory` | 每连接流量/规则/链路/进程 |
| 切节点 | `PUT /proxies/{group}` body `{"name": ...}` | 程序化选节点 |
| 组内测速 | `GET /group/{name}/delay`、`GET /proxies/{name}/delay` | 主动探测 |
| 规则热更 | `PUT /providers/rules/{name}` | **rule-provider 的 payload/file 可外部重写后触发热载**——自动生成规则的正确挂点 |
| 整体重载 | `PUT /configs?force=true`（可带 path/payload）、`PATCH /configs` | 局部调参（端口/DNS/模式） |
| 存储 | `GET/PUT/DELETE /storage/{key}`（≤1MB JSON） | **学习状态可直接存内核里**，无需另建 DB |
| 辅助 | `POST /restart`、`/cache/fakeip/flush`、`/cache/dns/flush`、`GET /dns/query` | DNS 主动探测/缓存失效 |
| 边界 | `/rules` 只有 GET（无运行时增删 API）；script（JS main 函数）为 Clash Premium 遗产，mihomo 文档已移除该节 | 闭环必须走「重写 rule-provider 文件 + 热载」，而非运行时加规则 |

**netroamer 闭环设计（结论）**：`/connections` 采集 → 本地统计（每域名 P95 延迟/失败率，按 SSID×时段分桶）→ 判定「慢/坏域名」→ 写入自有 rule-provider（`behavior: domain` 的 yaml）→ `PUT /providers/rules/{name}` 热载。全程无 root、无内核改动、无云端。

### 3.3 预测式「无感优化」手段清单

| 手段 | 原理 | 实现挂点 | 成本 |
|---|---|---|---|
| **DNS 预取** | 对「未来 5 分钟大概率访问的域名」提前解析并缓存 | 学习到的频次矩阵 + mihomo DNS 缓存（`/dns/query` 预热）；Chrome 的 `dns-prefetch`/`preconnect` 分级模型（web.dev 体系）可借鉴 | 极低 |
| **连接预热（preconnect）** | 高频域名维持热 TLS/代理连接，省 DNS+TCP+TLS RTT | 定期向 `/connections` 确认；对 proxy 握手做 keepalive 周期性打点 | 低 |
| **提前切节点** | 识别「当前节点质量下降趋势」（P95 RTT 连续 3 个窗口恶化）在用户感知前切换 | `PUT /proxies/{group}` + `/group/{name}/delay` | 低 |
| **拥塞窗口/sysctl 调优（Linux）** | `tcp_congestion_control=bbr` + `default_qdisc=fq`、`tcp_rmem/tcp_wmem` max 提到 64–128MB（按 BDP）、`tcp_mtu_probing=1` | 写 `/etc/sysctl.d/`（netroamer 已有此能力域） | 低；服务器侧 2024 最佳实践见 OneUnity/AWS hardened image |
| **QUIC 参数** | 0-RTT resumption、connection migration（Wi-Fi↔蜂窝切换不断流） | 取决于节点协议栈（H3/Hy2/TUIC 原生支持），agent 侧只能"选支持它的节点" | 中 |
| **Happy Eyeballs (RFC 8305)** | v4/v6 竞速连接 | mihomo 层不暴露；只能通过 DNS 策略（如关 v6）间接实现 | 低 |
| **fake-ip 缓存管理** | 避免学习期 fake-ip 漂移造成连接抖动 | `/cache/fakeip/flush` 择机执行 | 极低 |
| **健康检查节律自适应** | 把 proxy-provider `health-check interval` 从固定值改为按网络质量曲线调节 | 重写 provider 配置 + `PUT /configs` | 低 |
| **提醒/诊断（LLM 可选层）** | 把统计摘要交给本地 LLM 生成「为什么慢」的解释（Marvis 式对话排障） | 纯增量功能，不影响数据面 | 中（叙事价值 > 功能价值） |

---

## 4. 产品形态推演

### 4.1 形态阶梯与成本/负担/付费证据

| 形态 | 开发/维护成本 | 分发摩擦 | 付费意愿证据 |
|---|---|---|---|
| **① 一键脚本**（现状） | 极低（shell，无常驻进程）；但脚本形态天花板 =「跑一次」，AI 学习需要常驻才有时间维度 | GitHub clone，零摩擦；国内需镜像 | 无直接收入；作为获客与声誉层 |
| **② 常驻轻量 agent** | 低–中：Go/Rust 单二进制 + launchd/systemd/任务计划；内存 <30MB；复杂度集中在状态管理 | 需处理开机自启/权限（macOS 无需 NE 即可调 mihomo API） | 免费；把「常驻 = 持续优化」讲清楚是升级动机 |
| **③ 菜单栏/GUI** | 中：Tauri/Electron 壳 + mihomo 内核捆绑；跨平台 UI 维护是长期负担（Clash Verge Rev/Mihomo Party 团队均为持续高强度维护） | macOS 公证、Windows 签名有年费 | **最强的中国开发者付费证据在代理工具**：Surge Mac $49.99 一次性 license（kb.nssurge.com 确认"license 终身有效、大版本收费升级"，clashx.tech 2026 对比文引 $49.99）；Quantumult X $7.99、Shadowrocket $2.99（App Store 长期定价）——开发者愿为「高级网络工具」付 $8–50 一次性费用，对订阅抗拒 |
| **④ 多端协同（手机/电脑/软路由）** | 高：iOS 需 NetworkExtension entitlement（Apple Developer $99/年）+ 审核；Android 需 VPNService；软路由 OpenWrt 插件生态割裂（vernesong Smart 内核已有 SFA/Nikki/OpenClash 端移植教程，说明需求存在但碎片化） | 三端 × 内核版本矩阵 | 多端 = 家庭/团队场景 → 订阅话术才成立（Tailscale：Personal 免费 / Starter $6·user·月 / Premium $18·user·月，tailscale.com 定价页，2025 口径） |
| **⑤ 订阅服务** | 最高：云端（配置同步、规则众包、模型托管）+ 客服 + 合规 | 国内主体 + 支付 + 敏感类目风险 | 国内用户为**机场订阅**付费成熟、为**工具订阅**付费意愿弱；卖「优化体验/规则订阅/同步服务」可行，卖「节点」则与机场竞争且承担合规风险——应避开 |

### 4.2 关键产品判断

1. **②+③ 是甜蜜点**：常驻 agent + 极简菜单栏（不是完整 GUI，避免与 Mihomo Party/Clash Verge 正面竞争）——「一个学习你习惯、自己会修网络的托盘图标」，与所有现役客户端的「展示型 GUI」形成心智差异。
2. **AI 学习是升级理由，不是入口**：入口仍是「一键修好网络」（已有脚本）；学习层解决的是脚本形态无解的「跑完之后呢」。
3. **本地优先隐私是护城河**：对标 Smart Core 的"数据仅本地"承诺并更进一步（开源自证），对冲云端 AI 产品的信任赤字。
4. **④ 走软路由优先**：OpenWrt/vernesong 内核生态已验证需求且无 Apple 审核门槛；iOS 最后做。

---

## 5. netroamer 可落地的 AI 优化路线（分阶段）

### Phase 0：Telemetry 基座（成本：1–2 人周；依赖：mihomo external-controller 已开启，netroamer 现状即满足）
- 新增常驻探针：WS 订阅 `/connections` + 轮询 `/group/{name}/delay`，落 SQLite（或直接用 mihomo `/storage` 存轻状态）。
- 产出：每（域名 × 进程 × SSID × 时段）的延迟/失败/流量矩阵。
- **零新依赖、零权限、跨三平台同构**。

### Phase 1：反应式闭环「慢域名自动直连 / 坏节点自动切换」（成本：2–4 人周；依赖：Phase 0 + rule-provider 文件管理）
- 统计判定（非 ML）：域名 P95 RTT > 阈值且直连可达 → 加入自动 DIRECT rule-provider；节点连续失败/超时 → `PUT /proxies` 切换 + 指数惩罚。
- 热载：写自有 rule-provider 文件 → `PUT /providers/rules/{name}`。
- 市场定位：检索确认**无先例**，是 netroamer 第一个独占卖点。
- 风险：误判直连墙内域名 → 需要「直连可达性先验证再切换」与一键回滚（保留 14 天决策日志）。

### Phase 2：预测式无感优化（成本：1–2 人月；依赖：Phase 0–1）
- 时段模型（EWMA/分位数，非 ML）：预测「9 点开工 = IDE+Slack+ registry 域名」→ 开工前 5 分钟 DNS 预取 + 连接预热 + 高频域名节点保活。
- 节点趋势切换：P95 RTT 三窗口恶化即提前迁移。
- Linux 侧 sysctl 调优固化（BBR+fq+缓冲按 BDP）。
- 交付仪式感：菜单栏显示「本周自动修复 N 次、平均延迟改善 X%」。

### Phase 3：本地 ML 选节点（成本：2–3 人月；依赖：Phase 2 数据积累 ≥2 周/用户）
- 两条路：(a) 直接支持/自动安装 vernesong Smart 内核（GPL，生态现成，`type: smart` + collectdata）；(b) 自训 LightGBM（特征 = Smart Core 公开列表：延迟、成功率、时段、ASN、网络质量），本地训练本地推理。
- 建议先 (a) 后 (b)：(a) 一周接入，(b) 作为长期差异化（更细特征 + 可解释报告）。

### Phase 4：多端协同 + 可选服务（成本：≥2 人季；依赖：Phase 3 + 商业化决策）
- 软路由（OpenWrt 插件 / vernesong 内核）优先，其次 Android（VPNService），iOS 最后（$99/年 + NE 审核）。
- 云端仅做三件无风险的事：**配置/学习状态同步（端到端加密）、规则库众包聚合、匿名质量数据捐赠（opt-in）**。
- 收费假设（证据支撑）：个人核心免费（对标 Smart Core/免费客户端），Pro 一次性 license（¥99–199，对标 Surge $49.99 的心智）或同步服务轻订阅（对标 Tailscale $6/user/月下限）；**不做节点转售**。

---

## 附：主要来源清单（检索/抓取日期 2026-09-28）

- mihomo external controller API：https://wiki.metacubex.one/api/
- mihomo 配置文档目录（script 节已移除确认）：https://wiki.metacubex.one/config/
- Clash Party Smart Core 原理（LightGBM、本地数据承诺、特征与选路分层）：https://clashparty.org/docs/guide/smart-core-principles 、https://clashparty.org/docs/guide/smart-core
- vernesong/mihomo Smart 内核：https://github.com/vernesong/mihomo/releases
- Aurora 公平性分析（2024）：https://arxiv.org/pdf/2403.01798
- QMUL RL-CC 综述（Orca/Aurora hybrid 设计）：https://coseners.qmul.ac.uk/wp-content/uploads/2023/08/Reinforcement-Learning-based-Congestion-Control.pdf
- ORC（APNet 2025）：https://dl.acm.org/doi/10.1145/3735358.3735381
- google/bbr（BBRv3，2024-11 更新）：https://github.com/google/bbr
- BBRv3 公网评测：https://www.researchgate.net/publication/382422167_BBRv3_in_the_public_Internet_a_boon_or_a_bane
- QUIC multipath 现状（Huitema, 2024-02）：https://www.privateoctopus.com ；quinn multipath issue：https://github.com/quinn-rs/quinn/issues/224
- Linux MPTCP 生态：https://www.mptcp.dev
- TA2LS 多路径调度（2024）：https://www.computer.org ；ISM（RL/MPQUIC）：https://dl.acm.org
- 吞吐预测（2025）：https://www.sciencedirect.com/science/article/pii/S2090447925003296
- OpenWrt 漫游官方文档：https://openwrt.org/docs/guide-user/network/wifi/roaming ；DAWN：https://openwrt.org/docs/guide-user/network/wifi/dawn ；802.11k/v 客户端现实：https://forum.openwrt.org/t/proper-configuration-of-802-11k-and-802-11v/152994
- Plume Adaptive WiFi（云端 AI 自优化）：https://www.plume.com
- WBA OpenRoaming 规模与认证（2025）：Yahoo Finance（Purple ConneX, 2025-11）、StockTitan（Cambium, 2025-03）、westermo.com
- Juniper Mist AI-Native Platform（2024-01）：https://vmblog.com ；Mist AI/Marvis 产品页：https://buy.hpe.com
- Pixel Adaptive Connectivity（ML 选网）：forums.whirlpool.net.au（Pixel 7 讨论）；AOSP framework/base：https://android.googlesource.com
- Apple Adaptive Roaming（wifid 诊断日志证据）：Apple Support Communities；iOS 26 Wi-Fi Aware：skymobile.ir（2025-06）
- Windows：无 AI 网络智能公告（Microsoft Learn 检索确认）：https://learn.microsoft.com/en-us/troubleshoot/windows-client/networking/wireless-network-connectivity-issues-troubleshooting
- FL+TinyML 能耗与隐私（IEEE Access 2024）：https://ieeexplore.ieee.org/iel8/6287639/10380310/10758420.pdf ；综述（2025）：https://www.sciencedirect.com/science/article/pii/S2405959525000839
- eBPF 观测（DeepFlow）：https://www.deepflow.io/blog/037-ebpf-the-key-technology-to-observability-en/index.html ；Red Hat NetObserv FlowRTT（2024-02）；Coroot/Retina/Pixie/groundcover
- WinTUN：https://www.wintun.net ；wintun+tun2socks 教程：awakecoding.com
- TCP_INFO/tcpi_rtt 采样先例：Mozilla Bugzilla（brotli 帖）；TU Wien 移动宽带测量平台（repositum.tuwien.at）
- Chrome 预连接模型：web.dev 体系（preconnect/dns-prefetch/Speculation Rules）
- TCP sysctl 最佳实践（BBR+fq+缓冲）：OneUnity "How to Configure Network Optimization"（2026-01）、AWS hardened image（BBR+fq+MTU probing）、Simcentric（2024-12）
- Surge 定价与 license 条款：https://kb.nssurge.com （pre-sales FAQ，终身 license/大版本付费）；$49.99 引证：clashx.tech（2026 对比文）
- Tailscale 定价：https://tailscale.com （Personal 免费 / Starter $6·user·月 / Premium $18·user·月）
