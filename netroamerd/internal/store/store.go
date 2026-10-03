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
  reverted INTEGER DEFAULT 0,
  params_hash TEXT DEFAULT ''     -- 判定参数指纹（P2-10：算法迭代可归因）
);
-- 主动探测留档（复测/预验证；节点身份不落库——§2 红线）
CREATE TABLE IF NOT EXISTS probes (
  ts INTEGER NOT NULL,
  target TEXT NOT NULL,           -- 域名（retest/precheck）
  side TEXT NOT NULL,             -- 'direct' | 'proxy'
  purpose TEXT NOT NULL,          -- 'precheck' | 'retest'
  lat_ms INTEGER,
  ok INTEGER NOT NULL,
  fail_kind TEXT DEFAULT ''       -- '' | 'timeout' | 'other'
);
CREATE INDEX IF NOT EXISTS idx_probes_target ON probes(target, purpose, ts);
CREATE INDEX IF NOT EXISTS idx_judgments_target ON judgments(target, ts);
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
	if err := migrateJudgments(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate judgments: %w", err)
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

// Stats 样本总量与时间跨度（status 展示用）。
func (s *Store) Stats(ctx context.Context) (total, first, last int64, err error) {
	err = s.db.QueryRowContext(ctx,
		`SELECT count(*), coalesce(min(ts),0), coalesce(max(ts),0) FROM samples`).
		Scan(&total, &first, &last)
	return
}

// RawSample 是 LoadWindow 返回的原始样本（analyzer 聚合输入）。
type RawSample struct {
	Ts     int64
	Host   string
	Bucket string
	Via    string
	LatMs  *int64
	OK     bool
}

// LoadWindow 取 since 之后的原始样本（按时间序）。
func (s *Store) LoadWindow(ctx context.Context, since time.Time) ([]RawSample, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT ts, host_sld, bucket, via, lat_ms, ok FROM samples WHERE ts >= ? ORDER BY ts`,
		since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RawSample
	for rows.Next() {
		var m RawSample
		var lat sql.NullInt64
		var ok int
		if err := rows.Scan(&m.Ts, &m.Host, &m.Bucket, &m.Via, &lat, &ok); err != nil {
			return nil, err
		}
		if lat.Valid {
			v := lat.Int64
			m.LatMs = &v
		}
		m.OK = ok == 1
		out = append(out, m)
	}
	return out, rows.Err()
}

// AggKey 是聚合主键（research/05 §3 agg 表）。
type AggKey struct{ Bucket, Host, Via string }

// AggRow 是 agg 表一行：某（时段桶 × 主域 × 路径）的滚动统计。
type AggRow struct {
	Bucket    string
	Host      string
	Via       string
	EwmaMs    float64
	P95Ms     float64
	FailRate  float64
	N         int
	UpdatedAt int64
}

func (s *Store) UpsertAgg(ctx context.Context, rows []AggRow) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO agg
		(bucket, host_sld, via, ewma_ms, p95_ms, fail_rate, n, updated_at)
		VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT(bucket, host_sld, via) DO UPDATE SET
		ewma_ms=excluded.ewma_ms, p95_ms=excluded.p95_ms, fail_rate=excluded.fail_rate,
		n=excluded.n, updated_at=excluded.updated_at`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, r := range rows {
		if _, err := stmt.ExecContext(ctx, r.Bucket, r.Host, r.Via,
			r.EwmaMs, r.P95Ms, r.FailRate, r.N, r.UpdatedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RecentAgg 按样本量降序取聚合行（status 展示）。
func (s *Store) RecentAgg(ctx context.Context, limit int) ([]AggRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT bucket, host_sld, via, ewma_ms, p95_ms, fail_rate, n, updated_at
		FROM agg ORDER BY n DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AggRow
	for rows.Next() {
		var r AggRow
		if err := rows.Scan(&r.Bucket, &r.Host, &r.Via, &r.EwmaMs, &r.P95Ms, &r.FailRate, &r.N, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// LastAggRun 最近一次聚合时间（0 = 从未聚合）。
func (s *Store) LastAggRun(ctx context.Context) (int64, error) {
	var ts int64
	err := s.db.QueryRowContext(ctx, `SELECT coalesce(max(updated_at),0) FROM agg`).Scan(&ts)
	return ts, err
}

// AllAgg 全量聚合行（judge 输入；agg 表行数有限，无需分页）。
func (s *Store) AllAgg(ctx context.Context) ([]AggRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT bucket, host_sld, via, ewma_ms, p95_ms, fail_rate, n, updated_at FROM agg`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AggRow
	for rows.Next() {
		var r AggRow
		if err := rows.Scan(&r.Bucket, &r.Host, &r.Via, &r.EwmaMs, &r.P95Ms, &r.FailRate, &r.N, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Judgment 是 judgments 表一行：一次自动判定/动作/回滚的留档。
type Judgment struct {
	TS         int64
	Kind       string
	Target     string
	Action     string
	Reason     string
	Reverted   bool
	ParamsHash string
}

func (s *Store) InsertJudgment(ctx context.Context, j Judgment) error {
	reverted := 0
	if j.Reverted {
		reverted = 1
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO judgments(ts, kind, target, action, reason, reverted, params_hash) VALUES (?,?,?,?,?,?,?)`,
		j.TS, j.Kind, j.Target, j.Action, j.Reason, reverted, j.ParamsHash)
	return err
}

// CountActions 窗口内的动作次数（配额判定；回滚不计入——P0-3 计数语义）。
func (s *Store) CountActions(ctx context.Context, kind, target string, since time.Time) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM judgments WHERE kind = ? AND target = ? AND ts >= ? AND action != 'revert'`,
		kind, target, since.Unix()).Scan(&n)
	return n, err
}

// LastRevert 目标最近一次回滚时间（0 = 无；冷却判定用）。
func (s *Store) LastRevert(ctx context.Context, target string) (int64, error) {
	var ts int64
	err := s.db.QueryRowContext(ctx,
		`SELECT coalesce(max(ts),0) FROM judgments WHERE target = ? AND action = 'revert'`,
		target).Scan(&ts)
	return ts, err
}

// RecentJudgments 最近 n 条判定/动作留档（时间倒序，控制台时间线）。
func (s *Store) RecentJudgments(ctx context.Context, n int) ([]Judgment, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT ts, kind, target, action, reason, reverted, coalesce(params_hash,'')
		 FROM judgments ORDER BY ts DESC LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Judgment
	for rows.Next() {
		var j Judgment
		var rev int
		if err := rows.Scan(&j.TS, &j.Kind, &j.Target, &j.Action, &j.Reason, &rev, &j.ParamsHash); err != nil {
			return nil, err
		}
		j.Reverted = rev == 1
		out = append(out, j)
	}
	return out, rows.Err()
}

// LastBadNodeSwitch 最近一次坏节点自动切换时刻（联动静默窗输入）。
func (s *Store) LastBadNodeSwitch(ctx context.Context) (int64, error) {
	var ts int64
	err := s.db.QueryRowContext(ctx,
		`SELECT coalesce(max(ts),0) FROM judgments WHERE kind = 'bad_node' AND action LIKE 'switch%'`).Scan(&ts)
	return ts, err
}

// RevertCount 目标累计回滚次数（终态判定：≥3 永久仅通知——P0-2 收敛）。
func (s *Store) RevertCount(ctx context.Context, target string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM judgments WHERE target = ? AND action = 'revert'`,
		target).Scan(&n)
	return n, err
}

// ActiveActions 未回滚且未满 24h 的直连动作（复测调度输入）。
func (s *Store) ActiveActions(ctx context.Context, now time.Time, window time.Duration) ([]Judgment, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT ts, kind, target, action, reason, reverted, coalesce(params_hash,'') FROM judgments
		 WHERE kind = ? AND action = ? AND reverted = 0 AND ts >= ?
		 ORDER BY ts`,
		"slow_direct", "DIRECT on", now.Add(-window).Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Judgment
	for rows.Next() {
		var j Judgment
		var rev int
		if err := rows.Scan(&j.TS, &j.Kind, &j.Target, &j.Action, &j.Reason, &rev, &j.ParamsHash); err != nil {
			return nil, err
		}
		j.Reverted = rev == 1
		out = append(out, j)
	}
	return out, rows.Err()
}

// ProbeRecord 是 probes 表一行：一次主动探测的留档。
type ProbeRecord struct {
	TS       int64
	Target   string
	Side     string // direct | proxy
	Purpose  string // precheck | retest
	LatMs    *int64
	OK       bool
	FailKind string
}

func (s *Store) InsertProbes(ctx context.Context, recs []ProbeRecord) error {
	if len(recs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO probes(ts, target, side, purpose, lat_ms, ok, fail_kind) VALUES (?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, r := range recs {
		var lat any
		if r.LatMs != nil {
			lat = *r.LatMs
		}
		ok := 0
		if r.OK {
			ok = 1
		}
		if _, err := stmt.ExecContext(ctx, r.TS, r.Target, r.Side, r.Purpose, lat, ok, r.FailKind); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ProbesSince 取某 purpose 下 since 之后的全部探测（延迟回填用）。
func (s *Store) ProbesSince(ctx context.Context, purpose string, since time.Time) ([]ProbeRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT ts, target, side, purpose, lat_ms, ok, coalesce(fail_kind,'') FROM probes
		 WHERE purpose = ? AND ts >= ? ORDER BY ts`,
		purpose, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProbeRecord
	for rows.Next() {
		var r ProbeRecord
		var lat sql.NullInt64
		var ok int
		if err := rows.Scan(&r.TS, &r.Target, &r.Side, &r.Purpose, &lat, &ok, &r.FailKind); err != nil {
			return nil, err
		}
		if lat.Valid {
			v := lat.Int64
			r.LatMs = &v
		}
		r.OK = ok == 1
		out = append(out, r)
	}
	return out, rows.Err()
}

// ProbeSeries 取目标的探测序列（回滚滑动窗口判定输入）。
func (s *Store) ProbeSeries(ctx context.Context, target, purpose string, since time.Time) ([]ProbeRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT ts, target, side, purpose, lat_ms, ok, coalesce(fail_kind,'') FROM probes
		 WHERE target = ? AND purpose = ? AND ts >= ? ORDER BY ts`,
		target, purpose, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProbeRecord
	for rows.Next() {
		var r ProbeRecord
		var lat sql.NullInt64
		var ok int
		if err := rows.Scan(&r.TS, &r.Target, &r.Side, &r.Purpose, &lat, &ok, &r.FailKind); err != nil {
			return nil, err
		}
		if lat.Valid {
			v := lat.Int64
			r.LatMs = &v
		}
		r.OK = ok == 1
		out = append(out, r)
	}
	return out, rows.Err()
}

// migrateJudgments 幂等迁移：老库补 judgments.params_hash 列（P2-10）。
// 表/列名是编译期常量，直接内联在语句里，无任何拼接。
func migrateJudgments(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(judgments)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, pk int
		var name, typ string
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return err
		}
		if name == "params_hash" {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = db.Exec(`ALTER TABLE judgments ADD COLUMN params_hash TEXT DEFAULT ''`)
	return err
}

func (s *Store) Close() error { return s.db.Close() }
