// Package mihomoapi 是 mihomo external controller REST 客户端
// （research/05 §2 端点清单）。W3 只用只读端点 + 延迟探测；
// 写端点（providers 热载 / proxies 切换）W4 引入。
package mihomoapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/Partiverse/netroamer/netroamerd/internal/config"
	"github.com/Partiverse/netroamer/netroamerd/internal/service"
)

// ErrUnauthorized secret 缺失或不匹配（doctor 据此给修复指令）。
var ErrUnauthorized = errors.New("401：mihomo 已设 secret，但 netroamerd 未发现或不匹配")

type Client struct {
	base   string // http://127.0.0.1:9090 或 http://mihomo-ipc 占位
	unix   string
	secret string
	hc     *http.Client
}

func New(cfg config.Config) *Client {
	return &Client{
		base:   cfg.BaseURL,
		unix:   cfg.UnixSocket,
		secret: cfg.Secret,
		hc:     service.NewHTTPClient(cfg.UnixSocket, 10*time.Second),
	}
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return err
	}
	if c.secret != "" {
		req.Header.Set("Authorization", "Bearer "+c.secret)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("连接 mihomo API 失败: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return ErrUnauthorized
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("GET %s: HTTP %d", path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) Version(ctx context.Context) (string, error) {
	var v struct {
		Version string `json:"version"`
	}
	if err := c.get(ctx, "/version", &v); err != nil {
		return "", err
	}
	return v.Version, nil
}

// Rule 是 /rules 的一条规则（只取判定所需字段）。
type Rule struct {
	Type    string `json:"type"`
	Payload string `json:"payload"`
	Proxy   string `json:"proxy"`
}

func (c *Client) Rules(ctx context.Context) ([]Rule, error) {
	var out struct {
		Rules []Rule `json:"rules"`
	}
	if err := c.get(ctx, "/rules", &out); err != nil {
		return nil, err
	}
	return out.Rules, nil
}

// DelayTest 经指定代理（或 DIRECT）对 testURL 发起同口径 TTFB 探测，
// 返回毫秒。mihomo 探测失败返回非 200（如 504），归一为 error。
func (c *Client) DelayTest(ctx context.Context, proxy, testURL string, timeout time.Duration) (int, error) {
	q := url.Values{}
	q.Set("timeout", strconv.Itoa(int(timeout.Milliseconds())))
	q.Set("url", testURL)
	var v struct {
		Delay int `json:"delay"`
	}
	if err := c.get(ctx, "/proxies/"+url.PathEscape(proxy)+"/delay?"+q.Encode(), &v); err != nil {
		return 0, err
	}
	if v.Delay <= 0 {
		return 0, fmt.Errorf("delay test %s: 异常返回 %d", proxy, v.Delay)
	}
	return v.Delay, nil
}

// ConnectionMetadata / Connection 是 /connections 的最小解析
// （判定时解析域名当前出口节点用；只进内存不落库）。
type ConnectionMetadata struct {
	Host string `json:"host"`
}

type Connection struct {
	Metadata ConnectionMetadata `json:"metadata"`
	Chains   []string           `json:"chains"`
}

func (c *Client) Connections(ctx context.Context) ([]Connection, error) {
	var out struct {
		Connections []Connection `json:"connections"`
	}
	if err := c.get(ctx, "/connections", &out); err != nil {
		return nil, err
	}
	return out.Connections, nil
}
