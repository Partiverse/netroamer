# netroamer 竞品深度调研报告

> 调研日期：2026-09-28。方法：WebSearch/WebFetch 公开信息 + GitHub API 实时核对 star 数（标注「API 实测」者为当日数据）。凡未查到的信息明确标注「未找到」，不做推测。

## 结论先行

1. **netroamer 的镜像层能力无独占性**：`chsrc`（6.9k stars，65+ 目标全平台换源）+ `nrm` + 各类教程已基本穷尽「包管理器换国内镜像」这一层，netroamer 在这一层只是聚合者。
2. **「shell 代理环境变量自动探测写入」作为独立工具不存在产品化竞品**——社区普遍用 5 行 dotfiles（`proxy_on/proxy_off`）和教程解决（真需求、无产品、付费意愿≈0）。这是空隙，但很窄。
3. **看门狗自愈（拉起死亡核心 + 本地通知）在全部调研对象中均未发现同类能力**，是最干净的差异点，但属 niche。
4. **进程级直连存在一个反直觉事实**：mihomo 原生支持 `PROCESS-NAME/PROCESS-PATH` 规则，但可靠匹配进程必须开 TUN（系统代理模式下流量由宿主进程发起）——netroamer「TUN 不开 + 进程级直连」的组合恰好绕开了这个矛盾（以 shell 会话为进程边界），是真正没人做的组合。
5. **AI 工具（Claude Code 类 CLI）网络配置是 2025–2026 的新痛点**：相关教程文章大量涌现（clashgateway.com 2026-09），但现成规则集（ACL4SSR、blackmatrix7）尚未针对性覆盖，无成熟产品。
6. **若做 AI 习惯学习，最大威胁来自 mihomo 客户端生态（Clash Verge Rev 148k stars 的 override script 机制 + external-controller 热加载）和订阅转换/增强服务（subconverter、SubBoost）**——数据（流量日志）与分发渠道都在他们手里；Tailscale（不做代理分流、国内 DERP 慢）与家用路由 AI（不面向开发者工具链）威胁较低。
7. 空隙不是伪需求，但**「产品化空隙」≠「技术空隙」**：所有能力单独看都是公开知识，netroamer 的机会在于「为中国开发者做默认正确的聚合 + 自愈」，护城河只能来自持续维护与场景语义（AI CLI/Tailscale/Docker），而非任何单项技术。

---

## 一、中国开发者侧分流/代理生态

### 1.1 Clash Verge Rev
- **定位**：基于 mihomo 内核的跨平台桌面代理 GUI（Tauri 2.0，Win/macOS/Linux），原 Clash Verge 的社区延续，GPL v3。
- **核心能力**：TUN 模式、轻量模式（后台常驻）、DNS 覆写、连接/日志检查页、解锁测试页、**全局扩展脚本（override）**——社区用它改写规则与配置，是类 netroamer 能力的官方挂载点。
- **AI 成分**：无。
- **多端**：三大桌面平台，无移动端。
- **诊断**：连接跟踪、日志、测延迟；无链路自愈、无主动告警。
- **定价**：免费开源。
- **规模**：**148,004 stars（GitHub API 实测 2026-09-28）**；2025-10 前后约 20.9k→28k，一年内爆发式增长。来源：https://github.com/clash-verge-rev/clash-verge-rev 、https://blog.mvpbang.com（2025-05-01）、CSDN 热榜（2025-10-14）。
- **明显短板**：不碰包管理器镜像、不写 shell env、终端工具在系统代理模式下依然裸奔；无自愈；默认面向「全局科学上网」而非「开发场景语义」。

