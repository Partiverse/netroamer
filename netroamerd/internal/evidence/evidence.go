// Package evidence 每次自愈动作生成「修复前后对照」留档
//（research/05 §1.1 evidence：传播素材 + 回滚依据；北极星要求可展示）。
// JSON 供程序消费，Markdown 供人读；目录 0700 / 文件 0600，滚动保留。
package evidence

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Partiverse/netroamer/netroamerd/internal/privs"
)

type Record struct {
	Time   time.Time          `json:"time"`
	Kind   string             `json:"kind"`   // slow_direct | bad_node | rollback
	Target string             `json:"target"` // 域名 或 组名
	Action string             `json:"action"`
	Reason string             `json:"reason"`
	Before map[string]float64 `json:"before,omitempty"` // 动作前统计（ewma_ms/p95_ms/n 或 score）
	After  map[string]float64 `json:"after,omitempty"`  // 复测/回滚时补充
}

// Write 落盘一对文件：<ts>-<kind>-<target>.json / .md，返回 JSON 路径。
func Write(dir string, rec Record) (string, error) {
	if err := privs.StateDir(dir); err != nil {
		return "", err
	}
	if rec.Time.IsZero() {
		rec.Time = time.Now()
	}
	base := fmt.Sprintf("%s-%s-%s", rec.Time.Format("20060102-150405"), rec.Kind, sanitize(rec.Target))
	jsonPath := filepath.Join(dir, base+".json")

	j, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(jsonPath, append(j, '\n'), 0o600); err != nil {
		return "", err
	}
	privs.ChmodFile(jsonPath)

	md := markdown(rec)
	if err := os.WriteFile(filepath.Join(dir, base+".md"), []byte(md), 0o600); err != nil {
		return "", err
	}
	privs.ChmodFile(filepath.Join(dir, base+".md"))
	return jsonPath, nil
}

// Prune 只保留最新 keep 组证据（按文件名时间戳排序）。
func Prune(dir string, keep int) (removed int, err error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	stems := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		if i := strings.LastIndex(name, "."); i > 0 {
			stems[name[:i]] = true
		}
	}
	ordered := make([]string, 0, len(stems))
	for s := range stems {
		ordered = append(ordered, s)
	}
	sort.Strings(ordered) // 文件名前缀是时间戳，字典序即时间序
	if len(ordered) <= keep {
		return 0, nil
	}
	for _, s := range ordered[:len(ordered)-keep] {
		for _, ext := range []string{".json", ".md"} {
			if err := os.Remove(filepath.Join(dir, s+ext)); err == nil {
				removed++
			}
		}
	}
	return removed, nil
}

func sanitize(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '.':
			return r
		default:
			return '-'
		}
	}, s)
	if len(s) > 60 {
		s = s[:60]
	}
	return s
}

func markdown(r Record) string {
	var b strings.Builder
	kind := map[string]string{
		"slow_direct": "慢域名自动直连",
		"bad_node":    "坏节点切换",
		"rollback":    "回滚",
	}[r.Kind]
	fmt.Fprintf(&b, "# %s：%s\n\n", kind, r.Target)
	fmt.Fprintf(&b, "- 时间：%s\n- 动作：`%s`\n- 判定依据：%s\n\n", r.Time.Format("2006-01-02 15:04:05"), r.Action, r.Reason)
	writeStats := func(title string, m map[string]float64) {
		if len(m) == 0 {
			return
		}
		fmt.Fprintf(&b, "## %s\n\n| 指标 | 值 |\n|---|---|\n", title)
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "| %s | %.1f |\n", k, m[k])
		}
		b.WriteString("\n")
	}
	writeStats("修复前", r.Before)
	writeStats("修复后", r.After)
	b.WriteString("---\n\n由 netroamerd 自动生成（本机数据，未外传）。\n")
	return b.String()
}
