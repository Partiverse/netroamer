// 新增镜像服务的切换逻辑（Yarn/cargo/HuggingFace/Maven/Gradle）。
// Maven/Gradle 的用户自管文件（存在自定义内容时）不做自动切换，仅展示。
package mirrors

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	yarnMirrorURL   = "https://registry.npmmirror.com"
	yarnOfficialURL = "https://registry.yarnpkg.com"
	hfMirrorURL     = "https://hf-mirror.com"

	mavenManagedMarker = "<!-- netroamer: managed mirror settings -->"
	mavenAliyunURL     = "https://maven.aliyun.com/repository/public"
	gradleManagedFlag  = "// netroamer: managed mirror"
	gradleAliyunURL    = "https://maven.aliyun.com/repository/public"
)

// switchYarn 同时写经典 .yarnrc 与 berry .yarnrc.yml（存在才写）。
func switchYarn(home, target string) error {
	mirror := target == "mirror"
	u := pick(target, yarnMirrorURL, yarnOfficialURL)
	yarnrc := filepath.Join(home, ".yarnrc")
	if _, err := os.Stat(yarnrc); err == nil || mirror {
		// 镜像时确保存在；官方且文件不存在则跳过（无事可做）
		line := `registry "` + u + `"`
		if err := replaceYarnLine(yarnrc, `registry`, line); err != nil {
			return err
		}
	}
	berry := filepath.Join(home, ".yarnrc.yml")
	if _, err := os.Stat(berry); err == nil {
		return replaceYarnLine(berry, "npmRegistryServer:", "npmRegistryServer: "+u)
	}
	return nil
}

// replaceYarnLine 替换 `key "value"` / `key: value` 行（存在替换、缺失追加）。
func replaceYarnLine(path, key, line string) error {
	var lines []string
	if data, err := os.ReadFile(path); err == nil {
		lines = strings.Split(string(data), "\n")
	}
	found := false
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), key) {
			lines[i] = line
			found = true
		}
	}
	if !found {
		lines = append(lines, line)
	}
	return os.WriteFile(path, []byte(strings.TrimRight(strings.Join(lines, "\n"), "\n")+"\n"), 0o600)
}

// switchCargo 管理 config.toml 的 crates-io 换源标记块。
func switchCargo(home, target string) error {
	p := filepath.Join(home, ".cargo", "config.toml")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	if target == "mirror" {
		block := beginEnd(cargoBlockBegin, cargoBlockEnd, []string{
			"[source.crates-io]",
			`replace-with = "rsproxy"`,
			"[source.rsproxy]",
			`registry = "https://rsproxy.cn/crates.io-index"`,
		})
		data, _ := os.ReadFile(p)
		if strings.Contains(string(data), cargoBlockBegin) {
			return nil // 幂等
		}
		body := string(data)
		body = strings.TrimRight(body, "\n")
		if body != "" {
			body += "\n\n"
		}
		return os.WriteFile(p, []byte(body+block+"\n"), 0o600)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	var kept []string
	inBlock := false
	for _, l := range lines {
		t := strings.TrimSpace(l)
		switch {
		case t == cargoBlockBegin:
			inBlock = true
		case t == cargoBlockEnd:
			inBlock = false
		case !inBlock:
			kept = append(kept, l)
		}
	}
	return os.WriteFile(p, []byte(strings.Join(kept, "\n")), 0o600)
}

const (
	cargoBlockBegin = "# netroamer: cargo mirror switch (managed block) [begin]"
	cargoBlockEnd   = "# netroamer: cargo mirror switch (managed block) [end]"
	hfBlockBegin    = "# netroamer: huggingface mirror switch (managed block) [begin]"
	hfBlockEnd      = "# netroamer: huggingface mirror switch (managed block) [end]"
)

func beginEnd(begin, end string, body []string) string {
	out := append([]string{begin}, body...)
	return strings.Join(append(out, end), "\n")
}

// switchHF 管理 rc 的 HF_ENDPOINT 标记块。
func switchHF(home, target string) error {
	rc := filepath.Join(home, ".zshrc")
	if target == "mirror" {
		return rcBlockAdd(rc, hfBlockBegin, hfBlockEnd, []string{
			`export HF_ENDPOINT=` + hfMirrorURL,
		})
	}
	return rcBlockRemove(rc, hfBlockBegin, hfBlockEnd)
}

const mavenSettings = `<?xml version="1.0" encoding="UTF-8"?>
` + mavenManagedMarker + `
<settings xmlns="http://maven.apache.org/SETTINGS/1.0.0">
  <mirrors>
    <mirror>
      <id>aliyunmaven</id>
      <mirrorOf>*</mirrorOf>
      <name>阿里云公共仓库</name>
      <url>` + mavenAliyunURL + `</url>
    </mirror>
  </mirrors>
</settings>
`

// switchMaven 仅在 settings.xml 不存在（或由本工具生成）时切换；
// 用户自管文件一律拒绝（改由 custom 状态提示）。
func switchMaven(home, target string) error {
	p := filepath.Join(home, ".m2", "settings.xml")
	data, err := os.ReadFile(p)
	switch {
	case err == nil && strings.Contains(string(data), mavenManagedMarker):
		if target == "official" {
			return os.Remove(p)
		}
		return nil // 已是镜像
	case err == nil:
		return fmt.Errorf("settings.xml 为用户自管内容，不自动切换（请手动处理）")
	case target == "mirror":
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return err
		}
		return os.WriteFile(p, []byte(mavenSettings), 0o600)
	default:
		return nil // 官方且文件不存在，无事可做
	}
}

const gradleInit = `// netroamer: managed mirror
allprojects {
    repositories {
        maven { url '` + gradleAliyunURL + `' }
    }
}
`

// switchGradle 仅管理本工具生成的 init.gradle。
func switchGradle(home, target string) error {
	p := filepath.Join(home, ".gradle", "init.gradle")
	data, err := os.ReadFile(p)
	switch {
	case err == nil && strings.Contains(string(data), gradleManagedFlag):
		if target == "official" {
			return os.Remove(p)
		}
		return nil
	case err == nil:
		return fmt.Errorf("init.gradle 为用户自管内容，不自动切换（请手动处理）")
	case target == "mirror":
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return err
		}
		return os.WriteFile(p, []byte(gradleInit), 0o600)
	default:
		return nil
	}
}
