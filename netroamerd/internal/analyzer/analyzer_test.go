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
