// Package privs 集中管理 netroamerd 状态目录/文件的权限约束：
// 目录 0700、文件 0600（research/05 §6「遥测 SQLite 700/600 权限」）。
package privs

import (
	"fmt"
	"os"
)

// StateDir 确保目录存在。新建时强制 0700；已存在的目录尊重现有权限
// （不 chmod 他人/系统目录，如 --db 指到 /tmp 的调试场景）。
func StateDir(dir string) error {
	if fi, err := os.Stat(dir); err == nil {
		if !fi.IsDir() {
			return fmt.Errorf("%s 已存在且不是目录", dir)
		}
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("chmod %s: %w", dir, err)
	}
	return nil
}

// ChmodFile 尽力而为地把文件权限收紧到 0600（SQLite 库文件与 WAL 伴生文件）。
func ChmodFile(path string) {
	_ = os.Chmod(path, 0o600)
}
