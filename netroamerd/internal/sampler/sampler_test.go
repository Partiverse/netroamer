package sampler

import (
	"context"
	"testing"
	"time"

	"github.com/Partiverse/netroamer/netroamerd/internal/store"
)

func seedAgg(t *testing.T, st *store.Store, host, via string, n int) {
	t.Helper()
	if err := st.UpsertAgg(context.Background(), []store.AggRow{{
		Bucket: "n0-1", Host: host, Via: via, N: n, UpdatedAt: time.Now().Unix(),
	}}); err != nil {
		t.Fatal(err)
	}
}

func TestTopProxyHosts(t *testing.T) {
	st, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	seedAgg(t, st, "busy.cn", "proxy", 50)
	seedAgg(t, st, "mid.cn", "proxy", 20)
	seedAgg(t, st, "low.cn", "proxy", 5)      // < MinSamples
	seedAgg(t, st, "direct.cn", "direct", 99) // via=direct 不该入选

	hosts, err := TopProxyHosts(context.Background(), st, 10, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 2 {
		t.Fatalf("hosts = %v, want busy.cn + mid.cn", hosts)
	}
	if hosts[0] != "busy.cn" { // 按样本量降序
		t.Errorf("应按样本量降序, got %v", hosts)
	}

	// TopN 限幅
	hosts, _ = TopProxyHosts(context.Background(), st, 1, 1)
	if len(hosts) != 1 {
		t.Errorf("TopN=1 应只 1 个: %v", hosts)
	}
}

func TestRecBuilders(t *testing.T) {
	now := time.Now()
	okR := okRec(now, "a.cn", "direct", 120)
	if !okR.OK || okR.LatMs == nil || *okR.LatMs != 120 || okR.Purpose != "sampling" {
		t.Errorf("okRec = %+v", okR)
	}
	failR := failRec(now, "a.cn", "proxy")
	if failR.OK || failR.FailKind != "timeout" {
		t.Errorf("failRec = %+v", failR)
	}
}
