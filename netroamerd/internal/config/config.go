// Package config 负责 netroamerd 运行配置与 mihomo API 凭据发现。
// secret 查找顺序：$NETROAMER_MIHOMO_SECRET → ~/.config/netroamer/mihomo.secret
// → mihomo/Clash Verge 常见配置路径的顶层 `secret:` 标量。
// 安全红线（research/05 §2）：secret 只进内存，String()/日志一律脱敏；
// 端点强制环回地址——发现的 0.0.0.0/局域网绑定会被改写为 127.0.0.1。
package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type Config struct {
	BaseURL           string // 形如 http://127.0.0.1:9097
	UnixSocket        string // 非空时走 unix domain socket（Clash Verge Rev 默认形态）
	Secret            string
	DBPath            string
	ProviderPath      string // 自有 rule-provider 文件（必须在 mihomo -d 目录内，SAFE_PATHS）
	LoopbackRewritten bool   // 发现的 external-controller 非环回，已被强制改写
}

// String 脱敏表示，可直接进日志。
func (c Config) String() string {
	return fmt.Sprintf("BaseURL=%s UnixSocket=%s Secret=<redacted %d bytes> DBPath=%s",
		c.BaseURL, c.UnixSocket, len(c.Secret), c.DBPath)
}

// SetBaseURL 用显式传入的地址覆盖自动发现结果（同样强制环回）。
func (c *Config) SetBaseURL(base string) error {
	u, rewritten, err := normalizeBase(base)
	if err != nil {
		return err
	}
	c.BaseURL = u
	c.LoopbackRewritten = rewritten
	return nil
}

func Load() (Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Config{}, fmt.Errorf("home dir: %w", err)
	}

	cfg := Config{DBPath: filepath.Join(home, ".local", "state", "netroamer", "telemetry.db")}
	if v := os.Getenv("NETROAMER_DB"); v != "" {
		cfg.DBPath = v
	}

	base := os.Getenv("NETROAMER_MIHOMO_URL")
	secret := os.Getenv("NETROAMER_MIHOMO_SECRET")

	if secret == "" {
		secret = readTrim(filepath.Join(home, ".config", "netroamer", "mihomo.secret"))
	}

	unixSock := ""
	for _, p := range mihomoConfigPaths(home) {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if base == "" {
			base = yamlScalar(string(data), "external-controller")
		}
		if unixSock == "" {
			unixSock = expandHome(yamlScalar(string(data), "external-controller-unix"), home)
		}
		if secret == "" {
			secret = yamlScalar(string(data), "secret")
		}
		if base != "" && unixSock != "" && secret != "" {
			break
		}
	}

	// Provider 文件位置：mihomo 内核（Verge Rev 编译版）要求 provider path
	// 必须在 -d 目录或编译期 SAFE_PATHS 内（实测事故：state 目录被拒，
	// 整份配置校验失败、内核不应用）。相对路径天然落在 -d 下，最稳。
	cfg.ProviderPath = providerPath(home)

	// 传输选择：Clash Verge Rev 默认只开 unix socket（config.yaml 里的
	// TCP external-controller 常是残留配置、运行时并未监听），
	// socket 文件真实在盘上即优先走 unix；否则走 TCP（强制环回）。
	if unixSock != "" && socketExists(unixSock) {
		cfg.BaseURL = "http://mihomo-ipc" // 占位 host，实际拨号走 cfg.UnixSocket
		cfg.UnixSocket = unixSock
	} else if err := cfg.SetBaseURL(base); err != nil {
		return Config{}, err
	}
	cfg.Secret = secret
	return cfg, nil
}

// MihomoConfigPaths 返回常见 mihomo/Clash Verge 配置路径（doctor 复用）。
func MihomoConfigPaths(home string) []string { return mihomoConfigPaths(home) }

// YAMLScalar 导出顶层标量解析（doctor 扫 external-controller/secret 复用）。
func YAMLScalar(text, key string) string { return yamlScalar(text, key) }

// normalizeBase 补 scheme 并强制环回。
func normalizeBase(base string) (string, bool, error) {
	if base == "" {
		base = "127.0.0.1:9090"
	}
	if !strings.Contains(base, "://") {
		base = "http://" + base
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", false, fmt.Errorf("mihomo url %q: %w", base, err)
	}
	host := u.Hostname()
	if host == "localhost" || (net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback()) {
		return u.Scheme + "://" + u.Host, false, nil
	}
	// 非环回：保留端口、强制 127.0.0.1（netroamerd 绝不跨机连 mihomo API）
	port := u.Port()
	if port == "" {
		port = "9090"
	}
	return u.Scheme + "://" + net.JoinHostPort("127.0.0.1", port), true, nil
}

func mihomoConfigPaths(home string) []string {
	var paths []string
	switch runtime.GOOS {
	case "darwin":
		base := filepath.Join(home, "Library", "Application Support",
			"io.github.clash-verge-rev.clash-verge-rev")
		paths = append(paths,
			filepath.Join(base, "config.yaml"),
			filepath.Join(base, "clash-verge.yaml")) // Verge 运行时合成配置（真实生效）
	case "windows":
		if ad := os.Getenv("APPDATA"); ad != "" {
			paths = append(paths, filepath.Join(ad,
				"io.github.clash-verge-rev.clash-verge-rev", "config.yaml"))
		}
	}
	return append(paths,
		filepath.Join(home, ".config", "mihomo", "config.yaml"),
		filepath.Join(home, ".config", "clash", "config.yaml"),
	)
}

// yamlScalar 无依赖地取顶层单行标量（只为读 external-controller / secret
// 一个值，不值得为此引入 YAML 解析器）。空值（” / ""）视同缺失。
func yamlScalar(text, key string) string {
	prefix := key + ":"
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, prefix) {
			continue // 带缩进的行天然跳过，只匹配顶层键
		}
		v := strings.TrimSpace(strings.TrimPrefix(line, prefix))
		if len(v) >= 2 && (v[0] == '\'' || v[0] == '"') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		return v
	}
	return ""
}

func readTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// providerPath 确定自有 rule-provider 文件路径：优先 Verge 应用目录
// （= Verge 内核 -d），次 mihomo 配置目录（纯 mihomo 的 -d），
// fallback state 目录（doctor 会告警 SAFE_PATHS 风险）。
func providerPath(home string) string {
	candidates := []string{}
	if runtime.GOOS == "darwin" {
		candidates = append(candidates, filepath.Join(home, "Library", "Application Support",
			"io.github.clash-verge-rev.clash-verge-rev"))
	}
	if ad := os.Getenv("APPDATA"); ad != "" && runtime.GOOS == "windows" {
		candidates = append(candidates, filepath.Join(ad,
			"io.github.clash-verge-rev.clash-verge-rev"))
	}
	for _, dir := range candidates {
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			return filepath.Join(dir, "netroamer", "autodirect.yaml")
		}
	}
	if fi, err := os.Stat(filepath.Join(home, ".config", "mihomo")); err == nil && fi.IsDir() {
		return filepath.Join(home, ".config", "mihomo", "netroamer", "autodirect.yaml")
	}
	return filepath.Join(home, ".local", "state", "netroamer", "autodirect.yaml")
}

func expandHome(path, home string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		return filepath.Join(home, strings.TrimPrefix(path, "~"))
	}
	return path
}

// socketExists 判断路径确实是 socket 文件（排除残留普通文件误判）。
func socketExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode()&os.ModeSocket != 0
}
