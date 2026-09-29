package judge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Partiverse/netroamer/netroamerd/internal/exempt"
	"github.com/Partiverse/netroamer/netroamerd/internal/mihomoapi"
	"github.com/Partiverse/netroamer/netroamerd/internal/store"
)

// fakeAPI 将探测结果映射为 DelayTest 返回值（不触网，满足 prober.DelayTester）。
type fakeAPI struct {
	delay map[string]int // proxy name → ms
	fail  map[string]bool
}

func (f *fakeAPI) DelayTest(_ context.Context, proxy, _ string, _ time.Duration) (int, error) {
	if f.fail[proxy] {
		return 0, errors.New("probe failed")
	}
	return f.delay[proxy], nil
}

// aggRows 构造某 host 的 proxy 聚合行：buckets 个桶，每桶 n 条、ewma/p95 指定值。
func aggRows(host string, buckets int, n int, ewma, p95 float64) []store.AggRow {
	var rows []store.AggRow
	for i := 0; i < buckets; i++ {
		rows = append(rows, store.AggRow{
			Bucket: fmt.Sprintf("n0-%d", i), Host: host, Via: "proxy",
			EwmaMs: ewma, P95Ms: p95, N: n, UpdatedAt: time.Now().Unix(),
		})
	}
	return rows
}

func depsFor(st *store.Store, api *fakeAPI, exitNode string) Deps {
	return Deps{
		Store:     st,
		API:       api,
		Matcher:   exempt.BuildMatcher(nil),
		AllowList: []string{"cdn-test.dev"},
		ExitNode: func(context.Context, string) (string, error) {
			if exitNode == "" {
				return "", errors.New("no conn")
			}
			return exitNode, nil
		},
		Now: time.Now,
	}
}

