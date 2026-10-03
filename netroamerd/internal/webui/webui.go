// Package webui 本地 Web 控制台 Phase A（research/06 §5）：只读观测，
// 服务自 127.0.0.1（隐私红线：无写端点、不展示 secret、无宽 CORS）。
// Phase B（控制面）将按同一审计通道（judgments）加写操作。
package webui

import (
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Partiverse/netroamer/netroamerd/internal/health"
	"github.com/Partiverse/netroamer/netroamerd/internal/judge"
	"github.com/Partiverse/netroamer/netroamerd/internal/store"
)

//go:embed index.html
var indexHTML []byte

// Ring 判定结论环形缓冲（影子建议 + 正式判定都进，控制台时间线用）。
type Ring struct {
	mu    sync.Mutex
	items []Entry
	max   int
}

// Entry 带时间戳的判定结论。
type Entry struct {
	At      time.Time     `json:"at"`
	Verdict judge.Verdict `json:"verdict"`
}

func NewRing(max int) *Ring { return &Ring{max: max} }

func (r *Ring) Add(v judge.Verdict) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items = append(r.items, Entry{At: time.Now(), Verdict: v})
	if len(r.items) > r.max {
		r.items = r.items[len(r.items)-r.max:]
	}
}

func (r *Ring) Recent(n int) []Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n > len(r.items) {
		n = len(r.items)
	}
	out := make([]Entry, n)
	copy(out, r.items[len(r.items)-n:])
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 { // 新的在前
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// Deps 控制台数据源。
type Deps struct {
	Version     string
	Actuate     bool
	Started     time.Time
	ST          *store.Store
	Health      *health.Manager
	Ring        *Ring
	EvidenceDir string
}

func Handler(d Deps) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(indexHTML)
	})
	mux.HandleFunc("/api/overview", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, overview(d))
	})
	mux.HandleFunc("/api/matrix", func(w http.ResponseWriter, r *http.Request) {
		rows, err := d.ST.RecentAgg(r.Context(), 30)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, rows)
	})
	mux.HandleFunc("/api/timeline", func(w http.ResponseWriter, r *http.Request) {
		judgments, _ := d.ST.RecentJudgments(r.Context(), 30)
		writeJSON(w, map[string]any{"shadow": d.Ring.Recent(50), "judgments": judgments})
	})
	mux.HandleFunc("/api/nodes", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, d.Health.Snapshot())
	})
	mux.HandleFunc("/api/evidence", func(w http.ResponseWriter, r *http.Request) {
		if name := r.URL.Query().Get("name"); name != "" {
			serveEvidence(w, d.EvidenceDir, name)
			return
		}
		writeJSON(w, listEvidence(d.EvidenceDir))
	})
	return safeHeaders(mux)
}

// safeHeaders 统一安全响应头：禁 MIME 嗅探、禁止跨源引用。
func safeHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func overview(d Deps) map[string]any {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	samples, _ := d.ST.Count(ctx)
	total, first, last, _ := d.ST.Stats(ctx)
	judgments, _ := d.ST.RecentJudgments(ctx, 1)
	return map[string]any{
		"version":      d.Version,
		"mode":         map[bool]string{true: "actuate（实操）", false: "shadow（影子）"}[d.Actuate],
		"started_at":   d.Started.Format("01-02 15:04"),
		"uptime_h":     time.Since(d.Started).Hours(),
		"samples":      samples,
		"total_judged": total,
		"first_ts":     first,
		"last_ts":      last,
		"judgments":    len(judgments),
		"evidence":     len(listEvidence(d.EvidenceDir)),
	}
}

func listEvidence(dir string) []map[string]string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []map[string]string{}
	}
	var out []map[string]string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, map[string]string{
			"name": e.Name(),
			"size": humanSize(info.Size()),
			"time": info.ModTime().Format("01-02 15:04"),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["name"] > out[j]["name"] })
	return out
}

// serveEvidence 只允许证据目录内的 .md 文件：
// ①仅取文件名成分（去目录）②拒绝 .. ③规范化后必须仍位于目录内。
func serveEvidence(w http.ResponseWriter, dir, name string) {
	name = filepath.Base(name) // 去掉任何目录成分
	if !strings.HasSuffix(name, ".md") || strings.Contains(name, "..") {
		http.Error(w, "invalid name", 400)
		return
	}
	cleanDir := filepath.Clean(dir)
	full := filepath.Join(cleanDir, name)
	if !strings.HasPrefix(full, cleanDir+string(os.PathSeparator)) {
		http.Error(w, "forbidden", 403)
		return
	}
	data, err := os.ReadFile(full)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(data)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(v)
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return strconv.FormatFloat(float64(n)/(1<<20), 'f', 1, 64) + "MB"
	case n >= 1<<10:
		return strconv.FormatFloat(float64(n)/(1<<10), 'f', 1, 64) + "KB"
	default:
		return strconv.FormatInt(n, 10) + "B"
	}
}
