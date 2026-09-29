// Package judge 慢域名自动直连判定器（05 §4.1 修订版，2026-09-29）：
// 两层统计（域名级初筛 + 桶级方向验证）→ 资格/豁免双闸 → 配额/冷却状态机
// → 同口径实时探测。W3 只产出 Verdict（只读），写 provider 与 judgments
// 动作记录由 W4 actuator 执行；judgments 的前置记录（配额/冷却输入）
// 在测试与回放中以 InsertJudgment 构造。
package judge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/Partiverse/netroamer/netroamerd/internal/exempt"
	"github.com/Partiverse/netroamer/netroamerd/internal/prober"
	"github.com/Partiverse/netroamer/netroamerd/internal/store"
)

type Params struct {
	ScreenMinSamples       int           // 域名级初筛最小样本
	SlowEwmaMs             float64       // 慢阈值（EWMA）
	SlowP95Ms              float64       // 慢阈值（P95）
	ActiveBuckets          int           // 桶级验证最少活跃桶
	ActiveBucketMinSamples int           // 活跃桶最小样本
	DirectFasterRatio      float64       // 直连 < 代理×ratio
	ProbeTimeout           time.Duration // 单次探测超时
	QuotaWindow            time.Duration // 动作计数窗口
	QuotaMax               int           // 窗口内最多动作次数
	RevertCooldown         time.Duration // 回滚后冷却
}

func Default() Params {
	return Params{
		ScreenMinSamples: 50, SlowEwmaMs: 800, SlowP95Ms: 1500,
		ActiveBuckets: 3, ActiveBucketMinSamples: 5,
		DirectFasterRatio: 0.5, ProbeTimeout: 5 * time.Second,
		QuotaWindow: 7 * 24 * time.Hour, QuotaMax: 2,
		RevertCooldown: 7 * 24 * time.Hour,
	}
}

// Hash 判定参数指纹：写入 judgments.params_hash，算法迭代后历史判定可归因。
func (p Params) Hash() string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%+v", p)))
	return hex.EncodeToString(sum[:8])
}

type Deps struct {
	Store      *store.Store
	API        prober.DelayTester                                     // 同口径探测（生产为 *mihomoapi.Client）
	Matcher    exempt.Matcher                                         // /rules 构建的豁免匹配器
	AllowList  []string                                               // 用户 allow 文件（资格扩充）
	ExemptList []string                                               // 用户 exempt 文件
	ExitNode   func(ctx context.Context, host string) (string, error) // 出口节点解析（内存）
	Now        func() time.Time
	Params     Params
}

// Verdict 单个域名的判定结论。Action 非空 = 建议动作（W4 执行）；
// 空则 Skipped 说明原因。Evidence 是人类可读数字摘要。
type Verdict struct {
	Host     string
	Action   string
	Skipped  string
	Evidence string
}

// Run 对 agg 表全体域名执行一轮判定。只读不改：不写 agg/judgments、
// 不写 provider（W4 actuator 职责）。
func Run(ctx context.Context, d Deps) ([]Verdict, error) {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Params == (Params{}) {
		d.Params = Default() // 零值防御：忘传参数时按默认跑，而非恒跳过
	}
	now := d.Now()
	rows, err := d.Store.AllAgg(ctx)
	if err != nil {
		return nil, err
	}
	// 按 host 收敛 via=proxy 的行
	byHost := map[string][]store.AggRow{}
	var hosts []string
	for _, r := range rows {
		if r.Via != "proxy" || r.Host == "" {
			continue
		}
		if _, ok := byHost[r.Host]; !ok {
			hosts = append(hosts, r.Host)
		}
		byHost[r.Host] = append(byHost[r.Host], r)
	}

	var out []Verdict
	for _, h := range hosts {
		v := judgeHost(ctx, d, now, h, byHost[h])
		if v != nil {
			out = append(out, *v)
		}
	}
	return out, nil
}

