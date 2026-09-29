package actuator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Partiverse/netroamer/netroamerd/internal/mihomoapi"
)

// fakeMihomo 实现 actuator.API：ruleCount 从 provider 文件实时解析（对账语义）。
type fakeMihomo struct {
	path        string
	mounted     bool
	putErr      error
	puts        int
	provider404 bool
}

func (f *fakeMihomo) PutProvider(context.Context, string) error {
	f.puts++
	return f.putErr
}

func (f *fakeMihomo) ProviderInfo(context.Context, string) (mihomoapi.ProviderInfo, error) {
	if f.provider404 {
		return mihomoapi.ProviderInfo{}, errors.New("GET /providers/rules: HTTP 404")
	}
	data, err := os.ReadFile(f.path)
	if err != nil {
		return mihomoapi.ProviderInfo{RuleCount: 0}, nil
	}
	return mihomoapi.ProviderInfo{Behavior: "domain", RuleCount: len(parseProviderYAML(data))}, nil
}

func (f *fakeMihomo) RuleSetMounted(context.Context, string) (bool, error) {
	return f.mounted, nil
}

func newTest(t *testing.T, fm *fakeMihomo) *Actuator {
	t.Helper()
	return New(fm, filepath.Join(t.TempDir(), "netroamer-autodirect.yaml"))
}

func TestProviderYAMLRoundTrip(t *testing.T) {
	y := ProviderYAML([]string{"Slow.CN", "b.cn"})
	if !strings.Contains(string(y), "  - 'slow.cn'") {
		t.Errorf("应小写化域名: %s", y)
	}
	got := parseProviderYAML(y)
	if len(got) != 2 || got[0] != "slow.cn" || got[1] != "b.cn" {
		t.Errorf("round trip 不符: %v", got)
	}
}

// 端到端：Enable 写文件 → 热载 → 双重验证通过 → Hosts 回读一致。
func TestEnableHappyPath(t *testing.T) {
	fm := &fakeMihomo{mounted: true}
	fm.path = filepath.Join(t.TempDir(), "p.yaml")
	act := New(fm, fm.path)
	ctx := context.Background()

	if err := act.Enable(ctx, "slow.cn"); err != nil {
		t.Fatal(err)
	}
	hosts, _ := act.Hosts()
	if len(hosts) != 1 || hosts[0] != "slow.cn" {
		t.Fatalf("hosts = %v", hosts)
	}
	if fm.puts == 0 {
		t.Error("应触发热载")
	}

	// 重复 Enable 幂等（不重复热载）
	puts := fm.puts
	if err := act.Enable(ctx, "slow.cn"); err != nil {
		t.Fatal(err)
	}
	if fm.puts != puts {
		t.Error("幂等 Enable 不应再热载")
	}

	// Disable 回滚路径
	if err := act.Disable(ctx, "slow.cn"); err != nil {
		t.Fatal(err)
	}
	hosts, _ = act.Hosts()
	if len(hosts) != 0 {
		t.Fatalf("Disable 后应清空: %v", hosts)
	}
}

// 验证失败（RULE-SET 未挂载）→ 重试后报错且文件还原（P1-8）。
func TestEnableVerifyFailureRestoresFile(t *testing.T) {
	fm := &fakeMihomo{mounted: false} // /rules 无 RULE-SET 行 → 验证必败
	fm.path = filepath.Join(t.TempDir(), "p.yaml")
	act := New(fm, fm.path)
	ctx := context.Background()

	// 先放一个已有域名作为「还原基准」
	if err := act.writeProvider([]string{"keep.cn"}); err != nil {
		t.Fatal(err)
	}
	err := act.Enable(ctx, "slow.cn")
	if err == nil || !strings.Contains(err.Error(), "RULE-SET") {
		t.Fatalf("应因未挂载失败: %v", err)
	}
	if fm.puts < maxVerifyRetries {
		t.Errorf("应重试 %d 次, 实际热载 %d 次", maxVerifyRetries, fm.puts)
	}
	hosts, _ := act.Hosts()
	if len(hosts) != 1 || hosts[0] != "keep.cn" {
		t.Fatalf("失败后文件应还原: %v", hosts)
	}
}
