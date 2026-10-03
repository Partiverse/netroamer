// 运行模式（影子↔实操）的热切换：state 目录下 mode 文件存 "actuate" 或 "shadow"。
// 文件优先于启动旗标——控制台写文件，run 循环每个判定周期读取，免重启生效。
package config

import (
	"os"
	"path/filepath"
	"strings"
)

// ReadMode 读运行模式：mode 文件存在且合法 → 其值；否则 fallback（启动旗标）。
func ReadMode(stateDir string, fallback bool) bool {
	data, err := os.ReadFile(filepath.Join(stateDir, "mode"))
	if err != nil {
		return fallback
	}
	switch strings.TrimSpace(strings.ToLower(string(data))) {
	case "actuate":
		return true
	case "shadow":
		return false
	}
	return fallback
}

// WriteMode 写运行模式文件（0600）。
func WriteMode(stateDir string, actuate bool) error {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	v := "shadow"
	if actuate {
		v = "actuate"
	}
	return os.WriteFile(filepath.Join(stateDir, "mode"), []byte(v+"\n"), 0o600)
}

// ModeWritten 该目录是否已写过 mode 文件（区分「跟随启动旗标」与「显式设置」）。
func ModeWritten(stateDir string) bool {
	_, err := os.Stat(filepath.Join(stateDir, "mode"))
	return err == nil
}
