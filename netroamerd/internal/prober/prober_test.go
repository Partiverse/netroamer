package prober

import "testing"

func mk(ms int) Result { return Result{OK: true, LatencyMs: ms} }

func TestDirectFaster(t *testing.T) {
	cases := []struct {
		name       string
		direct, px Result
		ratio      float64
		want       bool
	}{
		{"直连一半以下", mk(100), mk(400), 0.5, true},
		{"直连恰好过半不算", mk(200), mk(400), 0.5, false},
		{"直连更慢", mk(500), mk(400), 0.5, false},
		{"直连探测失败", Result{OK: false}, mk(400), 0.5, false},
		{"代理探测失败=不可比较", mk(50), Result{OK: false}, 0.5, false},
	}
	for _, c := range cases {
		if got := DirectFaster(c.direct, c.px, c.ratio); got != c.want {
			t.Errorf("%s: DirectFaster = %v, want %v", c.name, got, c.want)
		}
	}
}
