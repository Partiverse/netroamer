// Package retest 直连动作的 24h 复测回滚状态机（05 §4.1，独立复评 P0-2 修订）：
// 每小时直连探测一次，滑动窗口判回滚（连续 3 败或 6 中 3 败，不用单点），
// 累计 3 次回滚进终态（judge 据此永久仅通知）。
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
)

type Manager struct {
	st          *store.Store
	act         *actuator.Actuator
	api         prober.DelayTester
	now         func() time.Time
	log         *slog.Logger
	evidenceDir string // 非空时回滚写证据
}

func New(st *store.Store, act *actuator.Actuator, api prober.DelayTester, log *slog.Logger, evidenceDir string) *Manager {
	return &Manager{st: st, act: act, api: api, now: time.Now, log: log, evidenceDir: evidenceDir}
}

// Tick 由 run 主循环每 5 分钟调用：为活跃动作补齐逐时探测并评估回滚。
func (m *Manager) Tick(ctx context.Context) error {
	now := m.now()
	actions, err := m.st.ActiveActions(ctx, now, RetestWindow)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, a := range actions {
		if seen[a.Target] {
			continue
		}
		seen[a.Target] = true
		if err := m.tickOne(ctx, a, now); err != nil {
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

	m.log.Warn("复测判回滚", "target", action.Target,
		"consecutive_failures", consec, "failures_in_window", fails, "window", len(last))
	if err := m.act.Disable(ctx, action.Target); err != nil {
		return fmt.Errorf("回滚热载失败: %w", err)
	}
	reason := fmt.Sprintf("复测回滚：窗口 %d 次中失败 %d 次（连续 %d）", len(last), fails, consec)
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
			After: map[string]float64{
				"window_size": float64(len(last)), "failures": float64(fails), "consecutive": float64(consec),
			},
		})
	}
	_ = notify.Send("netroamerd", "已回滚 "+action.Target+"：直连复测失败")
	return nil
}
