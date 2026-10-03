package mirrors

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func seed(t *testing.T, home, rel, content string) {
	t.Helper()
	p := filepath.Join(home, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDetectMirrored(t *testing.T) {
	home := t.TempDir()
	seed(t, home, ".zshrc", `export HOMEBREW_API_DOMAIN="https://gh-proxy.com/https://formulae.brew.sh/api"
export RUSTUP_DIST_SERVER=https://rsproxy.cn`)
	seed(t, home, ".config/pip/pip.conf", "[global]\nindex-url = https://pypi.tuna.tsinghua.edu.cn/simple\n")
	seed(t, home, ".npmrc", "registry=https://registry.npmmirror.com\n")
	seed(t, home, ".config/go/env", "GOPROXY=https://goproxy.cn,direct")
	seed(t, home, ".gitconfig", "[url \"https://gh-proxy.com/https://github.com/\"]\n\tinsteadOf = https://github.com\n")

	st := map[string]Status{}
	for _, m := range Detect(home, nil) {
		st[m.Service] = m
	}
	for _, svc := range []string{"Homebrew", "pip", "npm", "Go", "Rust (rustup)", "git (GitHub)"} {
		m, ok := st[svc]
		if !ok {
			t.Fatalf("缺 %s 检测行", svc)
		}
		if m.State != "mirrored" {
			t.Errorf("%s state = %s (%s), want mirrored", svc, m.State, m.Source)
		}
	}
}

func TestDetectOfficialAndNotFound(t *testing.T) {
	home := t.TempDir()
	seed(t, home, ".npmrc", "registry=https://registry.npmjs.org\n")
	found := map[string]Status{}
	for _, m := range Detect(home, nil) {
		found[m.Service] = m
	}
	if m := found["npm"]; m.State != "official" {
		t.Errorf("npm state = %s, want official", m.State)
	}
	// PATH 上必有 git（CI/开发机），无镜像配置 → official
	if m := found["git (GitHub)"]; m.State == "mirrored" {
		t.Errorf("git 不应误判镜像: %+v", m)
	}
	// Homebrew 无配置：开发机大概率装了 brew → official；否则 not-found（两者皆合法）
	if m := found["Homebrew"]; m.State != "official" && m.State != "not-found" {
		t.Errorf("Homebrew state = %s", m.State)
	}
}

func TestClashSources(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "merge.yaml")
	os.WriteFile(p, []byte("rule-providers:\n  url: https://gh-proxy.com/https://raw.githubusercontent.com/x\n"), 0o600)
	m := clashSources([]string{p})
	if m.State != "mirrored" {
		t.Errorf("gh-proxy 规则源应 mirrored: %+v", m)
	}
	m = clashSources([]string{filepath.Join(dir, "missing.yaml")})
	if m.State != "official" {
		t.Errorf("无文件应 official: %+v", m)
	}
}

func TestConfValueSetu(t *testing.T) {
	home := t.TempDir()
	seed(t, home, ".zshrc", "export NO_PROXY=\"localhost,127.0.0.0/8,.cn\"\n")
	rc := rcExports(home)
	if v := rc["NO_PROXY"]; !strings.Contains(v, "127.0.0.0/8") {
		t.Errorf("NO_PROXY = %q", v)
	}
}
