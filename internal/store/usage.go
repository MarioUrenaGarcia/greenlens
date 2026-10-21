// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/indicators"
	"github.com/MarioUrenaGarcia/greenlens/internal/usage"
)

// Watermarks of the usage rollups in rollup_state.
const (
	usage1m = "usage_1m"
	usage1h = "usage_1h"
)

// usageLag is how long after a minute ends its usage rows are folded.
// Rows are written once the counters of the backends have been read,
// which takes up to the connector timeout after the interval ends.
const usageLag = 30 * time.Second

// BackendInfo describes a backend for the backends table.
type BackendInfo struct {
	Name     string
	Kind     string
	Endpoint string
}

// EnsureBackends records the backends and refreshes their last_seen time.
func (s *Store) EnsureBackends(ctx context.Context, bs []BackendInfo, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range bs {
		if _, err := s.backendID(ctx, s.db, b, now); err != nil {
			return err
		}
	}
	return nil
}

type execQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (s *Store) backendID(ctx context.Context, db execQuerier, b BackendInfo, now time.Time) (int64, error) {
	var endpoint sql.NullString
	if b.Endpoint != "" {
		endpoint = sql.NullString{String: b.Endpoint, Valid: true}
	}
	ms := now.UnixMilli()
	var id int64
	err := db.QueryRowContext(ctx, `
		INSERT INTO backends (name, kind, endpoint, first_seen, last_seen) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET kind = excluded.kind, endpoint = excluded.endpoint, last_seen = excluded.last_seen
		RETURNING id`, b.Name, b.Kind, endpoint, ms, ms).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("record backend %s: %w", b.Name, err)
	}
	return id, nil
}

func modelID(ctx context.Context, db execQuerier, backend int64, name string, now time.Time) (sql.NullInt64, error) {
	if name == "" {
		return sql.NullInt64{}, nil
	}
	ms := now.UnixMilli()
	var id int64
	err := db.QueryRowContext(ctx, `
		INSERT INTO models (backend_id, name, first_seen, last_seen) VALUES (?, ?, ?, ?)
		ON CONFLICT(backend_id, name) DO UPDATE SET last_seen = excluded.last_seen
		RETURNING id`, backend, name, ms, ms).Scan(&id)
	if err != nil {
		return sql.NullInt64{}, fmt.Errorf("record model %s: %w", name, err)
	}
	return sql.NullInt64{Int64: id, Valid: true}, nil
}

func consumerID(ctx context.Context, db execQuerier, name, keyHMAC string, now time.Time) (sql.NullInt64, error) {
	if name == "" {
		return sql.NullInt64{}, nil
	}
	var h sql.NullString
	if keyHMAC != "" {
		h = sql.NullString{String: keyHMAC, Valid: true}
	}
	var id int64
	err := db.QueryRowContext(ctx, `
		INSERT INTO consumers (alias, key_hmac, created_at) VALUES (?, ?, ?)
		ON CONFLICT(alias) DO UPDATE SET key_hmac = coalesce(consumers.key_hmac, excluded.key_hmac)
		RETURNING id`, name, h, now.UnixMilli()).Scan(&id)
	if err != nil {
		return sql.NullInt64{}, fmt.Errorf("record consumer: %w", err)
	}
	return sql.NullInt64{Int64: id, Valid: true}, nil
}

// EnsureConsumer records a consumer with the HMAC of its key, when it has
// no alias.
func (s *Store) EnsureConsumer(ctx context.Context, name, keyHMAC string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := consumerID(ctx, s.db, name, keyHMAC, now)
	return err
}

func nullable(v *int64) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *v, Valid: true}
}

// InsertUsage stores the rows of one interval. info gives the kind and
// endpoint of the backends; a backend missing from it is recorded as a
// generic process.
func (s *Store) InsertUsage(ctx context.Context, rows []usage.Row, info map[string]BackendInfo, now time.Time) error {
	if len(rows) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	ids := map[string]int64{}
	for _, r := range rows {
		id, ok := ids[r.Backend]
		if !ok {
			b, known := info[r.Backend]
			if !known {
				b = BackendInfo{Name: r.Backend, Kind: "process"}
			}
			if id, err = s.backendID(ctx, tx, b, now); err != nil {
				_ = tx.Rollback()
				return err
			}
			ids[r.Backend] = id
		}
		model, err := modelID(ctx, tx, id, r.Model, now)
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		consumer, err := consumerID(ctx, tx, r.Consumer, "", now)
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO usage_intervals (ts_ms, interval_ms, backend_id, model_id, consumer_id,
				cpu_energy_j, dram_energy_j, gpu_energy_j, platform_energy_j,
				prompt_tokens, generated_tokens, requests, method, quality)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.Start.UnixMilli(), r.Interval.Milliseconds(), id, model, consumer,
			r.CPUJ, r.DRAMJ, r.GPUJ, r.PlatformJ,
			nullable(r.PromptTokens), nullable(r.GeneratedTokens), nullable(r.Requests), r.Method, string(r.Quality)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("store usage of %s: %w", r.Backend, err)
		}
	}
	return tx.Commit()
}

