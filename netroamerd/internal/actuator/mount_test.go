package actuator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `profile:
  store-selected: true

rule-providers:
  icloud:
    type: http
# netroamer:mount-begin —— netroamerd 自有直连 provider
  netroamer-autodirect:
    type: file
    behavior: domain
    path: netroamer/autodirect.yaml
  # netroamer:mount-end

prepend:
  # netroamer:mount-begin —— netroamerd 挂载行
  - RULE-SET,netroamer-autodirect,DIRECT
  # netroamer:mount-end
`

func TestCleanupMount(t *testing.T) {
	p := filepath.Join(t.TempDir(), "merge.yaml")
	if err := os.WriteFile(p, []byte(sample), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := CleanupMount(p)
	if err != nil || !changed {
		t.Fatalf("changed = %v err = %v", changed, err)
	}
	out := strings.Join(strings.Split(read(t, p), "\n"), "\n")
	if strings.Contains(out, "netroamer:mount") || strings.Contains(out, "netroamer-autodirect") {
		t.Errorf("指纹段未清干净:\n%s", out)
	}
	// 用户自有内容保留
	for _, want := range []string{"store-selected: true", "icloud:", "type: http"} {
		if !strings.Contains(out, want) {
			t.Errorf("误删用户内容 %q:\n%s", want, out)
		}
	}
	// 幂等：再跑一次无变化
	if changed, err := CleanupMount(p); err != nil || changed {
		t.Fatalf("第二次应无变化: %v %v", changed, err)
	}
	// 不存在的文件视为已清理
	if changed, err := CleanupMount(filepath.Join(t.TempDir(), "nope.yaml")); err != nil || changed {
		t.Fatalf("缺失文件: %v %v", changed, err)
	}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