### 1.2 mihomo（Clash.Meta）
- **定位**：规则代理内核（MetaCubeX 维护），原 Clash 项目 2023-11 删库后的延续，几乎所有国产 GUI 的引擎。
- **核心能力**：多协议、TUN、内置 DNS（DoH/DoT/fake-IP）、透明代理、**`PROCESS-NAME`/`PROCESS-PATH` 进程规则**（需 `find-process-mode: always/strict`，且系统代理模式下不生效、必须 TUN）、external-controller（:9090）热加载配置。
- **AI 成分**：无。**规模**：34,487 stars（API 实测 2026-09-28，周增约 +247）。注意：该仓库当前 description 伪装成「星穹铁道 Pydantic 模型」（疑似规避审查的常见做法），核对时以 README/commit 为准。来源：https://github.com/MetaCubeX/mihomo 、https://www.star-history.com 、dl-clash.net PROCESS-NAME 教程（2025–2026）、linux.do 进程分流讨论。
- **短板**：内核层只管「流量怎么走」，不管「开发环境该不该走」；镜像、env、自愈一概不管。**但它对 netroamer 既是底座又是最大潜在吸收者**（override 生态可一键吸收 netroamer 的全部规则增强）。

### 1.3 sing-box
- **定位**：「The universal proxy platform」（SagerNet），与 mihomo 并列的两大内核之一，VLESS/Hysteria2/TUIC/Reality 协议前沿。
- **规模**：38,392 stars（API 实测 2026-09-28）。多平台 GUI 客户端（SFI/SFM/SFA/SFT）的后端。来源：https://github.com/SagerNet/sing-box 、SegmentFault Trending（2025-04-19）。
- **AI**：无。**短板**：配置复杂度高于 Clash 生态，无开发场景任何配套；进程规则与 TUN 约束同 mihomo。

### 1.4 Surge（Mac/iOS）
- **定位**：最高价、最专业的规则代理 + 网络调试工具（闭源）。
- **核心能力**：规则分流、MitM、脚本、DHCP/DNS、性能分析；在专业开发者中口碑最高。
- **定价**：Mac 单设备授权 $69.99；**2025-06 起随 Surge 6 转 Mac 订阅制（新功能解锁约 $45.99/年）**，iOS $9.99/设备。来源：https://nssurge.com/pricing 、https://kb.nssurge.com（2026-08-12）、V2EX 讨论（2025-06）。
- **AI**：无。**短板**：贵；不解决镜像与 env；闭源不可自愈定制。

### 1.5 Stash / Shadowrocket / Loon / Quantumult X（iOS 四强）
- **Stash**：Clash 规则兼容的 iOS/tvOS 客户端，约 $5.99–6.99 买断；macOS helper 曾爆提权漏洞（2025-06-11，KE-CERT）。来源：https://apps.apple.com 、https://stash.ws 、https://cert.kenet.or.ke（2025-06-11）。
- **Shadowrocket（小火箭）**：$2.99 买断，中国区无上架，装机量极大但无官方统计。**Loon**：社区评价「功能超 Quantumult X、价格更便宜」。**Quantumult X**：买断制（社区普遍记为 $7.99，官方页未直接核实）。
- **共性**：移动端流量分流为主，脚本能力存在但面向「规则玩家」；**无一涉及开发环境镜像/env/自愈**；AI 均无。来源：https://github.com/usun/jichang-personal README、V2EX（2023-12–2024）。

