# netroamerd

netroamer 常驻 agent：基于 mihomo external controller API 的本地无感自愈网络层。
**6 周 MVP（P0 遥测 + P1 自愈闭环）已全部交付**；当前为影子模式——所有自愈
动作只记录建议不执行，确认稳定后 `run --actuate` 启用。
设计蓝图：[../research/05-netroamerd-design.md](../research/05-netroamerd-design.md)。

## 模块全景

- **collector**：WS 订阅 `/connections`（每秒快照），连接消失即产出样本；
  断线指数退避 + 端点自动重发现；只订阅、不注入。
- **store**：SQLite（纯 Go 无 cgo），WAL、0700/0600、samples 7 天保留；
  agg / judgments（含 params_hash 指纹与终态计数）/ probes 表。
- **analyzer**：每 5 分钟滚动聚合（时段桶 × 主域 × 路径）→ EWMA / P95 / 失败率。
- **judge**：慢域名自动直连两层判定（域名级初筛 + 桶级方向验证）+
  资格/豁免双闸（全本地）+ 配额/冷却/终态状态机 + 联动静默窗。
- **prober**：同口径探测（DIRECT vs 出口节点，同一 generate_204 URL）。
- **actuator**：自有 rule-provider 写文件 → PUT 热载 → ruleCount 对账 +
  RULE-SET 行验证，失败退避重试并还原文件；挂载指纹清理（uninstall --full）。
- **retest**：24h 逐时直连复测，滑动窗口回滚（连续 3 败或 6 中 3 败），
  累计 3 次回滚进终态（永久仅通知）。
- **health**：节点健康状态机（分层探测、连续性判坏、score=ewma×penalty、
  恢复四阶段、切换滞回、手动选择 30 分钟窗口硬故障例外）。
- **evidence / notify**：每次动作生成修复前后对照（JSON+Markdown）+ 系统通知。

## 使用

```sh
cd netroamerd
go build -o netroamerd ./cmd/netroamerd
./netroamerd doctor            # 七项自检（secret/API/暴露面/NO_PROXY/权限/挂载/常驻）
./netroamerd install           # 用户级常驻（LaunchAgent / systemd --user，影子模式）
./netroamerd status            # 样本跨度 / TOP 域名延迟矩阵
./netroamerd judge             # 判定器单轮试运行（只读）
./netroamerd rollback --last 2 # 手动撤销最近动作
./netroamerd uninstall --full  # 全量回滚（服务+provider+挂载指纹段；数据保留）
```

或直接 `bash bootstrap.sh`（有 Go 时自动构建并安装 netroamerd）。

启用实际自愈：`~/Library/LaunchAgents/com.netroamer.agent.plist` 的
ProgramArguments 追加 `--actuate`，然后 `launchctl bootout gui/$(id -u)/com.netroamer.agent && launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.netroamer.agent.plist`。

证据：`~/.local/state/netroamer/evidence/`（每次动作的修复前后对照）。
日志：`~/.local/state/netroamer/netroamerd.log`。

## 隐私：不回传清单

netroamerd 没有任何对外网络出口——除连接本机 mihomo API（环回 TCP / unix socket）
与对候选域名的主动探测外，不发起任何网络请求。**永不采集、存储或上传**：

- 完整 URL、请求路径、请求/响应内容；
- 源 IP:端口 对、订阅链接与订阅身份、节点名称（probes 只存组名，路径只存 direct|proxy）；
- SSID 明文（时段桶内单向哈希）；
- 原始连接时间线（仅时段桶聚合 + 样本 7 天保留）。

唯一例外：无域名连接（裸 IP 直连）在 host_sld 列以目的 IP 充当标识，仍不出设备。

## 安全边界（research/05 §2）

- 只连 `127.0.0.1` + Bearer；发现的非环回绑定强制改写。
- 只写自有 rule-provider 与 `PUT /proxies/{group}`；不读/不写订阅与用户主配置；
  不动 TUN/DNS。
- provider 文件位于内核 -d 目录（SAFE_PATHS 约束）；挂载段带
  `netroamer:mount` 指纹，卸载按指纹精确清理。
- LaunchAgent / systemd unit 均为用户级，绝不 root。

## 构建与运行

```sh
cd netroamerd
go build -o netroamerd ./cmd/netroamerd
./netroamerd run --once 30s   # 调试 30 秒
./netroamerd doctor           # 环境自检（secret/可达/暴露面/NO_PROXY/权限/常驻）
./netroamerd install          # 安装并启动用户级常驻服务（无 root，LaunchAgent 不设 UserName）
./netroamerd status           # 库 / 样本跨度 / TOP 域名×路径 延迟矩阵
./netroamerd uninstall        # 停止常驻（数据与 ~/.local/bin/netroamerd 保留）
```

数据：`~/.local/state/netroamer/telemetry.db`（本机 only，无任何上云）。
日志：`~/.local/state/netroamer/netroamerd.log`。

## 隐私：不回传清单

netroamerd 没有任何对外网络出口——除连接本机 mihomo API（环回 TCP / unix socket）外
不发起任何网络请求。**永不采集、存储或上传**：

- 完整 URL、请求路径、请求/响应内容；
- 源 IP:端口 对、订阅链接与订阅身份；
- 节点名称（代理路径只存 `direct | proxy` 二值，节点健康分 W5 起仅存组内索引）；
- SSID 明文（仅时段桶内单向哈希，盐存本机 0600 文件）；
- 原始连接时间线（仅存时段桶聚合 + 样本环形保留 7 天）。

唯一例外：无域名连接（裸 IP 直连）在 `host_sld` 列以目的 IP 充当标识，仍不出设备。

## 安全边界（research/05 §2）

- 只连 `127.0.0.1` + `Authorization: Bearer`；发现的非环回绑定强制改写。
- 不读/不写订阅与用户主配置；不动 TUN/DNS。
- LaunchAgent / systemd unit 均为用户级，绝不 root（防 MITRE T1543 误报与提权面）。
