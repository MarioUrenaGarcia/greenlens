// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

//go:build e2e

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/api"
	"github.com/MarioUrenaGarcia/greenlens/internal/config"
	"github.com/MarioUrenaGarcia/greenlens/internal/indicators"
	"github.com/MarioUrenaGarcia/greenlens/internal/paths"
	"github.com/MarioUrenaGarcia/greenlens/internal/store"
)

// queryBudget is the 99th percentile allowed for a query of 30 days.
const queryBudget = 50 * time.Millisecond

// seedThirtyDays writes the rollups of a busy node over the 30 days before
// now: six energy channels, and four backends with five models each, one
// loaded at a time per backend, serving two consumers. Raw samples cover
// the last minutes only; a query of 30 days reads them through the primary
// key range, so older raw rows would not change its cost.
func seedThirtyDays(t *testing.T, path string, now time.Time) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	db := st.DB()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := tx.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	end := now.Truncate(time.Minute)
	start := end.Add(-30 * 24 * time.Hour)
	ms := now.UnixMilli()
	channels := []struct{ name, src, domain, quality string }{
		{"rapl:package-0", "rapl", "package", "measured"}, {"rapl:dram-0", "rapl", "dram", "measured"},
		{"nvml:0000:01:00.0", "nvml", "gpu", "measured"}, {"nvml:0000:02:00.0", "nvml", "gpu", "measured"},
		{"ipmi:platform", "ipmi", "platform", "platform"}, {"rapl:psys", "rapl", "psys", "platform"},
	}
	for i, c := range channels {
		exec(`INSERT INTO sources (id, name, kind, domain, quality, first_seen, last_seen) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			100+i, c.name, c.src, c.domain, c.quality, start.UnixMilli(), ms)
	}
	for b := range 4 {
		exec(`INSERT INTO backends (id, name, kind, endpoint, first_seen, last_seen) VALUES (?, ?, 'ollama', ?, ?, ?)`,
			100+b, fmt.Sprintf("backend-%d", b), fmt.Sprintf("http://127.0.0.1:%d", 11434+b), start.UnixMilli(), ms)
		for m := range 5 {
			exec(`INSERT INTO models (id, backend_id, name, first_seen, last_seen) VALUES (?, ?, ?, ?, ?)`,
				1000+b*5+m, 100+b, fmt.Sprintf("model-%d-%d", b, m), start.UnixMilli(), ms)
		}
	}
	for c := range 2 {
		exec(`INSERT INTO consumers (id, alias, key_hmac, created_at) VALUES (?, ?, ?, ?)`, 100+c, fmt.Sprintf("team-%d", c), fmt.Sprintf("%064x", c), ms)
	}
	node, err := tx.PrepareContext(ctx, `INSERT INTO node_rollups (bucket_start_ms, resolution, source_id, energy_j, samples) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		t.Fatal(err)
	}
	use, err := tx.PrepareContext(ctx, `INSERT INTO usage_rollups (bucket_start_ms, resolution, backend_id, model_id, consumer_id,
		energy_j, prompt_tokens, generated_tokens, requests) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		t.Fatal(err)
	}
	rows := 0
	for _, step := range []struct {
		res  string
		size time.Duration
	}{{store.Resolution1m, time.Minute}, {store.Resolution1h, time.Hour}} {
		n := float64(step.size / time.Minute)
		for at := start; at.Before(end); at = at.Add(step.size) {
			b := at.UnixMilli()
			for i := range channels {
				if _, err := node.ExecContext(ctx, b, step.res, 100+i, 3000*n, 60*int(n)); err != nil {
					t.Fatal(err)
				}
				rows++
			}
			for be := range 4 {
				model := 1000 + be*5 + at.Hour()%5
				for c := range 2 {
					if _, err := use.ExecContext(ctx, b, step.res, 100+be, model, 100+c, 900*n, 2000*int(n), 600*int(n), 4*int(n)); err != nil {
						t.Fatal(err)
					}
					rows++
				}
			}
		}
	}
	done := end.UnixMilli()
	for _, res := range []string{store.Resolution1m, "usage_1m"} {
		exec(`INSERT INTO rollup_state (resolution, done_until_ms) VALUES (?, ?)`, res, done)
	}
	for _, res := range []string{store.Resolution1h, "usage_1h"} {
		exec(`INSERT INTO rollup_state (resolution, done_until_ms) VALUES (?, ?)`, res, end.Truncate(time.Hour).UnixMilli())
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	t.Logf("seeded %d rollup rows over 30 days", rows)
}

func TestBudgetOfThirtyDayQueries(t *testing.T) {
	now := time.Now()
	dir := t.TempDir()
	seedThirtyDays(t, filepath.Join(dir, paths.DatabaseName), now)
	listen := freePort(t)
	// An hourly profile and tariff periods make every query split its
	// buckets by hour, the slower path.
	var profile indicators.Profile
	for d := range profile {
		for h := range profile[d] {
			profile[d][h] = float64(400 + 5*h)
		}
	}
	base := 1.0
	r := startAgentWith(t, "", func(c *config.Config) {
		c.Agent.DataDir = dir
		c.Server.Enabled = true
		c.Server.Listen = listen
		c.Indicators.Profile, c.Indicators.Location = &profile, time.UTC
		c.Indicators.Tariff.PricePerKWh = &base
		c.Indicators.Tariff.Periods = []config.TariffPeriod{{Days: []string{"mon", "tue", "wed", "thu", "fri"}, From: "18:00", To: "22:00", PricePerKWh: 3}}
	})
	sock := call{t: t, c: socketClient(r.socket), base: "http://agent"}
	resp, body := sock.do("POST", api.PathTokens, api.CreateToken{Name: "budget", Role: "viewer"})
	var tok api.CreatedToken
	if resp.code != 200 && resp.code != 201 || json.Unmarshal(body, &tok) != nil {
		t.Fatalf("token: %d %s", resp.code, body)
	}
	from := now.Add(-30 * 24 * time.Hour).UTC().Format(time.RFC3339)
	to := now.UTC().Format(time.RFC3339)
	// Periods split in buckets start on the hour, as the interface sends
	// them: the reports at midnight and the history of a model at the
	// hour, so every bucket reads hourly rollups.
	hour := now.Add(-30 * 24 * time.Hour).Truncate(time.Hour).UTC().Format(time.RFC3339)
	// The queries of 30 days the interface makes: the summary and the
	// reports by day or hour, and the models, backends and consumers.
	queries := []url.Values{
		{"from": {from}, "to": {to}},
		{"from": {hour}, "to": {to}, "bucket": {"24h"}},
		{"from": {hour}, "to": {to}, "bucket": {"1h"}},
		{"from": {from}, "to": {to}, "group_by": {"model"}},
		{"from": {hour}, "to": {to}, "group_by": {"model"}, "bucket": {"24h"}},
		{"from": {hour}, "to": {to}, "group_by": {"backend"}, "bucket": {"24h"}},
		{"from": {from}, "to": {to}, "group_by": {"consumer"}},
		{"from": {from}, "to": {to}, "kind": {"sci"}, "group_by": {"model"}},
	}
	client := &http.Client{Timeout: 10 * time.Second}
	// Requests are spaced below the rate limit of one address.
	pace := time.NewTicker(time.Second / (requestRate - 2))
	defer pace.Stop()
	for _, q := range queries {
		var took []time.Duration
		var res string
		for i := range 61 {
			<-pace.C
			path := api.PathUsage
			if q.Has("kind") {
				path = api.PathReports
			}
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+listen+path+"?"+q.Encode(), nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer "+tok.Secret)
			start := time.Now()
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			var doc struct {
				Resolution string `json:"resolution"`
				Rows       []struct {
					Indicators *indicators.Values `json:"indicators"`
				} `json:"rows"`
			}
			err = json.NewDecoder(resp.Body).Decode(&doc)
			_ = resp.Body.Close()
			d := time.Since(start)
			if err != nil || resp.StatusCode != 200 || len(doc.Rows) == 0 || doc.Rows[0].Indicators == nil || doc.Rows[0].Indicators.Cost == nil {
				t.Fatalf("%s: %d %v, %d rows", q.Encode(), resp.StatusCode, err, len(doc.Rows))
			}
			res = doc.Resolution
			// The first request warms the page cache of SQLite.
			if i > 0 {
				took = append(took, d)
			}
		}
		slices.Sort(took)
		p99 := took[len(took)*99/100-1]
		t.Logf("%-5s bucket %-3s group %-8s resolution %s: p50 %s, p99 %s", q.Get("kind"), q.Get("bucket"), q.Get("group_by"), res,
			took[len(took)/2].Round(time.Microsecond), p99.Round(time.Microsecond))
		if p99 > queryBudget {
			t.Errorf("%s: p99 %s over %s", q.Encode(), p99, queryBudget)
		}
	}
}
