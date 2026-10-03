// Package mirrors 检测系统里哪些服务被镜像替换（控制台「镜像状态」面板）。
// 检测走配置文件而非进程环境——LaunchAgent 守护进程不继承 shell 的 export，
// rc 文件 / 包管理器配置才是事实源。全部只读，零外部命令（LookPath 除外）。
package mirrors

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Status 一条镜像检测结果。
type Status struct {
	Service string `json:"service"`
	State   string `json:"state"` // mirrored | official | custom | not-found
	Source  string `json:"source"`
	Via     string `json:"via"` // 检测来源（文件或 PATH）
}

var mirrorHosts = []string{
	"gh-proxy.com", "ghfast.", "ghproxy.", "mirror.ghproxy.com",
	"pypi.tuna.tsinghua.edu.cn", "mirrors.aliyun.com", "mirrors.cloud.tencent.com",
	"mirrors.ustc.edu.cn", "npmmirror.com", "registry.npmmirror.com",
	"goproxy.cn", "goproxy.io", "rsproxy.cn", "mirrors.",
}
var officialHosts = []string{
	"formulae.brew.sh", "ghcr.io", "pypi.org", "files.pythonhosted.org",
	"registry.npmjs.org", "registry.yarnpkg.com", "proxy.golang.org", "static.rust-lang.org", "github.com",
}

// Detect 汇总全部服务的镜像状态。extraFiles 为额外扫描的配置文件
// （如 Verge 增强文件——检测 clash 规则源是否走 gh-proxy）。
func Detect(home string, extraFiles []string) []Status {
	rc := rcExports(home)
	out := []Status{}

	// Homebrew（HOMEBREW_API_DOMAIN / BOTTLE_DOMAIN，bootstrap 写入 .zshrc 带指纹）
	v := firstNonEmpty(rc["HOMEBREW_API_DOMAIN"], rc["HOMEBREW_BOTTLE_DOMAIN"])
	out = append(out, row("Homebrew", v, home, ".zshrc HOMEBREW_*", "brew"))

	// Go（`go env -w` 写入 ~/.config/go/env）
	out = append(out, row("Go", goProxy(home), home, "~/.config/go/env GOPROXY", "go"))
	out = append(out, pipRow(home))
	out = append(out, npmRow(home))
	out = append(out, yarnRow(home))
	out = append(out, rustRow(rc, home))
	out = append(out, cargoRow(home))
	out = append(out, hfRow(rc, home))
	out = append(out, mavenRow(home))
	out = append(out, gradleRow(home))
	out = append(out, gitRow(home))
	out = append(out, dockerRow(home))
	out = append(out, clashSources(extraFiles))
	return out
}

// yarnRow yarn 经典配置（.yarnrc）或 berry（.yarnrc.yml）。
func yarnRow(home string) Status {
	if v := yarnrcValue(filepath.Join(home, ".yarnrc.yml"), "npmRegistryServer:"); v != "" {
		return Status{Service: "Yarn (berry)", State: classify(v), Source: v, Via: "~/.yarnrc.yml"}
	}
	if v := yarnrcValue(filepath.Join(home, ".yarnrc"), `registry`); v != "" {
		return Status{Service: "Yarn", State: classify(v), Source: v, Via: "~/.yarnrc registry"}
	}
	return row("Yarn", "", home, "~/.yarnrc", "yarn")
}

// cargoRow ~/.cargo/config.toml 是否有 crates-io 换源（rsproxy 等）。
func cargoRow(home string) Status {
	data, err := os.ReadFile(filepath.Join(home, ".cargo", "config.toml"))
	if err != nil {
		return row("cargo", "", home, "~/.cargo/config.toml", "cargo")
	}
	t := string(data)
	if strings.Contains(t, "replace-with") && strings.Contains(t, "rsproxy") {
		return Status{Service: "cargo", State: "mirrored", Source: "crates.io → rsproxy.cn", Via: "~/.cargo/config.toml"}
	}
	if strings.Contains(t, "[source.crates-io]") {
		return Status{Service: "cargo", State: "custom", Source: "存在自定义 source 替换", Via: "~/.cargo/config.toml"}
	}
	return Status{Service: "cargo", State: "official", Source: "crates.io（官方）", Via: "~/.cargo/config.toml"}
}

