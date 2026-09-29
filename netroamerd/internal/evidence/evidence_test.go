package evidence

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteAndParse(t *testing.T) {
	dir := t.TempDir()
	rec := Record{
		Time:   time.Date(2026, 9, 29, 15, 30, 5, 0, time.Local),
		Kind:   "slow_direct",
		Target: "slow.cn",
		Action: "DIRECT on",
		Reason: "ewma 1200ms",
		Before: map[string]float64{"ewma_ms": 1200, "p95_ms": 2000, "n": 52},
	}
	jp, err := Write(dir, rec)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(jp) != dir || !strings.HasSuffix(jp, ".json") {
		t.Fatalf("路径不符: %s", jp)
	}
	// JSON 可解析
	data, _ := os.ReadFile(jp)
	if !strings.Contains(string(data), `"target": "slow.cn"`) {
		t.Errorf("JSON 内容缺 target: %s", data)
	}
	// MD 可读且含对照表
	md := strings.Replace(jp, ".json", ".md", 1)
	mb, _ := os.ReadFile(md)
	for _, want := range []string{"慢域名自动直连", "修复前", "ewma_ms", "DIRECT on"} {
		if !strings.Contains(string(mb), want) {
			t.Errorf("Markdown 缺 %q", want)
		}
	}
	// 文件权限 0600
	fi, _ := os.Stat(jp)
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("权限 = %v", fi.Mode().Perm())
	}
}

func TestPruneKeepsNewest(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 9, 29, 12, 0, 0, 0, time.Local)
	for i := 0; i < 5; i++ {
		if _, err := Write(dir, Record{Time: base.Add(time.Duration(i) * time.Minute),
			Kind: "bad_node", Target: "g"}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond) // 确保文件名时间戳唯一（秒级）
	}
	removed, err := Prune(dir, 2)
	if err != nil || removed != 6 { // 3 组 × 2 文件
		t.Fatalf("removed = %d err = %v, want 6", removed, err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 4 { // 2 组 × 2 文件
		t.Fatalf("剩余 %d 个文件, want 4", len(entries))
	}
	// 剩下的应是最新的（13:04 / 13:05 两分钟）
	if _, err := os.Stat(filepath.Join(dir, "20260929-120400-bad_node-g.md")); err != nil {
		t.Errorf("最新证据应保留: %v", err)
	}
}