### 1.6 开发环境配置脚本生态（netroamer 的同层竞品，最需警惕）
- **chsrc（RubyMetric/chsrc）**：全平台换源工具与框架，C11 编写，**65+ 目标**（Linux 发行版、npm/pip/conda/rustup/Homebrew/Docker 等），一条命令换源，`curl https://chsrc.run/posix | bash` 安装。**6,884 stars（API 实测 2026-09-28）**，曾上 GitHub Trending。**这是 netroamer 镜像层的直接等价物，且目标更多、语言更中立**。来源：https://github.com/RubyMetric/chsrc 、weekly.mengpeng.tech 周刊。
- **nrm**：npm registry 切换老牌 CLI（`nrm use taobao`），仅 npm 一层。来源：blog.51cto.com 教程。
- **dotfiles `proxy_on/proxy_off` 模式**：`export http_proxy=...` shell 函数，散布于无数 dotfiles 与 gist，是「env 写入」的民间标准答案；无探测、无自愈、无聚合。来源：clashgateway.com《Clash for Developers》（2026-09-07）、fosslinux.com。
- **ghproxy 类 GitHub 加速**：原 ghproxy.com 已失效；当前主流为 ghfast.top、gh-proxy、ghps、wget.la 等**公共镜像，域名频繁漂移、需先测可用**；自建方案 `hunshcn/gh-proxy`（9,029 stars，API 实测）+ Cloudflare Workers 是社区长期推荐；Watt Toolkit（Steam++）提供免费 GitHub 加速 GUI。**公共镜像的不稳定性恰恰是 netroamer「探测+回退」的价值点，但也是 netroamer 无法根除的依赖脆弱性**。来源：CSDN（2026-09-20）、https://github.com/hunshcn/gh-proxy 。
- **规则/订阅转换**：subconverter、ACL4SSR、blackmatrix7/ios_rule_script、SubBoost（可视化订阅增强）。**未找到任何以 LLM 自动生成分流规则为核心功能的成熟开源项目**（2026-09 搜索确认，多为个人脚本级尝试）。来源：https://github.com/remann2/subscribe2clash 、s1oz.github.io、linux.do。

## 二、国际网络优化/加速

| 产品 | 定位/能力 | AI | 定价 | 规模 | 对 netroamer 相关性 |
|---|---|---|---|---|---|
| **Speedify** | 多链路聚合 VPN（bonding Wi-Fi/5G/Starlink），无缝切换 | 无 | 个人 $7.49/mo、年付 $4.99/mo；家庭 $11.25/mo；Teams $5.99/user/mo 起 | 未公开（老牌，App Store 在架） | 低：解决「多路」，不解决「分流/镜像」 |
| **ExitLag** | 游戏路由优化（1000+ 游戏，多路径选路），支持 Mac | 无 | $9.99/mo、年付 $4.99/mo | Trustpilot 存在，用户量未公开 | 低：付费按月，游戏语义 |
| **WTFast** | 最老牌 GPN | 无 | ~$9.99–13.37/mo | 未公开 | 低 |
| **NoPing** | GPN，拉美最强 | 无 | ~$10/mo | 未公开 | 低 |
| **Cloudflare WARP** | 免费 WireGuard 消费级 VPN，WARP+ 走 Argo 智能路由；2025 内上线抗量子密钥 | 路由层智能（Argo），非用户习惯 | 免费（WARP+ 付费/邀请） | Cloudflare 官方，全球亿级 | 中：同为「免费、无感」，但无中国镜像/分流语义，且在国内可用性存疑（未深入核实） |
| **Tailscale** | WireGuard mesh 组网；MagicDNS、exit node、subnet router | 少量（ACL 建议/AI 投资人功能未见产品化） | 个人免费（3 用户/100 设备，新口径 6 用户；tagged 资源 50 个后 $1/个/月）；商业 $6–18/user/mo | **36,963 stars（API 实测）**，公司融资 >1.6 亿美元（2024） | **高**：netroamer 已含 Tailscale/DERP 直连优化；国内官方 DERP 慢、打洞失败≈不可用，社区大量自建 DERP/Headscale——netroamer 的 Tailscale 适配是真痛点但受众重合度有限 |
| **NetBird** | 开源 WireGuard mesh + SSO/MFA（德国） | 无 | 开源免费 + 云订阅 | **29,582 stars（API 实测）** | 低 |
| **ZeroTier** | L2/L3 虚拟局域网 | 无 | 免费额度 + 付费 | **17,141 stars（API 实测）** | 低 |
| **Twingate** | 商业 ZTNA（闭源 SaaS） | 无 | 询价 | 未公开 | 低 |
| **OpenZiti** | 身份优先零信任 overlay（应用内嵌，Apache 2.0） | 无 | 开源免费 | **4,411 stars（API 实测）** | 低 |
| **Mullvad** | 隐私 VPN 标杆：扁平 €5/mo（2009 年至今未涨价），无邮箱账号、可现金支付 | 无 | €5/mo 单价 | 未公开 | 低 |

