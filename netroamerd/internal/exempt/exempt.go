// Package exempt 实现慢域名自动直连的资格与豁免双闸（05 §4.1 修订版）：
// 资格闸（正向白名单）：.cn TLD / 内置境内域名 / 用户 allow 文件；
// 豁免闸（负向硬闸）：mihomo /rules 中任何 DOMAIN*→非 DIRECT 命中、用户 exempt 文件。
// 全部本地判定、零外部拉取（修复评审 P0-4 自举依赖）。
package exempt

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/Partiverse/netroamer/netroamerd/internal/mihomoapi"
)

// domestic 内置境内域名白名单（保守小集合，按需扩充）。
var domestic = []string{
	"baidu.com", "qq.com", "taobao.com", "tmall.com", "jd.com", "alipay.com",
	"163.com", "126.com", "bilibili.com", "zhihu.com", "douyin.com", "weibo.com",
	"aliyun.com", "tencent.com", "aliyuncs.com", "myqcloud.com",
	"qiniu.com", "upyun.com", "steamchina.com",
}

// Eligible 资格闸：host 是否具备自动直连候选资格。
func Eligible(host string, allowList []string) bool {
	h := normalize(host)
	if h == "" {
		return false
	}
	if h == "cn" || strings.HasSuffix(h, ".cn") {
		return true
	}
	for _, d := range domestic {
		if suffixMatch(h, d) {
			return true
		}
	}
	for _, d := range allowList {
		if suffixMatch(h, d) {
			return true
		}
	}
	return false
}

// Matcher 由 mihomo /rules 构建的本地规则豁免匹配器。
type Matcher struct {
	exacts   map[string]struct{}
	suffixes []string
	keywords []string
}

// BuildMatcher 收集所有「非 DIRECT 结论」的 DOMAIN* 规则
// （命中即永不自动直连——订阅分流认为该域名必须走代理）。
func BuildMatcher(rules []mihomoapi.Rule) Matcher {
	m := Matcher{exacts: map[string]struct{}{}}
	for _, r := range rules {
		if r.Proxy == "DIRECT" || r.Proxy == "" || r.Payload == "" {
			continue
		}
		p := normalize(r.Payload)
		switch r.Type {
		case "DOMAIN":
			m.exacts[p] = struct{}{}
		case "DOMAIN-SUFFIX":
			m.suffixes = append(m.suffixes, p)
		case "DOMAIN-KEYWORD":
			m.keywords = append(m.keywords, p)
		}
	}
	return m
}

// Blocked 豁免闸：host 是否被任何非 DIRECT 的 DOMAIN* 规则覆盖。
func (m Matcher) Blocked(host string) bool {
	h := normalize(host)
	if h == "" {
		return false
	}
	if _, ok := m.exacts[h]; ok {
		return true
	}
	for _, s := range m.suffixes {
		if suffixMatch(h, s) {
			return true
		}
	}
	for _, k := range m.keywords {
		if strings.Contains(h, k) {
			return true
		}
	}
	return false
}

// UserList 用户显式列表（allow / exempt 共用格式）：
// 每行一个域名后缀，兼容 YAML 列表风格「- domain」，# 与空行忽略。
func UserList(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if i := strings.Index(line, "#"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		line = strings.TrimPrefix(line, "- ")
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, strings.ToLower(line))
		}
	}
	return out
}

// SaveList 覆写用户列表文件（allow/exempt 管理用），0600。
func SaveList(path string, items []string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	body := "# netroamer 管理列表（每行一个域名后缀）\n"
	for _, it := range items {
		body += strings.ToLower(strings.TrimSpace(it)) + "\n"
	}
	return os.WriteFile(path, []byte(body), 0o600)
}

func normalize(host string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
}

func suffixMatch(host, suffix string) bool {
	return host == suffix || strings.HasSuffix(host, "."+suffix)
}
