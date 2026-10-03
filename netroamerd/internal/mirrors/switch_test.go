package mirrors

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 逐服务往返：官方→镜像→官方，状态与文件内容都应可逆。
func TestSwitchRoundTrip(t *testing.T) {
	home := t.TempDir()

	round := func(service string) {
		t.Helper()
		st, err := Switch(home, service, "mirror")
		if err != nil || st != "mirrored" {
			t.Fatalf("%s → mirror: state=%q err=%v", service, st, err)
		}
		st, err = Switch(home, service, "official")
		if err != nil || st != "official" {
			t.Fatalf("%s → official: state=%q err=%v", service, st, err)
		}
	}
	for _, svc := range []string{"pip", "npm", "Go", "Homebrew", "Rust (rustup)", "git (GitHub)", "Docker Hub"} {
		round(svc)
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
	// 既有内容保留：再设置一个无关键后切官方，无关键应仍在
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
