// netroamerd —— netroamer 常驻 agent（research/05 设计蓝图）。
// P0：W1 遥测采集（WS /connections → SQLite）+ W2 聚合/自检/常驻安装。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/Partiverse/netroamer/netroamerd/internal/actuator"
	"github.com/Partiverse/netroamer/netroamerd/internal/analyzer"
	"github.com/Partiverse/netroamer/netroamerd/internal/collector"
	"github.com/Partiverse/netroamer/netroamerd/internal/config"
	"github.com/Partiverse/netroamer/netroamerd/internal/doctor"
	"github.com/Partiverse/netroamer/netroamerd/internal/evidence"
	"github.com/Partiverse/netroamer/netroamerd/internal/exempt"
	"github.com/Partiverse/netroamer/netroamerd/internal/health"
	"github.com/Partiverse/netroamer/netroamerd/internal/judge"
	"github.com/Partiverse/netroamer/netroamerd/internal/mihomoapi"
	"github.com/Partiverse/netroamer/netroamerd/internal/notify"
	"github.com/Partiverse/netroamer/netroamerd/internal/retest"
	"github.com/Partiverse/netroamer/netroamerd/internal/sampler"
	"github.com/Partiverse/netroamer/netroamerd/internal/service"
	"github.com/Partiverse/netroamer/netroamerd/internal/store"
	"github.com/Partiverse/netroamer/netroamerd/internal/webui"
)

const version = "0.1.0-alpha"

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "run":
		run(os.Args[2:], log)
	case "status":
		statusCmd(log)
	case "judge":
		judgeCmd(log)
	case "rollback":
		fs := flag.NewFlagSet("rollback", flag.ExitOnError)
		last := fs.Int("last", 1, "撤销最近 N 条未回滚动作")
		_ = fs.Parse(os.Args[2:])
		n := max(min(*last, 50), 1) // 限幅 1..50，防异常输入
		rollbackCmd(n, log)
	case "doctor":
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		os.Exit(doctor.Run(ctx, os.Stdout))
	case "install":
		if err := service.Install(mustExe()); err != nil {
			log.Error("安装失败", "err", err)
			os.Exit(1)
		}
		stateDir, _ := service.StateDir()
		fmt.Printf("已安装并启动 %s（用户级，开机自启 + 崩溃拉起）\n日志: %s\n验证: netroamerd status / netroamerd doctor\n",
			service.Label, filepath.Join(stateDir, "netroamerd.log"))
	case "uninstall":
		fs := flag.NewFlagSet("uninstall", flag.ExitOnError)
		full := fs.Bool("full", false, "全量回滚：同时清理 provider 文件与配置中的挂载指纹段")
		_ = fs.Parse(os.Args[2:])
		uninstallCmd(*full, log)
	case "ui":
		// 自动打开仅用字面量 URL（不引入任何外部输入到 exec）；
		// 自定义地址只校验并打印。
		const defaultURL = "http://127.0.0.1:7790"
		if len(os.Args) > 2 {
			clean, ok := loopbackHostPort(os.Args[2])
			if !ok {
				fmt.Fprintln(os.Stderr, "拒绝非环回或非法端口的地址:", os.Args[2])
				os.Exit(2)
			}
			fmt.Println("控制台地址: http://"+clean, "（需常驻运行中且未禁用 --ui）")
			return
		}
		fmt.Println("控制台:", defaultURL, "（需常驻运行中且未禁用 --ui；在浏览器打开即可）")
	case "version", "--version", "-v":
		fmt.Println("netroamerd", version)
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "未知子命令 %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

// loopbackHostPort 控制台地址安全解析：主机必须环回（127.x/::1/localhost），
// 端口必须 1..65535 纯数字。返回清洗后的 host:port；不合法一律拒绝——
// 结果只由白名单字符构成，可安全用于监听与拼 URL/外部命令参数。
func loopbackHostPort(addr string) (string, bool) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", false
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "", false
	}
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return "", false
		}
	}
	return net.JoinHostPort(host, port), true
}

