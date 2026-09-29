// Package health 节点健康状态机与坏节点自动切换（05 §4.2 W5 定稿）：
// 判坏要求连续性（硬故障=连续 3 败、劣化=连续 2 次超 EWMA+3×MAD）、
// 显式健康分 score=ewma×penalty、恢复期 5/10/15/30min 四阶段减半、
// 切换滞回（进入 ×1.2）、手动选择 30 分钟尊重窗口（硬故障例外）、
// 单节点组仅通知。节点名只在内存，probes 落档用 组名×索引。
package health

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/Partiverse/netroamer/netroamerd/internal/evidence"
	"github.com/Partiverse/netroamer/netroamerd/internal/mihomoapi"
	"github.com/Partiverse/netroamer/netroamerd/internal/notify"
	"github.com/Partiverse/netroamer/netroamerd/internal/prober"
	"github.com/Partiverse/netroamer/netroamerd/internal/store"
)

const (
	// 探测节奏（P1-7 分层）：当前出口 60s；全组仅事件驱动
	ProbeEvery      = time.Minute
	HardFailures    = 3 // 连续 3 败 = 硬故障
	SlowStrikes     = 2 // 连续 2 次超 EWMA+3×MAD = 劣化
	EwmaAlpha       = 0.3
	MadK            = 3.0
	PenaltyMax      = 32.0
	EnterRatio      = 1.2 // 切换进入阈值：score(cur) > score(best)×1.2
	SwitchCooldown  = 10 * time.Minute
	ManualWindow    = 30 * time.Minute // 手动选择尊重窗口
	ManualHardDown  = ManualWindow     // 例外覆盖窗口期（硬故障）
	FreezeDirectFor = 2 * time.Hour    // 联动静默窗：切换后冻结 §4.1
	RestoreWindow   = 2 * time.Hour    // 重启后从 probes 重建的历史窗口
)

// NodeHealth 单节点健康状态（内存；重启经 probes 重建）。
type NodeHealth struct {
	Ewma        float64
	Mad         float64
	Penalty     float64 // 1.0 起，失败 ×2（上限 32），恢复成功分阶段减半
	ConsecFail  int
	ConsecSlow  int
	Samples     int
	LastRecover time.Time // 恢复期上一次成功时刻（驱动减半节奏）
}

func newNodeHealth() *NodeHealth { return &NodeHealth{Penalty: 1.0} }

// Score 健康分：越小越健康。
func (h *NodeHealth) Score() float64 { return h.Ewma * h.Penalty }

// FailPenaltyMs 失败按探测超时值计入 EWMA（失败≈极慢，否则失败节点
// Ewma 停留 0 导致 score 反而最低、永不触发切换）。
const FailPenaltyMs = 5000

// Observe 记录一次探测结果并推进状态机。
// 劣化判定用「历史基线」（更新前的 EWMA/MAD）——样本自身会把基线拉高，
// 用更新后的值判异常永远追不上（实测 bug）。
func (h *NodeHealth) Observe(ok bool, latMs int, now time.Time) {
	h.Samples++
	if !ok {
		h.ConsecFail++
		h.ConsecSlow = 0
		h.Penalty = min(h.Penalty*2, PenaltyMax)
		// 失败按超时值计入 EWMA
		if h.Samples == 1 || h.Ewma == 0 {
			h.Ewma = FailPenaltyMs
		} else {
			h.Ewma = EwmaAlpha*FailPenaltyMs + (1-EwmaAlpha)*h.Ewma
		}
		return
	}
	h.ConsecFail = 0
	// 劣化判定（连续性，用更新前的历史基线）
	threshold := h.Ewma + MadK*h.Mad
	slow := h.Samples > 5 && float64(latMs) > threshold
	// EWMA / MAD 在线更新
	if h.Samples == 1 || h.Ewma == 0 {
		h.Ewma = float64(latMs)
	} else {
		prev := h.Ewma
		h.Ewma = EwmaAlpha*float64(latMs) + (1-EwmaAlpha)*h.Ewma
		dev := abs(float64(latMs) - prev)
		if h.Mad == 0 {
			h.Mad = dev
		} else {
			h.Mad = 0.3*dev + 0.7*h.Mad
		}
	}
	if slow {
		h.ConsecSlow++
	} else {
		h.ConsecSlow = 0
	}
	// 恢复期减半：距上次恢复动作超过当前阶段时长则 penalty 减半
	if h.Penalty > 1.0 {
		stage := h.recoveryStage()
		if now.Sub(h.LastRecover) >= stage {
			h.Penalty = max(h.Penalty/2, 1.0)
			h.LastRecover = now
		}
	} else {
		h.LastRecover = time.Time{}
	}
}

