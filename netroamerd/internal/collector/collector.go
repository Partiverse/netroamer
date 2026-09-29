// Package collector 订阅 mihomo WS /connections 全量快照流，
// 跟踪连接生命周期并产出样本（research/05 §1.1 collector、§3 samples）。
// 对未知字段宽容解析（§8 风险预案）；只订阅、不注入任何请求。
package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/coder/websocket"
	"golang.org/x/net/publicsuffix"

	"github.com/Partiverse/netroamer/netroamerd/internal/store"
)

// Snapshot 是 mihomo /connections 每秒推送的全量活跃连接快照。
type Snapshot struct {
	DownloadTotal int64  `json:"downloadTotal"`
	UploadTotal   int64  `json:"uploadTotal"`
	Connections   []Conn `json:"connections"`
	Memory        *int64 `json:"memory"`
}

// Conn 单个连接。mihomo 未提供的字段（如握手时长）保持 nil/零值。
type Conn struct {
	ID          string    `json:"id"`
	Metadata    Metadata  `json:"metadata"`
	Upload      int64     `json:"upload"`
	Download    int64     `json:"download"`
	Start       time.Time `json:"start"`
	Chains      []string  `json:"chains"`
	Rule        string    `json:"rule"`
	RulePayload string    `json:"rulePayload"`
	// mihomo 若提供连接建立时长字段则自动采集（宽容解析）；缺省为 nil
	ConnectDurationMs *int64 `json:"connectDurationMs"`
}

// Metadata 只取遥测需要的子集（research/05 §2 红线4：不存 IP/端口等可识别信息）。
type Metadata struct {
	Network       string `json:"network"`
	Type          string `json:"type"`
	Host          string `json:"host"`
	DestinationIP string `json:"destinationIP"`
	ProcessPath   string `json:"processPath"`
}

// Options 采集参数。
type Options struct {
	URL        string           // mihomo base URL（http/https；必须环回，由 config 层保证）
	UnixSocket string           // 非空时走 unix domain socket（URL host 仅作占位）
	Secret     string           // Bearer；可为空（API 未设 secret 时）
	Grace      time.Duration    // 连接从快照消失到判结束的宽限（防单帧解析失败重复落样本）
	Now        func() time.Time // 可注入时钟；nil = time.Now
	// Rediscover 断线重连前重新发现端点（P1-9：Clash Verge 重启后 unix socket
	// 路径含临时目录哈希会变化）。返回空 URL 表示沿用当前端点。
	Rediscover func() (url, unixSocket, secret string)
}

// Run 阻塞采集：断线后指数退避（1s→60s + 抖动）重连，
// 一条流健康存活超过 30s 则重置退避。ctx 取消后返回 ctx.Err()。
func Run(ctx context.Context, opt Options, log *slog.Logger, onSamples func(context.Context, []store.Sample) error) error {
	if opt.Now == nil {
		opt.Now = time.Now
	}
	backoff := time.Second
	for {
		alive, err := streamOnce(ctx, opt, log, onSamples)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if alive > 30*time.Second {
			backoff = time.Second
		}
		if opt.Rediscover != nil {
			if u, us, sec := opt.Rediscover(); u != "" {
				if u != opt.URL || us != opt.UnixSocket {
					log.Info("mihomo 端点已重发现", "url", u, "unix", us != "")
				}
				opt.URL, opt.UnixSocket, opt.Secret = u, us, sec
			}
		}
		wait := backoff + time.Duration(rand.Int64N(int64(backoff/2)+1))
		log.Warn("connections 流断开，退避重连",
			"err", err, "alive", alive.Round(time.Second), "wait", wait.Round(time.Millisecond))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		backoff *= 2
		if backoff > 60*time.Second {
			backoff = 60 * time.Second
		}
	}
}

func streamOnce(ctx context.Context, opt Options, log *slog.Logger, onSamples func(context.Context, []store.Sample) error) (time.Duration, error) {
	started := opt.Now()
	wsURL := wsBase(opt.URL) + "/connections"
	hdr := http.Header{}
	if opt.Secret != "" {
		hdr.Set("Authorization", "Bearer "+opt.Secret)
	}
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	dialOpt := &websocket.DialOptions{HTTPHeader: hdr}
	if opt.UnixSocket != "" {
		// unix domain socket 传输（Clash Verge Rev 默认形态）：URL host 仅为占位
		dialOpt.HTTPClient = &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					var d net.Dialer
					return d.DialContext(ctx, "unix", opt.UnixSocket)
				},
			},
		}
	}
	c, _, err := websocket.Dial(dialCtx, wsURL, dialOpt)
	if err != nil {
		return opt.Now().Sub(started), fmt.Errorf("dial: %w", err)
	}
	defer c.Close(websocket.StatusNormalClosure, "")
	c.SetReadLimit(32 << 20) // 全量快照可达数 MB，库默认 32KB 上限不够

	tr := NewTracker()
	firstFrame := true
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			return opt.Now().Sub(started), err
		}
		var snap Snapshot
		if err := json.Unmarshal(data, &snap); err != nil {
			log.Warn("快照 JSON 解析失败，跳过本帧", "err", err)
			continue
		}
		batch := tr.Observe(opt.Now(), &snap, firstFrame, opt.Grace)
		firstFrame = false
		if len(batch) > 0 {
			if err := onSamples(ctx, batch); err != nil {
				return opt.Now().Sub(started), err
			}
		}
	}
}