func mustExe() string {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "无法定位自身二进制:", err)
		os.Exit(1)
	}
	return exe
}

func usage() {
	fmt.Print(`netroamerd —— 本地无感自愈网络层常驻 agent（P0 W4）

用法:
  netroamerd run [flags]     常驻采集 → 聚合 → 判定（缺省影子模式，--actuate 实际动作）
  netroamerd status          延迟矩阵摘要（库/样本/聚合/服务状态）
  netroamerd judge           慢域名判定器单轮试运行（只读，不写规则/不动作）
  netroamerd rollback [--last N]  撤销最近 N 条直连动作（默认 1）
  netroamerd doctor          环境自检：secret、API 可达、暴露面、NO_PROXY、权限、挂载、常驻
  netroamerd install         安装并启动用户级常驻（macOS LaunchAgent / Linux systemd --user）
  netroamerd uninstall [--full]  停止常驻；--full 全量回滚（provider+挂载指纹段）
  netroamerd ui [addr]       打开本地控制台（默认 http://127.0.0.1:7790）
  netroamerd version

run flags:
  --once <dur>   调试：运行指定时长后退出（如 30s），缺省常驻
  --url <u>      覆盖 mihomo API 地址（默认自动发现，须环回）
  --db <path>    覆盖 SQLite 路径（默认 ~/.local/state/netroamer/telemetry.db）
  --actuate      实际执行判定动作（写 provider + 热载 + 复测回滚）；缺省影子模式只记录

secret 发现顺序: $NETROAMER_MIHOMO_SECRET → ~/.config/netroamer/mihomo.secret
  → mihomo/Clash Verge 配置 secret:（内容不回显、不写日志）
`)
}