// judgeHost 返回 nil 表示该域名未进入候选（不产生 Verdict，避免噪音）。
func judgeHost(ctx context.Context, d Deps, now time.Time, host string, rows []store.AggRow) *Verdict {
	p := d.Params

	// 条件 1：域名级初筛
	totalN := 0
	wSum, wN := 0.0, 0.0
	maxP95 := 0.0
	for _, r := range rows {
		totalN += r.N
		wSum += r.EwmaMs * float64(r.N)
		wN += float64(r.N)
		if r.P95Ms > maxP95 {
			maxP95 = r.P95Ms
		}
	}
	weightedEwma := 0.0
	if wN > 0 {
		weightedEwma = wSum / wN
	}
	if totalN < p.ScreenMinSamples {
		return nil
	}
	slowByEwma := weightedEwma > p.SlowEwmaMs
	slowByP95 := maxP95 > p.SlowP95Ms
	if !slowByEwma && !slowByP95 {
		return nil
	}

	// 条件 2：桶级验证（活跃桶 = N≥5；同向超阈 EWMA 的桶数 ≥ ActiveBuckets）
	activeSlow := 0
	for _, r := range rows {
		if r.N >= p.ActiveBucketMinSamples && r.EwmaMs > p.SlowEwmaMs {
			activeSlow++
		}
	}
	if activeSlow < p.ActiveBuckets {
		return nil
	}

	ev := func(reason string) *Verdict {
		return &Verdict{Host: host, Skipped: reason,
			Evidence: fmt.Sprintf("n=%d ewma=%.0fms p95=%.0fms 慢桶=%d", totalN, weightedEwma, maxP95, activeSlow)}
	}

	// 条件 4a：资格闸（正向白名单）
	if !exempt.Eligible(host, d.AllowList) {
		return ev("无直连资格（非 .cn/境内列表/allow）")
	}
	// 条件 4b：豁免闸（本地规则 + 用户 exempt）
	if d.Matcher.Blocked(host) {
		return ev("订阅规则命中非 DIRECT 结论")
	}
	for _, e := range d.ExemptList {
		if host == e || strings.HasSuffix(host, "."+e) {
			return ev("用户 exempt 列表")
		}
	}
	// 配额与冷却（P0-3：动作计数；回滚不计但设冷却）
	if n, err := d.Store.CountActions(ctx, "slow_direct", host, now.Add(-p.QuotaWindow)); err == nil && n >= p.QuotaMax {
		return ev(fmt.Sprintf("配额用尽（%d 天内已动作 %d 次）", int(p.QuotaWindow.Hours()/24), n))
	}
	if lastRevert, err := d.Store.LastRevert(ctx, host); err == nil && lastRevert > 0 &&
		now.Sub(time.Unix(lastRevert, 0)) < p.RevertCooldown {
		return ev(fmt.Sprintf("回滚冷却中（剩余 %.0f 小时）", p.RevertCooldown.Hours()-now.Sub(time.Unix(lastRevert, 0)).Hours()))
	}

	// 条件 3：同口径实时探测
	if d.ExitNode == nil {
		return ev("无出口节点解析器")
	}
	exitNode, err := d.ExitNode(ctx, host)
	if err != nil || exitNode == "" {
		return ev("当前无活跃代理连接可解析出口节点")
	}
	direct, proxy := prober.Probe(ctx, d.API, host, exitNode, p.ProbeTimeout)
	if !prober.DirectFaster(direct, proxy, p.DirectFasterRatio) {
		return ev(fmt.Sprintf("直连探测未达标（direct ok=%v %dms / proxy[%s] ok=%v %dms）",
			direct.OK, direct.LatencyMs, exitNode, proxy.OK, proxy.LatencyMs))
	}

	return &Verdict{
		Host:   host,
		Action: "DIRECT on",
		Evidence: fmt.Sprintf("n=%d ewma=%.0fms p95=%.0fms 慢桶=%d；直连 %dms < 代理[%s] %dms×%.0f%%",
			totalN, weightedEwma, maxP95, activeSlow,
			direct.LatencyMs, exitNode, proxy.LatencyMs, p.DirectFasterRatio*100),
	}
}
