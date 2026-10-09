# HANDOVER — 跨机器交接文档

> 本文档是 netroamer 的跨机器交接锚点：macOS 开发机 → Kubuntu（zcode）继续开发。
> 最后更新：2026-10-09，HEAD `6063167`。换机后第一件事：读完本文，再动手。

---

## 1. 项目一句话

netroamer = 中国开发者的**本地无感自愈网络层**。基于 mihomo external controller API
做「观测 → 习惯学习 → 规则热载」闭环：慢域名自动直连、死节点自动切换、代理变优自动退出。
核心守护进程是 Go 写的 `netroamerd`，仓库根还有 `bootstrap.sh`（开发环境一键镜像配置）。

**不可协商红线**（设计约束，任何改动不得违反）：
TUN 永不开；永不用 root（用户级 LaunchAgent / systemd --user）；数据不出设备；
判定宁缺勿滥（绝不自动直连墙内域名）；一切可回滚（`uninstall --full`）。

## 2. 当前状态快照（2026-10-09）

- **版本**：v0.1.0-alpha（GitHub Release 预发布，CI 五平台二进制 + SHA256SUMS 自动附带）。
- **里程碑**：P1.5 放权运营期已生效（allow 白名单 + `--actuate` 实操模式）；
  **Phase B 控制面收官**（`d6469f7`）：模式热切、allow 白名单增删、rollback 按钮、
  镜像面板 18 服务（13 可切换）+ 诊断按钮全绿闭环（`3151fa7`→`6063167`）。
- **原 macOS 开发机真机终态**：实操模式运行中，全机 15 镜像 / 0 官方源超时
  （goproxy.cn / rsproxy / gh-proxy 等已切）。
- **本地==远端**：所有提交已 push 至 `Partiverse/netroamer` main。
- **测试**：16 个测试包全绿（`cd netroamerd && go test ./...`）。

## 3. 仓库结构导览

| 路径 | 内容 |
|---|---|
| `netroamerd/` | Go 主模块（`module github.com/Partiverse/netroamer/netroamerd`，go 1.26） |
| `netroamerd/cmd/netroamerd/` | CLI 入口（run/install/uninstall/status/ui/doctor/…） |
| `netroamerd/internal/` | 18 个包：collector/analyzer/judge/actuator/retest（核心闭环）、mihomoapi（API 客户端）、webui（控制台）、mirrors（镜像切换）、service（常驻）、notify/evidence/exempt/prober/sampler/… |
| `bootstrap.sh` / `bootstrap.ps1` | 开发环境一键镜像配置（macOS/Linux/Windows） |
| `research/` | 深度调研 + 路线图（`06-progress-roadmap-gui.md` 是路线定稿） |
| `reviews/` | 对抗评审归档（**W4 级别改动开工前必读** `2026-09-29-05-independent.md`） |
| `clash/` | mihomo 挂载/聚合相关脚本与模板 |
| `share/` | 共享文档 |

## 4. 构建与测试（Kubuntu 直接可用）

```bash
# Go ≥ 1.26（go.mod 要求 go 1.26.0）
cd netroamerd
go build ./cmd/netroamerd
go test ./...          # 16 测试包，应全绿
go vet ./...
```

控制台：`netroamerd ui` 起 `127.0.0.1:7790`（环回 + ui-token，安全模型见 §7）。

## 5. Kubuntu 迁移清单（macOS → Linux 差异）

| 维度 | macOS（原开发机） | Kubuntu（新环境） |
|---|---|---|
| 常驻安装 | LaunchAgent `com.netroamer.agent` | **`systemd --user` unit，代码已实现**（`internal/service/service.go` `installLinux`），但**从未在真机 Linux 验证——这是新机第一优先验证项** |
| 通知 | osascript | notify-send（`internal/notify` 双平台已实现） |
| 内核 | Clash Verge Rev（macOS 版） | Linux 版 Clash Verge Rev 或裸 mihomo；配置目录路径需实机确认（macOS 是 `~/Library/Application Support/io.github.clash-verge-rev/`，Linux 一般 `~/.config/clash-verge/` 一类） |
| ext-ctl 传输 | 本机 mihomo 只开 unix domain socket | netroamerd 已实现「socket 在盘优先 unix，否则 TCP 强制环回」自动选择（`internal/mihomoapi`），两条路都有代码 |
| bootstrap | 全功能 | 支持 Linux；`--dns` 写 systemd-resolved 需 sudo；Docker 走 `systemctl restart docker`；mihomo 需手动启动（脚本只提示） |
| 路径 | 与 Linux 完全一致 | `~/.config/netroamer/`（配置/allow/exempt）、`~/.local/state/netroamer/`（db/evidence/ui-token）、`~/.local/bin/netroamerd`（二进制） |

