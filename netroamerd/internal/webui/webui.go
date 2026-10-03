// Package webui 本地 Web 控制台 Phase A（research/06 §5）：只读观测，
// 服务自 127.0.0.1（隐私红线：无写端点、不展示 secret、无宽 CORS）。
// Phase B（控制面）将按同一审计通道（judgments）加写操作。
package webui

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Partiverse/netroamer/netroamerd/internal/actuator"
	"github.com/Partiverse/netroamer/netroamerd/internal/config"
	"github.com/Partiverse/netroamer/netroamerd/internal/exempt"
	"github.com/Partiverse/netroamer/netroamerd/internal/health"
	"github.com/Partiverse/netroamer/netroamerd/internal/judge"
	"github.com/Partiverse/netroamer/netroamerd/internal/mihomoapi"
	"github.com/Partiverse/netroamer/netroamerd/internal/mirrors"
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

// GroupSource 组视图所需的 mihomo 能力子集（*mihomoapi.Client 满足）。
type GroupSource interface {
	Groups(ctx context.Context) (map[string]mihomoapi.ProxyGroup, error)
}

// Deps 控制台数据源。
type Deps struct {
	Version      string
	Actuate      bool
	Started      time.Time
	ST           *store.Store
	Health       *health.Manager
	Ring         *Ring
	EvidenceDir  string
	Cfg          config.Config // 依赖状态检查用（secret 不回显）
	API          GroupSource   // 组视图（nil = 面板显示不可用）
	StateDirPath string        // 写操作令牌文件所在（ui-token）
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
	mux.HandleFunc("/api/deps", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, depsStatus(r.Context(), d))
	})
	mux.HandleFunc("/api/groups", func(w http.ResponseWriter, r *http.Request) {
		if d.API == nil {
			writeJSON(w, map[string]any{"available": false})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
		defer cancel()
		groups, err := d.API.Groups(ctx)
		if err != nil {
			writeJSON(w, map[string]any{"available": false, "err": err.Error()})
			return
		}
		type GroupView struct {
			Name    string   `json:"name"`
			Type    string   `json:"type"`
			Now     string   `json:"now"`
			Members []string `json:"members"`
			HasAgg  bool     `json:"has_agg"` // 是否包含「聚合」组（承载流量判定依据）
		}
		out := []GroupView{}
		for name, g := range groups {
			out = append(out, GroupView{Name: name, Type: g.Type, Now: g.Now,
				Members: g.All, HasAgg: strings.Contains(strings.Join(g.All, "\u0000"), "聚合")})
		}
		sort.Slice(out, func(i, j int) bool {
			if (out[i].Name == "聚合") != (out[j].Name == "聚合") {
				return out[i].Name == "聚合" // 聚合置顶
			}
			return out[i].Name < out[j].Name
		})
		aggEngaged := false
		for _, g := range groups {
			if g.Name != "聚合" && g.Now == "聚合" {
				aggEngaged = true // 某主组当前选中聚合 → 聚合正承载流量
				break
			}
		}
		writeJSON(w, map[string]any{"available": true, "groups": out, "agg_engaged": aggEngaged})
	})
	// 同源下发写操作令牌：SOP 保证跨源页面读不到响应体；
	// hostCheck 已挡 DNS rebinding。前端自动取用，用户零输入。
	mux.HandleFunc("/api/token", func(w http.ResponseWriter, r *http.Request) {
		token, err := ensureToken(d.StateDirPath)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, map[string]any{"token": token})
	})
	mux.HandleFunc("/api/mirrors", func(w http.ResponseWriter, r *http.Request) {
		home, _ := os.UserHomeDir()
		extra := []string{}
		vergeProfiles := filepath.Join(home, "Library", "Application Support",
			"io.github.clash-verge-rev.clash-verge-rev", "profiles")
		if entries, err := os.ReadDir(vergeProfiles); err == nil {
			for _, e := range entries {
				if !e.IsDir() && strings.HasSuffix(e.Name(), ".yaml") {
					extra = append(extra, filepath.Join(vergeProfiles, e.Name()))
				}
			}
		}
		writeJSON(w, map[string]any{"items": mirrors.Detect(home, append(extra, config.MihomoConfigPaths(home)...))})
	})
	// 写操作：镜像切换（Phase B 首块）。令牌鉴权 + judgments 审计。
	mux.HandleFunc("POST /api/mirrors/switch", func(w http.ResponseWriter, r *http.Request) {
		token, err := ensureToken(d.StateDirPath)
		if err != nil {
			http.Error(w, "令牌初始化失败: "+err.Error(), 500)
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Auth")), []byte(token)) != 1 {
			http.Error(w, "unauthorized：写操作需 X-Auth 令牌（内容见 ~/.local/state/netroamer/ui-token 第一行）", 401)
			return
		}
		var req struct {
			Service string `json:"service"`
			Target  string `json:"target"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		home, _ := os.UserHomeDir()
		state, err := mirrors.Switch(home, req.Service, req.Target)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = d.ST.InsertJudgment(ctx, store.Judgment{
			TS: time.Now().Unix(), Kind: "mirror_switch", Target: req.Service,
			Action: "→ " + req.Target,
			Reason: "控制台手动切换，切换后状态 " + state,
		})
		// 切到镜像后立刻探测新源可达性——切换必须可验证，不做摆设
		verified, reach := false, ""
		row := mirrors.Status{}
		for _, m := range mirrors.Detect(home, nil) {
			if m.Service == req.Service {
				row = m
			}
		}
		if req.Target == "mirror" {
			verified, reach = mirrors.VerifyReachable(row.Source)
		}
		writeJSON(w, map[string]any{"service": req.Service, "target": req.Target,
			"state": state, "verified": verified, "reach": reach,
			"effective": row.Source, "via": row.Via})
	})
	mux.HandleFunc("/api/evidence", func(w http.ResponseWriter, r *http.Request) {
		if name := r.URL.Query().Get("name"); name != "" {
			serveEvidence(w, d.EvidenceDir, name)
			return
		}
		writeJSON(w, listEvidence(d.EvidenceDir))
	})
	return hostCheck(safeHeaders(mux))
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

// hostCheck DNS rebinding 防护：Host 必须为环回（攻击页无法伪造 Host 头），
// 这使得同源下发令牌 /api/token 不会被跨源页面读取。
func hostCheck(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(r.Host); err == nil {
			host = h
		}
		if host != "127.0.0.1" && host != "localhost" && host != "::1" {
			http.Error(w, "forbidden：Host 非环回（DNS rebinding 防护）", 403)
			return
		}
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

// Dep 是一条依赖的健康状态。
type Dep struct {
	Name   string `json:"name"`
	State  string `json:"state"` // ok | warn | fail
	Detail string `json:"detail"`
}

// depsStatus 依赖状态检查（全部只读；与 doctor 口径一致但更轻量）。
func depsStatus(ctx context.Context, d Deps) []Dep {
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	api := mihomoapi.New(d.Cfg)
	transport := map[bool]string{true: "unix socket", false: "环回 TCP"}[d.Cfg.UnixSocket != ""]

	deps := []Dep{}
	// 1) mihomo API
	if ver, err := api.Version(ctx); err != nil {
		deps = append(deps, Dep{"mihomo API", "fail", err.Error() + "（" + transport + "）"})
	} else {
		deps = append(deps, Dep{"mihomo API", "ok", strings.TrimPrefix(ver, "v") + " · " + transport})
	}
	// 2) secret
	if d.Cfg.Secret == "" {
		deps = append(deps, Dep{"mihomo secret", "warn", "未发现——API 已设 secret 时会 401（netroamerd doctor 有修复指令）"})
	} else {
		deps = append(deps, Dep{"mihomo secret", "ok", "已发现（值不回显）"})
	}
	// 3) provider 挂载与加载
	info, infoErr := api.ProviderInfo(ctx, actuator.ProviderName)
	mounted, mountErr := api.RuleSetMounted(ctx, actuator.ProviderName)
	switch {
	case infoErr != nil:
		deps = append(deps, Dep{"直连 provider", "fail", "未加载（" + infoErr.Error() + "）"})
	case mountErr != nil:
		deps = append(deps, Dep{"直连 provider", "warn", "已加载 ruleCount=" + strconv.Itoa(info.RuleCount) + "，挂载校验失败"})
	case !mounted:
		deps = append(deps, Dep{"直连 provider", "fail", "已加载但 /rules 无 RULE-SET 行——挂载行缺失，动作不会生效"})
	default:
		deps = append(deps, Dep{"直连 provider", "ok",
			"ruleCount=" + strconv.Itoa(info.RuleCount) + " · RULE-SET 已挂载"})
	}
	// 4) SQLite
	if fi, err := os.Stat(d.Cfg.DBPath); err != nil {
		deps = append(deps, Dep{"遥测库", "fail", d.Cfg.DBPath + "：未创建"})
	} else {
		deps = append(deps, Dep{"遥测库", "ok", humanSize(fi.Size()) + " · " + d.Cfg.DBPath})
	}
	// 5) allow / exempt 列表
	home, _ := os.UserHomeDir()
	allow := exempt.UserList(filepath.Join(home, ".config", "netroamer", "netroamer-allow.txt"))
	if len(allow) == 0 {
		deps = append(deps, Dep{"allow 白名单", "warn", "为空——自动直连不会放行任何域名（写入 ~/.config/netroamer/netroamer-allow.txt）"})
	} else {
		deps = append(deps, Dep{"allow 白名单", "ok", strconv.Itoa(len(allow)) + " 个域名：" + strings.Join(allow, ", ")})
	}
	exm := exempt.UserList(filepath.Join(home, ".config", "netroamer", "netroamer-exempt.txt"))
	if len(exm) == 0 {
		deps = append(deps, Dep{"exempt 列表", "ok", "未配置（可选）"})
	} else {
		deps = append(deps, Dep{"exempt 列表", "ok", strconv.Itoa(len(exm)) + " 个域名（永不自动直连）"})
	}
	// 6) 主动采样成功率（近 24h，直接反映出站探测链路健康）
	probes, err := d.ST.ProbesSince(ctx, "sampling", time.Now().Add(-24*time.Hour))
	if err != nil || len(probes) == 0 {
		deps = append(deps, Dep{"主动采样", "warn", "近 24h 无采样记录（30min/轮，启动即有首轮）"})
	} else {
		okN := 0
		for _, pr := range probes {
			if pr.OK {
				okN++
			}
		}
		ratio := 100 * okN / len(probes)
		state := "ok"
		if ratio < 50 {
			state = "fail"
		} else if ratio < 80 {
			state = "warn"
		}
		deps = append(deps, Dep{"主动采样", state,
			strconv.Itoa(okN) + "/" + strconv.Itoa(len(probes)) + " 成功（近 24h，直连+代理侧）"})
	}
	return deps
}

// ensureToken 写操作令牌：首次生成随机 hex 存 state 目录（0600，仅一行令牌），
// 用户复制进控制台（localStorage）后作为 X-Auth 头。读取时取文件第一行——
// 文件可能含人工附加的说明行（实测：整文件读回会混入提示行致比较失败）。
func ensureToken(stateDir string) (string, error) {
	if stateDir == "" {
		return "", fmt.Errorf("state 目录未配置")
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return "", err
	}
	p := filepath.Join(stateDir, "ui-token")
	if b, err := os.ReadFile(p); err == nil {
		first := strings.TrimSpace(strings.SplitN(string(b), "\n", 2)[0])
		if len(first) >= 16 {
			return first, nil
		}
	}
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	t := hex.EncodeToString(buf)
	if err := os.WriteFile(p, []byte(t+"\n"), 0o600); err != nil {
		return "", err
	}
	return t, nil
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
