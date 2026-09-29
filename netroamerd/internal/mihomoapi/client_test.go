package mihomoapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Partiverse/netroamer/netroamerd/internal/config"
)

func TestVersionAndAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer v" {
			http.Error(w, "forbidden", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/version":
			w.Write([]byte(`{"version":"v1.19.31"}`))
		case "/rules":
			w.Write([]byte(`{"rules":[{"type":"DOMAIN-SUFFIX","payload":"google.com","proxy":"PROXY"},{"type":"DOMAIN-SUFFIX","payload":"baidu.com","proxy":"DIRECT"},{"type":"DOMAIN-KEYWORD","payload":"facebook","proxy":"PROXY"}]}`))
		case "/proxies/DIRECT/delay":
			if r.URL.Query().Get("url") == "" {
				t.Error("缺 url 参数")
			}
			w.Write([]byte(`{"delay":42}`))
		case "/proxies/HK-01/delay":
			http.Error(w, `{"message":"An error occurred"}`, http.StatusGatewayTimeout)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := New(config.Config{BaseURL: srv.URL, Secret: "v"})
	ctx := context.Background()

	if v, err := c.Version(ctx); err != nil || v != "v1.19.31" {
		t.Fatalf("Version = %q, %v", v, err)
	}
	if _, err := New(config.Config{BaseURL: srv.URL, Secret: "wrong"}).Version(ctx); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("secret 错误应返回 ErrUnauthorized, got %v", err)
	}

	rules, err := c.Rules(ctx)
	if err != nil || len(rules) != 3 {
		t.Fatalf("Rules = %v, %v", rules, err)
	}

	d, err := c.DelayTest(ctx, "DIRECT", "https://x.com/generate_204", 3*time.Second)
	if err != nil || d != 42 {
		t.Fatalf("DelayTest DIRECT = %d, %v", d, err)
	}
	if _, err := c.DelayTest(ctx, "HK-01", "https://x.com/generate_204", 3*time.Second); err == nil {
		t.Fatal("节点探测失败应返回 error")
	}
}
