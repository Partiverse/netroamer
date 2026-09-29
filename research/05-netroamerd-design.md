# netroamer 设计（五）：netroamerd 常驻 agent 架构与 MVP 排期

> 日期：2026-09-28。依据：[00-executive-summary.md](00-executive-summary.md) 综合建议的近期三步、[02-ai-optimization.md](02-ai-optimization.md) §3/§5 技术配方、[04-security-encryption.md](04-security-encryption.md) §5 加固 checklist。
> 本文是 P0/P1 的实施蓝图：架构、数据模型、判定算法、周粒度排期与验收标准。
> **修订 2026-09-29**：按 [reviews/2026-09-29-05-adversarial.md](reviews/2026-09-29-05-adversarial.md) 修订 §3/§4/§5——修复 4 个 P0（桶稀释 / 回滚不可测 / 无冷却 / 豁免自举）与 P1-5/6/7/8。

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

-- 自动判定与动作日志（保留 14 天，回滚依据；P2-10 补索引与参数指纹）
CREATE TABLE judgments (
  ts INTEGER, kind TEXT,          -- 'slow_direct' | 'bad_node' | 'rollback'
  target TEXT,                    -- 域名 或 组名×节点索引
  action TEXT,                    -- 'DIRECT on' | 'switch to #k' | 'revert'
  reason TEXT,                    -- 人类可读判定依据（含关键数字）
  reverted INTEGER DEFAULT 0,
  params_hash TEXT DEFAULT ''     -- 判定参数指纹：算法迭代后历史判定可归因
);
CREATE INDEX idx_judgments_target ON judgments(target, ts);

-- 主动探测留档（复测/预验证/健康检查；节点身份按 §2 只存组内索引，不存名称）
CREATE TABLE IF NOT EXISTS probes (
  ts INTEGER NOT NULL,
  target TEXT NOT NULL,           -- 域名（retest/precheck）或 组名×节点索引（health）
  side TEXT NOT NULL,             -- 'direct' | 'proxy'
  purpose TEXT NOT NULL,          -- 'precheck' | 'retest' | 'health'
  lat_ms INTEGER,
  ok INTEGER NOT NULL,
  fail_kind TEXT DEFAULT ''       -- '' | 'dns' | 'timeout' | 'tls' | 'other'
);
CREATE INDEX idx_probes_target ON probes(target, purpose, ts);
```

为什么不用 mihomo `/storage` 代替 SQLite：≤1MB 上限装不下 14 天样本；但 agent 仍用 `/storage` 存「上次动作摘要」供 mihomo 侧面板类工具读取。

---

## 4. P1 判定算法（统计方法，参数有先例；2026-09-29 按对抗评审修订）

### 4.1 慢域名自动直连（独占卖点，无开源先例）

**触发条件（两层判定，全部满足才动作，宁缺勿滥）**：

1. **域名级初筛**：该主域跨桶聚合的代理路径样本 ≥ 50 条，且加权 EWMA > 800ms **或** 最大 P95 > 1500ms（可配置）；
2. **桶级验证**：最近聚合窗口内 ≥ 3 个活跃桶（桶内样本 ≥ 5 即「活跃」，不要求凑满大样本）EWMA 同向超阈——只要求方向一致，不做 168 桶全采样（修复 P0-1 桶稀释：原「单桶 ≥30 样本 × 持续 3 桶」组合在真实强度下触发周期以周计）；
3. **同口径直连更快**：判定时对同一测试 URL（`https://<domain>/generate_204`）分别经 DIRECT 与当前出口节点实时探测（两侧同为 TTFB，修复 P1-5 口径失真），直连成功且延迟 < 代理路径的 50%；出口节点名取自当前连接 chains（仅内存使用，不落库——隐私边界 §2 不变）；
4. **本地化豁免双闸**（修复 P0-4 自举依赖）：
   - **资格闸（正向白名单）**：候选域名必须 `.cn` TLD、内置境内域名列表、或用户显式 allow 文件（`~/.config/netroamer/netroamer-allow.txt`，每行一域）三者之一——没有资格就没有候选资格；
   - **豁免闸（负向硬闸）**：域名命中 mihomo `GET /rules` 中任何「DOMAIN / DOMAIN-SUFFIX / DOMAIN-KEYWORD → 非 DIRECT」规则、或用户 exempt 文件（`netroamer-exempt.txt`）→ 永不自动直连。判定全本地、零外部依赖；greatfire 列表仅作离线参考数据随包分发，不再运行时拉取。
   - 已知局限（W3 记录在案）：GEOSITE/GEOIP/rule-provider 类规则无法本地展开，靠条件 3 的直连探测失败兜底（被墙域名直连探测大概率不通）+ 条件 5 的配额/回滚止损。

**动作**：把域名写入自有 rule-provider（`behavior: domain` 的 `netroamer-autodirect.yaml`）→ `PUT /providers/rules/{name}` 热载 → **`GET /rules` 回读验证域名可见**（修复 P1-8：不可见则指数退避重试，3 次失败回滚文件并告警，杜绝「文件已写、内核未载」的状态漂移）→ 记录 evidence（切换前窗口 P50/P95 vs 切换后 24h 复测序列）。

