// Package notify 系统通知（research/05 §4.2）：macOS osascript /
// Linux notify-send，尽力而为——通知失败不影响动作。
package notify

import (
	"os/exec"
	"runtime"
	"strings"
)

// Send 发系统通知。title 建议 "netroamerd"。
func Send(title, body string) error {
	title, body = sanitize(title), sanitize(body)
	switch runtime.GOOS {
	case "darwin":
		script := "display notification \"" + body + "\" with title \"" + title + "\""
		return exec.Command("osascript", "-e", script).Run()
	case "linux":
		return exec.Command("notify-send", title, body).Run()
	default:
		return nil // Windows 计划任务通知后续里程碑
	}
}

// sanitize 去掉会破坏引号包裹的字符。
func sanitize(s string) string {
	s = strings.ReplaceAll(s, "\"", "'")
	s = strings.ReplaceAll(s, "\\", "")
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