// hfRow HuggingFace 镜像（HF_ENDPOINT=hf-mirror.com，AI 开发常用）。
func hfRow(rc map[string]string, home string) Status {
	v := rc["HF_ENDPOINT"]
	if v == "" {
		return row("HuggingFace", "", home, ".zshrc HF_ENDPOINT", "huggingface-cli")
	}
	if strings.Contains(v, "hf-mirror.com") {
		return Status{Service: "HuggingFace", State: "mirrored", Source: v, Via: ".zshrc HF_ENDPOINT"}
	}
	return Status{Service: "HuggingFace", State: "official", Source: v, Via: ".zshrc HF_ENDPOINT"}
}

// mavenRow ~/.m2/settings.xml：不存在=官方；含 aliyun=镜像；其余=自定义。
func mavenRow(home string) Status {
	p := filepath.Join(home, ".m2", "settings.xml")
	data, err := os.ReadFile(p)
	if err != nil {
		return row("Maven", "", home, "~/.m2/settings.xml", "mvn")
	}
	t := string(data)
	if strings.Contains(t, "maven.aliyun.com") {
		return Status{Service: "Maven", State: "mirrored", Source: "maven.aliyun.com/repository/public", Via: "~/.m2/settings.xml"}
	}
	return Status{Service: "Maven", State: "custom", Source: "settings.xml 为用户自管内容（不自动切换）", Via: "~/.m2/settings.xml"}
}

// gradleRow ~/.gradle/init.gradle：不存在=官方；含 aliyun=镜像；其余=自定义。
func gradleRow(home string) Status {
	p := filepath.Join(home, ".gradle", "init.gradle")
	data, err := os.ReadFile(p)
	if err != nil {
		return row("Gradle", "", home, "~/.gradle/init.gradle", "gradle")
	}
	t := string(data)
	if strings.Contains(t, "maven.aliyun.com") {
		return Status{Service: "Gradle", State: "mirrored", Source: "maven.aliyun.com/repository/public", Via: "~/.gradle/init.gradle"}
	}
	return Status{Service: "Gradle", State: "custom", Source: "init.gradle 为用户自管内容（不自动切换）", Via: "~/.gradle/init.gradle"}
}

// yarnrcValue 取 `key "value"` / `key: value` 形式的值。
func yarnrcValue(path, key string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, key) {
			continue
		}
		v := strings.TrimSpace(strings.TrimPrefix(t, key))
		v = strings.Trim(v, "\"'")
		if v != "" {
			return v
		}
	}
	return ""
}

// row 组装单条：source 非空 → 分类；空 → 按 LookPath 区分官方/未安装。
// 搜索目录含 brew/cargo/go 常见安装位——守护进程 PATH 很窄，不能只看 PATH。
func row(service, source, home, via, bin string) Status {
	if source == "" {
		if lookPath(bin, home) {
			return Status{Service: service, State: "official", Source: "官方源（未检出镜像配置）", Via: "PATH"}
		}
		return Status{Service: service, State: "not-found", Source: "未检测到安装与镜像配置", Via: "PATH"}
	}
	return Status{Service: service, State: classify(source), Source: source, Via: via}
}

func lookPath(bin, home string) bool {
	dirs := []string{
		"/opt/homebrew/bin", "/usr/local/bin", "/usr/local/go/bin",
		filepath.Join(home, ".local", "bin"), filepath.Join(home, ".cargo", "bin"),
	}
	if _, err := exec.LookPath(bin); err == nil {
		return true
	}
	for _, d := range dirs {
		if _, err := os.Stat(filepath.Join(d, bin)); err == nil {
			return true
		}
	}
	return false
}

func pipRow(home string) Status {
	for _, p := range []string{
		filepath.Join(home, ".config", "pip", "pip.conf"),
		filepath.Join(home, ".pip", "pip.conf"),
	} {
		if v := confValue(p, "index-url"); v != "" {
			return Status{Service: "pip", State: classify(v), Source: v, Via: p}
		}
	}
	return row("pip", "", home, "pip.conf", "pip3")
}

