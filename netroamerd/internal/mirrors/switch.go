// 镜像切换器（控制台写操作，Phase B 首块）：pip/npm/Go/Homebrew/Rust/
// git/Docker 七个服务支持「官方 ↔ 镜像」互切。原则：
// ①改动全部落在可辨识的管理块/单值上，可逆；②rc 改动仅对新终端生效；
// ③Docker 改 daemon.json 后需手动重启 Docker 才生效；④service 经
// Switch 入口的白名单 switch-case 匹配，任意外部输入到不了 exec 参数。
package mirrors

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	brewBlockBegin = "# netroamer: homebrew mirror switch (managed block) [begin]"
	brewBlockEnd   = "# netroamer: homebrew mirror switch (managed block) [end]"
	rustBlockBegin = "# netroamer: rustup mirror switch (managed block) [begin]"
	rustBlockEnd   = "# netroamer: rustup mirror switch (managed block) [end]"

	pipMirrorURL   = "https://pypi.tuna.tsinghua.edu.cn/simple"
	pipOfficialURL = "https://pypi.org/simple"
	npmMirrorURL   = "https://registry.npmmirror.com"
	npmOfficialURL = "https://registry.npmjs.org"
	goMirrorLine   = "GOPROXY=https://goproxy.cn,direct"
	goOfficialLine = "GOPROXY=https://proxy.golang.org,direct"
	gitMirrorHost  = "https://gh-proxy.com/https://github.com/"
	dockerMirror   = "https://docker.m.daocloud.io"
)

// Switch 把 service 切到 target（"mirror" | "official"），返回切换后的状态。
// service 走白名单 case 匹配，未匹配即拒绝。
func Switch(home, service, target string) (string, error) {
	if target != "mirror" && target != "official" {
		return "", fmt.Errorf("target 须为 mirror 或 official")
	}
	var err error
	switch service {
	case "pip":
		err = setConfLine(filepath.Join(home, ".config", "pip", "pip.conf"),
			"index-url", "[global]", pick(target, pipMirrorURL, pipOfficialURL))
	case "npm":
		err = setConfLine(filepath.Join(home, ".npmrc"),
			"registry", "", pick(target, npmMirrorURL, npmOfficialURL))
	case "Go":
		err = setConfLine(filepath.Join(home, ".config", "go", "env"),
			"GOPROXY", "", pick(target, goMirrorLine, goOfficialLine))
	case "Homebrew":
		err = switchBrew(home, target)
	case "Rust (rustup)":
		err = switchRust(home, target)
	case "git (GitHub)":
		err = gitInsteadOf(home, target == "mirror")
	case "Docker Hub":
		err = dockerMirrors(home, target == "mirror")
	case "Yarn":
		err = switchYarn(home, target)
	case "cargo":
		err = switchCargo(home, target)
	case "HuggingFace":
		err = switchHF(home, target)
	case "Maven":
		err = switchMaven(home, target)
	case "Gradle":
		err = switchGradle(home, target)
	default:
		return "", fmt.Errorf("服务 %q 不支持切换（仅展示）", service)
	}
	if err != nil {
		return "", err
	}
	for _, s := range Detect(home, nil) {
		if s.Service == service {
			return s.State, nil
		}
	}
	return "", nil
}

func pick(target, mirror, official string) string {
	if target == "mirror" {
		return mirror
	}
	return official
}

func switchBrew(home, target string) error {
	if target == "mirror" {
		return rcBlockAdd(filepath.Join(home, ".zshrc"), brewBlockBegin, brewBlockEnd, []string{
			`export HOMEBREW_BREW_GIT_REMOTE="https://gh-proxy.com/https://github.com/Homebrew/brew.git"`,
			`export HOMEBREW_CORE_GIT_REMOTE="https://gh-proxy.com/https://github.com/Homebrew/homebrew-core.git"`,
			`export HOMEBREW_API_DOMAIN="https://gh-proxy.com/https://formulae.brew.sh/api"`,
			`export HOMEBREW_BOTTLE_DOMAIN="https://ghfast.cloud"`,
		})
	}
	return rcBlockRemove(filepath.Join(home, ".zshrc"), brewBlockBegin, brewBlockEnd)
}

