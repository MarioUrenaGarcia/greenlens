// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/MarioUrenaGarcia/greenlens/internal/bench"
)

// ErrNotFound means no row has the given id.
var ErrNotFound = errors.New("not found")

// SaveBenchmark stores a benchmark and returns its id. The prompt set is
// identified by its hash only.
func (s *Store) SaveBenchmark(ctx context.Context, r bench.Result) (int64, error) {
	params, err := json.Marshal(r.Params)
	if err != nil {
		return 0, err
	}
	bctx, err := json.Marshal(r.Context)
	if err != nil {
		return 0, err
	}
	results, err := json.Marshal(r)
	if err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO benchmarks (started_at_ms, ended_at_ms, backend_kind, model, prompt_set_sha256, params, context, runs,
			energy_j_mean, energy_j_stddev, generated_tokens, j_per_token_mean, results)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.StartedAt.UnixMilli(), r.EndedAt.UnixMilli(), string(r.Target.Kind), r.Target.Model, r.PromptSetSHA256,
		string(params), string(bctx), len(r.Runs), r.Energy.Mean, r.Energy.StdDev, r.GeneratedTokens, r.JPerToken.Mean, string(results))
	if err != nil {
		return 0, fmt.Errorf("store benchmark: %w", err)
	}
	return res.LastInsertId()
}

// Benchmark returns one stored benchmark.
func (s *Store) Benchmark(ctx context.Context, id int64) (bench.Result, error) {
	var data string
	err := s.db.QueryRowContext(ctx, `SELECT results FROM benchmarks WHERE id = ?`, id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return bench.Result{}, ErrNotFound
	}
	if err != nil {
		return bench.Result{}, err
	}
	var r bench.Result
	if err := json.Unmarshal([]byte(data), &r); err != nil {
		return bench.Result{}, fmt.Errorf("benchmark %d: %w", id, err)
	}
	r.ID = id
	return r, nil
}

// Benchmarks returns the latest stored benchmarks, newest first.
func (s *Store) Benchmarks(ctx context.Context, limit int) ([]bench.Result, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, results FROM benchmarks ORDER BY started_at_ms DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []bench.Result{}
	for rows.Next() {
		var id int64
		var data string
		if err := rows.Scan(&id, &data); err != nil {
			return nil, err
		}
		var r bench.Result
		if err := json.Unmarshal([]byte(data), &r); err != nil {
			return nil, fmt.Errorf("benchmark %d: %w", id, err)
		}
		r.ID = id
		out = append(out, r)
	}
	return out, rows.Err()
}