// 验收场景（05 §5 W3）：回放数据集上豁免域名零动作。
func TestRunReplayZeroExemptActions(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()

	// 三类候选全部满足「慢」统计
	rows := aggRows("slow.cn", 4, 13, 1200, 2000)                    // 有资格且直连快 → 唯一应动作
	rows = append(rows, aggRows("google.com", 4, 13, 1200, 2000)...) // 规则豁免 → 零动作
	rows = append(rows, aggRows("fast.cn", 4, 10, 100, 150)...)      // 不慢 → 不进候选
	if err := st.UpsertAgg(ctx, rows); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertAgg(ctx, aggRows("cdn-test.dev", 4, 13, 1200, 2000)); err != nil {
		t.Fatal(err)
	}
	// google.com 在订阅规则中命中 PROXY
	m := exempt.BuildMatcher([]mihomoapi.Rule{
		{Type: "DOMAIN-SUFFIX", Payload: "google.com", Proxy: "PROXY"},
	})
	api := &fakeAPI{delay: map[string]int{"DIRECT": 50, "HK-01": 900}}

	verdicts, err := Run(ctx, Deps{
		Store: st, API: api, Matcher: m,
		AllowList: []string{"cdn-test.dev"},
		ExitNode:  func(context.Context, string) (string, error) { return "HK-01", nil },
		Now:       time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	byHost := map[string]Verdict{}
	for _, v := range verdicts {
		byHost[v.Host] = v
	}
	if v, ok := byHost["slow.cn"]; !ok || v.Action != "DIRECT on" {
		t.Fatalf("slow.cn 应动作: %+v", v)
	}
	if v, ok := byHost["google.com"]; !ok || v.Action != "" || v.Skipped == "" {
		t.Fatalf("google.com 必须被豁免闸拦截: %+v", v)
	}
	if v, ok := byHost["cdn-test.dev"]; !ok || v.Action != "DIRECT on" {
		t.Fatalf("allow 列表域名应动作: %+v", v)
	}
	if _, ok := byHost["fast.cn"]; ok {
		t.Fatal("不慢的域名不应进入 Verdict")
	}
}

func TestRunQuotaAndCooldown(t *testing.T) {
	st, _ := store.Open(t.TempDir() + "/t.db")
	defer st.Close()
	ctx := context.Background()
	now := time.Now()

	if err := st.UpsertAgg(ctx, aggRows("quota.cn", 4, 13, 1200, 2000)); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertAgg(ctx, aggRows("cooled.cn", 4, 13, 1200, 2000)); err != nil {
		t.Fatal(err)
	}
	api := &fakeAPI{delay: map[string]int{"DIRECT": 50, "HK-01": 900}}
	d := depsFor(st, api, "HK-01")

	// 配额：7 天内已动作 2 次 → 跳过
	for i := 0; i < 2; i++ {
		st.InsertJudgment(ctx, store.Judgment{TS: now.Add(-time.Hour).Unix(),
			Kind: "slow_direct", Target: "quota.cn", Action: "DIRECT on", ParamsHash: d.Params.Hash()})
	}
	// 冷却：3 天前回滚 → 跳过（即使配额未用）
	st.InsertJudgment(ctx, store.Judgment{TS: now.Add(-72 * time.Hour).Unix(),
		Kind: "slow_direct", Target: "cooled.cn", Action: "revert", Reverted: true, ParamsHash: d.Params.Hash()})
	// 8 天前的动作已滑出窗口 → 不影响
	st.InsertJudgment(ctx, store.Judgment{TS: now.Add(-8 * 24 * time.Hour).Unix(),
		Kind: "slow_direct", Target: "cooled.cn", Action: "DIRECT on", ParamsHash: d.Params.Hash()})

	verdicts, err := Run(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	skip := map[string]string{}
	for _, v := range verdicts {
		skip[v.Host] = v.Skipped
	}
	if !strings.Contains(skip["quota.cn"], "配额用尽") {
		t.Errorf("quota.cn 应因配额跳过: %q", skip["quota.cn"])
	}
	if !strings.Contains(skip["cooled.cn"], "冷却") {
		t.Errorf("cooled.cn 应因回滚冷却跳过（旧动作已滑出窗口）: %q", skip["cooled.cn"])
	}
}

// 两层统计：桶级不达标（活跃慢桶 < 3）不进入候选（评审 P0-1 回归）。
func TestRunBucketValidation(t *testing.T) {
	st, _ := store.Open(t.TempDir() + "/t.db")
	defer st.Close()
	ctx := context.Background()

	// 域名级达标（n=200、ewma 高）但只有 1 个活跃慢桶
	if err := st.UpsertAgg(ctx, aggRows("diluted.cn", 1, 200, 1200, 2000)); err != nil {
		t.Fatal(err)
	}
	verdicts, err := Run(ctx, depsFor(st, &fakeAPI{}, "HK-01"))
	if err != nil {
		t.Fatal(err)
	}
	if len(verdicts) != 0 {
		t.Fatalf("桶级不达标不应产生 Verdict: %+v", verdicts)
	}
}

// 直连探测不达标（未快 50% 或代理侧失败）→ 跳过（评审 P1-5 回归）。
func TestRunDirectProbeGate(t *testing.T) {
	st, _ := store.Open(t.TempDir() + "/t.db")
	defer st.Close()
	ctx := context.Background()
	if err := st.UpsertAgg(ctx, aggRows("slow2.cn", 4, 13, 1200, 2000)); err != nil {
		t.Fatal(err)
	}

	// 直连 500ms vs 代理 900ms：未快 50%
	api := &fakeAPI{delay: map[string]int{"DIRECT": 500, "HK-01": 900}}
	verdicts, _ := Run(ctx, depsFor(st, api, "HK-01"))
	if len(verdicts) != 1 || verdicts[0].Action != "" || !strings.Contains(verdicts[0].Skipped, "未达标") {
		t.Fatalf("未快 50%% 应跳过: %+v", verdicts)
	}

	// 代理探测失败 = 不可比较，保守跳过
	api2 := &fakeAPI{delay: map[string]int{"DIRECT": 50}, fail: map[string]bool{"HK-01": true}}
	verdicts, _ = Run(ctx, depsFor(st, api2, "HK-01"))
	if len(verdicts) != 1 || verdicts[0].Action != "" {
		t.Fatalf("代理探测失败应保守跳过: %+v", verdicts)
	}
}

// 终态回归（独立复评 P0-2）：累计 3 次回滚 → 永久仅通知。
func TestRunTerminalAfterThreeReverts(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	now := time.Now()

	if err := st.UpsertAgg(ctx, aggRows("term.cn", 4, 13, 1200, 2000)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := st.InsertJudgment(ctx, store.Judgment{TS: now.Add(-time.Duration(i+1) * time.Hour).Unix(),
			Kind: "slow_direct", Target: "term.cn", Action: "revert", Reverted: true, ParamsHash: "h"}); err != nil {
			t.Fatal(err)
		}
	}
	api := &fakeAPI{delay: map[string]int{"DIRECT": 50, "HK-01": 900}}
	verdicts, err := Run(ctx, depsFor(st, api, "HK-01"))
	if err != nil {
		t.Fatal(err)
	}
	if len(verdicts) != 1 || verdicts[0].Action != "" || !strings.Contains(verdicts[0].Skipped, "终态") {
		t.Fatalf("3 次回滚后应进终态仅通知: %+v", verdicts)
	}
}
