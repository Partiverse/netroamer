# netroamer

> 中国开发者的**本地无感自愈网络层**。基于 mihomo external controller API 做
> 「观测 → 习惯学习 → 规则热载」闭环：网络慢了自己修，节点死了自己切，
> 全程留在本机，不开 TUN、不用 root、不上云。

[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

---

## 它解决什么

国内开发者的日常网络摩擦，你多半都遇到过：

- 某个域名经代理绕远路，慢到影响干活，但直连明明很快；
- 订阅里某个节点抽风，所有走它的流量一起变慢；
- 代理进程死了，系统代理还开着，浏览器直接抓瞎。

这些问题的共同点：**都有明确的「症状 → 修复」对应关系，但没人盯着**。
netroamerd 就是那个盯着的人——它在后台观测，发现问题就动手修，并留下
「修复前 vs 修复后」的证据。

**核心设计约束**（不可协商）：

| 约束 | 含义 |
|---|---|
| TUN 永不开 | 不抢系统网络栈，不与 VPN 客户端打架 |
| 永不用 root | 用户级 LaunchAgent / systemd --user |
| 数据不出设备 | 无遥测上报、无云端依赖，断网也能工作 |
| 宁缺勿滥 | 判定阈值保守，宁可不修也不误修（自动直连墙内域名是合规风险） |
| 可完全回滚 | `uninstall --full` 后配置与安装前等价 |

---

## Quick start

```bash
git clone https://github.com/Partiverse/netroamer.git
cd netroamer
bash bootstrap.sh          # 装环境变量 + Clash 规则模板；有 Go 时顺带装 netroamerd
```

或只装 agent：

```bash
cd netroamerd
go build -o netroamerd ./cmd/netroamerd
./netroamerd doctor        # 七项自检：secret / API / 暴露面 / NO_PROXY / 权限 / 挂载 / 常驻
./netroamerd install       # 用户级常驻（影子模式：只记录自愈建议，不动作）
```

**挂载自愈规则**（让 mihomo 认得 netroamerd 的直连 provider）：

1. `clash/rules-merge.yaml` 的 `netroamer:mount` 段 → Clash Verge「编辑 Merge」
   （或 mihomo `~/.config/mihomo/config.yaml`）
2. `clash/rules-prepend.yaml` 的 `netroamer:mount` 段 → 订阅「编辑规则」
3. 在 Clash Verge 重新激活订阅

完成后 `netroamerd doctor` 的「provider 挂载」项应显示 ✓。

**启用实际自愈**（默认影子模式，先观察再放权）：

```bash
# 编辑 ~/Library/LaunchAgents/com.netroamer.agent.plist，在 ProgramArguments 末尾加 --actuate
launchctl bootout gui/$(id -u)/com.netroamer.agent
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.netroamer.agent.plist
```

---

## 日常命令

```bash
netroamerd status              # 样本跨度、TOP 域名延迟矩阵、服务状态
netroamerd doctor              # 环境自检 + 可执行修复指令
netroamerd judge               # 判定器单轮试运行（只读，不动作）
netroamerd rollback --last 2   # 手动撤销最近动作
netroamerd uninstall --full    # 全量回滚：服务 + provider + 配置挂载段（数据留档）
```

---

## 工作原理

```
mihomo API ──▶ collector ──▶ store ──▶ analyzer ──▶ judge ──▶ actuator
(环回 TCP      WS 快照       SQLite     5min 滚动     两层判定   provider 热载
 + secret)     退避重连      0700/0600  EWMA/P95     双闸+配额   + 回读验证
                    │                                    ▲
                    └──▶ sampler（30min 主动探测）────────┘
                             │
                             └──▶ health（60s 节点健康）──▶ PUT /proxies 切换
                                       │                      │
                                       └──▶ retest（24h 复测）─┘
                                                 │
                                                 ▼
                                          evidence（修复前后对照）+ 系统通知
```

**慢域名自动直连**（两条触发条件全满足才动手）：

1. 域名级初筛：跨桶样本 ≥50，EWMA > 800ms 或 P95 > 1500ms；
2. 桶级方向验证：≥3 个活跃桶同向超阈（防单次抖动）；
3. 资格闸：`.cn` / 境内域名列表 / 用户 allow 文件（正向白名单）；
4. 豁免闸：订阅规则中该域名命中任何非 DIRECT 结论 → 永不自动直连（负向硬闸）；
5. 状态机：7 天内最多动作 2 次；回滚后 7 天冷却；累计 3 次回滚进终态（永久仅通知）；
6. 同口径验证：动作前对同一 URL 分别经 DIRECT 与出口节点实时探测，直连须快 50% 以上。

**坏节点自动切换**：当前出口节点 60s 探测一次；连续 3 败判硬故障、连续 2 次
超 `EWMA+3×MAD` 判劣化；健康分 `score = ewma × penalty`（失败 ×2，恢复按
5/10/15/30 分钟四阶段减半）；切换带滞回与 10 分钟防抖；用户手动选择的组 30 分钟
内不动（硬故障例外并留痕）；切换后 2 小时内冻结慢域名直连动作——一个故障根因
只允许一个自愈动作。

---

## 隐私：不回传清单

netroamerd **没有任何对外网络出口**。除连接本机 mihomo API（环回 TCP 或
unix socket）与对候选域名的主动探测外，不发起任何请求。

永不采集、存储或上传：

- ❌ 完整 URL、请求路径、请求/响应内容
- ❌ 源 IP:端口 对、订阅链接与订阅身份
- ❌ 节点名称（探测记录只存组名，路径只存 `direct | proxy`）
- ❌ SSID 明文（时段桶内单向哈希）
- ❌ 原始连接时间线（仅时段桶聚合 + 样本 7 天保留）

唯一例外：无域名连接（裸 IP 直连）在 `host_sld` 列以目的 IP 充当标识，仍不出设备。

所有数据在 `~/.local/state/netroamer/`，目录 0700、文件 0600。

---

## 文档

| 文档 | 内容 |
|---|---|
| [research/00-executive-summary.md](research/00-executive-summary.md) | 定位、竞品结论、推荐路线 |
| [research/05-netroamerd-design.md](research/05-netroamerd-design.md) | 架构、数据模型、判定算法、6 周排期 |
| [research/reviews/](research/reviews/) | 两轮对抗评审（6 个 P0 全部落实） |
| [netroamerd/README.md](netroamerd/README.md) | agent 模块全景与命令手册 |
| [clash/](clash/) | Merge / rules prepend 挂载模板 |

---

## 当前状态

**0.1.0-alpha** —— 6 周 MVP 功能全部交付（W1 遥测 → W2 聚合自检 → W3 判定器 → W4 热载回滚
→ W5 节点切换 → W6 证据与卸载闭环），16 测试包全绿。已进入**实操验证期**：
自愈动作刚放权（--actuate + 白名单种子域名），动作链路的实战数据还在积累——
这就是版本号是 0.1-alpha 而不是 1.0 的原因。

后续规划：P2 Web 控制台控制面与分发完善、P3 本地模型升级、P4 多端协同。

## License

MIT
