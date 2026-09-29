// Package actuator 是唯一允许改变 mihomo 行为的模块（05 §1.1）：
// 写自有 rule-provider 文件 → PUT 热载 → 双重回读验证（provider ruleCount
// 对账 + /rules 中 RULE-SET 行存在），失败退避重试，最终失败还原文件。
// 绝不触碰用户订阅与主配置（§2 红线）；挂载段归属见 05 §4.1 provider 挂载归属。
package actuator

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Partiverse/netroamer/netroamerd/internal/mihomoapi"
	"github.com/Partiverse/netroamer/netroamerd/internal/privs"
)

const (
	// ProviderName 自有 rule-provider 名（挂载指纹见 clash/rules-merge.yaml）。
	ProviderName = "netroamer-autodirect"
	// maxVerifyRetries 热载/验证失败重试次数（指数退避 1s→2s→4s）。
	maxVerifyRetries = 3
)

// API 是 actuator 所需的 mihomo 能力子集（*mihomoapi.Client 满足）。
type API interface {
	PutProvider(ctx context.Context, name string) error
	ProviderInfo(ctx context.Context, name string) (mihomoapi.ProviderInfo, error)
	RuleSetMounted(ctx context.Context, name string) (bool, error)
}

// Actuator 管理 netroamer-autodirect.yaml 的生命周期。
type Actuator struct {
	api          API
	providerPath string
}

func New(api API, providerPath string) *Actuator {
	return &Actuator{api: api, providerPath: providerPath}
}

// ProviderYAML 生成 behavior: domain 的 rule-provider 内容。
func ProviderYAML(hosts []string) []byte {
	var b strings.Builder
	b.WriteString("payload:\n")
	for _, h := range hosts {
		b.WriteString("  - '" + strings.ToLower(h) + "'\n")
	}
	return []byte(b.String())
}

// parseProviderYAML 从 provider 文件读回当前域名集合（升序去重由调用方保证）。
func parseProviderYAML(data []byte) []string {
	var hosts []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "- ")
		line = strings.Trim(line, "'\"")
		if line != "" && line != "payload:" {
			hosts = append(hosts, strings.ToLower(line))
		}
	}
	return hosts
}

// Hosts 当前 provider 文件中的域名集合（文件不存在 = 空）。
func (a *Actuator) Hosts() ([]string, error) {
	data, err := os.ReadFile(a.providerPath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return parseProviderYAML(data), nil
}

// Enable 把 host 加入 provider 并热载验证；任何失败都还原文件内容。
func (a *Actuator) Enable(ctx context.Context, host string) error {
	return a.modify(ctx, host, true)
}

// Disable 把 host 从 provider 移除并热载验证（回滚路径）。
func (a *Actuator) Disable(ctx context.Context, host string) error {
	return a.modify(ctx, host, false)
}

func (a *Actuator) modify(ctx context.Context, host string, add bool) error {
	hosts, err := a.Hosts()
	if err != nil {
		return err
	}
	prev := append([]string(nil), hosts...)
	host = strings.ToLower(host)
	found := false
	for _, h := range hosts {
		if h == host {
			found = true
			break
		}
	}
	if add == found {
		return nil // 幂等：已是/已不在目标状态
	}
	if add {
		hosts = append(hosts, host)
	} else {
		var kept []string
		for _, h := range hosts {
			if h != host {
				kept = append(kept, h)
			}
		}
		hosts = kept
	}

	if err := a.writeProvider(hosts); err != nil {
		return err
	}
	if err := a.reloadAndVerify(ctx, len(hosts)); err != nil {
		// 还原文件（热载失败不消耗动作语义——见 05 §4.1 计数语义）
		_ = a.writeProvider(prev)
		return err
	}
	return nil
}

func (a *Actuator) writeProvider(hosts []string) error {
	if err := privs.StateDir(dirOf(a.providerPath)); err != nil {
		return err
	}
	if err := os.WriteFile(a.providerPath, ProviderYAML(hosts), 0o600); err != nil {
		return err
	}
	privs.ChmodFile(a.providerPath)
	return nil
}

// reloadAndVerify 热载 + 双重回读，指数退避重试（P1-8：杜绝「文件已写、
// 内核未载」的状态漂移；验证口径为 ruleCount 对账 + RULE-SET 行存在——P1-4）。
func (a *Actuator) reloadAndVerify(ctx context.Context, wantRules int) error {
	var lastErr error
	backoff := time.Second
	for i := 0; i < maxVerifyRetries; i++ {
		if err := a.api.PutProvider(ctx, ProviderName); err != nil {
			lastErr = fmt.Errorf("热载: %w", err)
		} else if info, err := a.api.ProviderInfo(ctx, ProviderName); err != nil {
			lastErr = fmt.Errorf("provider 回读: %w", err)
		} else if info.RuleCount < wantRules {
			lastErr = fmt.Errorf("provider 回读 ruleCount=%d < 期望 %d", info.RuleCount, wantRules)
		} else if mounted, err := a.api.RuleSetMounted(ctx, ProviderName); err != nil {
			lastErr = fmt.Errorf("/rules 回读: %w", err)
		} else if !mounted {
			lastErr = fmt.Errorf("/rules 中无 RULE-SET %s 行（挂载段缺失或位置错误，见 doctor mount 检查）", ProviderName)
		} else {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
	}
	return fmt.Errorf("热载验证失败（已还原文件）: %w", lastErr)
}

func dirOf(path string) string {
	i := strings.LastIndex(path, "/")
	if i <= 0 {
		return "."
	}
	return path[:i]
}
