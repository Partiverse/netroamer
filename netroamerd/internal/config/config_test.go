package config

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// 清空相关环境变量（HOME 由各测试用例自行设置）
func clearEnv(t *testing.T) {
	t.Helper()
	t.Setenv("NETROAMER_MIHOMO_URL", "")
	t.Setenv("NETROAMER_MIHOMO_SECRET", "")
	t.Setenv("NETROAMER_DB", "")
}

func writeVergeConfig(t *testing.T, home, content string) {
	t.Helper()
	dir := filepath.Join(home, "Library", "Application Support", "io.github.clash-verge-rev.clash-verge-rev")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadFromClashVergeConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	clearEnv(t)
	writeVergeConfig(t, home, "mode: rule\nexternal-controller: 127.0.0.1:9097\nsecret: abc123\n")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BaseURL != "http://127.0.0.1:9097" {
		t.Errorf("BaseURL = %q, want http://127.0.0.1:9097", cfg.BaseURL)
	}
	if cfg.Secret != "abc123" {
		t.Errorf("Secret 未从配置发现")
	}
	if cfg.DBPath != filepath.Join(home, ".local", "state", "netroamer", "telemetry.db") {
		t.Errorf("DBPath = %q", cfg.DBPath)
	}
	if strings.Contains(cfg.String(), "abc123") {
		t.Error("String() 泄漏 secret")
	}
}

func TestDefaultAndNonLoopbackRewrite(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	clearEnv(t)

	cfg, err := Load() // 无任何配置 → 默认 127.0.0.1:9090
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BaseURL != "http://127.0.0.1:9090" || cfg.LoopbackRewritten {
		t.Errorf("默认值不符: %+v", cfg)
	}

	if err := cfg.SetBaseURL("0.0.0.0:9090"); err != nil {
		t.Fatal(err)
	}
	if !cfg.LoopbackRewritten || cfg.BaseURL != "http://127.0.0.1:9090" {
		t.Errorf("非环回应改写 127.0.0.1: BaseURL=%q rewritten=%v", cfg.BaseURL, cfg.LoopbackRewritten)
	}
}

func TestSecretFilePriority(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	clearEnv(t)
	writeVergeConfig(t, home, "secret: fromconfig\n")
	netroamerDir := filepath.Join(home, ".config", "netroamer")
	if err := os.MkdirAll(netroamerDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(netroamerDir, "mihomo.secret"), []byte("fromfile\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Secret != "fromfile" {
		t.Errorf("netroamer secret 文件应优先于 mihomo 配置, got %q", cfg.Secret)
	}
}

func TestUnixSocketDiscovery(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix socket 仅 POSIX")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	clearEnv(t)
	// 造一个真 socket 文件（Listen 后 Close，文件保留在盘上）。
	// 放 /tmp 短路径：macOS sun_path 上限 104 字节，t.TempDir() 太长。
	dir, err := os.MkdirTemp("/tmp", "nrtest-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "m.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	// macOS 上 Close 会自动 unlink socket 文件，故首次 Load 前保持打开
	writeVergeConfig(t, home,
		"external-controller: 127.0.0.1:9097\nexternal-controller-unix: "+sock+"\nsecret: s1\n")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UnixSocket != sock || cfg.BaseURL != "http://mihomo-ipc" {
		t.Errorf("socket 在盘应优先走 unix: UnixSocket=%q BaseURL=%q", cfg.UnixSocket, cfg.BaseURL)
	}

	// 回退场景：socket 位置是普通文件 → 走 TCP（强制环回）
	l.Close()
	_ = os.Remove(sock)
	if err := os.WriteFile(sock, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UnixSocket != "" || cfg.BaseURL != "http://127.0.0.1:9097" {
		t.Errorf("残留普通文件应回退 TCP: UnixSocket=%q BaseURL=%q", cfg.UnixSocket, cfg.BaseURL)
	}
}

func TestYamlScalar(t *testing.T) {
	cases := []struct {
		text, key, want string
	}{
		{"secret: abc\n", "secret", "abc"},
		{"secret: ''\n", "secret", ""},                               // 空引号 = 缺失
		{"secret: 'a b'\n", "secret", "a b"},                         // 引号剥离
		{"  secret: x\n", "secret", ""},                              // 缩进行不匹配（只取顶层）
		{"external-controller-cors: y\n", "external-controller", ""}, // 前缀相同但键不同
		{"external-controller: 127.0.0.1:9097\n", "external-controller", "127.0.0.1:9097"},
	}
	for _, c := range cases {
		if got := yamlScalar(c.text, c.key); got != c.want {
			t.Errorf("yamlScalar(%q, %q) = %q, want %q", c.text, c.key, got, c.want)
		}
	}
}
