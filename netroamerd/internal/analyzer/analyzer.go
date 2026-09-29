// Package analyzer 按（时段桶 × 主域 × 路径）聚合延迟与失败率，
// 每 5 分钟滚动刷新 agg 表（research/05 §1.1 analyzer、§3）。
// 纯统计、无 ML 依赖；P1 判定器（慢域名/坏节点）在 agg 之上实现。
package analyzer

import (
	"context"
	"math"
	"sort"
	"time"

	"github.com/Partiverse/netroamer/netroamerd/internal/store"
)

const (
	// EWMAAlpha 新样本权重：0.3 在「响应近期变化」与「抗单点抖动」间折中，
	// 与 Smart Core 指数平滑的量级一致（research/05 §4）。
	EWMAAlpha = 0.3

	// Window 每次聚合回看的样本窗口（rolling 语义：整窗重算）。
	Window = 6 * time.Hour
)

// Update 重算窗口内聚合并整表刷新 agg，返回写入行数。
// W1 样本暂无延迟（lat_ms 全 NULL），ewma/p95 为 0，n/fail_rate 先行可用；
// W2+ 主动探测接入后延迟列自动填充。
func Update(ctx context.Context, st *store.Store, now time.Time) (int, error) {
	samples, err := st.LoadWindow(ctx, now.Add(-Window))
	if err != nil {
		return 0, err
	}
	type acc struct {
		lats  []int64 // 时间序（EWMA 依赖顺序）
		fails int
		n     int
	}
	groups := make(map[string]*acc)
	keys := make(map[string]store.AggKey)
	for _, s := range samples {
		if s.Host == "" {
			continue
		}
		k := store.AggKey{Bucket: s.Bucket, Host: s.Host, Via: s.Via}
		id := k.Bucket + "\x00" + k.Host + "\x00" + k.Via
		a := groups[id]
		if a == nil {
			a = &acc{}
			groups[id] = a
			keys[id] = k
		}
		a.n++
		if !s.OK {
			a.fails++
		}
		if s.LatMs != nil {
			a.lats = append(a.lats, *s.LatMs)
		}
	}

	out := make([]store.AggRow, 0, len(groups))
	for id, a := range groups {
		k := keys[id]
		row := store.AggRow{
			Bucket:    k.Bucket,
			Host:      k.Host,
			Via:       k.Via,
			N:         a.n,
			UpdatedAt: now.Unix(),
			FailRate:  float64(a.fails) / float64(a.n),
		}
		if len(a.lats) > 0 {
			row.EwmaMs = float64(a.lats[0])
			for _, x := range a.lats[1:] {
				row.EwmaMs = EWMAAlpha*float64(x) + (1-EWMAAlpha)*row.EwmaMs
			}
			sorted := append([]int64(nil), a.lats...)
			sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
			idx := int(math.Ceil(0.95*float64(len(sorted)))) - 1
			if idx < 0 {
				idx = 0
			}
			row.P95Ms = float64(sorted[idx])
		}
		out = append(out, row)
	}
	if err := st.UpsertAgg(ctx, out); err != nil {
		return 0, err
	}
	return len(out), nil
}
