package doctor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Partiverse/netroamer/netroamerd/internal/config"
)

func writeVerge(t *testing.T, home, content string) {
	t.Helper()
	dir := filepath.Join(home, "Library", "Application Support", "io.github.clash-verge-rev.clash-verge-rev")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// 验收场景 1（research/05 §5 W2）：未配置 secret → 给出可执行修复指令。
func TestCheckSecretMissing(t *testing.T) {
	f := checkSecret(config.Config{})
	if f.Level != WARN || !strings.Contains(f.Fix, "openssl rand -hex 16") {
		t.Fatalf("缺 secret 应 WARN 且修复指令可执行: %+v", f)
	}
	if ok := checkSecret(config.Config{Secret: "x"}); ok.Level != OK {
		t.Fatalf("有 secret 应 OK: %+v", ok)
	}
}

// 验收场景 2：external-controller 绑 0.0.0.0 → FAIL + 修复指令。
func TestCheckExposureNonLoopback(t *testing.T) {
	home := t.TempDir()
	writeVerge(t, home, "external-controller: 0.0.0.0:9090\nsecret: s3cret\n")
	fs := checkExposure(home)
	if len(fs) == 0 {
		t.Fatal("0.0.0.0 绑定应产生发现")
	}
	binding := fs[0]
	if binding.Level != FAIL || !strings.Contains(binding.Detail, "0.0.0.0:9090") {
		t.Fatalf("非环回绑定应 FAIL 并含绑定值: %+v", binding)
	}

	// 环回 + 有 secret：无发现
	home2 := t.TempDir()
	writeVerge(t, home2, "external-controller: 127.0.0.1:9097\nsecret: s3cret\n")
	if fs := checkExposure(home2); len(fs) != 0 {
		t.Fatalf("环回+secret 不应有发现: %+v", fs)
	}

	// 开了 API 但没 secret：FAIL
	home3 := t.TempDir()
	writeVerge(t, home3, "external-controller: 127.0.0.1:9090\n")
	if fs := checkExposure(home3); len(fs) != 1 || !strings.Contains(fs[0].Fix, "secret") {
		t.Fatalf("缺 secret 应 FAIL: %+v", fs)
	}
}

// 验收场景 3：NO_PROXY 缺段 → WARN + 区分泄漏风险/绕行变慢 + bootstrap 修复。
func TestCheckNoProxy(t *testing.T) {
	home := t.TempDir() // 无 rc 文件
	fs := checkNoProxy(home)
	if len(fs) != 1 || fs[0].Level != WARN || !strings.Contains(fs[0].Fix, "bootstrap.sh") {
		t.Fatalf("无 rc 应 WARN 且指向 bootstrap.sh: %+v", fs)
	}

	// 部分缺失：少 169.254/16 与 100.64/10
	home2 := t.TempDir()
	rc := "export NO_PROXY=\"localhost,127.0.0.0/8,10.0.0.0/8,192.168.0.0/16,.cn\"\n"
	if err := os.WriteFile(filepath.Join(home2, ".zshrc"), []byte(rc), 0o600); err != nil {
		t.Fatal(err)
	}
	fs = checkNoProxy(home2)
	if len(fs) != 1 || fs[0].Level != WARN {
		t.Fatalf("缺段应 WARN: %+v", fs)
	}
	if !strings.Contains(fs[0].Detail, "169.254.0.0/16") || !strings.Contains(fs[0].Detail, "100.64.0.0/10") {
		t.Fatalf("应点名缺失段: %s", fs[0].Detail)
	}
	if !strings.Contains(fs[0].Detail, "泄漏风险") {
		t.Fatalf("内网段缺失应标注泄漏风险: %s", fs[0].Detail)
	}

	// 完整：OK
	home3 := t.TempDir()
	full := "export NO_PROXY=\"localhost,127.0.0.0/8,::1,.local,.lan,.internal,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,169.254.0.0/16,100.64.0.0/10,198.18.0.0/15,.cn,npmmirror.com,hf-mirror.com,bigmodel.cn,vectide.cn,zhipuai.cn,z.ai\"\n"
	if err := os.WriteFile(filepath.Join(home3, ".zshrc"), []byte(full), 0o600); err != nil {
		t.Fatal(err)
	}
	if fs := checkNoProxy(home3); fs[0].Level != OK {
		t.Fatalf("完整 NO_PROXY 应 OK: %+v", fs)
	}
}

func TestProbeAPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/version" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer v" {
			http.Error(w, "forbidden", http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"version":"v1.19.12"}`))
	}))
	defer srv.Close()

	ver, err := probeAPI(context.Background(), config.Config{BaseURL: srv.URL, Secret: "v"})
	if err != nil || ver != "v1.19.12" {
		t.Fatalf("probeAPI = %q, %v", ver, err)
	}

	_, err = probeAPI(context.Background(), config.Config{BaseURL: srv.URL, Secret: "wrong"})
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("secret 错误应报 401: %v", err)
	}
}

func TestCheckDBPerms(t *testing.T) {
	home := t.TempDir()
	db := filepath.Join(home, "telemetry.db")
	if fs := checkDBPerms(db); len(fs) != 0 {
		t.Fatalf("库不存在不应有发现: %+v", fs)
	}
	if err := os.WriteFile(db, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	fs := checkDBPerms(db)
	if len(fs) != 2 { // 库 644 + 目录非 700
		t.Fatalf("权限过宽应有 2 条发现: %+v", fs)
	}
}