func switchRust(home, target string) error {
	if target == "mirror" {
		return rcBlockAdd(filepath.Join(home, ".zshrc"), rustBlockBegin, rustBlockEnd, []string{
			`export RUSTUP_DIST_SERVER=https://rsproxy.cn`,
			`export RUSTUP_UPDATE_ROOT=https://rsproxy.cn/rustup`,
		})
	}
	return rcBlockRemove(filepath.Join(home, ".zshrc"), rustBlockBegin, rustBlockEnd)
}

// gitInsteadOf 用 git CLI 管理 insteadOf（.gitconfig 节区手工拼装易错）。
// 命令与参数全部为字面量+包级常量，无任何外部输入进入参数列表。
func gitInsteadOf(home string, mirror bool) error {
	const key = `url.` + gitMirrorHost + `.insteadOf`
	if mirror {
		c := exec.Command("git", "config", "--global", key, "https://github.com/")
		c.Env = append(os.Environ(), "HOME="+home)
		c.Dir = home
		if out, err := c.CombinedOutput(); err != nil {
			return fmt.Errorf("git config: %v: %s", err, out)
		}
		return nil
	}
	c := exec.Command("git", "config", "--global", "--unset-all", key)
	c.Env = append(os.Environ(), "HOME="+home)
	c.Dir = home
	if out, err := c.CombinedOutput(); err != nil {
		if strings.Contains(string(out), "Nothing to delete") {
			return nil // 幂等
		}
		return fmt.Errorf("git config --unset: %v: %s", err, out)
	}
	return nil
}

// dockerMirrors 编辑 ~/.docker/daemon.json 的 registry-mirrors（改后需重启 Docker）。
func dockerMirrors(home string, mirror bool) error {
	p := filepath.Join(home, ".docker", "daemon.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	doc := map[string]any{}
	if data, err := os.ReadFile(p); err == nil {
		if err := json.Unmarshal(data, &doc); err != nil {
			return fmt.Errorf("daemon.json 解析失败: %w", err)
		}
	}
	if mirror {
		doc["registry-mirrors"] = []string{dockerMirror}
	} else {
		delete(doc, "registry-mirrors")
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o600)
}

// setConfLine 设置 key=value 行（存在替换、缺失追加）。
func setConfLine(path, key, section, value string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	var lines []string
	if data, err := os.ReadFile(path); err == nil {
		lines = strings.Split(string(data), "\n")
	}
	found := false
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), key) {
			lines[i] = key + " = " + value
			found = true
		}
	}
	if !found {
		if section != "" && !containsLine(lines, section) {
			lines = append(lines, section)
		}
		lines = append(lines, key+" = "+value)
	}
	return os.WriteFile(path, []byte(strings.TrimRight(strings.Join(lines, "\n"), "\n")+"\n"), 0o600)
}

func containsLine(lines []string, want string) bool {
	for _, l := range lines {
		if strings.TrimSpace(l) == want {
			return true
		}
	}
	return false
}

// rcBlockAdd / rcBlockRemove 管理标记块（begin/end 之间原样替换）。
func rcBlockAdd(rcPath, begin, end string, body []string) error {
	var lines []string
	if data, err := os.ReadFile(rcPath); err == nil {
		lines = strings.Split(string(data), "\n")
	}
	var kept []string
	inBlock := false
	for _, l := range lines {
		t := strings.TrimSpace(l)
		switch {
		case t == begin:
			inBlock = true
		case t == end:
			inBlock = false
		case !inBlock:
			kept = append(kept, l)
		}
	}
	block := append([]string{begin}, body...)
	block = append(block, end)
	kept = append(kept, block...)
	return os.WriteFile(rcPath, []byte(strings.TrimLeft(strings.Join(kept, "\n"), "\n")+"\n"), 0o600)
}

func rcBlockRemove(rcPath, begin, end string) error {
	data, err := os.ReadFile(rcPath)
	if err != nil {
		return nil
	}
	var kept []string
	inBlock := false
	for _, l := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(l)
		switch {
		case t == begin:
			inBlock = true
		case t == end:
			inBlock = false
		case !inBlock:
			kept = append(kept, l)
		}
	}
	return os.WriteFile(rcPath, []byte(strings.Join(kept, "\n")), 0o644)
}