来源：https://apps.apple.com（Speedify，2026-08-10）、https://www.exitlag.com 、https://www.gamestop.com（WTFast）、https://tailscale.com/pricing 、https://vploq.com（2026-09-08）、https://blog.cloudflare.com / noise.getoto.net（2025）、https://www.g2.com / ip-trackers.com（2026-04-17，Mullvad）、各 GitHub API。**共同短板：无一面向「开发工具链在受限网络下的可用性」；无一提供镜像/DNS 国内化/env 注入；订阅制为主。**

## 三、消费级/家庭 AI 网络优化

- **Plume（Adaptive WiFi）**：自称首个自适应 WiFi SaaS 平台，**通过运营商/ISP 分发而非零售**；自优化 WiFi、云安全、接入控制（Haystack AI 遥测优化）。对 netroamer 的启示：**「AI 优化网络」在消费端的落地形态 = 云端遥测 + 持续订阅，绑定硬件/运营商**。来源：Wi-Fi NOW 报道（2024–2025）。
- **eero（Amazon）Intelligent Mesh**：mesh 动态选路 + eero Secure/Plus 订阅（威胁拦截、家长控制；历史价 ~$2.99/mo 或 $99/年，2025 现价未直接核实）；Max 7 Wi-Fi 7 上市（2025-09 PCMag 报道降价 $120）。无开发者语义。
- **TP-Link HomeShield**：免费版（安全扫描、IoT 识别、基础 QoS）+ **Pro 订阅**（实时防护、高级家长/IoT，30 天试用）。来源：https://www.amazon.com（Archer AXE300 listing）、TP-Link 官网。
- **ASUS AiProtection Pro**：Trend Micro 引擎，**终身免费**（恶意网站、漏洞、感染设备侦测）；ASUSWRT 6.0 新增广告/追踪拦截。与 HomeShield 的免费/付费差异是家庭安全层的标准分野。来源：https://www.asus.com 。
- **Google Nest Wifi Pro**：$199.99（Wi-Fi 6E 单只）起步，mesh + Home app 网络体检；无 AI 习惯学习，无工具链感知。来源：零售页对比（2025）。
- **共性短板（= 对 netroamer 的反向启示）**：家庭 AI 网络优化已把「无感、自愈、按设备语义分流」做成了**路由器硬件 + 订阅**生意；但它们的分流语义是「设备/应用类别」（游戏机、IoT、儿童设备），**没有「开发者终端工具」这一语义层**，也不存在中国镜像生态。

## 四、企业 AIOps 网络

- **Juniper Mist（Marvis）**：业界首个对话式 AI 网络助手；2025-08–09 HPE 升级为 **agentic AI 多智能体排障**（telemetry → 定位 → 建议修复），覆盖 wired/wireless/WAN/DC，愿景「self-driving network」。企业订阅制（按 AP/交换机，询价）。来源：https://www.juniper.net/us/en/products/cloud-services/marvis-ai-assistant-datasheet.html 、https://itbrief.asia（2025-08-26）、apmdigest.com。
- **Cisco ThousandEyes**：互联网/DEM 监测事实标准；按 test 数 + 测试频率 + agent 数计价（**无公开价目，小规模约 $500/mo 起**，常打包进 Cisco EA）。来源：motadata.com、cubeapm.com、ThousandEyes《Pricing 101》（slideshare）。
- **Aruba Central AIOps**：HPE 云管平台，AI 异常检测/自动排障/建议，Foundation/Advanced 分层订阅（示例价 ~$115.67/年/AP，On-Prem Foundation）；合作商询价。来源：securewirelessworks.com、HPE 官网。
- **Netdata**：每秒级指标 + AI 异常检测；**免费 ≤5 节点，Business $4.50/node/mo（年付）**。来源：https://www.netdata.cloud/pricing 。
- **Obkio**：SaaS 网络性能监测（合成监测 agent、路径分析）；**约 $249–1,199/mo 分层**（Software Advice 起价 $249/mo）。来源：softwareadvice.com（2026）。
- **共性短板**：全部面向企业 IT 预算与运维团队；「AI 排障」的形态是 dashboard + 对话 + 工单，**没有任何一款下沉到个人开发者终端，更没有中国网络语境**。Marvis 的 agentic 排障范式是 netroamer「AI 习惯学习 + 自愈」最值得抄的产品形态参照。

