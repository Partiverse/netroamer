# netroamerd

netroamer 常驻 agent：基于 mihomo external controller API 的本地无感自愈网络层。
设计蓝图：[../research/05-netroamerd-design.md](../research/05-netroamerd-design.md)。

## 当前进度：P0 W1（遥测基座）

- **collector**：WS 订阅 `/connections`（每秒全量快照），连接消失即产出样本；
  断线指数退避重连（1s→60s + 抖动）；对未知字段宽容解析；只订阅、不注入。
- **store**：SQLite（modernc 纯 Go 无 cgo），WAL + busy_timeout + 单连接；
  目录 0700 / 文件 0600；samples 保留 7 天；agg/judgments 表结构先行建好（W2/W3 用）。
- **config**：mihomo 端点与 secret 自动发现（env → `~/.config/netroamer/mihomo.secret`
  → Clash Verge / mihomo 配置），secret 不回显；端点强制环回。
- **CLI**：`run`（常驻采集，`--once 30s` 调试）；`status`/`doctor` W2 交付。

## 构建与运行

```sh
cd netroamerd
go build -o netroamerd ./cmd/netroamerd
./netroamerd run --once 30s   # 调试 30 秒
./netroamerd run              # 常驻（W2 提供 LaunchAgent / systemd --user 安装器）
```

数据：`~/.local/state/netroamer/telemetry.db`（本机 only，无任何上云）。

## 安全边界（research/05 §2）

- 只连 `127.0.0.1` + `Authorization: Bearer`；发现的非环回绑定强制改写。
- 不读/不写订阅与用户主配置；不动 TUN/DNS。
- 不存完整 URL、节点名称、订阅身份、原始时间线；只存主域 + 进程名 + 时段桶聚合。
