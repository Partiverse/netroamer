package service

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPlistContent(t *testing.T) {
	bin := filepath.Join("/Users/x", ".local", "bin", "netroamerd")
	log := filepath.Join("/Users/x", ".local", "state", "netroamer", "netroamerd.log")
	p := plistContent(bin, log)
	for _, want := range []string{Label, bin, log, "RunAtLoad", "KeepAlive"} {
		if !strings.Contains(p, want) {
			t.Errorf("plist 缺少 %s", want)
		}
	}
	// 安全红线：用户级 agent 绝不设 root UserName（research/05 §1.3）
	if strings.Contains(p, "UserName") {
		t.Error("plist 不应包含 UserName")
	}
}

func TestUnitContent(t *testing.T) {
	u := unitContent("/home/x/.local/bin/netroamerd")
	for _, want := range []string{"ExecStart=/home/x/.local/bin/netroamerd run", "Restart=always", "WantedBy=default.target"} {
		if !strings.Contains(u, want) {
			t.Errorf("unit 缺少 %s", want)
		}
	}
}
