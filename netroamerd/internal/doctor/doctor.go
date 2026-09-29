// Package doctor 实现环境自检（research/05 §5 W2）：secret、API 可达、
// external-controller 暴露面、NO_PROXY 完整性、数据权限、常驻安装状态。
// 每种异常给出可执行修复指令；secret 值永不回显。
package doctor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Partiverse/netroamer/netroamerd/internal/config"
	"github.com/Partiverse/netroamer/netroamerd/internal/service"
)

type Level int

const (
	OK Level = iota
	WARN
	FAIL
)

type Finding struct {
	Level  Level
	Name   string
	Detail string
	Fix    string
}

// 与 bootstrap.sh 写入的 NO_PROXY 保持一致（05 文档 §6 doctor 收编项）。
var expectedSegments = []string{
	"127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
	"169.254.0.0/16", "100.64.0.0/10", "198.18.0.0/15",
}
var expectedDomains = []string{
	"localhost", "::1", ".local", ".cn",
	"npmmirror.com", "hf-mirror.com", "bigmodel.cn", "vectide.cn", "zhipuai.cn", "z.ai",
}

// Run 执行全部检查，输出结果并返回进程退出码（0 通过 / 1 有告警 / 2 有错误）。
func Run(ctx context.Context, out io.Writer) int {
	fmt.Fprintln(out, "=== netroamerd doctor ===")
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(out, "[✗] 配置加载失败: %v\n", err)
		return 2
	}

	var findings []Finding
	findings = append(findings, checkSecret(cfg))

	if ver, err := probeAPI(ctx, cfg); err != nil {
		findings = append(findings, Finding{FAIL, "mihomo API 可达", err.Error(),
			"确认 Clash Verge / mihomo 正在运行；或检查 external-controller 配置后重启内核"})
	} else {
		findings = append(findings, Finding{OK, "mihomo API 可达", "version=" + ver, ""})
	}

	home, _ := os.UserHomeDir()
	findings = append(findings, checkExposure(home)...)
	findings = append(findings, checkNoProxy(home)...)
	findings = append(findings, checkDBPerms(cfg.DBPath)...)
	if st, err := service.Status(); err != nil {
		findings = append(findings, Finding{WARN, "常驻服务状态", err.Error(), ""})
	} else if strings.Contains(st, "未安装") {
		findings = append(findings, Finding{WARN, "常驻服务", "未安装（当前无开机自启/崩溃拉起）", "运行 `netroamerd install` 安装用户级常驻服务"})
	} else {
		findings = append(findings, Finding{OK, "常驻服务", st + " [" + service.Label + "]", ""})
	}

	code := 0
	for _, f := range findings {
		switch f.Level {
		case OK:
			fmt.Fprintf(out, "[✓] %s: %s\n", f.Name, f.Detail)
		case WARN:
			code = max(code, 1)
			fmt.Fprintf(out, "[⚠] %s: %s\n", f.Name, f.Detail)
		case FAIL:
			code = 2
			fmt.Fprintf(out, "[✗] %s: %s\n", f.Name, f.Detail)
		}
		if f.Fix != "" {
			fmt.Fprintf(out, "    修复: %s\n", f.Fix)
		}
	}
	if code == 0 {
		fmt.Fprintln(out, "全部检查通过")
	}
	return code
}

func checkSecret(cfg config.Config) Finding {
	if cfg.Secret == "" {
		return Finding{WARN, "mihomo secret", "未发现 secret：netroamerd 无法连接已设防的 API（401）",
			`在 mihomo 配置加入 secret: "$(openssl rand -hex 16)" 并重启内核；` +
				"然后 `umask 077 && echo 同值 > ~/.config/netroamer/mihomo.secret`（值不回显）"}
	}
	return Finding{OK, "mihomo secret", "已发现（值不回显，来源见 netroamerd run 日志）", ""}
}

// probeAPI 探活 /version（TCP 强制环回或 unix socket，均带 Bearer）。
func probeAPI(ctx context.Context, cfg config.Config) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	client := service.NewHTTPClient(cfg.UnixSocket, 4*time.Second)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.BaseURL+"/version", nil)
	if err != nil {
		return "", err
	}
	if cfg.Secret != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.Secret)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("连接 %s 失败: %v", cfg.BaseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return "", fmt.Errorf("401：mihomo 已设 secret，但 netroamerd 未发现/不匹配")
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var v struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return "", fmt.Errorf("响应解析失败: %v", err)
	}
	return v.Version, nil
}

