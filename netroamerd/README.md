# netroamerd

netroamer 常驻 agent：基于 mihomo external controller API 的本地无感自愈网络层。
设计蓝图：[../research/05-netroamerd-design.md](../research/05-netroamerd-design.md)。

## 当前进度：P0 W3（判定器：探测器 / 豁免双闸 / 慢域名判定）

- **collector**：WS 订阅 `/connections`（每秒全量快照），连接消失即产出样本；
  断线指数退避重连（1s→60s + 抖动）；断线后自动重发现端点（Verge 重启换 socket 路径）；
  对未知字段宽容解析；只订阅、不注入。
- **store**：SQLite（modernc 纯 Go 无 cgo），WAL + busy_timeout + 单连接；
  目录 0700 / 文件 0600；samples 保留 7 天；judgments 带 target 索引与 params_hash。
- **analyzer**：每 5 分钟滚动聚合（时段桶 × 主域 × 路径）→ EWMA / P95 / 失败率。
- **mihomoapi**：REST 客户端（version / rules / delay / connections），
  unix socket 与环回 TCP 双传输，Bearer 鉴权，401 显式区分。
- **exempt**：慢域名自动直连的资格/豁免双闸（评审 P0-4 修订）——
  资格 = `.cn` TLD / 内置境内列表 / `~/.config/netroamer/netroamer-allow.txt`；
  豁免 = 订阅 /rules 中 DOMAIN*→非 DIRECT 本地匹配 + `netroamer-exempt.txt`；零外部拉取。
- **prober**：同口径探测（同一 `generate_204` URL 分别经 DIRECT 与出口节点），
  消除握手/首包口径失真；代理侧探测失败 = 不可比较、保守不动作。
- **judge**：两层统计判定（域名级初筛 ≥50 样本 + 桶级方向验证 ≥3 活跃慢桶）
  + 配额（7 天 2 次）/ 回滚冷却（7 天）状态机 + params_hash 参数指纹；
  `netroamerd judge` 单轮试运行只读输出（写 provider 与动作在 W4 actuator）。
- **CLI**：`run` / `status` / `judge` / `doctor` / `install` / `uninstall`。

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
