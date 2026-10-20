// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package store keeps measurements in SQLite, through the pure Go driver so
// the binary stays static. The database runs in WAL mode, migrations are
// embedded and applied in a transaction at start, raw samples are folded
// into one-minute and one-hour rollups, and each resolution is pruned after
// its retention period.
package store

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver

	"github.com/MarioUrenaGarcia/greenlens/internal/calibrate"
	"github.com/MarioUrenaGarcia/greenlens/internal/sampler"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Resolutions of node energy.
const (
	ResolutionRaw = "raw"
	Resolution1m  = "1m"
	Resolution1h  = "1h"
)

// Store is an open database.
type Store struct {
	db   *sql.DB
	path string

	mu       sync.Mutex
	channels map[string]int64
}

// Open opens or creates the database at path and applies pending
// migrations. A new file is created readable only by its owner.
func Open(ctx context.Context, p string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE, 0o600) //nolint:gosec // the path comes from the configuration
	if err != nil {
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	q := url.Values{}
	// auto_vacuum takes effect only before the database file is initialized,
	// which journal_mode(WAL) does, so it must come first.
	for _, pragma := range []string{"auto_vacuum(INCREMENTAL)", "busy_timeout(5000)", "journal_mode(WAL)", "foreign_keys(1)", "synchronous(NORMAL)", "temp_store(MEMORY)"} {
		q.Add("_pragma", pragma)
	}
	db, err := sql.Open("sqlite", "file:"+p+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	s := &Store{db: db, path: p, channels: map[string]int64{}}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Path returns the database file.
func (s *Store) Path() string { return s.path }

// DB exposes the connection pool, for backups.
func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		return err
	}
	entries, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		return err
	}
	for _, e := range entries {
		num, _, ok := strings.Cut(e.Name(), "_")
		v, err := strconv.Atoi(num)
		if !ok || err != nil {
			return fmt.Errorf("migration %s: name must start with its number", e.Name())
		}
		var done int
		if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations WHERE version = ?`, v).Scan(&done); err != nil {
			return err
		}
		if done > 0 {
			continue
		}
		body, err := fs.ReadFile(migrations, path.Join("migrations", e.Name()))
		if err != nil {
			return err
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %s: %w", e.Name(), err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`, v, time.Now().UnixMilli()); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migration %s: %w", e.Name(), err)
		}
	}
	return nil
}

// SchemaVersion returns the highest applied migration.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var v sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT max(version) FROM schema_migrations`).Scan(&v)
	return int(v.Int64), err
}

// EnsureChannels records the channels in the sources table and refreshes
// their last_seen time.
func (s *Store) EnsureChannels(ctx context.Context, chans []source.Channel, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ms := now.UnixMilli()
	for _, c := range chans {
		var id int64
		err := s.db.QueryRowContext(ctx, `
			INSERT INTO sources (name, kind, domain, quality, first_seen, last_seen) VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(name) DO UPDATE SET kind = excluded.kind, domain = excluded.domain,
				quality = excluded.quality, last_seen = excluded.last_seen
			RETURNING id`, c.ID, c.Source, string(c.Domain), string(c.Quality), ms, ms).Scan(&id)
		if err != nil {
			return fmt.Errorf("record channel %s: %w", c.ID, err)
		}
		s.channels[c.ID] = id
	}
	return nil
}

