// Package prober 同口径直连/代理探测（05 §4.1 条件 3 修订版）：
// 同一测试 URL 分别经 DIRECT 与出口节点走 mihomo delay test（同为 TTFB），
// 消除评审 P1-5 的口径失真（握手 vs 首包直接比比例）。
package prober

import (
	"context"
	"time"
)

// TestURL 构造探测 URL（generate_204：无 body、返回即 TTFB）。
func TestURL(domain string) string {
	return "https://" + domain + "/generate_204"
}

// DelayTester 是探测所需的 mihomo 能力子集（*mihomoapi.Client 满足；
// 抽成接口以便判定器单测注入 fake）。
type DelayTester interface {
	DelayTest(ctx context.Context, proxy, testURL string, timeout time.Duration) (int, error)
}

type Result struct {
	OK        bool
	LatencyMs int
	Err       error
}

// Probe 同口径探测：direct 与 proxy（出口节点名）两次 DelayTest。
func Probe(ctx context.Context, api DelayTester, domain, exitNode string, timeout time.Duration) (direct, proxy Result) {
	u := TestURL(domain)
	ms, err := api.DelayTest(ctx, "DIRECT", u, timeout)
	direct = Result{OK: err == nil, LatencyMs: ms, Err: err}
	ms, err = api.DelayTest(ctx, exitNode, u, timeout)
	proxy = Result{OK: err == nil, LatencyMs: ms, Err: err}
	return
}

// DirectFaster 判定「直连可达且更快」：两侧都必须成功（代理探测失败视为
// 无法比较——不在代理宕机瞬间做切换决策），且 direct < proxy×maxRatio。
func DirectFaster(direct, proxy Result, maxRatio float64) bool {
	if !direct.OK || !proxy.OK {
		return false
	}
	return float64(direct.LatencyMs) < float64(proxy.LatencyMs)*maxRatio
}
