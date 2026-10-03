package health

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Partiverse/netroamer/netroamerd/internal/mihomoapi"
	"github.com/Partiverse/netroamer/netroamerd/internal/store"
)

// ---- 状态机单测 ----

func TestNodeHealthPenaltyAndRecovery(t *testing.T) {
	h := newNodeHealth()
	t0 := time.Now()

	// 连续失败：penalty ×2，上限 32
	for i := 0; i < 6; i++ {
		h.Observe(false, 0, t0)
	}
	if h.Penalty != PenaltyMax {
		t.Errorf("penalty = %v, want 上限 32", h.Penalty)
	}
	if !h.HardDown() {
		t.Error("连续 3+ 败应 HardDown")
	}

	// 恢复：5min 阶段每次成功减半
	h.Observe(true, 100, t0.Add(6*time.Minute))
	if h.Penalty != 16 {
		t.Errorf("第一次恢复成功 penalty = %v, want 16", h.Penalty)
	}
	// 5 分钟内再次成功：不减半（阶段节奏）
	h.Observe(true, 100, t0.Add(7*time.Minute))
	if h.Penalty != 16 {
		t.Errorf("阶段内不应减半, penalty = %v", h.Penalty)
	}
	h.Observe(true, 100, t0.Add(12*time.Minute))
	if h.Penalty != 8 {
		t.Errorf("跨阶段成功应减半, penalty = %v", h.Penalty)
	}
}

func TestNodeHealthDegraded(t *testing.T) {
	h := newNodeHealth()
	t0 := time.Now()
	// 先建立 EWMA≈100 基线（6 次，确保 Samples>5 阈值生效）
	for i := 0; i < 6; i++ {
		h.Observe(true, 100, t0)
	}
	if h.Degraded() {
		t.Fatal("正常样本不应 Degraded")
	}
	// 单次超阈（EWMA+3×MAD 级别）：不劣化（禁止单点判坏）
	h.Observe(true, 500, t0)
	if h.Degraded() {
		t.Error("单次超阈不应 Degraded")
	}
	// 连续第二次更极端（500 已把基线拉到 220+3×120=580，需 >580 才算异常）
	h.Observe(true, 2000, t0)
	if !h.Degraded() {
		t.Error("连续 2 次超阈应 Degraded")
	}
}

// ---- Manager 集成（httptest mock mihomo）----

func mockMihomo(t *testing.T, actuate bool, consecFails *atomic.Int64, selected *atomic.Value, switchCount *atomic.Int64) (*Manager, *store.Store) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/proxies", func(w http.ResponseWriter, r *http.Request) {
		now, _ := selected.Load().(string)
		json.NewEncoder(w).Encode(map[string]any{"proxies": map[string]any{
			"Proxy": map[string]any{"name": "Proxy", "type": "Selector", "now": now,
				"all": []string{"Bad", "Good", "DIRECT"}},
		}})
	})
	// 分层订阅下 /proxies/{node}/delay 不可用，探测走 /group/{组}/delay（穿透嵌套组）
	mux.HandleFunc("/proxies/Bad/delay", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	mux.HandleFunc("/group/Proxy/delay", func(w http.ResponseWriter, r *http.Request) {
		if consecFails.Load() >= 2 { // 当前节点失败：结果里不含 Bad
			json.NewEncoder(w).Encode(map[string]int{"Good": 100})
			return
		}
		json.NewEncoder(w).Encode(map[string]int{"Bad": 300, "Good": 100})
	})
	mux.HandleFunc("/proxies/Proxy", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			http.NotFound(w, r)
			return
		}
		var body struct {
			Name string `json:"name"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		selected.Store(body.Name)
		switchCount.Add(1)
		w.WriteHeader(http.StatusNoContent)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	st, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	m := New(mihomoapi.New(mihomoapi.ConfigForTest(srv.URL)), st, log, func() bool { return actuate }, "")
	fixed := time.Now()
	m.now = func() time.Time { return fixed }
	return m, st
}

func TestManagerShadowAdvice(t *testing.T) {
	var fails atomic.Int64
	var sel atomic.Value
	sel.Store("Bad")
	var switches atomic.Int64
	m, _ := mockMihomo(t, false, &fails, &sel, &switches)

	// 3 轮失败探测：影子模式不切换
	for i := 0; i < 3; i++ {
		m.Tick(context.Background())
	}
	if switches.Load() != 0 {
		t.Fatalf("影子模式不应切换, switches = %d", switches.Load())
	}
	n, err := m.st.CountActions(context.Background(), "bad_node", "Proxy", time.Now().Add(-time.Hour))
	if err != nil || n != 0 {
		t.Fatalf("影子模式不应写 judgments: %d %v", n, err)
	}
}

func TestManagerActuateSwitches(t *testing.T) {
	var fails atomic.Int64
	var sel atomic.Value
	sel.Store("Bad")
	var switches atomic.Int64
	m, st := mockMihomo(t, true, &fails, &sel, &switches)
	ctx := context.Background()

	// 第 1 轮：探测成功（fails<2），不触发评估
	m.Tick(ctx)
	// 节点开始失败 → 后续轮次事件驱动评估 → 切换到 Good
	fails.Add(2)
	for i := 0; i < 3; i++ {
		m.Tick(ctx)
	}
	if switches.Load() != 1 {
		t.Fatalf("actuate 应切换 1 次, got %d", switches.Load())
	}
	if got := sel.Load().(string); got != "Good" {
		t.Fatalf("应切到 Good, got %s", got)
	}
	if n, _ := st.CountActions(ctx, "bad_node", "Proxy", time.Now().Add(-time.Hour)); n != 1 {
		t.Fatalf("judgments 应记录 1 次切换, got %d", n)
	}
	if _, err := st.LastBadNodeSwitch(ctx); err != nil {
		t.Fatalf("LastBadNodeSwitch: %v", err)
	}

	// 防抖：10 分钟内不再切换（时钟固定）
	m.Tick(ctx)
	if switches.Load() != 1 {
		t.Fatalf("防抖期内不应再切换, got %d", switches.Load())
	}
}

func TestManagerManualWindowRespected(t *testing.T) {
	var fails atomic.Int64
	var sel atomic.Value
	sel.Store("Good") // 用户手动选了 Good（非我们设置）
	var switches atomic.Int64
	m, _ := mockMihomo(t, true, &fails, &sel, &switches)
	ctx := context.Background()

	// 首轮 Tick 记录 now=Good；随后 Bad 挂但当前是 Good 且用户手动 → 不切
	for i := 0; i < 4; i++ {
		m.Tick(ctx)
	}
	// 当前节点 Good 健康（delay 100），不会触发评估——手动窗口路径由
	// 手动切换检测覆盖：模拟用户切到 Bad 后窗口内不切回
	sel.Store("Bad")
	m.Tick(ctx) // 记录手动切换（now 与 expectNow 不一致）
	for i := 0; i < 3; i++ {
		m.Tick(ctx)
	}
	if switches.Load() != 0 {
		t.Fatalf("手动选择 30 分钟窗口内不应自动切换, got %d", switches.Load())
	}
}
