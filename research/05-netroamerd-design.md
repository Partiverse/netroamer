# netroamer 设计（五）：netroamerd 常驻 agent 架构与 MVP 排期

> 日期：2026-09-28。依据：[00-executive-summary.md](00-executive-summary.md) 综合建议的近期三步、[02-ai-optimization.md](02-ai-optimization.md) §3/§5 技术配方、[04-security-encryption.md](04-security-encryption.md) §5 加固 checklist。
> 本文是 P0/P1 的实施蓝图：架构、数据模型、判定算法、周粒度排期与验收标准。

---

## 0. 目标与范围

**北极星**（继承 00 报告）：用户「无感」——装完 30 天内手动碰网络配置次数为 0，且每次自愈留下「修复前 vs 修复后」的可展示证据。

**MVP 范围**：P0 本地遥测（1–2 周）→ P1 慢域名自动直连 + 坏节点自动切换（2–4 周）。明确不含：GUI/菜单栏（P2）、多端协同（P4）、任何云端组件、自训模型（P3 之前用统计方法封顶）。

**形态定位**：netroamerd 是一个用户级常驻单二进制 agent（Go），与现有 bootstrap.sh（一次性配置）和 share/net-watchdog.sh（轻量探活）组成三层：脚本负责装好，看门狗负责「死了拉起」，netroamerd 负责「活着但慢/坏时自动修」。三者原则一致：TUN 永不开、不 root、数据不出设备。

---

## 1. 总体架构

```
                ┌────────────────────────────────────────────────┐
                │                 netroamerd (Go)                │
                │                                                │
 mihomo API ───▶│  collector ──▶ store ──▶ analyzer ──▶ actuator │
 (127.0.0.1     │  (WS/REST)    (SQLite)  (统计判定)   (热载/切换) │
  + secret)     │                                │               │
                │                                ▼               │
                │                              evidence ──▶ CLI  │
                │                    (修复前后对照)   (status/doctor/│
                │                                    rollback)    │
                └────────────────────────────────────────────────┘
                       ▲ 用户级 LaunchAgent / systemd --user
```

### 1.1 模块职责

| 模块 | 职责 | 关键实现点 |
|---|---|---|
| collector | WS 订阅 `/connections`（1s 推送全量活跃连接）+ 低频轮询 `GET /group/{name}/delay` | 断线指数退避重连；只订阅不注入 |
| store | SQLite 落样本与判定，保留期默认 14 天（对齐 Smart Core 的 LRU 持久化先例） | 文件 `~/.local/state/netroamer/telemetry.db`，chmod 600、目录 700 |
| analyzer | 按（域名主域 × 进程 × 时段桶）聚合 EWMA 延迟与失败率；输出「慢域名 / 坏节点」判定 | 纯统计，无 ML 依赖；判定参数见 §4 |
| actuator | 写自有 rule-provider 文件 → `PUT /providers/rules/{name}` 热载；坏节点 `PUT /proxies/{group}` | 只改 netroamer 自有 provider，绝不重写用户订阅/主配置 |
| evidence | 每次动作生成修复前后延迟对照（JSON + 人读 Markdown） | 落 `~/.local/state/netroamer/evidence/`，是传播素材与回滚依据 |
| CLI | `netroamerd status/doctor/rollback/uninstall` | doctor 吸收 bootstrap.sh 第3层与 5.5 节检查，成为长期入口 |

### 1.2 语言与运行时选型：Go

- mihomo 本身是 Go 生态，WS/REST 客户端、SQLite（modernc.org/sqlite 纯 Go 无 cgo）库成熟；
- 单二进制交叉编译 macOS/Linux/Windows，安装 = 放进 `~/.local/bin`，无运行时依赖（对标 02 报告 §4.1 形态②「内存 <30MB」约束，Go 稳定达成，Rust 亦可但迭代速度慢一档）；
- shell 无法承载常驻状态机与 WS 长连接；Python 有运行时分发负担。**结论：Go，无争议项。**

### 1.3 部署与既有组件关系