// energyExpr is the energy of a usage row: the platform share under the
// wall policy, otherwise processor, memory and GPU.
const energyExpr = `CASE WHEN platform_energy_j > 0 THEN platform_energy_j ELSE cpu_energy_j + dram_energy_j + gpu_energy_j END`

// rollupUsage folds usage rows as Rollup does for samples. Each bucket is
// folded once, so plain inserts are enough; rows without a model have a
// NULL key that a conflict clause would not match anyway. Indicators are
// derived from the energy when queried, with the factors configured then.
func (s *Store) rollupUsage(ctx context.Context, now time.Time) error {
	steps := []struct {
		res, query, first string
		until             int64
	}{
		{usage1m, `
			INSERT INTO usage_rollups (bucket_start_ms, resolution, backend_id, model_id, consumer_id,
				energy_j, gpu_energy_j, prompt_tokens, generated_tokens, requests)
			SELECT (ts_ms / 60000) * 60000, '1m', backend_id, model_id, consumer_id,
				sum(` + energyExpr + `), sum(gpu_energy_j), sum(prompt_tokens), sum(generated_tokens), sum(requests)
			FROM usage_intervals WHERE ts_ms >= ? AND ts_ms < ?
			GROUP BY 1, backend_id, model_id, consumer_id`,
			`SELECT coalesce(min(ts_ms), 9223372036854775807) FROM usage_intervals`,
			now.Add(-usageLag).Truncate(time.Minute).UnixMilli()},
		{usage1h, `
			INSERT INTO usage_rollups (bucket_start_ms, resolution, backend_id, model_id, consumer_id,
				energy_j, gpu_energy_j, prompt_tokens, generated_tokens, requests)
			SELECT (bucket_start_ms / 3600000) * 3600000, '1h', backend_id, model_id, consumer_id,
				sum(energy_j), sum(gpu_energy_j), sum(prompt_tokens), sum(generated_tokens), sum(requests)
			FROM usage_rollups WHERE resolution = '1m' AND bucket_start_ms >= ? AND bucket_start_ms < ?
			GROUP BY 1, backend_id, model_id, consumer_id`,
			`SELECT coalesce(min(bucket_start_ms), 9223372036854775807) FROM usage_rollups WHERE resolution = '1m'`,
			0},
	}
	for i, st := range steps {
		until := st.until
		if i == 1 {
			// Hours are folded from the minutes already folded.
			var m int64
			if err := s.db.QueryRowContext(ctx, `SELECT coalesce((SELECT done_until_ms FROM rollup_state WHERE resolution = ?), 0)`, usage1m).Scan(&m); err != nil {
				return err
			}
			until = time.UnixMilli(m).Truncate(time.Hour).UnixMilli()
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		var from int64
		err = tx.QueryRowContext(ctx, `SELECT done_until_ms FROM rollup_state WHERE resolution = ?`, st.res).Scan(&from)
		if errors.Is(err, sql.ErrNoRows) {
			err = tx.QueryRowContext(ctx, st.first).Scan(&from)
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
	return nil
}

// UsageEnergy is the energy and tokens of one backend, or one model of it,
// in a period.
type UsageEnergy struct {
	// BucketStart is set when the usage is split in buckets.
	BucketStart time.Time `json:"bucket_start,omitzero"`
	Backend     string    `json:"backend"`
	Kind        string    `json:"kind"`
	Model       string    `json:"model,omitempty"`
	Consumer    string    `json:"consumer,omitempty"`
	EnergyJ     float64   `json:"energy_j"`
	// GPUEnergyJ is the GPU part of EnergyJ.
	GPUEnergyJ      float64 `json:"gpu_energy_j"`
	PromptTokens    int64   `json:"prompt_tokens"`
	GeneratedTokens int64   `json:"generated_tokens"`
	Requests        int64   `json:"requests"`
	// Counted is false when the backend reported no tokens in the period.
	Counted bool `json:"tokens_counted"`
	// Hour is the UTC hour of the usage, set by UsageHourly.
	Hour time.Time `json:"-"`
	// Indicators are set by the agent from the factors in force.
	Indicators *indicators.Values `json:"indicators,omitempty"`
}

// Usage groupings.
const (
	ByBackend  = "backend"
	ByModel    = "model"
	ByConsumer = "consumer"
)

// Usage sums the usage in [from, to) by backend, model or consumer. A
// positive bucket splits the period in buckets that start at from. As for
// NodeEnergy, the resolution returned is the finest kept for from.
func (s *Store) Usage(ctx context.Context, from, to time.Time, by string, bucket time.Duration, now time.Time, r Retention) ([]UsageEnergy, string, error) {
	return s.usage(ctx, from, to, by, bucket, now, r, false)
}

// UsageHourly is Usage with each row split further by UTC hour, for
// indicators whose factors change with the time of day.
func (s *Store) UsageHourly(ctx context.Context, from, to time.Time, by string, bucket time.Duration, now time.Time, r Retention) ([]UsageEnergy, string, error) {
	return s.usage(ctx, from, to, by, bucket, now, r, true)
}

func (s *Store) usage(ctx context.Context, from, to time.Time, by string, bucket time.Duration, now time.Time, r Retention, hourly bool) ([]UsageEnergy, string, error) {
	res := finest(from, now, r)
	done1m, done1h, err := s.folded(ctx, usage1m, usage1h)
	if err != nil {
		return nil, res, err
	}
	plan := planRead(from.UnixMilli(), to.UnixMilli(), done1m, done1h, bucket.Milliseconds(), res)
	q, args := plan.union(
		`SELECT bucket_start_ms AS t, backend_id, model_id, consumer_id, energy_j AS e, gpu_energy_j AS gj, prompt_tokens AS p, generated_tokens AS g, requests AS q
			FROM usage_rollups WHERE resolution = ? AND bucket_start_ms >= ? AND bucket_start_ms < ?`,
		`SELECT ts_ms AS t, backend_id, model_id, consumer_id, `+energyExpr+` AS e, gpu_energy_j AS gj, prompt_tokens AS p, generated_tokens AS g, requests AS q
			FROM usage_intervals WHERE ts_ms >= ? AND ts_ms < ?`)
	group, model, consumer := `b.id`, `''`, `''`
	switch by {
	case ByModel:
		group, model = `b.id, u.model_id`, `coalesce(m.name, '')`
	case ByConsumer:
		group, consumer = `b.id, u.consumer_id`, `coalesce(c.alias, '')`
	}
	// The bucket of a row counts whole buckets from the start of the
	// period, so every bucket starts at from plus a multiple of its size.
	slot, order := `0`, ``
	if ms := bucket.Milliseconds(); ms > 0 {
		slot, order = `(u.t - ?) / ?`, `slot, `
		args = append([]any{from.UnixMilli(), ms}, args...)
		group += `, slot`
	}
	if hourly {
		group += `, hour`
	}
	// Only the constant fragments above are formatted into the query;
	// every value goes through placeholders.
	//nolint:gosec // constant fragments, see above
	query := `SELECT ` + slot + ` AS slot, ` + hourOf("u.t", hourly) + ` AS hour, b.name, b.kind, ` + model + `, ` + consumer + `, sum(u.e), sum(u.gj), coalesce(sum(u.p), 0), coalesce(sum(u.g), 0), coalesce(sum(u.q), 0),
			count(u.p) > 0 OR count(u.g) > 0
		FROM (` + q + `) AS u JOIN backends b ON b.id = u.backend_id LEFT JOIN models m ON m.id = u.model_id
			LEFT JOIN consumers c ON c.id = u.consumer_id
		GROUP BY ` + group + ` ORDER BY ` + order + `sum(u.e) DESC, b.name`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, res, err
	}
	defer func() { _ = rows.Close() }()
	var out []UsageEnergy
	for rows.Next() {
		var u UsageEnergy
		var slotN, hour int64
		if err := rows.Scan(&slotN, &hour, &u.Backend, &u.Kind, &u.Model, &u.Consumer, &u.EnergyJ, &u.GPUEnergyJ, &u.PromptTokens, &u.GeneratedTokens, &u.Requests, &u.Counted); err != nil {
			return nil, res, err
		}
		if bucket > 0 {
			u.BucketStart = from.Add(time.Duration(slotN) * bucket).UTC()
		}
		if hourly {
			u.Hour = time.UnixMilli(hour).UTC()
		}
		out = append(out, u)
	}
	return out, res, rows.Err()
}

// IntervalTokens is one usage interval of one model, for the regression of
// energy on tokens.
type IntervalTokens struct {
	EnergyJ         float64
	PromptTokens    float64
	GeneratedTokens float64
}

// ModelIntervals returns the raw usage intervals of a model in [from, to)
// that counted tokens and were attributed to it alone, so their energy is
// not itself a split by tokens.
func (s *Store) ModelIntervals(ctx context.Context, backend, model string, from, to time.Time) ([]IntervalTokens, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+energyExpr+`, u.prompt_tokens, u.generated_tokens
		FROM usage_intervals u JOIN backends b ON b.id = u.backend_id JOIN models m ON m.id = u.model_id
		WHERE b.name = ? AND m.name = ? AND u.ts_ms >= ? AND u.ts_ms < ?
			AND u.prompt_tokens IS NOT NULL AND u.generated_tokens IS NOT NULL AND u.method <> ?
		ORDER BY u.ts_ms`, backend, model, from.UnixMilli(), to.UnixMilli(), usage.MethodTokenWeighted)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []IntervalTokens
	for rows.Next() {
		var it IntervalTokens
		if err := rows.Scan(&it.EnergyJ, &it.PromptTokens, &it.GeneratedTokens); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}
