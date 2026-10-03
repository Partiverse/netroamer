package mirrors

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func seedFile(t *testing.T, home, rel, content string) {
	t.Helper()
	p := filepath.Join(home, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// ---- 切换器（官方 ↔ 镜像）----

func TestSwitchRoundTrip(t *testing.T) {
	home := t.TempDir()
	for _, svc := range []string{"pip", "npm", "Go", "Homebrew", "Rust (rustup)", "git (GitHub)", "Docker Hub"} {
		st, err := Switch(home, svc, "mirror")
		if err != nil || st != "mirrored" {
			t.Fatalf("%s → mirror: state=%q err=%v", svc, st, err)
		}
		st, err = Switch(home, svc, "official")
		if err != nil {
			t.Fatalf("%s → official: %v", svc, err)
		}
		// 官方步：official 或 not-found 皆合法
		//（文件被清理 + CLI 不在守护 PATH 时，诚实报告 not-found 优于谎报 official）
		if st != "official" && st != "not-found" {
			t.Fatalf("%s → official: state=%q", svc, st)
		}
	}
}

func TestSwitchPIPConfContent(t *testing.T) {
	home := t.TempDir()
	if _, err := Switch(home, "pip", "mirror"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(home, ".config", "pip", "pip.conf"))
	if !strings.Contains(string(data), "index-url = https://pypi.tuna.tsinghua.edu.cn/simple") ||
		!strings.Contains(string(data), "[global]") {
		t.Errorf("pip.conf 内容不符:\n%s", data)
	}
	setConfLine(filepath.Join(home, ".config", "pip", "pip.conf"), "timeout", "", "30")
	if _, err := Switch(home, "pip", "official"); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(filepath.Join(home, ".config", "pip", "pip.conf"))
	if !strings.Contains(string(data), "timeout = 30") {
		t.Errorf("无关配置丢失:\n%s", data)
	}
	if !strings.Contains(string(data), "index-url = https://pypi.org/simple") {
		t.Errorf("官方源未写入:\n%s", data)
	}
}

func TestSwitchBrewBlock(t *testing.T) {
	home := t.TempDir()
	rc := filepath.Join(home, ".zshrc")
	os.WriteFile(rc, []byte("export FOO=1\n"), 0o600)

	if _, err := Switch(home, "Homebrew", "mirror"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(rc)
	s := string(data)
	if !strings.Contains(s, "HOMEBREW_API_DOMAIN") || !strings.Contains(s, "export FOO=1") {
		t.Errorf("镜像块或既有内容丢失:\n%s", s)
	}
	if _, err := Switch(home, "Homebrew", "official"); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(rc)
	s = string(data)
	if strings.Contains(s, "HOMEBREW_") {
		t.Errorf("官方切换后镜像块应移除:\n%s", s)
	}
	if !strings.Contains(s, "export FOO=1") {
		t.Errorf("既有内容丢失:\n%s", s)
	}
}

func TestSwitchRejectsUnknownServiceAndTarget(t *testing.T) {
	home := t.TempDir()
	if _, err := Switch(home, "clash 规则源", "mirror"); err == nil {
		t.Error("仅展示服务应拒绝切换")
	}
	if _, err := Switch(home, "pip", "荒野"); err == nil {
		t.Error("非法 target 应拒绝")
	}
}

func TestSwitchGitUsesHomeOverride(t *testing.T) {
	home := t.TempDir()
	if _, err := Switch(home, "git (GitHub)", "mirror"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".gitconfig"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "gh-proxy.com") {
		t.Errorf("镜像 insteadOf 未写入 HOME 隔离的 gitconfig:\n%s", data)
	}
	if _, err := Switch(home, "git (GitHub)", "official"); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(filepath.Join(home, ".gitconfig"))
	if strings.Contains(string(data), "gh-proxy.com") {
		t.Errorf("官方切换后未移除:\n%s", data)
	}
}

// 新增服务：Yarn/cargo/HuggingFace/Maven/Gradle 往返。
func TestSwitchExtraServices(t *testing.T) {
	home := t.TempDir()
	for _, svc := range []string{"Yarn", "cargo", "HuggingFace", "Maven", "Gradle"} {
		st, err := Switch(home, svc, "mirror")
		if err != nil || st != "mirrored" {
			t.Fatalf("%s → mirror: state=%q err=%v", svc, st, err)
		}
		st, err = Switch(home, svc, "official")
		if err != nil {
			t.Fatalf("%s → official: %v", svc, err)
		}
		// 官方步：official 或 not-found 皆合法
		//（管理块被移除 + CLI 不在守护 PATH 时，诚实报告 not-found 优于谎报 official）
		if st != "official" && st != "not-found" {
			t.Fatalf("%s → official: state=%q", svc, st)
		}
	}
}

// cargo 标记块幂等 + 官方移除后不残留。
func TestSwitchCargoBlockIdempotent(t *testing.T) {
	home := t.TempDir()
	if _, err := Switch(home, "cargo", "mirror"); err != nil {
		t.Fatal(err)
	}
	if _, err := Switch(home, "cargo", "mirror"); err != nil { // 二次不重复写
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(home, ".cargo", "config.toml"))
	if got := strings.Count(string(data), cargoBlockBegin); got != 1 {
		t.Fatalf("标记块应恰好 1 个, got %d", got)
	}
	if _, err := Switch(home, "cargo", "official"); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(filepath.Join(home, ".cargo", "config.toml"))
	if strings.Contains(string(data), "rsproxy") {
		t.Errorf("官方切换后应无 rsproxy:\n%s", data)
	}
}

// Maven 用户自管文件必须拒绝自动切换。
func TestSwitchMavenUserFileRejected(t *testing.T) {
	home := t.TempDir()
	p := filepath.Join(home, ".m2", "settings.xml")
	os.MkdirAll(filepath.Dir(p), 0o700)
	os.WriteFile(p, []byte("<settings><mirrors><mirror><url>https://my.internal/maven</url></mirror></mirrors></settings>"), 0o600)
	if _, err := Switch(home, "Maven", "mirror"); err == nil {
		t.Fatal("用户自管 settings.xml 应拒绝自动切换")
	}
	data, _ := os.ReadFile(p)
	if strings.Contains(string(data), "aliyun") {
		t.Error("用户文件不应被改写")
	}
}

// HuggingFace 切镜像后 rc 有 HF_ENDPOINT 且检测为 mirrored。
func TestSwitchHFDetects(t *testing.T) {
	home := t.TempDir()
	if _, err := Switch(home, "HuggingFace", "mirror"); err != nil {
		t.Fatal(err)
	}
	rc := rcExports(home)
	if rc["HF_ENDPOINT"] != hfMirrorURL {
		t.Fatalf("HF_ENDPOINT = %q, want %s", rc["HF_ENDPOINT"], hfMirrorURL)
	}
	found := map[string]Status{}
	for _, m := range Detect(home, nil) {
		found[m.Service] = m
	}
	if m := found["HuggingFace"]; m.State != "mirrored" {
		t.Errorf("HF 检测 = %s", m.State)
	}
}