- macOS：用户级 LaunchAgent（`RunAtLoad` + `StartInterval` 双保险），绝不设 root UserName（04 报告 §3.2 陷阱）；Linux：systemd `--user` unit；Windows：任务计划程序用户级。
- **net-watchdog.sh 保留不动**：它是 5 分钟粒度的「生死探活」，netroamerd 是秒级「质量自愈」；P1 交付后才考虑把「代理死亡 → 清空系统代理」的应用级 kill switch 分支从看门狗迁移进 agent（作为统一的健康状态机）。
- bootstrap.sh 增加 netroamerd 安装/升级步骤（P0 完成后）。

---

## 2. 与 mihomo 的交互面（含安全边界）

端点清单（02 报告 §3.2 已核实 wiki.metacubex.one，2026-09）：

| 用途 | 端点 | netroamerd 用法 |
|---|---|---|
| 实时观测 | `GET /connections` (WS) | 采集每连接 host/processPath/rule/proxyChain/延迟 |
| 主动探测 | `GET /group/{name}/delay`、`GET /proxies/{name}/delay` | 节点健康分、直连可达性预验证 |
| 规则热载 | `PUT /providers/rules/{name}` | 慢域名 DIRECT 规则热载的唯一挂点 |
| 切节点 | `PUT /proxies/{group}` | 坏节点切换 |
| 轻状态 | `GET/PUT /storage/{key}`（≤1MB） | 只存游标/开关/上次动作摘要；样本序列一律 SQLite |
| 边界 | `/rules` 仅 GET、无运行时加规则 API | 闭环必须走「重写自有 rule-provider 文件 + 热载」 |

**安全红线**（04 报告 §2，实现时逐条对齐）：

1. 连接 mihomo 只走 `127.0.0.1` + `Authorization: Bearer <secret>`；secret 来源：首次运行 `netroamerd doctor` 引导用户写入 mihomo 配置（bootstrap.sh 5.5 节自检已能发现缺失并给出修复指令）；**不回显 secret 内容**。
2. 订阅不可信：netroamerd 永不读取/改写订阅内容，只追加自有 rule-provider 文件（路径固定在 netroamer 状态目录，白名单校验）。
3. 不开/关 TUN、不改 DNS、不动用户主配置；`PATCH /configs` 仅允许改 netroamer 自有 provider 的健康检查参数（P2 再启用）。
4. 遥测黑名单落地（04 报告 §3.3）：不存完整 URL（只存主域 + 可选子域前缀哈希）、不存节点名称/订阅身份（只存组内索引 + 延迟序列）、SSID 存单向哈希（盐放本机 keychain/文件权限 600）、不存原始时间线（只存桶聚合 + 最近样本环形缓冲）。

---

## 3. 数据模型（SQLite，`~/.local/state/netroamer/telemetry.db`）

```sql
-- 原始连接样本（保留 7 天，过期删除）
CREATE TABLE samples (
  ts INTEGER NOT NULL,            -- unix 秒
  host_sld TEXT NOT NULL,         -- 主域（如 github.com），已截断
  proc TEXT,                      -- 进程名（processPath 的 basename）
  bucket TEXT NOT NULL,           -- ssid_hash × hour-of-week 时段桶
  via TEXT NOT NULL,              -- 'direct' | 'proxy'
  lat_ms INTEGER,                 -- 连接建立/首包延迟；NULL=失败
  ok INTEGER NOT NULL             -- 0/1
);
CREATE INDEX idx_samples_key ON samples(bucket, host_sld, via, ts);

-- 聚合视图物化（每 5 分钟滚动更新）
CREATE TABLE agg (
  bucket TEXT, host_sld TEXT, via TEXT,
  ewma_ms REAL, p95_ms REAL, fail_rate REAL, n INTEGER,
  updated_at INTEGER, PRIMARY KEY (bucket, host_sld, via)
);

-- 自动判定与动作日志（保留 14 天，回滚依据）
CREATE TABLE judgments (
  ts INTEGER, kind TEXT,          -- 'slow_direct' | 'bad_node' | 'rollback'
  target TEXT,                    -- 域名 或 组名×节点索引
  action TEXT,                    -- 'DIRECT on' | 'switch to #k' | 'revert'
  reason TEXT,                    -- 人类可读判定依据（含关键数字）
  reverted INTEGER DEFAULT 0
);
```