---

## 核心问题回答

### Q1. netroamer 的核心优势在竞品中是否已存在？

| netroamer 能力 | 是否已存在 | 谁覆盖 / 谁没有 |
|---|---|---|
| 自动探测本地代理端口 + 写 shell env | **部分**（模式普遍，产品缺位） | dotfiles/教程覆盖模式；**无任何产品自动探测+写入**；GUI 客户端全都不做 |
| 包管理器国内镜像一键配置 | **是，基本完全覆盖** | **chsrc（65+ 目标）最强等价物**，nrm 单点覆盖，教程覆盖长尾 |
| DNS 国内化（223.5.5.5/119.29.29.29） | **部分** | mihomo/sing-box 内置 DNS 能力更强但服务代理场景；AdGuard Home/路由器可做；「纯直连国内化」无人产品化 |
| GitHub 镜像加速 | **是** | ghfast/gh-proxy/Watt Toolkit；netroamer 仅为聚合+探测回退 |
| Clash 规则增强（AI 工具进程级直连、Tailscale/DERP 直连） | **否（组合独有）** | mihomo 有 PROCESS-NAME 原语但需 TUN；现成规则集（ACL4SSR/blackmatrix7）未针对 AI CLI 与 Tailscale/DERP；**无人做「TUN 不开前提下的进程级直连」** |
| 看门狗自愈（拉起死亡核心 + 链路异常本地通知） | **否（全部对象中未发现）** | 所有 GUI 客户端、所有国际/家庭/企业产品均无此等价物（企业 AIOps 有告警无「本机拉起」） |

**结论：镜像层被 chsrc 覆盖（最接近的单一竞品）；「探测+env+自愈+AI 工具规则」作为组合无人覆盖；单项中最硬的独占是看门狗自愈与 TUN-free 进程级直连。**

### Q2. 差异化空隙与「伪需求」判定

由短板反推的真实空隙：
1. **系统代理的「中间态黑洞」**：GUI 客户端解决浏览器，终端工具（git/npm/curl/AI CLI）在系统代理模式下不读代理——每个中国开发者都手抄过 `export http_proxy`。需求为真（教程持续产出），但**可用 5 行 dotfiles 满足，付费意愿≈0**——只适合开源引流，不适合收费。
2. **AI CLI 工具时代的新配置面**：Claude Code 类工具同时命中「代理可用性 + 镜像 + 长连接保活」，规则集与教程 2025–2026 才开始出现，**尚无默认正确方案**——这是当前最「新」的空隙，不是伪需求。
3. **公共 GitHub 镜像的漂移问题**：镜像域名频繁失效 → 「可用性探测+自动回退」是真需求且无人产品化，但 netroamer 自身也会被同一脆弱性拖累（需持续维护镜像列表，这是成本不是护城河）。
4. **自愈**：真痛点（核心进程死亡/链路异常无人管），但受众为重载用户，niche。