func npmRow(home string) Status {
	if v := confValue(filepath.Join(home, ".npmrc"), "registry"); v != "" {
		return Status{Service: "npm", State: classify(v), Source: v, Via: "~/.npmrc registry"}
	}
	return row("npm", "", home, "~/.npmrc", "npm")
}

func rustRow(rc map[string]string, home string) Status {
	v := firstNonEmpty(rc["RUSTUP_DIST_SERVER"], rc["RUSTUP_UPDATE_ROOT"])
	return row("Rust (rustup)", v, home, ".zshrc RUSTUP_*", "rustup")
}

func gitRow(home string) Status {
	data, err := os.ReadFile(filepath.Join(home, ".gitconfig"))
	if err == nil {
		section := ""
		for _, line := range strings.Split(string(data), "\n") {
			t := strings.TrimSpace(line)
			if strings.HasPrefix(t, "[url") {
				section = strings.Trim(t, "[]") // 镜像前缀在 section 头
				continue
			}
			if strings.HasPrefix(t, "insteadOf") && strings.Contains(section, "://") {
				return Status{Service: "git (GitHub)", State: classify(section),
					Source: section + "（insteadOf 重写）", Via: "~/.gitconfig"}
			}
		}
	}
	return row("git (GitHub)", "", home, "~/.gitconfig", "git")
}

func dockerRow(home string) Status {
	p := filepath.Join(home, ".docker", "daemon.json")
	if data, err := os.ReadFile(p); err == nil {
		var dj struct {
			RegistryMirrors []string `json:"registry-mirrors"`
		}
		if json.Unmarshal(data, &dj) == nil && len(dj.RegistryMirrors) > 0 {
			return Status{Service: "Docker Hub", State: "mirrored",
				Source: strings.Join(dj.RegistryMirrors, ", "), Via: "~/.docker/daemon.json"}
		}
		return Status{Service: "Docker Hub", State: "official", Source: "registry-1.docker.io（官方）", Via: "~/.docker/daemon.json"}
	}
	return row("Docker Hub", "", home, "~/.docker/daemon.json", "docker")
}

// clashSources clash 规则源是否走 gh-proxy（rule-providers URL 前缀）。
func clashSources(extraFiles []string) Status {
	for _, p := range extraFiles {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if strings.Contains(string(data), "gh-proxy.com") {
			return Status{Service: "clash 规则源", State: "mirrored",
				Source: "rule-providers 经 gh-proxy.com 加速", Via: filepath.Base(p)}
		}
	}
	return Status{Service: "clash 规则源", State: "official", Source: "未检出 gh-proxy 加速（规则源直连）", Via: "配置扫描"}
}

func goProxy(home string) string {
	return confValue(filepath.Join(home, ".config", "go", "env"), "GOPROXY")
}

// confValue  key=value / key = value 形式配置文件的第一个匹配值。
func confValue(path, key string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, key) {
			v := strings.TrimSpace(strings.TrimPrefix(t, key))
			v = strings.TrimPrefix(v, "=")
			v = strings.TrimSpace(v)
			v = strings.Trim(v, `"'`)
			if v != "" {
				return v
			}
		}
	}
	return ""
}

// rcExports 从 zshrc/bashrc 提取 export KEY=VALUE（最后一条生效）。
func rcExports(home string) map[string]string {
	out := map[string]string{}
	for _, rc := range []string{".zshrc", ".bashrc"} {
		data, err := os.ReadFile(filepath.Join(home, rc))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			t := strings.TrimSpace(line)
			t = strings.TrimPrefix(t, "export ")
			if !strings.Contains(t, "=") || strings.HasPrefix(t, "#") {
				continue
			}
			k, v, _ := strings.Cut(t, "=")
			k = strings.TrimSpace(k)
			v = strings.TrimSpace(v)
			if k == "" || strings.Contains(k, " ") {
				continue
			}
			v = strings.Trim(v, `"'`)
			if v != "" {
				out[k] = v
			}
		}
	}
	return out
}

func classify(source string) string {
	l := strings.ToLower(source)
	for _, m := range mirrorHosts {
		if strings.Contains(l, m) {
			return "mirrored"
		}
	}
	for _, o := range officialHosts {
		if strings.Contains(l, o) {
			return "official"
		}
	}
	return "custom"
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
