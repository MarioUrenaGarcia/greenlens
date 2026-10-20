// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package store

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/bench"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
	"github.com/MarioUrenaGarcia/greenlens/internal/stats"
	"github.com/MarioUrenaGarcia/greenlens/internal/usage"
)

func tokens(n int64) *int64 { return &n }

func row(start time.Time, backend, model string, cpu, gpu float64, p, g int64, method string) usage.Row {
	return usage.Row{
		Start: start, Interval: usage.Interval, Backend: backend, Model: model,
		Energy:       usage.Energy{CPUJ: cpu, GPUJ: gpu, Method: method, Quality: source.QualityMeasured},
		PromptTokens: tokens(p), GeneratedTokens: tokens(g), Requests: tokens(1),
	}
}

func TestUsage(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	now := time.Date(2026, 10, 20, 12, 30, 0, 0, time.UTC)
	info := map[string]BackendInfo{"vllm-8000": {Name: "vllm-8000", Kind: "vllm", Endpoint: "http://127.0.0.1:8000"}}
	start := now.Add(-20 * time.Minute)
	var rows []usage.Row
	for i := range 60 {
		at := start.Add(time.Duration(i) * usage.Interval)
		rows = append(rows,
			row(at, "vllm-8000", "qwen", 10, 20, 100, 50, usage.MethodTokenWeighted),
			row(at, "vllm-8000", "llama", 5, 5, 10, 10, usage.MethodTokenWeighted))
		// A process backend without tokens or model.
		rows = append(rows, usage.Row{Start: at, Interval: usage.Interval, Backend: "batch", Energy: usage.Energy{CPUJ: 1, Method: usage.MethodCPUShare, Quality: source.QualityEstimated}})
	}
	if err := s.InsertUsage(ctx, rows, info, now); err != nil {
		t.Fatal(err)
	}
	r := Retention{Raw: time.Hour, Rollup1m: 24 * time.Hour, Rollup1h: 48 * time.Hour}
	byModel, res, err := s.Usage(ctx, start, now, ByModel, now, r)
	if err != nil || res != ResolutionRaw || len(byModel) != 3 {
		t.Fatalf("%+v %s %v", byModel, res, err)
	}
	q := byModel[0]
	if q.Backend != "vllm-8000" || q.Kind != "vllm" || q.Model != "qwen" || math.Abs(q.EnergyJ-1800) > 1e-6 || q.PromptTokens != 6000 || q.GeneratedTokens != 3000 || !q.Counted {
		t.Fatalf("qwen %+v", q)
	}
	b := byModel[2]
	if b.Backend != "batch" || b.Kind != "process" || b.Model != "" || b.Counted || math.Abs(b.EnergyJ-60) > 1e-6 {
		t.Fatalf("batch %+v", b)
	}
	byBackend, _, err := s.Usage(ctx, start, now, ByBackend, now, r)
	if err != nil || len(byBackend) != 2 || math.Abs(byBackend[0].EnergyJ-2400) > 1e-6 || byBackend[0].Model != "" {
		t.Fatalf("%+v %v", byBackend, err)
	}

	// Minutes are folded only once their rows can no longer arrive.
	if err := s.Rollup(ctx, now); err != nil {
		t.Fatal(err)
	}
	var done int64
	if err := s.DB().QueryRowContext(ctx, `SELECT done_until_ms FROM rollup_state WHERE resolution = 'usage_1m'`).Scan(&done); err != nil {
		t.Fatal(err)
	}
	if want := now.Add(-time.Minute).UnixMilli(); done != want {
		t.Fatalf("folded until %v, want %v", time.UnixMilli(done).UTC(), time.UnixMilli(want).UTC())
	}
	// Totals are the same from the rollups as from the raw rows.
	later := now.Add(2 * time.Hour)
	if err := s.Rollup(ctx, later); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Prune(ctx, later, r); err != nil {
		t.Fatal(err)
	}
	var raw int
	if err := s.DB().QueryRowContext(ctx, `SELECT count(*) FROM usage_intervals`).Scan(&raw); err != nil || raw != 0 {
		t.Fatalf("raw rows left %d, %v", raw, err)
	}
	again, res, err := s.Usage(ctx, start, now, ByModel, later, r)
	if err != nil || res != Resolution1m || len(again) != 3 {
		t.Fatalf("%+v %s %v", again, res, err)
	}
	for i := range again {
		if math.Abs(again[i].EnergyJ-byModel[i].EnergyJ) > 1e-6 || again[i].GeneratedTokens != byModel[i].GeneratedTokens {
			t.Errorf("rollup %+v, raw %+v", again[i], byModel[i])
		}
	}
}

func TestModelIntervals(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	rows := []usage.Row{
		row(now, "llama", "q", 10, 0, 100, 20, usage.MethodCPUShare),
		row(now.Add(usage.Interval), "llama", "q", 12, 0, 50, 40, usage.MethodGPUProcess),
		// Split by tokens: its energy says nothing of the cost of a token.
		row(now.Add(2*usage.Interval), "llama", "q", 9, 0, 10, 10, usage.MethodTokenWeighted),
		row(now.Add(3*usage.Interval), "llama", "other-model", 9, 0, 10, 10, usage.MethodCPUShare),
	}
	if err := s.InsertUsage(ctx, rows, nil, now); err != nil {
		t.Fatal(err)
	}
	got, err := s.ModelIntervals(ctx, "llama", "q", now, now.Add(time.Hour))
	if err != nil || len(got) != 2 || got[1].EnergyJ != 12 || got[1].GeneratedTokens != 40 {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestBenchmarks(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	r := bench.Result{StartedAt: now, EndedAt: now.Add(time.Minute), Target: bench.Target{Kind: "ollama", Endpoint: "http://127.0.0.1:11434", Model: "qwen2.5:0.5b"},
		Params: bench.DefaultParams(), PromptSetSHA256: bench.Hash(bench.DefaultPrompts()), Runs: []bench.Run{{EnergyJ: 10}, {EnergyJ: 11}},
		JPerToken: stats.Summary{N: 2, Mean: 0.5}, GeneratedTokens: 40}
	id, err := s.SaveBenchmark(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Benchmark(ctx, id)
	if err != nil || got.ID != id || got.Target.Model != "qwen2.5:0.5b" || len(got.Runs) != 2 || got.JPerToken.Mean != 0.5 {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := s.Benchmark(ctx, id+1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	r.StartedAt = now.Add(time.Hour)
	if _, err := s.SaveBenchmark(ctx, r); err != nil {
		t.Fatal(err)
	}
	list, err := s.Benchmarks(ctx, 10)
	if err != nil || len(list) != 2 || list[0].ID != id+1 {
		t.Fatalf("%+v %v", list, err)
	}
	// The prompts themselves are never stored.
	var stored string
	if err := s.DB().QueryRowContext(ctx, `SELECT results FROM benchmarks WHERE id = ?`, id).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	for _, p := range bench.DefaultPrompts() {
		if strings.Contains(stored, p) {
			t.Fatalf("a prompt was stored")
		}
	}
}