**新机初始化建议顺序**：
1. 装 Go 1.26 + git；克隆本仓库。
2. `bash bootstrap.sh`（无 sudo 部分）配镜像；apt 换源不在 18 服务里，按 Kubuntu 惯例手动做。
3. 装并启动 mihomo/Clash Verge，确认 ext-ctl（unix socket 或 TCP 环回）。
4. `go test ./...` 全绿后 `go build`；`./netroamerd doctor` 看七项。
5. **真机验证 `netroamerd install` 的 systemd 路径**（`systemctl --user status com.netroamer.agent`）。
6. 先影子模式跑几天看 judgments，再决定是否 `--actuate`。

**Kubuntu 上注意**：
- mirrors 18 服务里 **Homebrew 段是 macOS-only**，Linux 上该行会 not-found，属预期。
- **apt 镜像切换是 Linux 新机会**（18 服务未覆盖），可作 P2.5 新增服务。
- P2 剩余的 brew formula 专项是 macOS-only，Kubuntu 上跳过或留给 mac 会话。
- git 镜像注意：`insteadOf` 只重写 fetch 会把 push 也劫进只读代理卡凭据——
  必须配 `pushInsteadOf` 身份回官方（bootstrap.sh 已沉淀此修复，`6063167`）。

## 6. 待办路线（来自 research/06 定稿 + 后续演进）

- **P2 剩余**：bootstrap 两段式 + 签名 + `--dry-run`（G6 分发收尾，需发布密钥体系，建议专项一轮）；
  brew formula（macOS-only）+ doctor brew 抽验。
- **P3 智能**：SSID 感知、本地模型（EWMA/LightGBM 级，前提是 judgments 数据积累充足）、
  原生托盘、macOS 公证。
- **P4 多端**：先解红线冲突。
- **Kubuntu 特有新增候选**：apt 镜像服务、systemd 真机验证、Linux 版 Clash Verge 配置目录适配确认。

## 7. 安全模型与关键实现事实（改动前必读）

- **控制台安全模型**：环回绑定 + 自定义头（跨源不可伪造）+ Host 校验（挡 DNS rebinding）
  + 服务白名单 + judgments 审计。ui-token 在 `~/.local/state/netroamer/ui-token` 第一行，
  前端首次点写操作时 prompt 输入，localStorage 记忆；`/api/token` 同源下发免手动输入。
- **挂载三教训**（Verge 增强链）：①provider path 必须在内核 `-d` 目录内（SAFE_PATHS 编译常量）；
  ②Merge 写 prepend-rules 透传不生效——必须进「订阅右键→编辑规则」增强；③订阅实际绑定的
  增强文件看 `profiles.yaml` → 订阅 option，改错文件不生效。
- **分层订阅穿透**：叶子 404 / 组 504，只有 `/group/{组}/delay` 能穿透嵌套 Selector；
  `chains` 末元素才是顶层组；`CurrentPathDelay` 用严格模式，绝不用组内其他节点延迟顶替。
- **遥测结构缺口已修**：`/connections` 无握手时长，被动遥测拿不到延迟——已有 `sampler`
  （30min 主动探测 TOP20）+ 24h 窗口回填；新功能取延迟一律走回填后的数据。
- **订阅聚合 v4 要点**：往订阅配置加组必须回答「谁把流量路由给它」（孤儿组=没加）；
  两家节点同名靠 `override.additional-prefix` 消歧义；yaml.dump 80 列折行会撕裂长 URL（必须单行手工引号）。
- **运维教训**：launchctl/systemctl 重启后必须验证 pid 变化；go build 后必须验证二进制
  mtime/strings 再部署（发生过「构建成功」实为旧产物）；`grep -cv` 计数为 0 时退出码 1 会断 `&&` 链。
- **macOS install 陷阱**：`netroamerd install` 重写 plist 会抹掉 `--actuate` 参数——升级后必须
  PlistBuddy 补参数再 bootout/bootstrap（bootout 后 sleep ≥4s 再 bootstrap 避竞态）。
  Linux systemd unit 若同样吞参数，是 Kubuntu 验证时第一个要查的点。

## 8. 交接锚点

- 历史脉络与实测教训全记录：macOS 机 `~/.zcode/cli/memories/projects/netroamer-*/memory/netroamer-v2-research-2026-09.md`
  （**不会随 git 同步**，本文档 §2/§6/§7 是其浓缩；Kubuntu 侧建议把本文件当唯一入口）。
- 路线定稿：`research/06-progress-roadmap-gui.md`；评审归档：`reviews/`。
- 版本哲学：版本号诚实化（1.x 虚高被用户指出，回落 0.1.0-alpha）；tag 触发 CI 自动出五平台二进制。