func run(args []string, log *slog.Logger) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	once := fs.Duration("once", 0, "调试运行时长，0=常驻")
	urlFlag := fs.String("url", "", "覆盖 mihomo API base URL")
	dbFlag := fs.String("db", "", "覆盖 SQLite 路径")
	actuate := fs.Bool("actuate", false, "实际执行判定动作（缺省影子模式）")
	uiFlag := fs.Bool("ui", true, "启动本地控制台（127.0.0.1 只读）")
	uiAddr := fs.String("ui-addr", "127.0.0.1:7790", "控制台监听地址（必须环回）")
	_ = fs.Parse(args)

	cfg, err := config.Load()
	if err != nil {
		log.Error("配置加载失败", "err", err)
		os.Exit(1)
	}
	if *urlFlag != "" {
		if err := cfg.SetBaseURL(*urlFlag); err != nil {
			log.Error("URL 无效", "err", err)
			os.Exit(1)
		}
		// 显式指定 TCP 端点时清掉自动发现的 unix socket：127.0.0.1 不是
		// socket 文件，沿用会 dial 失败（且 127.0.0.1:9097 是 Verge 下
		// 常见的未监听残留端口）
		cfg.UnixSocket = ""
	}
	if *dbFlag != "" {
		cfg.DBPath = *dbFlag
	}
	if cfg.LoopbackRewritten {
		log.Warn("external-controller 非环回地址，已强制改写为 127.0.0.1（安全红线，research/05 §2）")
	}
	if cfg.Secret == "" {
		log.Warn("未发现 mihomo secret；若 API 已设 secret 将持续 401。修复：运行 `netroamerd doctor` 获取指令")
	}
	log.Info("netroamerd 启动", "version", version, "config", cfg.String())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *once > 0 {
		log.Info("调试模式：限时运行", "duration", *once)
		time.AfterFunc(*once, stop)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Error("SQLite 打开失败", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	collectorErr := make(chan error, 1)
	opts := collector.Options{URL: cfg.BaseURL, UnixSocket: cfg.UnixSocket, Secret: cfg.Secret}
	opts.Rediscover = func() (string, string, string) {
		nc, err := config.Load()
		if err != nil {
			return "", "", ""
		}
		// 显式覆盖了端点时不重发现（否则会被自动发现结果覆盖回无效组合）
		if *urlFlag != "" {
			return "", "", ""
		}
		return nc.BaseURL, nc.UnixSocket, nc.Secret
	}
	go func() {
		collectorErr <- collector.Run(ctx, opts, log, st.InsertSamples)
	}()

	// 判定/动作/复测（05 §4.1；W4）+ 节点健康（§4.2；W5）：缺省影子模式
	api := mihomoapi.New(cfg)
	evidenceDir := filepath.Join(filepath.Dir(cfg.DBPath), "evidence")
	hm := health.New(api, st, log, *actuate, evidenceDir)
	act := actuator.New(api, cfg.ProviderPath)
	rt := retest.New(st, act, api, log, evidenceDir)
	// 主动延迟采样：补齐 /connections 无握手时长的缺口（否则判定器永不触发）
	sp := sampler.New(api, st, log, exitNodeResolver(api))

	verdictRing := webui.NewRing(500) // 判定结论环形缓冲（控制台时间线）

	// 本地控制台（research/06 §5 Phase A：只读，仅环回）
	if *uiFlag {
		if clean, ok := loopbackHostPort(*uiAddr); !ok {
			log.Warn("控制台地址必须为 127.0.0.1/localhost 环回且端口合法，已禁用", "addr", *uiAddr)
		} else {
			*uiAddr = clean
			handler := webui.Handler(webui.Deps{
				Version: version, Actuate: *actuate, Started: time.Now(),
				ST: st, Health: hm, Ring: verdictRing, EvidenceDir: evidenceDir,
			})
			srv := &http.Server{Addr: *uiAddr, Handler: handler, ReadHeaderTimeout: 5 * time.Second}
			go func() {
				log.Info("控制台已启动", "addr", "http://"+*uiAddr)
				if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					log.Warn("控制台退出", "err", err)
				}
			}()
			defer srv.Close()
		}
	}

	paramsHash := judge.Default().Hash()
	runJudge := func() {
		ctxJ, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		rules, err := api.Rules(ctxJ)
		if err != nil {
			log.Warn("judge 跳过本轮：读取 /rules 失败", "err", err)
			return
		}
		home, _ := os.UserHomeDir()
		ncDir := filepath.Join(home, ".config", "netroamer")
		verdicts, err := judge.Run(ctxJ, judge.Deps{
			Store:      st,
			API:        api,
			Matcher:    exempt.BuildMatcher(rules),
			AllowList:  exempt.UserList(filepath.Join(ncDir, "netroamer-allow.txt")),
			ExemptList: exempt.UserList(filepath.Join(ncDir, "netroamer-exempt.txt")),
			ExitNode:   exitNodeResolver(api),
			Params:     judge.Default(),
			LastBadNode: func() (time.Time, bool) {
				t, ok, err := hm.LastBadNodeSwitch(ctxJ)
				if err != nil || !ok {
					return time.Time{}, false
				}
				return t, true
			},
		})
		if err != nil {
			log.Warn("judge 失败", "err", err)
			return
		}
		for _, v := range verdicts {
			verdictRing.Add(v)
			if v.Action == "" {
				log.Info("judge", "host", v.Host, "skip", v.Skipped)
				continue
			}
			if !*actuate {
				log.Info("judge 影子建议（--actuate 后执行）", "host", v.Host, "evidence", v.Evidence)
				continue
			}
			if err := act.Enable(ctxJ, v.Host); err != nil {
				log.Error("直连动作失败（文件已还原，不消耗配额）", "host", v.Host, "err", err)
				continue
			}
			_ = st.InsertJudgment(ctxJ, store.Judgment{TS: time.Now().Unix(),
				Kind: "slow_direct", Target: v.Host, Action: "DIRECT on",
				Reason: v.Evidence, ParamsHash: paramsHash})
			_, _ = evidence.Write(evidenceDir, evidence.Record{
				Time: time.Now(), Kind: "slow_direct", Target: v.Host,
				Action: "DIRECT on", Reason: v.Evidence,
			})
			_ = notify.Send("netroamerd", "慢域名已自动直连："+v.Host)
			log.Info("已执行直连动作", "host", v.Host, "evidence", v.Evidence)
		}
	}

	updateAgg := func() {
		now := time.Now()
		n, err := analyzer.Update(ctx, st, now)
		if err != nil {
			log.Warn("聚合失败", "err", err)
			return
		}
		filled, err := analyzer.FillLatencyFromProbes(ctx, st, now)
		if err != nil {
			log.Warn("延迟回填失败", "err", err)
		}
		log.Info("聚合完成", "groups", n, "延迟回填", filled)
	}
	updateAgg() // 启动先聚合一次（空库无妨）

	prune := time.NewTicker(time.Hour) // samples 保留 7 天（research/05 §3）
	defer prune.Stop()
	agg := time.NewTicker(5 * time.Minute) // 滚动聚合（research/05 §3）
	defer agg.Stop()
	judgeTick := time.NewTicker(5 * time.Minute) // 判定 + 复测状态机
	defer judgeTick.Stop()
	healthTick := time.NewTicker(time.Minute) // 节点健康探测（§4.2：出口 60s/次）
	defer healthTick.Stop()
	sampleTick := time.NewTicker(sampler.Interval) // 主动延迟采样（30min/轮）
	defer sampleTick.Stop()
	beat := time.NewTicker(10 * time.Minute) // 心跳：72h 挂机验收的存活观测点
	defer beat.Stop()

	// 启动顺序：先采样（补延迟）→ 再聚合（回填）→ 再判定，保证首轮即有完整输入
	sp.Tick(ctx)
	updateAgg()
	runJudge()

	for {
		select {
		case err := <-collectorErr:
			n, _ := st.Count(context.Background())
			if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				log.Error("collector 异常退出", "err", err)
				os.Exit(1)
			}
			log.Info("collector 已停止", "samples_total", n)
			return
		case <-agg.C:
			updateAgg()
		case <-judgeTick.C:
			runJudge()
			if err := rt.Tick(ctx); err != nil {
				log.Warn("复测状态机异常", "err", err)
			}
		case <-healthTick.C:
			hm.Tick(ctx)
		case <-sampleTick.C:
			sp.Tick(ctx)
		case <-prune.C:
			if n, err := st.Prune(ctx, time.Now(), 7*24*time.Hour); err != nil {
				log.Warn("样本清理失败", "err", err)
			} else if n > 0 {
				log.Info("清理过期样本", "deleted", n)
			}
		case <-beat.C:
			if n, err := st.Count(ctx); err == nil {
				log.Info("心跳", "samples_total", n)
			}
		case <-ctx.Done():
			log.Info("退出中", "reason", "signal")
			select {
			case <-collectorErr:
			case <-time.After(3 * time.Second):
				log.Warn("collector 收尾超时，强制退出")
			}
			return
		}
	}
}

