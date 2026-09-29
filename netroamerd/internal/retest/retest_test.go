package retest

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Partiverse/netroamer/netroamerd/internal/actuator"
	"github.com/Partiverse/netroamer/netroamerd/internal/mihomoapi"
	"github.com/Partiverse/netroamer/netroamerd/internal/store"
)

// fakeProbe 可控直连探测结果。
type fakeProbe struct{ fail bool }

func (f *fakeProbe) DelayTest(context.Context, string, string, time.Duration) (int, error) {
	if f.fail {
		return 0, errors.New("dial timeout")
	}
	return 100, nil
}

// fakeMihomo 提供 actuator.API 最小实现。
type fakeMihomo struct {
	path    string
	mounted bool
}

func (f *fakeMihomo) PutProvider(context.Context, string) error { return nil }

func (f *fakeMihomo) ProviderInfo(_ context.Context, _ string) (mihomoapi.ProviderInfo, error) {
	data, _ := os.ReadFile(f.path)
	return mihomoapi.ProviderInfo{RuleCount: len(strings.Split(string(data), "\n")) - 1}, nil
}

func (f *fakeMihomo) RuleSetMounted(context.Context, string) (bool, error) { return f.mounted, nil }

// setup 返回可推进的时钟指针（闭包捕获变量本体）。
func setup(t *testing.T, probe *fakeProbe) (*store.Store, *Manager, *actuator.Actuator, *time.Time) {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	fm := &fakeMihomo{mounted: true, path: filepath.Join(t.TempDir(), "p.yaml")}
	act := actuator.New(fm, fm.path)
	m := New(st, act, probe, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	cur := time.Now()
	m.now = func() time.Time { return cur }
	return st, m, act, &cur
}

// 滑动窗口回归（P0-2）：连续 3 次失败 → 回滚 + judgments revert；
// 前两次失败不回滚（替代被否决的「24 点 95%」单点口径）。
func TestRetestConsecutiveFailuresRollback(t *testing.T) {
	st, m, act, nowP := setup(t, &fakeProbe{fail: true})
	ctx := context.Background()
	now := *nowP

	if err := act.Enable(ctx, "slow.cn"); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertJudgment(ctx, store.Judgment{TS: now.Unix(),
		Kind: "slow_direct", Target: "slow.cn", Action: "DIRECT on", ParamsHash: "h"}); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		*nowP = nowP.Add(ProbeInterval + time.Minute) // 越过节流
		if err := m.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		if i < 2 {
			if rc, _ := st.RevertCount(ctx, "slow.cn"); rc != 0 {
				t.Fatalf("第 %d 次失败不应回滚", i+1)
			}
		}
	}
	if rc, _ := st.RevertCount(ctx, "slow.cn"); rc != 1 {
		t.Fatalf("连续 3 次失败应回滚, revert count = %d", rc)
	}
	hosts, _ := act.Hosts()
	if len(hosts) != 0 {
		t.Fatalf("回滚后 provider 应清空: %v", hosts)
	}
}

// 健康序列（6 中 0 败）不回滚。
func TestRetestHealthyNoRollback(t *testing.T) {
	st, m, act, nowP := setup(t, &fakeProbe{fail: false})
	ctx := context.Background()
	now := *nowP

	if err := act.Enable(ctx, "ok.cn"); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertJudgment(ctx, store.Judgment{TS: now.Unix(),
		Kind: "slow_direct", Target: "ok.cn", Action: "DIRECT on", ParamsHash: "h"}); err != nil {
		t.Fatal(err)
	}
	// 预置 5 次成功历史
	var recs []store.ProbeRecord
	for i := 0; i < 5; i++ {
		lat := int64(100)
		recs = append(recs, store.ProbeRecord{TS: now.Unix() - int64(600-i*100),
			Target: "ok.cn", Side: "direct", Purpose: "retest", OK: true, LatMs: &lat})
	}
	if err := st.InsertProbes(ctx, recs); err != nil {
		t.Fatal(err)
	}
	*nowP = nowP.Add(ProbeInterval + time.Minute)
	if err := m.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if rc, _ := st.RevertCount(ctx, "ok.cn"); rc != 0 {
		t.Fatalf("健康序列不应回滚, revert = %d", rc)
	}
}

// 6 中 3 败（非连续）也回滚（窗口判据的另一半）。
func TestRetestWindowFailuresRollback(t *testing.T) {
	st, m, act, nowP := setup(t, &fakeProbe{fail: false})
	ctx := context.Background()
	now := *nowP

	_ = act.Enable(ctx, "w.cn")
	// 动作时间提前 30min，保证手动预置的探测记录落在观察窗内（ts >= action.TS）
	_ = st.InsertJudgment(ctx, store.Judgment{TS: now.Add(-30 * time.Minute).Unix(),
		Kind: "slow_direct", Target: "w.cn", Action: "DIRECT on", ParamsHash: "h"})
	// 预置 3 败 2 成（交错，不连续 3 败）
	pattern := []bool{false, true, false, true, false}
	var recs []store.ProbeRecord
	for i, ok := range pattern {
		r := store.ProbeRecord{TS: now.Unix() - int64(250-i*50),
			Target: "w.cn", Side: "direct", Purpose: "retest", OK: ok}
		if !ok {
			r.FailKind = "timeout"
		}
		recs = append(recs, r)
	}
	if err := st.InsertProbes(ctx, recs); err != nil {
		t.Fatal(err)
	}
	// 本轮探测再失败：6 中 4 败 → 回滚（probe 现在 stub 为失败）
	*nowP = nowP.Add(ProbeInterval + time.Minute)
	mFail := New(st, m.act, &fakeProbe{fail: true}, m.log)
	mFail.now = m.now
	if err := mFail.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if rc, _ := st.RevertCount(ctx, "w.cn"); rc != 1 {
		t.Fatalf("6 中 ≥3 败应回滚, revert = %d", rc)
	}
}
