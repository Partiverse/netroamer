// Package service 管理 netroamerd 常驻安装：macOS 用户级 LaunchAgent /
// Linux systemd --user unit（research/05 §1.3）。
// 红线：绝不写 root UserName / 系统级目录；uninstall 保留数据与二进制。
package service

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Label 是 LaunchAgent / systemd unit 的标识（防误报说明见 README）。
const Label = "com.netroamer.agent"

// StateDir netroamerd 状态目录（db/日志）。
func StateDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "netroamer"), nil
}

func binDest() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "bin", "netroamerd"), nil
}

// Install 安装并启动：拷贝二进制 → 写服务定义 → 加载。
func Install(srcBin string) error {
	switch runtime.GOOS {
	case "darwin":
		return installDarwin(srcBin)
	case "linux":
		return installLinux(srcBin)
	default:
		return fmt.Errorf("%s 常驻安装在后续里程碑交付；当前可手动运行 netroamerd run", runtime.GOOS)
	}
}

// Uninstall 停止并移除服务定义；数据与二进制保留（全量回滚在 W6 的
// netroamerd uninstall 交付）。
func Uninstall() error {
	switch runtime.GOOS {
	case "darwin":
		return uninstallDarwin()
	case "linux":
		return uninstallLinux()
	default:
		return fmt.Errorf("%s 无已安装的常驻服务", runtime.GOOS)
	}
}

// Status 返回人类可读的运行状态。
func Status() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		return statusDarwin()
	case "linux":
		return statusLinux()
	default:
		return "未安装（" + runtime.GOOS + "）", nil
	}
}

// ---- macOS LaunchAgent ----

func installDarwin(src string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	stateDir, err := StateDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	dest, err := binDest()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if abs, _ := filepath.Abs(src); abs != dest {
		if err := copyFile(src, dest, 0o755); err != nil {
			return fmt.Errorf("安装二进制: %w", err)
		}
	}
	logPath := filepath.Join(stateDir, "netroamerd.log")
	plistPath := filepath.Join(home, "Library", "LaunchAgents", Label+".plist")
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		return err
	}
	// 用户级 agent：不设 UserName（绝不以 root 跑，research/05 §1.3）
	if err := os.WriteFile(plistPath, []byte(plistContent(dest, logPath)), 0o644); err != nil {
		return err
	}
	uid := fmt.Sprint(os.Getuid())
	_ = exec.Command("launchctl", "bootout", "gui/"+uid+"/"+Label).Run() // 已存在则先卸载
	if out, err := exec.Command("launchctl", "bootstrap", "gui/"+uid, plistPath).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl bootstrap: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func plistContent(bin, logPath string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>` + Label + `</string>
  <key>ProgramArguments</key>
  <array>
    <string>` + bin + `</string>
    <string>run</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ProcessType</key><string>Background</string>
  <key>Nice</key><integer>10</integer>
  <key>LowPriorityIO</key><true/>
  <key>StandardOutPath</key><string>` + logPath + `</string>
  <key>StandardErrorPath</key><string>` + logPath + `</string>
</dict>
</plist>
`
}

func uninstallDarwin() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	uid := fmt.Sprint(os.Getuid())
	_ = exec.Command("launchctl", "bootout", "gui/"+uid+"/"+Label).Run()
	return os.Remove(filepath.Join(home, "Library", "LaunchAgents", Label+".plist"))
}

func statusDarwin() (string, error) {
	uid := fmt.Sprint(os.Getuid())
	out, err := exec.Command("launchctl", "print", "gui/"+uid+"/"+Label).CombinedOutput()
	if err != nil {
		return "未安装/未加载", nil
	}
	for _, line := range strings.Split(string(out), "\n") {
		if i := strings.Index(line, "pid = "); i >= 0 {
			return "运行中 (" + strings.TrimSpace(line[i+6:]) + ")", nil
		}
	}
	return "已加载但未运行", nil
}

// ---- Linux systemd --user ----

func installLinux(src string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	stateDir, err := StateDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	dest, err := binDest()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if abs, _ := filepath.Abs(src); abs != dest {
		if err := copyFile(src, dest, 0o755); err != nil {
			return fmt.Errorf("安装二进制: %w", err)
		}
	}
	unitDir := filepath.Join(home, ".config", "systemd", "user")
	if err := os.MkdirAll(unitDir, 0o755); err != nil {
		return err
	}
	unitPath := filepath.Join(unitDir, Label+".service")
	if err := os.WriteFile(unitPath, []byte(unitContent(dest)), 0o644); err != nil {
		return err
	}
	for _, args := range [][]string{
		{"--user", "daemon-reload"},
		{"--user", "enable", "--now", Label + ".service"},
	} {
		if out, err := exec.Command("systemctl", args...).CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

func unitContent(bin string) string {
	return `[Unit]
Description=netroamerd — 本地无感自愈网络层遥测 agent
After=network-online.target

[Service]
ExecStart=` + bin + ` run
Restart=always
RestartSec=5

[Install]
WantedBy=default.target
`
}

func uninstallLinux() error {
	_ = exec.Command("systemctl", "--user", "disable", "--now", Label+".service").Run()
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	return os.Remove(filepath.Join(home, ".config", "systemd", "user", Label+".service"))
}

func statusLinux() (string, error) {
	out, err := exec.Command("systemctl", "--user", "is-active", Label+".service").Output()
	if err != nil {
		return "未安装/未激活", nil
	}
	return strings.TrimSpace(string(out)), nil
}

// ---- 公共 ----

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return os.Chmod(dst, mode)
}

// NewHTTPClient 返回走 unix socket（非空时）或普通 TCP 的 HTTP 客户端
// （doctor 探活复用）。
func NewHTTPClient(unixSocket string, timeout time.Duration) *http.Client {
	c := &http.Client{Timeout: timeout}
	if unixSocket == "" {
		return c
	}
	c.Transport = &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", unixSocket)
		},
	}
	return c
}