// exitNodeResolver 解析域名当前出口路径（仅内存使用不落库，research/05 §2
// 红线：不存节点名称）。返回 顶层组名|当前叶子节点名 —— 分层订阅下只有
// /group/{顶层组}/delay 能穿透嵌套组（实测：叶子节点 404、组 504）。
func exitNodeResolver(api *mihomoapi.Client) func(context.Context, string) (string, error) {
	return func(ctx context.Context, host string) (string, error) {
		conns, err := api.Connections(ctx)
		if err != nil {
			return "", err
		}
		for _, c := range conns {
			if strings.HasSuffix(strings.ToLower(c.Metadata.Host), host) &&
				len(c.Chains) > 0 && c.Chains[0] != "DIRECT" {
				return c.Chains[len(c.Chains)-1] + "|" + c.Chains[0], nil
			}
		}
		return "", fmt.Errorf("无 %s 的活跃代理连接", host)
	}
}

// uninstallCmd 停止并移除常驻；--full 时全量回滚：
// LaunchAgent/服务 + 自有 provider 文件 + Verge/mihomo 配置中的
// netroamer:mount 指纹段。遥测库与 judgments 保留（回溯依据），
// 二进制保留（~/.local/bin/netroamerd，可手动删）。
// 注意：指纹段清理后需在 Verge 重新激活订阅。
// uninstallCmd 全量回滚（full 由 main 层解析；所有文件路径来自 config 发现）。
func uninstallCmd(full bool, log *slog.Logger) {
	if err := service.Uninstall(); err != nil && !os.IsNotExist(err) {
		log.Error("停止服务失败", "err", err)
		os.Exit(1)
	}
	fmt.Printf("已停止并移除 %s\n", service.Label)

	home, _ := os.UserHomeDir()
	if full {
		cfg, err := config.Load()
		if err == nil {
			if err := os.Remove(cfg.ProviderPath); err == nil {
				fmt.Println("已删除自有 provider:", cfg.ProviderPath)
			}
		}
		cleaned := 0
		for _, p := range actuator.MountFingerprintFiles(home) {
			if ok, err := actuator.CleanupMount(p); err == nil && ok {
				cleaned++
				fmt.Println("已清理挂载指纹段:", filepath.Base(p))
			}
		}
		if cleaned > 0 {
			fmt.Println("!! 请在 Clash Verge 重新激活订阅，使清理生效")
		}
	}
	fmt.Println("保留：遥测库 ~/.local/state/netroamer/telemetry.db（judgments 留档）、二进制 ~/.local/bin/netroamerd（可手动删）")
}

