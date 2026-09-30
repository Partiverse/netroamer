package analyzer

import (
	"context"
	"testing"
	"time"

	"github.com/Partiverse/netroamer/netroamerd/internal/store"
)

func TestUpdateAggregates(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	now := time.Now()

	lat := func(ms int64) *int64 { return &ms }
	samples := []store.Sample{
		// github.com×proxy：延迟 100/200/300/400ms、1 条失败
		{TS: now.Unix() - 10, Host: "github.com", Bucket: "n0-32", Via: "proxy", LatMs: lat(100), OK: true},
		{TS: now.Unix() - 9, Host: "github.com", Bucket: "n0-32", Via: "proxy", LatMs: lat(200), OK: true},
		{TS: now.Unix() - 8, Host: "github.com", Bucket: "n0-32", Via: "proxy", LatMs: lat(300), OK: true},
		{TS: now.Unix() - 7, Host: "github.com", Bucket: "n0-32", Via: "proxy", LatMs: lat(400), OK: false},
		{TS: now.Unix() - 6, Host: "github.com", Bucket: "n0-32", Via: "proxy", OK: true}, // lat nil
		// 窗口外的过期样本：不得进入聚合
		{TS: now.Unix() - int64((Window + time.Hour).Seconds()), Host: "old.dev", Bucket: "n0-1", Via: "proxy", LatMs: lat(9), OK: true},
	}
	if err := st.InsertSamples(ctx, samples); err != nil {
		t.Fatal(err)
	}

	n, err := Update(ctx, st, now)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("窗口内应只有 1 个聚合组, got %d", n)
	}
	rows, err := st.RecentAgg(ctx, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("RecentAgg = %v, %v", rows, err)
	}
	r := rows[0]
	if r.Host != "github.com" || r.Via != "proxy" || r.Bucket != "n0-32" {
		t.Fatalf("聚合键不符: %+v", r)
	}
	if r.N != 5 {
		t.Errorf("n = %d, want 5", r.N)
	}
	if r.FailRate <= 0.19 || r.FailRate >= 0.21 {
		t.Errorf("fail_rate = %v, want 0.2", r.FailRate)
	}
	// EWMA(α=0.3) 手算：100 → 130 → 160 → 190（失败样本无延迟不参与）
	wantEwma := 100.0
	for _, x := range []float64{200, 300, 400} {
		wantEwma = 0.3*x + 0.7*wantEwma
	}
	if diff := r.EwmaMs - wantEwma; diff > 0.01 || diff < -0.01 {
		t.Errorf("ewma = %v, want %v", r.EwmaMs, wantEwma)
	}
	if r.P95Ms != 400 {
		t.Errorf("p95 = %v, want 400", r.P95Ms)
	}

	// 二次聚合：整窗重算应幂等（n 不翻倍）
	if _, err := Update(ctx, st, now); err != nil {
		t.Fatal(err)
	}
	rows, _ = st.RecentAgg(ctx, 10)
	if len(rows) != 1 || rows[0].N != 5 {
		t.Fatalf("重算应幂等: %+v", rows)
	}
}

func TestUpdateEmptyWindow(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	n, err := Update(context.Background(), st, time.Now())
	if err != nil || n != 0 {
		t.Fatalf("空库 Update = %d, %v; want 0, nil", n, err)
	}
}

// 回归（实测缺口）：被动样本无握手时长 → agg ewma 恒 0 → 判定器永不触发。
// 主动采样落 probes 后，FillLatencyFromProbes 必须把延迟回填进 agg。
func TestFillLatencyFromProbes(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	now := time.Now()

	// 被动样本：agg 只有 n、没有延迟
	if err := st.UpsertAgg(ctx, []store.AggRow{
		{Bucket: "n0-1", Host: "slow.cn", Via: "proxy", N: 60, UpdatedAt: now.Unix()},
	}); err != nil {
		t.Fatal(err)
	}
	if filled, err := FillLatencyFromProbes(ctx, st, now); err != nil || filled != 0 {
		t.Fatalf("无 probes 时应回填 0: %d %v", filled, err)
	}

	// 主动采样：direct 与 proxy 侧各 4 次
	var recs []store.ProbeRecord
	for i, ms := range []int64{800, 900, 1000, 1100} {
		d := ms
		recs = append(recs, store.ProbeRecord{TS: now.Unix() - int64(i*60), Target: "slow.cn",
			Side: "proxy", Purpose: "sampling", LatMs: &d, OK: true})
	}
	for i, ms := range []int64{100, 120, 150, 180} {
		d := ms
		recs = append(recs, store.ProbeRecord{TS: now.Unix() - int64(i*60), Target: "slow.cn",
			Side: "direct", Purpose: "sampling", LatMs: &d, OK: true})
	}
	if err := st.InsertProbes(ctx, recs); err != nil {
		t.Fatal(err)
	}

	filled, err := FillLatencyFromProbes(ctx, st, now)
	if err != nil || filled != 1 {
		t.Fatalf("filled = %d err = %v, want 1", filled, err)
	}
	rows, _ := st.AllAgg(ctx)
	if len(rows) != 1 {
		t.Fatalf("rows = %d", len(rows))
	}
	if rows[0].EwmaMs < 800 || rows[0].EwmaMs > 1100 {
		t.Errorf("proxy ewma = %v, want 800..1100", rows[0].EwmaMs)
	}
	if rows[0].P95Ms != 1100 {
		t.Errorf("proxy p95 = %v, want 1100", rows[0].P95Ms)
	}
	if rows[0].N != 60 {
		t.Errorf("回填不应改动样本量 n = %d", rows[0].N)
	}
}