func wsBase(u string) string {
	switch {
	case strings.HasPrefix(u, "https://"):
		return "wss://" + strings.TrimPrefix(u, "https://")
	case strings.HasPrefix(u, "http://"):
		return "ws://" + strings.TrimPrefix(u, "http://")
	default:
		return "ws://" + u
	}
}

// Tracker 把全量快照流转换为连接生命周期事件：
// 上一帧存在、本帧（过宽限后仍）消失的连接视为结束，产出一条样本。
type Tracker struct {
	live    map[string]*trackedConn
	maxLive int // 防御上限：服务端异常时避免 map 无界增长
}

type trackedConn struct {
	conn     Conn
	lastSeen time.Time
}

func NewTracker() *Tracker {
	return &Tracker{live: make(map[string]*trackedConn), maxLive: 8192}
}

// Observe 处理一帧快照。首帧（含重连后的首帧）只登记不产出——
// 否则每次重连都会把当时仍活跃的连接误计为「已结束」各落一条样本。
func (t *Tracker) Observe(now time.Time, snap *Snapshot, firstFrame bool, grace time.Duration) []store.Sample {
	seen := make(map[string]struct{}, len(snap.Connections))
	for i := range snap.Connections {
		c := snap.Connections[i]
		if c.ID == "" {
			continue
		}
		seen[c.ID] = struct{}{}
		if e, ok := t.live[c.ID]; ok {
			e.conn = c
			e.lastSeen = now
			continue
		}
		if len(t.live) >= t.maxLive {
			t.evictOldest()
		}
		t.live[c.ID] = &trackedConn{conn: c, lastSeen: now}
	}
	if firstFrame {
		return nil
	}
	var out []store.Sample
	for id, e := range t.live {
		if _, ok := seen[id]; ok {
			continue // 本帧仍在场，绝不判结束
		}
		if now.Sub(e.lastSeen) < grace {
			continue
		}
		out = append(out, sampleOf(e.conn, now))
		delete(t.live, id)
	}
	return out
}

func (t *Tracker) evictOldest() {
	var oldestID string
	var oldest time.Time
	first := true
	for id, e := range t.live {
		if first || e.lastSeen.Before(oldest) {
			oldestID, oldest, first = id, e.lastSeen, false
		}
	}
	if !first {
		delete(t.live, oldestID)
	}
}

func sampleOf(c Conn, closedAt time.Time) store.Sample {
	return store.Sample{
		TS:     closedAt.Unix(),
		Host:   RegistrableHost(c.Metadata.Host, c.Metadata.DestinationIP),
		Proc:   baseName(c.Metadata.ProcessPath),
		Bucket: BucketOf(closedAt),
		Via:    ViaOf(c.Chains),
		LatMs:  c.ConnectDurationMs,
		OK:     true,
	}
}

// RegistrableHost 取主域（eTLD+1）；无域名（IP 直连）或解析失败时
// 原样截断保留（仍是本地数据，不外传）。
func RegistrableHost(host, fallback string) string {
	h := strings.TrimSuffix(host, ".")
	if h == "" {
		h = fallback
	}
	if h == "" {
		return ""
	}
	if root, err := publicsuffix.EffectiveTLDPlusOne(h); err == nil {
		h = root
	}
	const maxHost = 253
	if len(h) > maxHost {
		h = h[:maxHost]
	}
	return h
}

func baseName(p string) string {
	if p == "" {
		return ""
	}
	b := path.Base(p)
	if b == "." || b == "/" {
		return ""
	}
	return b
}

// ViaOf 按 mihomo 语义：chains[0] 是出口节点，"DIRECT" 即直连。
// 未知一律按 proxy 计——统计上宁可高估代理路径，
// 不给后续「直连更快」判定留危险方向的误判空间。
func ViaOf(chains []string) string {
	if len(chains) > 0 && chains[0] == "DIRECT" {
		return "direct"
	}
	return "proxy"
}

// BucketOf 时段桶：n0 哨兵 × hour-of-week（周一 0 点起算，本地时区）。
// SSID 感知 W2 接入后替换 n0（research/05 §3 bucket 语义）。
func BucketOf(now time.Time) string {
	isoDow := (int(now.Weekday()) + 6) % 7 // 周一=0
	return fmt.Sprintf("n0-%d", isoDow*24+now.Hour())
}