// rollbackCmd 手动撤销最近 N 条直连动作（provider 移除 + 热载验证 + judgments 留档）。
// n 由 main 层解析并限幅（1..50）；文件路径全部来自 config 发现，不接受外部路径。
func rollbackCmd(n int, log *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cfg, err := config.Load()
	if err != nil {
		log.Error("配置加载失败", "err", err)
		os.Exit(1)
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Error("SQLite 打开失败", "err", err)
		os.Exit(1)
	}
	defer st.Close()
	api := mihomoapi.New(cfg)
	act := actuator.New(api, cfg.ProviderPath)
	actions, err := st.ActiveActions(ctx, time.Now(), 30*24*time.Hour)
	if err != nil {
		log.Error("读取动作失败", "err", err)
		os.Exit(1)
	}
	var targets []string
	seen := map[string]bool{}
	for i := len(actions) - 1; i >= 0 && len(targets) < n; i-- {
		if t := actions[i].Target; !seen[t] {
			seen[t] = true
			targets = append(targets, t)
		}
	}
	if len(targets) == 0 {
		fmt.Println("没有可撤销的直连动作")
		return
	}
	for _, t := range targets {
		if err := act.Disable(ctx, t); err != nil {
			log.Error("回滚失败", "target", t, "err", err)
			continue
		}
		_ = st.InsertJudgment(ctx, store.Judgment{TS: time.Now().Unix(),
			Kind: "slow_direct", Target: t, Action: "revert",
			Reason: "手动 rollback", Reverted: true, ParamsHash: judge.Default().Hash()})
		_, _ = evidence.Write(filepath.Join(filepath.Dir(cfg.DBPath), "evidence"),
			evidence.Record{Time: time.Now(), Kind: "rollback", Target: t,
				Action: "revert", Reason: "手动 rollback"})
		fmt.Println("已回滚:", t)
	}
}

