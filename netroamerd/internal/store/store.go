// Package store 管理 SQLite 遥测库（research/05 §3 数据模型）。
// P0 W1 交付 samples 表的写入与保留期清理；agg/judgments 结构先行建好
// （W2 analyzer / W3 判定使用）。文件为本机 only，无任何上云。
package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // 纯 Go SQLite，无 cgo

	"github.com/Partiverse/netroamer/netroamerd/internal/privs"
)

// Sample 是 samples 表一行：一条已结束连接的观测记录。
// 隐私边界（research/05 §2 红线4）：只存主域与进程名，
// 不存完整 URL、节点名称、订阅身份、原始时间线。
type Sample struct {
	TS     int64  // unix 秒（连接结束时刻）
	Host   string // 主域（eTLD+1）；IP 直连时为 IP
	Proc   string // 进程名（processPath 的 basename）
	Bucket string // 时段桶 ssid_hash × hour-of-week（W1 暂用 n0 哨兵）
	Via    string // direct | proxy
	LatMs  *int64 // 连接建立/首包延迟；nil = 未能测得（主动探测 W2+ 接入）
	OK     bool
}

const schema = `
CREATE TABLE IF NOT EXISTS samples (
  ts INTEGER NOT NULL,            -- unix 秒
  host_sld TEXT NOT NULL,         -- 主域（如 github.com），已截断
  proc TEXT,                      -- 进程名（processPath 的 basename）
  bucket TEXT NOT NULL,           -- ssid_hash × hour-of-week 时段桶
  via TEXT NOT NULL,              -- 'direct' | 'proxy'
  lat_ms INTEGER,                 -- 连接建立/首包延迟；NULL=未测得
  ok INTEGER NOT NULL             -- 0/1
);
CREATE INDEX IF NOT EXISTS idx_samples_key ON samples(bucket, host_sld, via, ts);

CREATE TABLE IF NOT EXISTS agg (
  bucket TEXT, host_sld TEXT, via TEXT,
  ewma_ms REAL, p95_ms REAL, fail_rate REAL, n INTEGER,
  updated_at INTEGER, PRIMARY KEY (bucket, host_sld, via)
);

CREATE TABLE IF NOT EXISTS judgments (
  ts INTEGER, kind TEXT,          -- 'slow_direct' | 'bad_node' | 'rollback'
  target TEXT,                    -- 域名 或 组名×节点索引
  action TEXT,                    -- 'DIRECT on' | 'switch to #k' | 'revert'
  reason TEXT,                    -- 人类可读判定依据（含关键数字）
  reverted INTEGER DEFAULT 0
);
`

// Store 打开 telemetry.db：WAL、busy_timeout、单连接（SQLite 单写者防锁表），
// 目录 0700 / 库文件与 WAL 伴生文件 0600。
type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	if err := privs.StateDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("schema: %w", err)
	}
	privs.ChmodFile(path)
	privs.ChmodFile(path + "-wal")
	privs.ChmodFile(path + "-shm")
	return &Store{db: db}, nil
}

// InsertSamples 单事务批量写入（每帧快照一批）。
func (s *Store) InsertSamples(ctx context.Context, samples []Sample) error {
	if len(samples) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO samples(ts, host_sld, proc, bucket, via, lat_ms, ok) VALUES (?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, m := range samples {
		var lat any
		if m.LatMs != nil {
			lat = *m.LatMs
		}
		ok := 0
		if m.OK {
			ok = 1
		}
		if _, err := stmt.ExecContext(ctx, m.TS, m.Host, m.Proc, m.Bucket, m.Via, lat, ok); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Prune 删除 keep 之前的原始样本（research/05 §3：保留 7 天），返回删除行数。
func (s *Store) Prune(ctx context.Context, now time.Time, keep time.Duration) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM samples WHERE ts < ?`, now.Add(-keep).Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Store) Count(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM samples`).Scan(&n)
	return n, err
}

func (s *Store) Close() error { return s.db.Close() }
