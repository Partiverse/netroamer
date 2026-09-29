package exempt

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Partiverse/netroamer/netroamerd/internal/mihomoapi"
)

func TestEligible(t *testing.T) {
	allow := []string{"fastly.net"}
	cases := []struct {
		host   string
		want   bool
		reason string
	}{
		{"www.gov.cn", true, ".cn TLD"},
		{"bilibili.com", true, "内置境内列表"},
		{"cdn.bilibili.com", true, "内置境内列表子域"},
		{"proxy.fastly.net", true, "用户 allow 列表"},
		{"github.com", false, "无资格：典型代理域名"},
		{"", false, "空"},
	}
	for _, c := range cases {
		if got := Eligible(c.host, allow); got != c.want {
			t.Errorf("Eligible(%q) = %v, want %v（%s）", c.host, got, c.want, c.reason)
		}
	}
}

func TestMatcherBlocked(t *testing.T) {
	rules := []mihomoapi.Rule{
		{Type: "DOMAIN-SUFFIX", Payload: "google.com", Proxy: "PROXY"},
		{Type: "DOMAIN", Payload: "openai.com", Proxy: "PROXY"},
		{Type: "DOMAIN-KEYWORD", Payload: "facebook", Proxy: "PROXY"},
		{Type: "DOMAIN-SUFFIX", Payload: "baidu.com", Proxy: "DIRECT"}, // DIRECT 结论不算豁免
		{Type: "GEOIP", Payload: "CN", Proxy: "DIRECT"},
	}
	m := BuildMatcher(rules)
	cases := []struct {
		host string
		want bool
	}{
		{"www.google.com", true},  // DOMAIN-SUFFIX
		{"google.com", true},      // 精确后缀
		{"notgoogle.com", false},  // 后缀语义：非子域
		{"openai.com", true},      // DOMAIN 精确
		{"www.openai.com", false}, // DOMAIN 不含子域（保守，避免误扩）
		{"xfacebook.com", true},   // KEYWORD contains
		{"api.baidu.com", false},  // DIRECT 规则不豁免
		{"github.com", false},     // 无覆盖
	}
	for _, c := range cases {
		if got := m.Blocked(c.host); got != c.want {
			t.Errorf("Blocked(%q) = %v, want %v", c.host, got, c.want)
		}
	}
}

func TestUserList(t *testing.T) {
	p := filepath.Join(t.TempDir(), "l.txt")
	os.WriteFile(p, []byte("# 注释\n- example.com\n\nplain.dev  # 行尾注释\nUPPER.ORG\n"), 0o600)
	got := UserList(p)
	want := []string{"example.com", "plain.dev", "upper.org"}
	if len(got) != len(want) {
		t.Fatalf("UserList = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("UserList[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if UserList(filepath.Join(t.TempDir(), "missing.txt")) != nil {
		t.Error("文件缺失应返回 nil")
	}
}