// judgeCmd 判定器单轮试运行（只读）：对当前 agg 数据跑一轮慢域名判定，
// 打印候选与跳过原因；不写 provider、不写 judgments（动作在 W4 actuator）。
func judgeCmd(log *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cfg, err := config.Load()
	if err != nil {
		log.Error("配置加载失败", "err", err)
		os.Exit(1)
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Error("SQLite 打开失败", "err", err)
		os.Exit(1)
	}
	defer st.Close()
	api := mihomoapi.New(cfg)
	rules, err := api.Rules(ctx)
	if err != nil {
		log.Error("读取 /rules 失败（豁免闸不可用，拒绝判定）", "err", err)
		os.Exit(1)
	}
	home, _ := os.UserHomeDir()
	ncDir := filepath.Join(home, ".config", "netroamer")
	verdicts, err := judge.Run(ctx, judge.Deps{
		Store:      st,
		API:        api,
		Matcher:    exempt.BuildMatcher(rules),
		AllowList:  exempt.UserList(filepath.Join(ncDir, "netroamer-allow.txt")),
		ExemptList: exempt.UserList(filepath.Join(ncDir, "netroamer-exempt.txt")),
		ExitNode:   exitNodeResolver(api),
		Now:        time.Now,
		Params:     judge.Default(),
	})
	if err != nil {
		log.Error("判定失败", "err", err)
		os.Exit(1)
	}
	fmt.Printf("netroamerd judge（试运行，规则 %d 条；W3 只读不动作）\n", len(rules))
	if len(verdicts) == 0 {
		fmt.Println("当前无候选域名（两层统计未筛出慢域名）")
		return
	}
	tw := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "  主域\t结论")
	for _, v := range verdicts {
		conclusion := v.Action
		if conclusion == "" {
			conclusion = "跳过" + v.Skipped
		}
		fmt.Fprintf(tw, "  %s\t%s\n", v.Host, conclusion)
		if v.Action != "" {
			fmt.Fprintf(tw, "    证据: %s\n", v.Evidence)
		}
	}
	tw.Flush()
}

func statusCmd(log *slog.Logger) {
	fmt.Println("netroamerd", version)
	if st, err := service.Status(); err == nil {
		fmt.Println("服务:", st, "["+service.Label+"]")
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Println("配置加载失败:", err)
		os.Exit(1)
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		fmt.Println("SQLite 打开失败（先运行 netroamerd run / install）:", err)
		os.Exit(1)
	}
	defer st.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	total, first, last, err := st.Stats(ctx)
	if err != nil {
		fmt.Println("读取样本失败:", err)
		os.Exit(1)
	}
	if sz, err := os.Stat(cfg.DBPath); err == nil {
		fmt.Printf("库: %s (%.1fMB)\n", cfg.DBPath, float64(sz.Size())/(1<<20))
	}
	if total == 0 {
		fmt.Println("样本: 0 条（采集器未运行？检查 `netroamerd status` 服务行与 `netroamerd doctor`）")
		return
	}
	fmt.Printf("样本: %d 条, %s → %s\n", total,
		time.Unix(first, 0).Format("01-02 15:04"), time.Unix(last, 0).Format("01-02 15:04"))

	if lastRun, err := st.LastAggRun(ctx); err == nil && lastRun > 0 {
		fmt.Println("聚合: 最近更新", time.Unix(lastRun, 0).Format("01-02 15:04"))
	} else {
		fmt.Println("聚合: 尚未运行（常驻模式下每 5 分钟滚动）")
	}

	rows, err := st.RecentAgg(ctx, 15)
	if err != nil || len(rows) == 0 {
		fmt.Println("TOP 域名: 暂无聚合数据")
		return
	}
	tw := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "  主域\t路径\tEWMA\tP95\t失败率\t样本")
	for _, r := range rows {
		ewma, p95 := "-", "-"
		if r.EwmaMs > 0 {
			ewma = fmt.Sprintf("%.0fms", r.EwmaMs)
		}
		if r.P95Ms > 0 {
			p95 = fmt.Sprintf("%.0fms", r.P95Ms)
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%.1f%%\t%d\n",
			r.Host, r.Via, ewma, p95, r.FailRate*100, r.N)
	}
	tw.Flush()
}
