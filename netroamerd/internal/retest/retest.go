// Package retest 直连动作的生命周期状态机（05 §4.1，独立复评 P0-2 修订 + G3 变优退出）：
// 24h 复测期：每小时直连探测，滑动窗口判回滚（连续 3 败或 6 中 3 败，不用单点）；
// 复测期后：每 6h 双侧探测（直连 + 当前代理路径），代理连续 3 次严格更优 → 自动撤销
// （「代理变优退出」——修复评审 P0-1③ 的退出路径缺失）；累计 3 次回滚进终态。
package retest

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Partiverse/netroamer/netroamerd/internal/actuator"
	"github.com/Partiverse/netroamer/netroamerd/internal/evidence"
	"github.com/Partiverse/netroamer/netroamerd/internal/notify"
	"github.com/Partiverse/netroamer/netroamerd/internal/prober"
	"github.com/Partiverse/netroamer/netroamerd/internal/store"
)

const (
	RetestWindow  = 24 * time.Hour // 动作后复测观察期
	ProbeInterval = time.Hour      // 复测节奏
	// 滑动窗口判据（P0-2：不用单点/固定 24 点 95%）
	ConsecutiveFailures = 3
	FailuresInWindow    = 3
	WindowSize          = 6
	// TerminalReverts 终态阈值：累计回滚次数
	TerminalReverts = 3

	// G3 代理变优退出
	ExitCheckInterval  = 6 * time.Hour       // 复测期后的双侧对照节奏
	ExitCheckHorizon   = 30 * 24 * time.Hour // 变优检查的动作回看窗
	ProxyBetterStrikes = 3                   // 连续 3 次代理严格更优 → 撤销
)

// ProxyProber 变优检查所需的代理路径探测能力（*mihomoapi.Client 满足）。
type ProxyProber interface {
	CurrentPathDelay(ctx context.Context, group, currentNode, testURL string, timeout time.Duration) (int, error)
}

type Manager struct {
	st          *store.Store
	act         *actuator.Actuator
	api         prober.DelayTester
	now         func() time.Time
	log         *slog.Logger
	evidenceDir string                                                 // 非空时回滚写证据
	exitNode    func(ctx context.Context, host string) (string, error) // 任一当前代理路径（顶层组|叶子）
	proxyProber ProxyProber
	exitStrikes map[string]int // G3：连续代理更优计数（内存；重启清零最多多做几轮探测）
}

func New(st *store.Store, act *actuator.Actuator, api prober.DelayTester, log *slog.Logger,
	evidenceDir string, exitNode func(context.Context, string) (string, error), proxyProber ProxyProber) *Manager {
	return &Manager{st: st, act: act, api: api, now: time.Now, log: log,
		evidenceDir: evidenceDir, exitNode: exitNode, proxyProber: proxyProber,
		exitStrikes: map[string]int{}}
}

// Tick 由 run 主循环每 5 分钟调用：按动作年龄分流——
// ≤24h 走复测状态机；>24h 走变优退出检查（G3）。
func (m *Manager) Tick(ctx context.Context) error {
	now := m.now()
	actions, err := m.st.ActiveActions(ctx, now, ExitCheckHorizon)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, a := range actions {
		if seen[a.Target] {
			continue
		}
		seen[a.Target] = true
		var err error
		if now.Sub(time.Unix(a.TS, 0)) <= RetestWindow {
			err = m.tickOne(ctx, a, now)
		} else {
			err = m.tickExitOne(ctx, a, now)
		}
		if err != nil {
			m.log.Warn("复测处理失败", "target", a.Target, "err", err)
		}
	}
	return nil
}

func (m *Manager) tickOne(ctx context.Context, action store.Judgment, now time.Time) error {
	series, err := m.st.ProbeSeries(ctx, action.Target, "retest", time.Unix(action.TS, 0))
	if err != nil {
		return err
	}
	// 逐时节流：最近一次探测不足 1h 则只做评估不再探测
	if n := len(series); n > 0 && now.Sub(time.Unix(series[n-1].TS, 0)) < ProbeInterval {
		return m.evaluate(ctx, action, series, now)
	}

	rec := probeOnce(ctx, m.api, action.Target, now)
	if err := m.st.InsertProbes(ctx, []store.ProbeRecord{rec}); err != nil {
		return err
	}
	series = append(series, rec)
	return m.evaluate(ctx, action, series, now)
}

// probeOnce 直连侧复测；失败按 P1-4 taxonomy 归类（delay test 层面
// 连接类失败归 timeout，其余归 other）。
func probeOnce(ctx context.Context, api prober.DelayTester, host string, now time.Time) store.ProbeRecord {
	ms, err := api.DelayTest(ctx, "DIRECT", prober.TestURL(host), 5*time.Second)
	rec := store.ProbeRecord{TS: now.Unix(), Target: host, Side: "direct", Purpose: "retest"}
	if err != nil {
		rec.OK = false
		rec.FailKind = classify(err)
		return rec
	}
	v := int64(ms)
	rec.OK = true
	rec.LatMs = &v
	return rec
}