// InsertSample stores the channels of one sample.
func (s *Store) InsertSample(ctx context.Context, smp sampler.Sample) error {
	if len(smp.Channels) == 0 || smp.Interval <= 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT OR REPLACE INTO node_samples (ts_ms, interval_ms, source_id, energy_j, power_w) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer func() { _ = stmt.Close() }()
	for _, c := range smp.Channels {
		id, ok := s.channels[c.Channel]
		if !ok {
			_ = tx.Rollback()
			return fmt.Errorf("channel %s was not recorded", c.Channel)
		}
		if _, err := stmt.ExecContext(ctx, smp.Time.UnixMilli(), smp.Interval.Milliseconds(), id, c.EnergyJ, c.PowerW); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// Rollup folds complete minutes of raw samples into one-minute rollups and
// complete hours of those into one-hour rollups, from where the previous
// run stopped up to now.
func (s *Store) Rollup(ctx context.Context, now time.Time) error {
	steps := []struct {
		res    string
		size   time.Duration
		query  string
		source string
	}{
		{Resolution1m, time.Minute, `
			INSERT INTO node_rollups (bucket_start_ms, resolution, source_id, energy_j, samples)
			SELECT (ts_ms / 60000) * 60000, '1m', source_id, sum(energy_j), count(*)
			FROM node_samples WHERE ts_ms >= ? AND ts_ms < ? GROUP BY 1, source_id
			ON CONFLICT(bucket_start_ms, resolution, source_id) DO UPDATE SET
				energy_j = node_rollups.energy_j + excluded.energy_j, samples = node_rollups.samples + excluded.samples`, "raw"},
		{Resolution1h, time.Hour, `
			INSERT INTO node_rollups (bucket_start_ms, resolution, source_id, energy_j, samples)
			SELECT (bucket_start_ms / 3600000) * 3600000, '1h', source_id, sum(energy_j), sum(samples)
			FROM node_rollups WHERE resolution = '1m' AND bucket_start_ms >= ? AND bucket_start_ms < ? GROUP BY 1, source_id
			ON CONFLICT(bucket_start_ms, resolution, source_id) DO UPDATE SET
				energy_j = node_rollups.energy_j + excluded.energy_j, samples = node_rollups.samples + excluded.samples`, "1m"},
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, st := range steps {
		until := now.Truncate(st.size).UnixMilli()
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		var from int64
		err = tx.QueryRowContext(ctx, `SELECT done_until_ms FROM rollup_state WHERE resolution = ?`, st.res).Scan(&from)
		if errors.Is(err, sql.ErrNoRows) {
			from, err = s.firstData(ctx, tx, st.source)
		}
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		if from >= until {
			_ = tx.Rollback()
			continue
		}
		if _, err := tx.ExecContext(ctx, st.query, from, until); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("rollup %s: %w", st.res, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO rollup_state (resolution, done_until_ms) VALUES (?, ?)
			ON CONFLICT(resolution) DO UPDATE SET done_until_ms = excluded.done_until_ms`, st.res, until); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return s.rollupUsage(ctx, now)
}

func (s *Store) firstData(ctx context.Context, tx *sql.Tx, from string) (int64, error) {
	q := `SELECT coalesce(min(ts_ms), 9223372036854775807) FROM node_samples`
	if from == Resolution1m {
		q = `SELECT coalesce(min(bucket_start_ms), 9223372036854775807) FROM node_rollups WHERE resolution = '1m'`
	}
	var v int64
	err := tx.QueryRowContext(ctx, q).Scan(&v)
	return v, err
}

// Retention is how long each resolution is kept.
type Retention struct {
	Raw, Rollup1m, Rollup1h time.Duration
}

// Prune deletes data older than its retention, keeping anything not yet
// folded into the next resolution, and returns the number of rows removed.
func (s *Store) Prune(ctx context.Context, now time.Time, r Retention) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	done := func(res string) int64 {
		var v int64
		if err := s.db.QueryRowContext(ctx, `SELECT done_until_ms FROM rollup_state WHERE resolution = ?`, res).Scan(&v); err != nil {
			return 0
		}
		return v
	}
	var total int64
	for _, d := range []struct {
		query  string
		cutoff int64
	}{
		{`DELETE FROM node_samples WHERE ts_ms < ?`, min(now.Add(-r.Raw).UnixMilli(), done(Resolution1m))},
		{`DELETE FROM node_rollups WHERE resolution = '1m' AND bucket_start_ms < ?`, min(now.Add(-r.Rollup1m).UnixMilli(), done(Resolution1h))},
		{`DELETE FROM node_rollups WHERE resolution = '1h' AND bucket_start_ms < ?`, now.Add(-r.Rollup1h).UnixMilli()},
		{`DELETE FROM usage_intervals WHERE ts_ms < ?`, min(now.Add(-r.Raw).UnixMilli(), done(usage1m))},
		{`DELETE FROM usage_rollups WHERE resolution = '1m' AND bucket_start_ms < ?`, min(now.Add(-r.Rollup1m).UnixMilli(), done(usage1h))},
		{`DELETE FROM usage_rollups WHERE resolution = '1h' AND bucket_start_ms < ?`, now.Add(-r.Rollup1h).UnixMilli()},
	} {
		res, err := s.db.ExecContext(ctx, d.query, d.cutoff)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
	}
	return total, nil
}

// Vacuum returns free pages to the file system.
func (s *Store) Vacuum(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `PRAGMA incremental_vacuum`)
	return err
}

// QuickCheck verifies the integrity of the database file.
func (s *Store) QuickCheck(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `PRAGMA quick_check`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	var problems []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return err
		}
		if line != "ok" {
			problems = append(problems, line)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(problems) > 0 {
		return fmt.Errorf("database integrity: %s", strings.Join(problems, "; "))
	}
	return nil
}

// SaveCalibration stores a calibration.
func (s *Store) SaveCalibration(ctx context.Context, r calibrate.Result) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO calibrations (ts_ms, duration_s, results) VALUES (?, ?, ?)`,
		r.StartedAt.UnixMilli(), int64(r.Duration.Seconds()), string(data))
	return err
}

// LatestCalibration returns the most recent calibration, if any.
func (s *Store) LatestCalibration(ctx context.Context) (calibrate.Result, bool, error) {
	var data string
	err := s.db.QueryRowContext(ctx, `SELECT results FROM calibrations ORDER BY ts_ms DESC, id DESC LIMIT 1`).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return calibrate.Result{}, false, nil
	}
	if err != nil {
		return calibrate.Result{}, false, err
	}
	var r calibrate.Result
	if err := json.Unmarshal([]byte(data), &r); err != nil {
		return calibrate.Result{}, false, fmt.Errorf("stored calibration: %w", err)
	}
	return r, true, nil
}

// Audit records an action of an operator or administrator.
func (s *Store) Audit(ctx context.Context, actor, action, detail string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO audit_log (ts_ms, actor, action, detail) VALUES (?, ?, ?, ?)`,
		time.Now().UnixMilli(), actor, action, detail)
	return err
}

// ChannelEnergy is the energy of one channel in a period or bucket.
type ChannelEnergy struct {
	BucketStart time.Time     `json:"bucket_start"`
	Channel     string        `json:"channel"`
	Source      string        `json:"source"`
	Domain      source.Domain `json:"domain"`
	Quality     string        `json:"quality"`
	EnergyJ     float64       `json:"energy_j"`
	Samples     int64         `json:"samples"`
}

// NodeEnergy sums the energy of every channel in [from, to), grouped in
// buckets of the given size (zero for a single total). It reads the finest
// resolution still kept for from, and adds the raw samples not yet folded
// into that resolution.
func (s *Store) NodeEnergy(ctx context.Context, from, to time.Time, bucket time.Duration, now time.Time, r Retention) ([]ChannelEnergy, string, error) {
	res := Resolution1h
	switch {
	case !from.Before(now.Add(-r.Raw)):
		res = ResolutionRaw
	case !from.Before(now.Add(-r.Rollup1m)):
		res = Resolution1m
	}
	bucketMS := bucket.Milliseconds()
	if bucketMS <= 0 {
		bucketMS = to.UnixMilli() - from.UnixMilli() + 1
	}
	group := func(col string) string {
		return fmt.Sprintf("(%s - %d) / %d * %d + %d", col, from.UnixMilli(), bucketMS, bucketMS, from.UnixMilli())
	}
	var queries []string
	var args []any
	raw := `SELECT ` + group("ts_ms") + ` AS b, source_id, energy_j, 1 AS n FROM node_samples WHERE ts_ms >= ? AND ts_ms < ?`
	if res == ResolutionRaw {
		queries = append(queries, raw)
		args = append(args, from.UnixMilli(), to.UnixMilli())
	} else {
		var done int64
		if err := s.db.QueryRowContext(ctx, `SELECT coalesce((SELECT done_until_ms FROM rollup_state WHERE resolution = ?), 0)`, res).Scan(&done); err != nil {
			return nil, res, err
		}
		queries = append(queries, `SELECT `+group("bucket_start_ms")+` AS b, source_id, energy_j, samples AS n FROM node_rollups
			WHERE resolution = ? AND bucket_start_ms >= ? AND bucket_start_ms < ?`, raw)
		args = append(args, res, from.UnixMilli(), min(to.UnixMilli(), done), max(from.UnixMilli(), done), to.UnixMilli())
	}
	union := strings.Join(queries, " UNION ALL ")
	// The subqueries are constants of this function with only integers
	// formatted into them; every value goes through placeholders.
	//nolint:gosec // constant subqueries, see above
	q := `SELECT u.b, s.name, s.kind, s.domain, s.quality, sum(u.energy_j), sum(u.n) FROM (` + union +
		`) AS u JOIN sources s ON s.id = u.source_id GROUP BY u.b, s.id ORDER BY u.b, s.name`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, res, err
	}
	defer func() { _ = rows.Close() }()
	var out []ChannelEnergy
	for rows.Next() {
		var ce ChannelEnergy
		var b int64
		var domain string
		if err := rows.Scan(&b, &ce.Channel, &ce.Source, &domain, &ce.Quality, &ce.EnergyJ, &ce.Samples); err != nil {
			return nil, res, err
		}
		ce.Domain = source.Domain(domain)
		ce.BucketStart = time.UnixMilli(b).UTC()
		out = append(out, ce)
	}
	return out, res, rows.Err()
}

// Tables lists the tables of the database, for diagnostics.
func (s *Store) Tables(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table'`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	slices.Sort(out)
	return out, rows.Err()
}