**判定：空隙真实但窄。它不是技术空隙（全部是公开知识的组合），而是「无人愿意维护的脏活」空隙。作为开源脚本成立；作为收费产品不成立（对照：同类全部免费，chsrc 6.9k stars 也未变现）。其价值应定位为 AI 习惯学习产品的获客入口与数据采集面。**

### Q3. AI 习惯学习型产品的最直接威胁

按威胁度排序：
1. **mihomo 客户端生态（最高）**：Clash Verge Rev 的 override script + external-controller 热加载已构成「外部脚本改写运行中配置」的完整 API 面；148k stars 的分发能力意味着一个热门社区 override 脚本即可实现 netroamer 全部规则增强，习惯学习只是其上的一层统计。**数据（连接日志/流量画像）天然在他们手里，加 AI 的边际成本最低**。
2. **订阅转换/增强服务（次高）**：subconverter、SubBoost 类服务已在做「自动把订阅变好用」，商业模式天然需要差异化，加 LLM 规则生成是顺路动作；它们缺的只是终端侧数据。
3. **Tailscale 及 mesh 阵营（中低）**：「无感组网」愿景重叠，但它不做代理分流、不做镜像，且国内 DERP 劣势反成其软肋；除非它做中国特化（未见迹象）。
4. **家用路由 AI / 安全软件（低）**：有遥测有 AI，但语义层是「家庭设备」而非「开发者工具链」，且分发载体（路由器）与 netroamer（终端 shell）不重叠。
5. **企业 AIOps 下沉（低但值得跟踪）**：Marvis 的 agentic 排障范式若出现「个人开发者版」，将直接正面竞争。

**结论：威胁不在「另一个 netroamer」，而在「Clash 生态自身长出这一层」。netroamer 的应对应是：抢占 chsrc 未覆盖的组合层（探测+env+自愈+AI CLI 规则默认值）、以 MIT/宽松协议降低被 mihomo 生态吸收的摩擦、并尽快把「习惯学习」所需的本地遥测做进 v1 数据模型。**

---

## 竞品对比总表