// checkExposure 扫描 mihomo/Verge 配置的 external-controller 暴露面
// （bootstrap.sh 5.5 节的 Go 版收编）。
func checkExposure(home string) []Finding {
	var findings []Finding
	for _, p := range config.MihomoConfigPaths(home) {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		text := string(data)
		ctl := config.YAMLScalar(text, "external-controller")
		if ctl == "" {
			continue
		}
		host, _, _ := net.SplitHostPort(ctl)
		if host != "localhost" && (net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback()) {
			findings = append(findings, Finding{FAIL, "external-controller 暴露面",
				fmt.Sprintf("%s 绑定 %s（%s）：非环回地址可被局域网直取配置", filepath.Base(p), ctl, p),
				"改为 127.0.0.1:端口（或仅 external-controller-unix）并设置 secret，重启内核"})
		}
		if config.YAMLScalar(text, "secret") == "" {
			findings = append(findings, Finding{FAIL, "external-controller secret",
				fmt.Sprintf("%s 已开启 API 但未设 secret：恶意网页可跨源调用本地 API 改配置", filepath.Base(p)),
				"配置中加 secret: <随机长串>（openssl rand -hex 16），重启内核"})
		}
	}
	return findings
}

// checkNoProxy 检查 rc 文件中 NO_PROXY 的内网段与关键域名完整性。
func checkNoProxy(home string) []Finding {
	val := lastRcNoProxy(home)
	if val == "" {
		return []Finding{{WARN, "NO_PROXY",
			"~/.zshrc 与 ~/.bashrc 均未发现 NO_PROXY 配置",
			"重新运行仓库根目录的 bootstrap.sh（会重建带完整域名与内网段的 rc 文件）"}}
	}
	missingSeg := missingIn(val, expectedSegments)
	missingDom := missingIn(val, expectedDomains)
	if len(missingSeg) == 0 && len(missingDom) == 0 {
		return []Finding{{OK, "NO_PROXY", "域名与 7 个内网段完整", ""}}
	}
	detail := ""
	if len(missingSeg) > 0 {
		detail += fmt.Sprintf("缺内网段 %s（云元数据 169.254.169.254、Tailscale 100.64/10 等会被送进代理出口，泄漏风险）；", strings.Join(missingSeg, " "))
	}
	if len(missingDom) > 0 {
		detail += fmt.Sprintf("缺域名 %s（仅绕行变慢）", strings.Join(missingDom, " "))
	}
	return []Finding{{WARN, "NO_PROXY", detail,
		"重新运行仓库根目录的 bootstrap.sh 重建 rc 文件（需新终端生效）"}}
}

func checkDBPerms(dbPath string) []Finding {
	var findings []Finding
	fi, err := os.Stat(dbPath)
	if err != nil {
		return nil // 库尚未创建（未跑过 run），不算问题
	}
	if fi.Mode().Perm() != 0o600 {
		findings = append(findings, Finding{WARN, "遥测库权限",
			fmt.Sprintf("%s 权限 %v，应为 600（本机遥测不应他读）", dbPath, fi.Mode().Perm()),
			"chmod 600 " + dbPath})
	}
	if dfi, err := os.Stat(filepath.Dir(dbPath)); err == nil && dfi.Mode().Perm() != 0o700 {
		findings = append(findings, Finding{WARN, "状态目录权限",
			fmt.Sprintf("%s 权限 %v，应为 700", filepath.Dir(dbPath), dfi.Mode().Perm()),
			"chmod 700 " + filepath.Dir(dbPath)})
	}
	return findings
}

// lastRcNoProxy 从 zshrc（优先）/ bashrc 取最后一条 NO_PROXY 赋值。
func lastRcNoProxy(home string) string {
	for _, rc := range []string{".zshrc", ".bashrc"} {
		data, err := os.ReadFile(filepath.Join(home, rc))
		if err != nil {
			continue
		}
		val := ""
		for _, line := range strings.Split(string(data), "\n") {
			if !strings.HasPrefix(strings.TrimLeft(line, " \t"), "NO_PROXY=") &&
				!strings.Contains(line, " NO_PROXY=") {
				continue
			}
			i := strings.Index(line, "NO_PROXY=")
			v := strings.TrimSpace(line[i+len("NO_PROXY="):])
			v = strings.Trim(v, `"'`)
			if v != "" {
				val = v
			}
		}
		if val != "" {
			return val
		}
	}
	return ""
}

func missingIn(val string, expected []string) []string {
	var missing []string
	for _, e := range expected {
		if !strings.Contains(val, e) {
			missing = append(missing, e)
		}
	}
	return missing
}