为什么不用 mihomo `/storage` 代替 SQLite：≤1MB 上限装不下 14 天样本；但 agent 仍用 `/storage` 存「上次动作摘要」供 mihomo 侧面板类工具读取。

---

## 4. P1 判定算法（统计方法，参数有先例）

### 4.1 慢域名自动直连（独占卖点，无开源先例）

**触发条件（全部满足才动作，宁缺勿滥）**：
1. 样本量：该（域名 × 时段桶）代理路径样本 ≥ 30 条；
2. 慢：代理路径 EWMA 延迟 > 800ms **或** P95 > 1500ms（可配置），且持续 ≥ 3 个独立时段桶（避免单次网络抖动）；
3. **直连可达且更快**：agent 从直连路径主动探测该域名（`GET /proxies/DIRECT/delay` 或本地 TCP 握手），直连成功且 EWMA 估计 < 代理路径的 50%；
4. **不在豁免名单**：域名命中 greatfire/代理分流规则集（订阅自带 provider）→ 永不自动直连——这是「误判直连墙内域名」风险（02 报告 §5 Phase 1 已识别）的硬闸；`.cn` 主域与已知境内域名白名单优先走快路径。

**动作**：把域名写入自有 rule-provider（`behavior: domain` 的 `netroamer-autodirect.yaml`）→ `PUT /providers/rules/{name}` 热载 → 记录 evidence（切换前 7 天 P50/P95 vs 切换后 24h 复测）。

**回滚**：切换后 24h 内直连成功率 < 95% 或延迟劣化 → 自动回滚（从 provider 移除 + 重新热载），judgments 表标记；`netroamerd rollback` 支持手动一键撤销最近 N 条；同一域名 7 天内最多自动切换 2 次，超过则降级为「通知 + 建议」不再自动动作（防震荡）。

### 4.2 坏节点自动切换

参数对标 Clash Party Smart Core 公开逻辑（02 报告 §3.2，先例可信）：

- **判坏**：节点握手超时 > 该节点历史 EWMA × 1.5，或连续 3 次探测失败，或 5 分钟窗口 fail_rate > 30%；
- **指数惩罚**：每次失败罚分 ×2 递增，恢复成功按 5/10/15/30 分钟分阶段降级（避免抖动振荡）；
- **切换**：当前节点健康分低于组内最优节点且差距 > 20% → `PUT /proxies/{group}`；同组 10 分钟内最多切换 1 次；「手动选择」的组（用户最近 30 分钟手动切过）一律不动——尊重用户意志是「无感」的边界；
- **通知**：切换即生成 evidence 并发系统通知（复用 net-watchdog.sh 的 notify 通道），附修复前后对照。

---

## 5. MVP 排期（周粒度）

### P0：遥测基座（第 1–2 周）

| 周 | 任务 | 验收 |
|---|---|---|
| W1 | Go module 骨架；collector：WS `/connections` 接入 + 断线退避重连；samples 落库；secret 引导流程；单元测试（用 mihomo API mock） | 挂 72h：零崩溃（launchd 自动拉起），内存 < 30MB，SQLite 持续增长无锁表 |
| W2 | analyzer 聚合（EWMA/P95/失败率，5 分钟滚动）；`netroamerd status`（延迟矩阵摘要）；`netroamerd doctor`（吸收 bootstrap.sh NO_PROXY/external-controller 检查）；LaunchAgent/systemd 安装器；隐私文档「不回传清单」 | doctor 在未配置 secret / 绑 0.0.0.0 / NO_PROXY 缺段三种场景给出可执行修复指令 |

### P1：自愈闭环 v1（第 3–6 周）

| 周 | 任务 | 验收 |
|---|---|---|
| W3 | 直连可达性预验证探测器；豁免名单机制（greatfire 命中即拒）；慢域名判定器 | 判定器在回放数据集上零「墙内域名误直连」 |
| W4 | 自有 rule-provider 生成 + `PUT /providers/rules` 热载；24h 复测回滚状态机；`netroamerd rollback` | 热载后 `GET /rules` 可见且顺序正确；人工制造慢域名场景端到端走通 |
| W5 | 节点健康分（1.5× 握手阈值 + 指数惩罚 + 分阶段恢复）；`PUT /proxies` 切换 + 防震荡限制；尊重手动选择 | 坏节点场景 3 分钟内自动切换且 10 分钟内不回切 |
| W6 | evidence 生成（JSON + Markdown）；系统通知接入；`netroamerd uninstall` 全量回滚（agent + LaunchAgent + 自有 provider + judgments 留档）；bootstrap.sh 集成安装；README/文档 | 卸载后 mihomo 配置与安装前等价（diff 为空） |

