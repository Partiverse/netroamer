package store

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestOpenPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX 权限模型")
	}
	path := filepath.Join(t.TempDir(), "sub", "telemetry.db") // sub 不存在，Open 应以 0700 创建
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	fi, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o700 {
		t.Errorf("目录权限 = %v, want 0700", fi.Mode().Perm())
	}
	fi, err = os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("库文件权限 = %v, want 0600", fi.Mode().Perm())
	}
}

func TestInsertCountPrune(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "telemetry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	lat := int64(42)
	now := time.Now()
	old := now.Add(-8 * 24 * time.Hour)
	err = s.InsertSamples(ctx, []Sample{
		{TS: now.Unix(), Host: "github.com", Proc: "curl", Bucket: "n0-32", Via: "direct", LatMs: &lat, OK: true},
		{TS: now.Unix(), Host: "example.com", Bucket: "n0-32", Via: "proxy", OK: true}, // proc 空、lat nil
		{TS: old.Unix(), Host: "stale.dev", Bucket: "n0-1", Via: "proxy", OK: true},
	})
	if err != nil {
		t.Fatal(err)
	}

	if n, err := s.Count(ctx); err != nil || n != 3 {
		t.Fatalf("count = %d err = %v, want 3", n, err)
	}

	deleted, err := s.Prune(ctx, now, 7*24*time.Hour)
	if err != nil || deleted != 1 {
		t.Fatalf("prune deleted = %d err = %v, want 1", deleted, err)
	}
	if n, _ := s.Count(ctx); n != 2 {
		t.Fatalf("prune 后 count = %d, want 2", n)
	}
}
