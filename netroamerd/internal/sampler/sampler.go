// Package sampler 主动延迟采样：定期对高活跃域名经 DIRECT 与出口节点做
// 同口径探测，落 probes 表（purpose='sampling'）——补齐被动遥测的结构性缺口
// （/connections 不含握手时长，agg 的 ewma/p95 恒为 0，判定器条件 1 永不满足）。
package sampler

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/Partiverse/netroamer/netroamerd/internal/mihomoapi"
	"github.com/Partiverse/netroamer/netroamerd/internal/prober"
	"github.com/Partiverse/netroamer/netroamerd/internal/store"
)

const (
	// Interval 采样节奏：30 分钟一轮（慢于判定，够用且省流量）
	Interval = 30 * time.Minute
	// TopN 每轮采样的域名数（按 via=proxy 样本量降序）
	TopN = 20
	// MinSamples 域名入选门槛（样本太少的域名不浪费探测）
	MinSamples = 10
)

// Sampler 主动延迟采样器。
type Sampler struct {
	api      *mihomoapi.Client
	st       *store.Store
	log      *slog.Logger
	now      func() time.Time
	exitNode func(ctx context.Context, host string) (string, error)
}

func New(api *mihomoapi.Client, st *store.Store, log *slog.Logger,
	exitNode func(context.Context, string) (string, error)) *Sampler {
	return &Sampler{api: api, st: st, log: log, now: time.Now, exitNode: exitNode}
}

// TopProxyHosts 返回按代理路径样本量降序的域名（前 TopN）。
func TopProxyHosts(ctx context.Context, st *store.Store, minSamples, topN int) ([]string, error) {
	rows, err := st.AllAgg(ctx)
	if err != nil {
		return nil, err
	}
	type kv struct {
		host string
		n    int
	}
	var list []kv
	for _, r := range rows {
		if r.Via == "proxy" && r.N >= minSamples && r.Host != "" {
			list = append(list, kv{r.Host, r.N})
		}
	}
	// 简单选择排序（列表短，避免引入依赖）
	for i := 0; i < len(list); i++ {
		max := i
		for j := i + 1; j < len(list); j++ {
			if list[j].n > list[max].n {
				max = j
			}
		}
		list[i], list[max] = list[max], list[i]
	}
	if len(list) > topN {
		list = list[:topN]
	}
	out := make([]string, 0, len(list))
	for _, k := range list {
		out = append(out, k.host)
	}
	return out, nil
}

// Tick 采样一轮：每域名探测 DIRECT + 出口节点，落 probes。
func (s *Sampler) Tick(ctx context.Context) {
	hosts, err := TopProxyHosts(ctx, s.st, MinSamples, TopN)
	if err != nil {
		s.log.Warn("sampler: 取候选域名失败", "err", err)
		return
	}
	if len(hosts) == 0 {
		return
	}
	now := s.now()
	ctxS, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	var recs []store.ProbeRecord
	probed := 0
	for _, host := range hosts {
		if ctxS.Err() != nil {
			break
		}
		// 直连侧（同口径 generate_204）
		if ms, err := s.api.DelayTest(ctxS, "DIRECT", prober.TestURL(host), probeTimeout); err == nil {
			recs = append(recs, okRec(now, host, "direct", int64(ms)))
		} else {
			r := failRec(now, host, "direct")
			recs = append(recs, r)
		}
		// 代理侧：解析顶层组|当前叶子 → /group/{组}/delay 穿透嵌套组取延迟
		if s.exitNode != nil {
			if path, err := s.exitNode(ctxS, host); err == nil && path != "" {
				group, leaf, _ := strings.Cut(path, "|")
				if ms, err := s.api.CurrentPathDelay(ctxS, group, leaf, prober.TestURL(host), probeTimeout); err == nil {
					recs = append(recs, okRec(now, host, "proxy", int64(ms)))
					probed++
				} else {
					recs = append(recs, failRec(now, host, "proxy"))
				}
			}
		}
	}
	if err := s.st.InsertProbes(ctx, recs); err != nil {
		s.log.Warn("sampler: 探测落档失败", "err", err)
		return
	}
	s.log.Info("sampler: 采样完成", "hosts", len(hosts), "含代理侧", probed, "probes", len(recs))
}

// probeTimeout 单次探测超时（与 judge 条件 3 同口径）。
const probeTimeout = 5 * time.Second

func okRec(now time.Time, host, side string, ms int64) store.ProbeRecord {
	return store.ProbeRecord{TS: now.Unix(), Target: host, Side: side,
		Purpose: "sampling", LatMs: &ms, OK: true}
}

func failRec(now time.Time, host, side string) store.ProbeRecord {
	return store.ProbeRecord{TS: now.Unix(), Target: host, Side: side,
		Purpose: "sampling", OK: false, FailKind: "timeout"}
}
