// Package mihomoapi 是 mihomo external controller REST 客户端
// （research/05 §2 端点清单）。W3 只用只读端点 + 延迟探测；
// 写端点（providers 热载 / proxies 切换）W4 引入。
package mihomoapi

import (
	"bytes"
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

// PutProvider 触发 rule-provider 热载（PUT /providers/rules/{name}）。
// 仅作用于 netroamer 自有 provider——绝不触碰用户订阅/主配置（§2 红线）。
func (c *Client) PutProvider(ctx context.Context, name string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.base+"/providers/rules/"+url.PathEscape(name), nil)
	if err != nil {
		return err
	}
	if c.secret != "" {
		req.Header.Set("Authorization", "Bearer "+c.secret)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("热载 %s 失败: %w", name, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return ErrUnauthorized
	case resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK:
		return fmt.Errorf("热载 %s: HTTP %d", name, resp.StatusCode)
	}
	return nil
}

// ProviderInfo 是 /providers/rules/{name} 的最小解析（回读验证用）。
type ProviderInfo struct {
	Name      string `json:"name"`
	Behavior  string `json:"behavior"`
	RuleCount int    `json:"ruleCount"`
}

// ProviderInfo 回读 provider 状态；404 表示未挂载（HTTP 404 走 get 的非 200 分支）。
func (c *Client) ProviderInfo(ctx context.Context, name string) (ProviderInfo, error) {
	var out ProviderInfo
	if err := c.get(ctx, "/providers/rules/"+url.PathEscape(name), &out); err != nil {
		return ProviderInfo{}, err
	}
	return out, nil
}

// RuleSetMounted 检查 /rules 中是否存在指向 name 的 RULE-SET 行
// （挂载验证的后半段：provider 加载成功 ≠ 主配置挂载了它）。
func (c *Client) RuleSetMounted(ctx context.Context, name string) (bool, error) {
	rules, err := c.Rules(ctx)
	if err != nil {
		return false, err
	}
	for _, r := range rules {
		if r.Type == "RuleSet" && r.Payload == name {
			return true, nil
		}
	}
	return false, nil
}

// ProxyGroup 是 GET /proxies 中一个策略组的最小解析。
type ProxyGroup struct {
	Name string   `json:"name"`
	Type string   `json:"type"`
	Now  string   `json:"now"`
	All  []string `json:"all"`
}

// Groups 返回全部策略组（Selector/URLTest/Fallback/LoadBalance）。
// 节点名称只在内存使用，不落库（§2 红线）。
func (c *Client) Groups(ctx context.Context) (map[string]ProxyGroup, error) {
	var out struct {
		Proxies map[string]ProxyGroup `json:"proxies"`
	}
	if err := c.get(ctx, "/proxies", &out); err != nil {
		return nil, err
	}
	groups := map[string]ProxyGroup{}
	for name, p := range out.Proxies {
		switch p.Type {
		case "Selector", "URLTest", "Fallback", "LoadBalance":
			groups[name] = p
		}
	}
	return groups, nil
}

// GroupDelay 对整个策略组发起延迟测试（事件驱动：当前节点劣化时才调用）。
// 返回 节点名→毫秒；探测失败的节点不在返回中。
func (c *Client) GroupDelay(ctx context.Context, group, testURL string, timeout time.Duration) (map[string]int, error) {
	q := url.Values{}
	q.Set("timeout", strconv.Itoa(int(timeout.Milliseconds())))
	q.Set("url", testURL)
	var out map[string]int
	if err := c.get(ctx, "/group/"+url.PathEscape(group)+"/delay?"+q.Encode(), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// SelectProxy 切换策略组选中节点（PUT /proxies/{group}）。W5 坏节点切换唯一挂点。
func (c *Client) SelectProxy(ctx context.Context, group, node string) error {
	body, err := json.Marshal(map[string]string{"name": node})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.base+"/proxies/"+url.PathEscape(group), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.secret != "" {
		req.Header.Set("Authorization", "Bearer "+c.secret)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("切换 %s: %w", group, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return ErrUnauthorized
	case resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK:
		return fmt.Errorf("切换 %s→%s: HTTP %d", group, node, resp.StatusCode)
	}
	return nil
}

// ConfigForTest 以完整 URL 构造 Client（单测注入 httptest server）。
func ConfigForTest(baseURL string) config.Config {
	return config.Config{BaseURL: baseURL}
}