// tickExitOne 变优退出检查（G3，>24h 的动作）：每 6h 双侧同口径探测，
// 直连成功且代理严格更优 → strike；连续 ProxyBetterStrikes 次 → 自动撤销。
// 任一侧探测失败都清零 strike（不可比 ≠ 代理更优，保守方向）。
func (m *Manager) tickExitOne(ctx context.Context, action store.Judgment, now time.Time) error {
	if m.exitNode == nil || m.proxyProber == nil {
		return nil // 未注入解析器（如单测），跳过变优检查
	}
	series, err := m.st.ProbeSeries(ctx, action.Target, "exit", time.Unix(action.TS, 0))
	if err != nil {
		return err
	}
	if n := len(series); n > 0 && now.Sub(time.Unix(series[n-1].TS, 0)) < ExitCheckInterval {
		return nil // 节流
	}

	// 直连侧
	direct, perr := m.api.DelayTest(ctx, "DIRECT", prober.TestURL(action.Target), 5*time.Second)
	dOK := perr == nil
	recs := []store.ProbeRecord{store.ProbeRecord{TS: now.Unix(), Target: action.Target,
		Side: "direct", Purpose: "exit", OK: dOK, FailKind: classify(perr)}}
	if dOK {
		v := int64(direct)
		recs[0].LatMs = &v
	}

	// 代理侧：任一当前活跃代理路径（域名已直连，取「代理现状」做对照）
	proxyBetter := false
	proxyMs := 0
	if path, perr := m.exitNode(ctx, action.Target); perr == nil && path != "" {
		group, leaf, _ := strings.Cut(path, "|")
		ms, err := m.proxyProber.CurrentPathDelay(ctx, group, leaf, prober.TestURL(action.Target), 5*time.Second)
		if err == nil {
			proxyMs, proxyBetter = ms, true
			v := int64(ms)
			recs = append(recs, store.ProbeRecord{TS: now.Unix(), Target: action.Target,
				Side: "proxy", Purpose: "exit", LatMs: &v, OK: true})
		}
	}
	if err := m.st.InsertProbes(ctx, recs); err != nil {
		return err
	}

	// 判定：直连与代理都成功、且代理严格更优才计 strike
	if dOK && proxyBetter && float64(proxyMs) < float64(direct) {
		m.exitStrikes[action.Target]++
	} else {
		delete(m.exitStrikes, action.Target)
	}
	if m.exitStrikes[action.Target] < ProxyBetterStrikes {
		return nil
	}
	delete(m.exitStrikes, action.Target)

	reason := fmt.Sprintf("代理变优退出：连续 %d 次探测代理（%dms）严格优于直连（%dms）",
		ProxyBetterStrikes, proxyMs, direct)
	m.log.Warn("G3 变优退出", "target", action.Target, "reason", reason)
	return m.rollback(ctx, action, reason, now)
}

func classify(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "timeout"), strings.Contains(msg, "deadline"), strings.Contains(msg, "Timeout"):
		return "timeout"
	case strings.Contains(msg, "dns"), strings.Contains(msg, "no such host"), strings.Contains(msg, "lookup"):
		return "dns"
	}
	return "other"
}

// RollbackLast 撤销最近 n 条未回滚动作（CLI 与控制台共用）。
// 返回已回滚的目标列表。actions 窗口 30 天。
func RollbackLast(ctx context.Context, st *store.Store, act *actuator.Actuator,
	n int, now time.Time, paramsHash string) ([]string, error) {
	actions, err := st.ActiveActions(ctx, now, 30*24*time.Hour)
	if err != nil {
		return nil, err
	}
	var targets []string
	seen := map[string]bool{}
	for i := len(actions) - 1; i >= 0 && len(targets) < n; i-- {
		if t := actions[i].Target; !seen[t] {
			seen[t] = true
			targets = append(targets, t)
		}
	}
	var rolled []string
	for _, t := range targets {
		if err := act.Disable(ctx, t); err != nil {
			return rolled, fmt.Errorf("回滚 %s: %w", t, err)
		}
		if err := st.InsertJudgment(ctx, store.Judgment{
			TS: now.Unix(), Kind: "slow_direct", Target: t, Action: "revert",
			Reason: "手动 rollback", Reverted: true, ParamsHash: paramsHash,
		}); err != nil {
			return rolled, err
		}
		rolled = append(rolled, t)
	}
	return rolled, nil
}

// rollback 统一回滚出口：provider 撤销 + judgments 留档 + 证据 + 通知。
func (m *Manager) rollback(ctx context.Context, action store.Judgment, reason string, now time.Time) error {
	if err := m.act.Disable(ctx, action.Target); err != nil {
		return fmt.Errorf("回滚热载失败: %w", err)
	}
	if err := m.st.InsertJudgment(ctx, store.Judgment{
		TS: now.Unix(), Kind: "slow_direct", Target: action.Target,
		Action: "revert", Reason: reason, Reverted: true,
		ParamsHash: action.ParamsHash,
	}); err != nil {
		return err
	}
	if m.evidenceDir != "" {
		_, _ = evidence.Write(m.evidenceDir, evidence.Record{
			Time: now, Kind: "rollback", Target: action.Target,
			Action: "revert", Reason: reason,
		})
	}
	_ = notify.Send("netroamerd", "已回滚 "+action.Target+"："+reason)
	return nil
}

// evaluate 滑动窗口回滚判定（P0-2：连续 3 败或 6 中 3 败）。
func (m *Manager) evaluate(ctx context.Context, action store.Judgment, series []store.ProbeRecord, now time.Time) error {
	n := len(series)
	if n == 0 {
		return nil
	}
	last := series
	if n > WindowSize {
		last = series[n-WindowSize:]
	}
	consec := 0
	fails := 0
	for _, r := range last {
		if r.OK {
			consec = 0
		} else {
			consec++
			fails++
		}
	}
	shouldRollback := consec >= ConsecutiveFailures || fails >= FailuresInWindow
	if !shouldRollback {
		return nil
	}

	reason := fmt.Sprintf("复测回滚：窗口 %d 次中失败 %d 次（连续 %d）", len(last), fails, consec)
	m.log.Warn("复测判回滚", "target", action.Target,
		"consecutive_failures", consec, "failures_in_window", fails, "window", len(last))
	return m.rollback(ctx, action, reason, now)
}
