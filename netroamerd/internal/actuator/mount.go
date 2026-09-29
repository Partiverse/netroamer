// 挂载指纹清理（uninstall --full 用，research/05 §4.1 provider 挂载归属：
// 卸载按 netroamer:mount 指纹段删除，恢复配置原状）。
package actuator

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	fingerprintBegin = "# netroamer:mount-begin"
	fingerprintEnd   = "# netroamer:mount-end"
)

// MountFingerprintFiles 已知的挂载指纹所在文件（Verge 绑定增强 + 常见配置）。
func MountFingerprintFiles(home string) []string {
	var paths []string
	base := filepath.Join(home, "Library", "Application Support",
		"io.github.clash-verge-rev.clash-verge-rev")
	// Verge 增强文件（绑定的 merge / rules prepend 都在 profiles 下）
	if entries, err := os.ReadDir(filepath.Join(base, "profiles")); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				paths = append(paths, filepath.Join(base, "profiles", e.Name()))
			}
		}
	}
	paths = append(paths,
		filepath.Join(base, "config.yaml"),
		filepath.Join(base, "clash-verge.yaml"),
		filepath.Join(home, ".config", "mihomo", "config.yaml"),
		filepath.Join(home, ".config", "clash", "config.yaml"),
	)
	return paths
}

// CleanupMount 删除文件中 netroamer:mount 指纹段（含段首注释行到段尾）。
// 返回清理的文件数；无指纹则不动文件。YAML 注释删除后语法不变。
func CleanupMount(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, nil // 不存在视为已清理
	}
	lines := strings.Split(string(data), "\n")
	out := make([]string, 0, len(lines))
	inBlock := false
	changed := false
	for _, line := range lines {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, fingerprintBegin):
			inBlock = true
			changed = true
			continue
		case strings.HasPrefix(t, fingerprintEnd):
			inBlock = false
			continue
		case inBlock:
			continue
		}
		out = append(out, line)
	}
	if !changed {
		return false, nil
	}
	clean := strings.TrimLeft(strings.Join(out, "\n"), "\n")
	if clean != "" && !strings.HasSuffix(clean, "\n") {
		clean += "\n"
	}
	if err := os.WriteFile(path, []byte(clean), 0o644); err != nil {
		return false, err
	}
	return true, nil
}