func (h *NodeHealth) recoveryStage() time.Duration {
	switch {
	case h.Penalty >= 16:
		return 5 * time.Minute
	case h.Penalty >= 8:
		return 10 * time.Minute
	case h.Penalty >= 4:
		return 15 * time.Minute
	default:
		return 30 * time.Minute
	}
}

// HardDown 硬故障：连续 3 败。
func (h *NodeHealth) HardDown() bool { return h.ConsecFail >= HardFailures }

// Degraded 劣化：连续 2 次超阈。
func (h *NodeHealth) Degraded() bool { return h.ConsecSlow >= SlowStrikes }

// Manager 组级状态与切换决策。
type Manager struct {
	api         *mihomoapi.Client
	st          *store.Store
	log         *slog.Logger
	actuate     bool
	evidenceDir string
	nodes       map[string]*NodeHealth // key: 组名\x00组内索引
	expectNow   map[string]string      // 我们设置的组选中值（区分用户手动切换）
	manualAt    map[string]time.Time   // 用户手动切换时刻
	lastSwitch  map[string]time.Time   // 组上次自动切换
	now         func() time.Time
}

func New(api *mihomoapi.Client, st *store.Store, log *slog.Logger, actuate bool, evidenceDir string) *Manager {
	return &Manager{
		api: api, st: st, log: log, actuate: actuate, evidenceDir: evidenceDir,
		nodes: map[string]*NodeHealth{}, expectNow: map[string]string{},
		manualAt: map[string]time.Time{}, lastSwitch: map[string]time.Time{},
		now: time.Now,
	}
}

// Tick 每 60s 调用：探测各 Selector 组当前节点 → 状态机 → 事件驱动全组评估。
func (m *Manager) Tick(ctx context.Context) {
	now := m.now()
	groups, err := m.api.Groups(ctx)
	if err != nil {
		m.log.Warn("health: 拉取策略组失败", "err", err)
		return
	}
	for gname, g := range groups {
		if g.Type != "Selector" || len(g.All) < 2 {
			continue // 单节点组/非 Selector 不自动切换
		}
		// 手动切换检测：now 变化且非我们设置的值
		if exp, ok := m.expectNow[gname]; ok && g.Now != exp {
			m.manualAt[gname] = now
			m.log.Info("health: 检测到手动切换，尊重 30 分钟窗口", "group", gname, "now", g.Now)
		}
		m.expectNow[gname] = g.Now

		key := gname + "\x00" + g.Now
		h := m.nodes[key]
		if h == nil {
			h = newNodeHealth()
			m.nodes[key] = h
		}
		ms, err := m.api.DelayTest(ctx, g.Now, prober.TestURL("www.gstatic.com"), 5*time.Second)
		if err != nil {
			h.Observe(false, 0, now)
		} else {
			h.Observe(true, ms, now)
		}
		m.recordProbe(gname, g.Now, err == nil, ms, now)

		// 事件驱动：连续 2 次失败即触发全组评估（不必等硬故障）
		if h.ConsecFail >= SlowStrikes {
			m.evaluateGroup(ctx, gname, g, now)
		}
	}
}

