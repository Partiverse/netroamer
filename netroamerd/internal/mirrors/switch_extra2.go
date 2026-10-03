// 开发场景镜像切换第二批：Flutter / Node·Electron 二进制 / Composer / gems / conda。
// Flutter 与 Node 二进制走 rc 标记块（env 生效面）；
// Composer/gems/conda 用户自管文件存在时拒绝自动切换（custom 态提示）。
package mirrors

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	flutterBlockBegin = "# netroamer: flutter mirror switch (managed block) [begin]"
	flutterBlockEnd   = "# netroamer: flutter mirror switch (managed block) [end]"
	nodeBinBlockBegin = "# netroamer: node-bin mirror switch (managed block) [begin]"
	nodeBinBlockEnd   = "# netroamer: node-bin mirror switch (managed block) [end]"

	flutterPubURL     = "https://pub.flutter-io.cn"
	flutterStorageURL = "https://storage.flutter-io.cn"
	electronMirrorURL = "https://npmmirror.com/mirrors/electron/"
	nodeMirrorURL     = "https://npmmirror.com/mirrors/node/"
	nvmMirrorURL      = "https://npmmirror.com/mirrors/node/"

	composerManagedMarker = "// netroamer: managed mirror"
	composerAliyunURL     = "https://mirrors.aliyun.com/composer"
	gemsManagedFlag       = "# netroamer: managed mirror"
	gemsMirrorURL         = "https://mirrors.tuna.tsinghua.edu.cn/rubygems/"
	condaManagedMarker    = "# netroamer: managed mirror"
)

func switchFlutter(home, target string) error {
	rc := filepath.Join(home, ".zshrc")
	if target == "mirror" {
		return rcBlockAdd(rc, flutterBlockBegin, flutterBlockEnd, []string{
			`export PUB_HOSTED_URL="https://pub.flutter-io.cn"`,
			`export FLUTTER_STORAGE_BASE_URL="https://storage.flutter-io.cn"`,
		})
	}
	return rcBlockRemove(rc, flutterBlockBegin, flutterBlockEnd)
}

func switchNodeBin(home, target string) error {
	rc := filepath.Join(home, ".zshrc")
	if target == "mirror" {
		return rcBlockAdd(rc, nodeBinBlockBegin, nodeBinBlockEnd, []string{
			`export ELECTRON_MIRROR="https://npmmirror.com/mirrors/electron/"`,
			`export NODEJS_ORG_MIRROR="https://npmmirror.com/mirrors/node/"`,
			`export NVM_NODEJS_ORG_MIRROR="https://npmmirror.com/mirrors/node/"`,
		})
	}
	return rcBlockRemove(rc, nodeBinBlockBegin, nodeBinBlockEnd)
}

const composerTemplate = `{
` + composerManagedMarker + `
  "repositories": [
    { "type": "composer", "url": "` + composerAliyunURL + `" }
  ]
}
`

// switchComposer 仅管理本工具生成的 config.json；用户自管内容拒绝。
func switchComposer(home, target string) error {
	p := filepath.Join(home, ".composer", "config.json")
	data, err := os.ReadFile(p)
	switch {
	case err == nil && strings.Contains(string(data), composerManagedMarker):
		if target == "official" {
			return os.Remove(p)
		}
		return nil
	case err == nil:
		return fmt.Errorf("config.json 为用户自管内容，不自动切换（请手动处理）")
	case target == "mirror":
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return err
		}
		return os.WriteFile(p, []byte(composerTemplate), 0o600)
	default:
		return nil
	}
}

const gemsTemplate = `# ` + gemsManagedFlag + `
---
:sources:
- ` + gemsMirrorURL + `
`

// switchGems 仅管理本工具生成的 .gemrc。
func switchGems(home, target string) error {
	p := filepath.Join(home, ".gemrc")
	data, err := os.ReadFile(p)
	switch {
	case err == nil && strings.Contains(string(data), gemsManagedFlag):
		if target == "official" {
			return os.Remove(p)
		}
		return nil
	case err == nil:
		return fmt.Errorf(".gemrc 为用户自管内容，不自动切换（请手动处理）")
	case target == "mirror":
		return os.WriteFile(p, []byte(gemsTemplate), 0o600)
	default:
		return nil
	}
}

const condaTemplate = `# ` + condaManagedMarker + `
channels:
  - https://mirrors.tuna.tsinghua.edu.cn/anaconda/pkgs/main/
  - https://mirrors.tuna.tsinghua.edu.cn/anaconda/pkgs/free/
  - defaults
show_channel_urls: true
`

// switchConda 仅管理本工具生成的 .condarc。
func switchConda(home, target string) error {
	p := filepath.Join(home, ".condarc")
	data, err := os.ReadFile(p)
	switch {
	case err == nil && strings.Contains(string(data), condaManagedMarker):
		if target == "official" {
			return os.Remove(p)
		}
		return nil
	case err == nil:
		return fmt.Errorf(".condarc 为用户自管内容，不自动切换（请手动处理）")
	case target == "mirror":
		return os.WriteFile(p, []byte(condaTemplate), 0o600)
	default:
		return nil
	}
}
