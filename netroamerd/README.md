# netroamerd

netroamer 常驻 agent：基于 mihomo external controller API 的本地无感自愈网络层。
设计蓝图：[../research/05-netroamerd-design.md](../research/05-netroamerd-design.md)。

## 当前进度：P0 W2（遥测 + 聚合 + 自检 + 常驻安装）

- **collector**：WS 订阅 `/connections`（每秒全量快照），连接消失即产出样本；
  断线指数退避重连（1s→60s + 抖动）；对未知字段宽容解析；只订阅、不注入。
- **store**：SQLite（modernc 纯 Go 无 cgo），WAL + busy_timeout + 单连接；
  目录 0700 / 文件 0600；samples 保留 7 天。
- **analyzer**：每 5 分钟滚动聚合（时段桶 × 主域 × 路径）→ EWMA / P95 / 失败率。
  W1 样本暂无延迟（主动探测 W3+ 接入后填充），样本量与失败率先行可用。
- **config**：mihomo 端点与 secret 自动发现（env → `~/.config/netroamer/mihomo.secret`
  → Clash Verge / mihomo 配置），secret 不回显；TCP 强制环回；
  实测 Clash Verge Rev 默认只开 unix socket → socket 在盘即优先走 unix。
- **CLI**：`run`（常驻采集）/ `status`（延迟矩阵摘要）/ `doctor`（六项自检 + 修复指令）/
  `install` / `uninstall`（用户级 LaunchAgent / systemd --user）。

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
