package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Partiverse/netroamer/netroamerd/internal/store"
)

var testLog = slog.New(slog.NewTextHandler(io.Discard, nil))

func sampleConn(id, host string) Conn {
	return Conn{
		ID:       id,
		Metadata: Metadata{Host: host, ProcessPath: "/usr/bin/curl"},
		Chains:   []string{"DIRECT"},
	}
}

func writeFrame(ctx context.Context, c *websocket.Conn, s Snapshot) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	wctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return c.Write(wctx, websocket.MessageText, b)
}

func TestTrackerLifecycle(t *testing.T) {
	tr := NewTracker()
	t0 := time.Date(2026, 9, 29, 8, 0, 0, 0, time.Local)
	snap1 := &Snapshot{Connections: []Conn{sampleConn("a", "api.github.com")}}

	if got := tr.Observe(t0, snap1, true, 0); len(got) != 0 {
		t.Fatalf("首帧只登记不产出, got %v", got)
	}
	if got := tr.Observe(t0.Add(time.Second), snap1, false, 0); len(got) != 0 {
		t.Fatalf("连接仍活跃, got %v", got)
	}
	got := tr.Observe(t0.Add(2*time.Second), &Snapshot{}, false, 0)
	if len(got) != 1 {
		t.Fatalf("连接消失应产出 1 条样本, got %d", len(got))
	}
	s := got[0]
	if s.Host != "github.com" {
		t.Errorf("host_sld = %q, want github.com（主域提取）", s.Host)
	}
	if s.Via != "direct" {
		t.Errorf("via = %q, want direct", s.Via)
	}
	if s.Proc != "curl" {
		t.Errorf("proc = %q, want curl", s.Proc)
	}
	if !s.OK {
		t.Error("ok 应为 true")
	}
	wantBucket := fmt.Sprintf("n0-%d", ((int(t0.Weekday())+6)%7)*24+t0.Hour())
	if s.Bucket != wantBucket {
		t.Errorf("bucket = %q, want %q", s.Bucket, wantBucket)
	}
	if s.TS != t0.Add(2*time.Second).Unix() {
		t.Errorf("ts = %d, want 结束时刻 %d", s.TS, t0.Add(2*time.Second).Unix())
	}
}

func TestTrackerViaAndHostFallback(t *testing.T) {
	tr := NewTracker()
	t0 := time.Now()
	tr.Observe(t0, &Snapshot{Connections: []Conn{
		{ID: "p1", Chains: []string{"HK-01", "Proxy 组"}},
		{ID: "p2"}, // chains 空 → 未知按 proxy
		{ID: "ip1", Metadata: Metadata{DestinationIP: "1.2.3.4"}}, // 无 host 用 IP
	}}, true, 0)
	got := tr.Observe(t0.Add(time.Second), &Snapshot{}, false, 0)
	if len(got) != 3 {
		t.Fatalf("want 3 samples, got %d", len(got))
	}
	byID := map[string]store.Sample{}
	for _, s := range got {
		byID[s.Host] = s
	}
	if s := byID["1.2.3.4"]; s.Via != "proxy" || s.Host != "1.2.3.4" {
		t.Errorf("IP 直连样本不符: %+v", s)
	}
	n := 0
	for _, s := range got {
		if s.Via != "direct" {
			n++
		}
	}
	if n != 3 { // p1 代理链、p2 未知、ip1 无 chains —— 全部按 proxy
		t.Errorf("未知/代理链路都应计为 proxy, got %d proxy", n)
	}
}

func TestTrackerGrace(t *testing.T) {
	tr := NewTracker()
	t0 := time.Now()
	tr.Observe(t0, &Snapshot{Connections: []Conn{sampleConn("a", "example.com")}}, true, 5*time.Second)
	// 单帧缺席（如上一帧 JSON 解析失败）：宽限期内不判结束，防重复落样本
	if got := tr.Observe(t0.Add(time.Second), &Snapshot{}, false, 5*time.Second); len(got) != 0 {
		t.Fatalf("宽限期内不应产出, got %v", got)
	}
	if got := tr.Observe(t0.Add(6*time.Second), &Snapshot{}, false, 5*time.Second); len(got) != 1 {
		t.Fatalf("超过宽限应产出 1 条, got %d", len(got))
	}
}

func TestCollectorEndToEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer testsecret" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close(websocket.StatusNormalClosure, "")
		ctx := r.Context()
		frames := []Snapshot{
			{Connections: []Conn{sampleConn("a", "api.github.com")}},
			{Connections: []Conn{sampleConn("a", "api.github.com")}},
			{}, // 连接消失 → 产出样本
		}
		for _, f := range frames {
			if err := writeFrame(ctx, c, f); err != nil {
				return
			}
		}
		<-ctx.Done() // 保持连接直到客户端退出
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch := make(chan store.Sample, 8)
	err := Run(ctx, Options{URL: srv.URL, Secret: "testsecret"}, testLog,
		func(_ context.Context, batch []store.Sample) error {
			for _, s := range batch {
				ch <- s
			}
			return nil
		})
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run err = %v", err)
	}
	select {
	case s := <-ch:
		if s.Host != "github.com" || s.Via != "direct" || !s.OK {
			t.Fatalf("样本不符: %+v", s)
		}
	default:
		t.Fatal("未收到样本")
	}
}

func TestCollectorReconnectsAfterDrop(t *testing.T) {
	var conns atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := conns.Add(1)
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close(websocket.StatusNormalClosure, "")
		ctx := r.Context()
		if n == 1 {
			return // 首连立即断开，触发客户端退避重连
		}
		if err := writeFrame(ctx, c, Snapshot{Connections: []Conn{sampleConn("b", "example.com")}}); err != nil {
			return
		}
		<-ctx.Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := Run(ctx, Options{URL: srv.URL}, testLog, func(context.Context, []store.Sample) error { return nil })
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run err = %v", err)
	}
	if got := conns.Load(); got < 2 {
		t.Fatalf("应发生断线重连, 实际连接次数 = %d", got)
	}
}

func TestCollectorOverUnixSocket(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix socket 仅 POSIX")
	}
	// /tmp 短路径：macOS sun_path 上限 104 字节，t.TempDir() 太长
	dir, err := os.MkdirTemp("/tmp", "nrtest-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "m.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer c.Close(websocket.StatusNormalClosure, "")
			ctx := r.Context()
			frames := []Snapshot{
				{Connections: []Conn{sampleConn("u", "api.github.com")}},
				{},
			}
			for _, f := range frames {
				if err := writeFrame(ctx, c, f); err != nil {
					return
				}
			}
			<-ctx.Done()
		}),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go srv.Serve(l)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch := make(chan store.Sample, 8)
	err = Run(ctx, Options{URL: "http://mihomo-ipc", UnixSocket: sock}, testLog,
		func(_ context.Context, batch []store.Sample) error {
			for _, s := range batch {
				ch <- s
			}
			return nil
		})
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run err = %v", err)
	}
	select {
	case s := <-ch:
		if s.Host != "github.com" || s.Via != "direct" {
			t.Fatalf("unix socket 样本不符: %+v", s)
		}
	default:
		t.Fatal("unix socket 传输未收到样本")
	}
}

func TestCollectorRetriesOnAuthFailure(t *testing.T) {
	var rejected atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer right" {
			rejected.Add(1)
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close(websocket.StatusNormalClosure, "")
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := Run(ctx, Options{URL: srv.URL, Secret: "wrong"}, testLog,
		func(context.Context, []store.Sample) error { return nil })
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run err = %v", err)
	}
	if got := rejected.Load(); got < 2 {
		t.Fatalf("secret 错误应持续退避重试, 实际被拒 %d 次", got)
	}
}