**复测与回滚**（2026-09-29 二次修订，落实独立复评 P0-2）：
- 切换后 24h 内**每小时经 DIRECT 主动探测**该域名（同口径），probes 表留档（purpose='retest'）——复测器是回滚状态机的唯一事实来源；
- **探测成功 taxonomy**（P1-4）：mihomo delay test 以 TTFB 计——完成 TLS + 收到任意 HTTP 状态码即成功（`.cn` 域名返回 404/403 不算失败）；DNS 失败 / 超时 / TLS 失败 = 失败并按 fail_kind 留档；
- 回滚判据（**滑动窗口，不用单点**）：最近 3 次连续失败 → 回滚；或最近 6 次中 ≥3 次失败 → 回滚。替代原「24 点 95%」（该口径下休眠唤醒 2 个孤立失败即误回滚）；
- **代理变优退出**（P0-1③ 的完整版依赖 W5 节点健康追踪的组内索引身份，W4 先落地降级版）：动作后该域名的代理路径样本归零，故退出路径以「直连复测持续健康 + 判定满 7 天」为自然复核点重新评估，避免误动作永久化；
- **终态**（P0-2）：同域名累计回滚 3 次（judgments reverted 计数）→ 永久降级「仅通知」，不再自动动作——状态机必须收敛；
- 回滚 = provider 移除 + 热载 + 回读验证 + judgments 记录 `revert`；`netroamerd rollback` 支持手动一键撤销最近 N 条。

**联动静默窗**（落实独立复评 P0-1）：
- 任一坏节点切换（§4.2，W5 交付）发生后 T=2h 内，冻结 §4.1 对任何域名的自动动作（一个故障根因只允许一个自愈动作，其余转通知）——judgments 关联查询实现；
- W4 过渡期（§4.2 尚未交付）的天然保护：条件 3 要求双侧探测均成功，节点劣化期代理侧探测劣化即无法通过；完整联动闸随 W5 落地。

**配额与冷却**（修复 P0-3 震荡路径）：
- **计数语义**：只有实际写 provider 的动作计数——同域名 7 天内最多 2 次；回滚**不计数**，但回滚立即设置同域名 **7 天冷却**（冷却期内即使配额剩余也不动作）；热载失败回滚不消耗配额（文件已还原）；
- **降级态退出条件**：冷却期满，且此后 3 个活跃桶不再满足条件 2（否则继续通知 + 建议，不自动动作）；
- **终态**：累计回滚 3 次 → 永久仅通知（见复测与回滚）。

**provider 挂载归属**（落实独立复评 P1-3）：
- provider 文件本身不产生匹配——主配置需要 `rule-providers: netroamer-autodirect` 声明 + `RULE-SET,netroamer-autodirect,DIRECT` 一行；
- 挂载段由本仓库模板交付（`clash/rules-merge.yaml` + `clash/rules-prepend.yaml`），带 `# netroamer:mount` 指纹注释标记边界；doctor 校验指纹存在、位置在用户 GEOSITE/catch-all 之前；
- 卸载（W6）按指纹清除挂载段；W6 卸载验收同步改为「除 secret 与挂载指纹段外 diff 为空」。

### 4.2 坏节点自动切换

参数对标 Clash Party Smart Core 公开逻辑（02 报告 §3.2，先例可信），修订两处（P1-6/P1-7）：

- **判坏**：节点握手超时 > 该节点历史 EWMA + 3×MAD，**阈值钳位 [200ms, 1000ms]**（替代裸 1.5× 比例——低延迟节点阈值过紧、高延迟节点形同虚设）；或连续 3 次探测失败；或 5 分钟窗口 fail_rate > 30%；
- **指数惩罚**：每次失败罚分 ×2 递增，恢复成功按 5/10/15/30 分钟分阶段降级（避免抖动振荡）；
- **切换**：当前节点健康分低于组内最优节点且差距 > 20% → `PUT /proxies/{group}`；同组 10 分钟内最多切换 1 次；
- **手动选择尊重窗口**：用户最近 30 分钟手动切过的组不动——**例外**：硬故障（连续 3 次探测失败且组内其他节点健康）时通知并覆盖，通知注明「因节点完全不可达临时越过手动选择」（不让用户为尊重窗口扛 30 分钟死节点）；
- **通知**：切换即生成 evidence 并发系统通知（复用 net-watchdog.sh 的 notify 通道），附修复前后对照。

### 4.3 判定基础设施（修订新增）

- judgments 表补 `(target, ts)` 索引与 `params_hash` 列（P2-10）：算法迭代后历史判定可归因到当时参数；
- 判定器输入统一走 agg + 实时探测，不在判定路径读原始样本；
- unix socket 路径失效（Clash Verge 重启后临时目录哈希变化）→ collector 重连前重新发现端点（P1-9），避免对死 socket 永久退避。

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
| W3 | mihomo REST 客户端（version/rules/delay/connections）；同口径直连/代理探测器；豁免双闸（资格白名单 + 本地规则豁免 + 用户 allow/exempt 文件）；慢域名判定器（两层统计 + 配额/冷却状态机）；judgments 索引与 params_hash 迁移；collector 端点重发现 | 判定器回放数据集上豁免域名零动作；配额与冷却语义单测全覆盖 |
| W4 | rule-provider 生成 + `PUT /providers/rules` 热载（`GET /providers/rules/{name}` ruleCount 对账 + `/rules` RULE-SET 行验证，失败退避重试）；24h 逐时直连复测 + 滑动窗口回滚状态机（连续 3 败或 6 中 3 败；终态 3 次回滚）；provider 挂载指纹（merge/prepend 模板 + doctor 校验）；`netroamerd rollback`；run 增加 `--actuate`（缺省影子模式：judge 只记录不动作） | 影子模式连续运行无写入；`--actuate` 下热载后 providers ruleCount 对账一致；人工制造慢域名端到端走通；复测失败自动回滚且 evidence 完整 |
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