**排期总览**：6 周 MVP；单人全职口径，按 02 报告成本估算（P0 1–2 人周 + P1 2–4 人周）一致。

---

## 6. 安全加固 checklist 执行状态（对照 04 报告 §5）

### 本轮已落地（2026-09-28，见 git 工作区）

- [x] **NO_PROXY 全量段**：`bootstrap.sh` 三处（WSL2 / PROXY_BLOCK / proxy_on）+ `bootstrap.ps1` 补齐 `127.0.0.0/8`、RFC1918、`169.254.0.0/16`（云元数据）、`198.18.0.0/15`（fake-ip）、`.lan/.internal`；`100.64.0.0/10` 此前已补。注意：IPv6-mapped 环回 `[::ffff:127.0.0.0]/104` 未写入（解析器兼容性差、收益边际），由 mihomo 侧 IP-CIDR 规则兜底。
- [x] **mihomo 侧不可删 DIRECT 规则**：`clash/rules-prepend.yaml` 顶部新增 7 段 IP-CIDR（no-resolve）+ `.lan/.local/.internal` 域名直连，置于 PROCESS-NAME 规则之后、AI 域名之前。
- [x] **external-controller 暴露面检测**：`bootstrap.sh` 新增 5.5 节——扫描 Clash Verge Rev / mihomo / clash 常见配置路径，发现非环回绑定或 secret 缺失即告警并给出修复指令（不回显 secret 值）。
- [x] **doctor 第3层增强**：NO_PROXY 检查从「仅 ZCode 域名」扩展为「域名 + 7 个内网段」，缺失时区分「绕行变慢」与「内网泄漏风险」两种提示。

### 待办（P0 启动前清完）

- [ ] 安装两段式 + SHA256SUMS 签名（GPG/Minisign，校验文件本身必须签名）；`--dry-run` 先审后装
- [ ] `netroamer doctor` 收编为 netroamerd CLI（上表 W2）
- [ ] 看门狗补「代理死亡 → 清空系统代理」分支（P1 后迁移进 agent，见 §1.3）
- [ ] 遥测 SQLite 700/600 权限 + 一键导出/删除（P0 store 模块实现时）
- [ ] **HOMEBREW_API_DOMAIN 经 gh-proxy 的供应链备注**：当前 brew 的 formulae API 与 bottles 均走 gh-proxy/ghfast，镜像可同源篡改「API 返回的哈希 + bottle」使校验失效。短期不动（速度是核心价值），P0 doctor 增加项：可连通官方 API 时抽验 bottle SHA256 是否一致，不一致即告警。

---

## 7. 不做清单（继承 00 报告 §5，防 scope creep）

诊断独立产品、通用传输加速、mesh 组网、卖节点、订阅制收费、TUN/内核改动、自研传输协议、云遥测（P3 前不做任何上云）。

---

## 8. 风险登记

| 风险 | 缓解 |
|---|---|
| 误判直连墙内域名（合规+可用性双重风险） | §4.1 条件 3/4 硬闸：直连可达预验证 + greatfire 豁免；7 天 2 次上限；judgments 全量留档 |
| mihomo API 变更 | 端点清单来自 2026-09 官方 wiki；collector 对未知字段宽容解析；集成测试锁行为 |
| Clash Verge Rev 生态吸收（竞品威胁，00 报告 §2） | 速度优先：W1 即发布可用的 status CLI 抢占心智；宽松协议（MIT + 文档化 rule-provider 格式）降低被吸收摩擦 |
| 常驻 agent 被安全软件误报（LaunchAgent 持久化是 MITRE T1543 向量） | 清晰命名 com.netroamer.agent、用户级无特权、一键 uninstall、README 说明 |
| 仓库可能随时消失（04 报告 §4.3） | 无服务端依赖的本地架构天然免疫；发布走多渠道镜像预案 |