func (m *Manager) recordProbe(group, node string, ok bool, ms int, now time.Time) {
	idx := 0
	// 索引未知时以 0 记（组内索引需要 Group.All 查找——由调用方保证内存 map）
	rec := store.ProbeRecord{TS: now.Unix(), Target: fmt.Sprintf("%s#%d", group, idx),
		Side: "proxy", Purpose: "health", OK: ok}
	if ok {
		v := int64(ms)
		rec.LatMs = &v
	} else {
		rec.FailKind = "timeout"
	}
	_ = m.st.InsertProbes(context.Background(), []store.ProbeRecord{rec})
	_ = node
}

// evaluateGroup 全组对比与切换决策。
func (m *Manager) evaluateGroup(ctx context.Context, gname string, g mihomoapi.ProxyGroup, now time.Time) {
	curKey := gname + "\x00" + g.Now
	cur := m.nodes[curKey]

	// 防抖：10 分钟内切换过不再动
	if last, ok := m.lastSwitch[gname]; ok && now.Sub(last) < SwitchCooldown {
		return
	}

	delays, err := m.api.GroupDelay(ctx, gname, prober.TestURL("www.gstatic.com"), 5*time.Second)
	if err != nil {
		m.log.Warn("health: 全组探测失败", "group", gname, "err", err)
		return
	}
	// 选最优（排除当前节点；节点名只在内存）
	type cand struct {
		name  string
		score float64
	}
	var best cand
	first := true
	for name, ms := range delays {
		if name == g.Now {
			continue
		}
		key := gname + "\x00" + name
		h := m.nodes[key]
		if h == nil {
			h = newNodeHealth()
			h.Ewma = float64(ms)
			h.Samples = 1
			m.nodes[key] = h
		}
		s := h.Score()
		if first || s < best.score {
			best, first = cand{name: name, score: s}, false
		}
	}
	if first || cur == nil {
		return
	}

	manual := now.Sub(m.manualAt[gname]) < ManualWindow
	hardDown := cur.HardDown()
	if manual && !hardDown {
		m.log.Info("health: 手动选择窗口内，不切换", "group", gname, "now", g.Now, "score", cur.Score())
		return
	}
	if cur.Score() <= best.score*EnterRatio {
		return // 未达进入阈值
	}

	reason := fmt.Sprintf("score=%.0f 超 组内最优 %s score=%.0f ×%.1f（连续失败 %d）",
		cur.Score(), best.name, best.score, EnterRatio, cur.ConsecFail)
	if manual && hardDown {
		reason = "硬故障越过手动选择窗口：" + reason
	}
	if !m.actuate {
		m.log.Info("health 影子建议（--actuate 后执行）", "group", gname, "from", g.Now, "to", best.name, "reason", reason)
		return
	}
	if err := m.api.SelectProxy(ctx, gname, best.name); err != nil {
		m.log.Error("health: 切换失败", "group", gname, "err", err)
		return
	}
	m.expectNow[gname] = best.name
	m.lastSwitch[gname] = now
	_ = m.st.InsertJudgment(ctx, store.Judgment{TS: now.Unix(),
		Kind: "bad_node", Target: gname, Action: "switch to " + best.name, Reason: reason})
	if m.evidenceDir != "" {
		_, _ = evidence.Write(m.evidenceDir, evidence.Record{
			Time: now, Kind: "bad_node", Target: gname,
			Action: "switch to " + best.name, Reason: reason,
			Before: map[string]float64{"score_before": cur.Score(), "consec_fail": float64(cur.ConsecFail)},
			After:  map[string]float64{"score_best": best.score},
		})
	}
	_ = notify.Send("netroamerd", "已切换 "+gname+"："+g.Now+" → "+best.name)
	m.log.Warn("health: 已切换坏节点", "group", gname, "from", g.Now, "to", best.name)
}

// LastBadNodeSwitch 最近一次自动切换时刻（联动静默窗输入）。
func (m *Manager) LastBadNodeSwitch(ctx context.Context) (time.Time, bool, error) {
	ts, err := m.st.LastBadNodeSwitch(ctx)
	if err != nil || ts == 0 {
		return time.Time{}, false, err
	}
	return time.Unix(ts, 0), true, nil
}

// SortGroups 稳定排序工具（测试用）。
func SortKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