| 竞品 | 定位 | AI 成分 | 多端 | 诊断/自愈 | 定价/模式 | 规模（2026-09-28 API 实测或标注） | shell env/镜像/开发语义 | 短板（vs netroamer） |
|---|---|---|---|---|---|---|---|---|
| Clash Verge Rev | 桌面代理 GUI | 无 | Win/mac/Linux | 连接页/日志，无自愈 | 免费开源 | **148,004★** | 无 | 终端工具不管、无自愈 |
| mihomo | 规则代理内核 | 无 | 内核级 | 无（有 :9090 控制 API） | 免费开源 | **34,487★** | 无 | 只管流量路径 |
| sing-box | 通用代理内核 | 无 | 内核+全平台 GUI | 无 | 免费开源 | **38,392★** | 无 | 配置复杂、无开发配套 |
| Surge | 专业代理+调试 | 无 | Mac/iOS | 强诊断，无自愈拉起 | $69.99 买断→$45.99/年订阅 | 付费用户量大（未公开） | 无 | 贵、闭源 |
| Stash / Shadowrocket / Loon / QX | iOS 代理 | 无 | iOS/tvOS(/Mac 部分) | 弱 | $2.99–7.99 买断 | 未公开（小火箭装机量极大） | 无 | 移动端为主 |
| chsrc | **全平台换源工具** | 无 | Linux/Win/mac | 无 | 免费开源 | **6,884★** | **镜像层完全覆盖** | 无代理探测/env/自愈/规则 |
| ghproxy 类（ghfast/gh-proxy/Watt Toolkit） | GitHub 加速 | 无 | 网页/客户端 | 需人工测可用 | 免费（自建 CF Workers） | hunshcn/gh-proxy 9,029★ | GitHub 单点 | 域名漂移、不稳定 |
| Speedify | 多链路聚合 VPN | 无 | 全平台+OpenWrt | 链路切换即自愈 | $4.99–11.25/mo | 未公开 | 无 | 不做分流/镜像 |
| ExitLag/WTFast/NoPing | 游戏 GPN | 无 | 桌面为主 | 路由选路 | ~$10/mo | 未公开 | 无 | 游戏语义、付费 |
| Cloudflare WARP | 免费 VPN | Argo 路由智能 | 全平台 | 基础 | 免费/WARP+ | 亿级（官方） | 无 | 无中国语义 |
| Tailscale | WireGuard mesh | 极少 | 全平台 | 集群监控 | 个人免费/商业 $6–18/user/mo | **36,963★** | 无 | 国内 DERP 慢、不代理分流 |
| NetBird/ZeroTier/Twingate/OpenZiti/Mullvad | mesh/ZTNA/隐私 VPN | 无 | 全平台 | 各有控制台 | 免费—€5/mo—询价 | 29.6k/17.1k/—/4.4k★ | 无 | 与开发环境正交 |
| Plume/eero/Nest Wifi | 家庭 mesh | 自适应 WiFi/遥测 | 硬件全家桶 | 云端自优化 | 硬件+订阅（eero Secure ~$2.99/mo 历史价） | ISP/零售量大 | 无 | 设备语义非工具链语义 |
| TP-Link HomeShield/ASUS AiProtection | 路由安全+QoS | 威胁识别 | 路由器 | 告警 | 免费/Pro 订阅/终身免费 | 出货量大 | 无 | 无开发者场景 |
| Juniper Mist(Marvis) | 企业 AI 网络 | **agentic 排障（最强参照）** | 企业设备 | AI 排障+建议修复 | 企业订阅询价 | 企业市场 | 无 | 不下沉个人 |
| ThousandEyes/Aruba AIOps | 企业监测 | AI 异常检测 | 企业 | 告警/根因 | 询价/~$500/mo 起 | 企业市场 | 无 | 同上 |
| Netdata/Obkio | 基础设施监测 | AI 异常检测 | 服务器 | 指标告警 | 免费≤5 节点/$4.50 起；$249–1199/mo | Netdata 开源装机广 | 无 | 面向运维非开发桌面 |

（「—」= 未找到公开数据）

## 主要来源清单

- GitHub API 实测（2026-09-28）：MetaCubeX/mihomo、clash-verge-rev、SagerNet/sing-box、tailscale、netbirdio/netbird、zerotier、openziti、RubyMetric/chsrc、hunshcn/gh-proxy、vernesong/OpenClash（27,600★）
- https://github.com/clash-verge-rev/clash-verge-rev / blog.mvpbang.com（2025-05-01）
- https://nssurge.com/pricing 、kb.nssurge.com（2026-08-12）
- https://apps.apple.com（Stash、Speedify，2026-08-10）、cert.kenet.or.ke（2025-06-11）
- dl-clash.net PROCESS-NAME 教程、linux.do（进程分流/Tailscale 自建，2026-02）
- CSDN：GitHub 加速镜像（2026-09-20）、Tailscale DERP 自建指南
- https://tailscale.com/pricing 、vploq.com（2026-09-08）
- https://www.exitlag.com 、gamestop.com、zerolagapp.com（2025–2026）
- blog.cloudflare.com / noise.getoto.net（2025，WARP 抗量子）
- https://www.g2.com 、ip-trackers.com（2026-04-17，Mullvad）
- Wi-Fi NOW（Plume）、PCMag（eero Max 7，2025-09）、asus.com、TP-Link/Amazon listing
- juniper.net Marvis datasheet、itbrief.asia（2025-08-26）、apmdigest.com
- motadata.com、cubeapm.com（ThousandEyes）、securewirelessworks.com（Aruba）
- netdata.cloud/pricing、softwareadvice.com（Obkio，2026）
- clashgateway.com（2026-09-07）、weekly.mengpeng.tech（chsrc）
