// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package agent

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"math"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/api"
	"github.com/MarioUrenaGarcia/greenlens/internal/config"
	"github.com/MarioUrenaGarcia/greenlens/internal/indicators"
	"github.com/MarioUrenaGarcia/greenlens/internal/paths"
	"github.com/MarioUrenaGarcia/greenlens/internal/report"
	"github.com/MarioUrenaGarcia/greenlens/internal/store"
	"github.com/MarioUrenaGarcia/greenlens/internal/units"
)

// seedWorkedDay stores the day of the worked cases of the documentation,
// spread over its 24 hourly rollups: 1.2 kWh of processor package and 0.3
// kWh of memory, measured, 0.9 kWh of GPU, estimated, and 3.1 kWh read by a
// platform meter. Of the 1.8 kWh of the backend, model qwen used 1.71 kWh,
// 0.81 of them on the GPU, and generated 40,000 tokens; model llama used
// 0.09 kWh for 10,000 tokens.
func seedWorkedDay(t *testing.T, path string, day time.Time) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	tx, err := st.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := tx.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	kwh := indicators.JoulesPerKWh
	ms := day.UnixMilli()
	for i, s := range []struct {
		name, kind, domain, quality string
		kwh                         float64
	}{
		{"rapl:package-0", "rapl", "package", "measured", 1.2}, {"rapl:dram-0", "rapl", "dram", "measured", 0.3},
		{"estimate:gpu", "estimate", "gpu", "estimated", 0.9}, {"ipmi:platform", "ipmi", "platform", "platform", 3.1},
	} {
		exec(`INSERT INTO sources (id, name, kind, domain, quality, first_seen, last_seen) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			100+i, s.name, s.kind, s.domain, s.quality, ms, ms)
		for h := range 24 {
			exec(`INSERT INTO node_rollups (bucket_start_ms, resolution, source_id, energy_j, samples) VALUES (?, ?, ?, ?, ?)`,
				day.Add(time.Duration(h)*time.Hour).UnixMilli(), store.Resolution1h, 100+i, s.kwh*kwh/24, 3600)
		}
	}
	exec(`INSERT INTO backends (id, name, kind, endpoint, first_seen, last_seen) VALUES (100, 'ollama', 'ollama', 'http://127.0.0.1:11434', ?, ?)`, ms, ms)
	for i, m := range []struct {
		name          string
		kwh, gpu, gen float64
	}{{"qwen", 1.71, 0.81, 40_000}, {"llama", 0.09, 0, 10_000}} {
		exec(`INSERT INTO models (id, backend_id, name, first_seen, last_seen) VALUES (?, 100, ?, ?, ?)`, 1000+i, m.name, ms, ms)
		for h := range 24 {
			// The first hour takes what does not divide evenly, so the day
			// adds up to the exact count.
			tokens := int(m.gen) / 24
			if h == 0 {
				tokens += int(m.gen) % 24
			}
			exec(`INSERT INTO usage_rollups (bucket_start_ms, resolution, backend_id, model_id, consumer_id, energy_j, gpu_energy_j,
				prompt_tokens, generated_tokens, requests) VALUES (?, ?, 100, ?, NULL, ?, ?, 0, ?, 1)`,
				day.Add(time.Duration(h)*time.Hour).UnixMilli(), store.Resolution1h, 1000+i, m.kwh*kwh/24, m.gpu*kwh/24, tokens)
		}
	}
	end := day.Add(24 * time.Hour).UnixMilli()
	for _, res := range []string{store.Resolution1m, "usage_1m", store.Resolution1h, "usage_1h"} {
		exec(`INSERT INTO rollup_state (resolution, done_until_ms) VALUES (?, ?)`, res, end)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6*math.Max(1, math.Abs(b)) }

// The worked cases of the documentation of the reports and of the SCI,
// through the whole agent: stored rollups, the read plan, the factors of
// the configuration, and the JSON, CSV and printable pages of the API.
func TestReportsMatchTheWorkedCases(t *testing.T) {
	dir := t.TempDir()
	// Two days back keeps the day within every retention and away from
	// the samples the agent takes while the test runs.
	day := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -2)
	seedWorkedDay(t, filepath.Join(dir, paths.DatabaseName), day)
	price, server, gpu, years := 2.5, 1200.0, 300.0, 4.0
	r := startAgentWith(t, "", func(c *config.Config) {
		c.Agent.DataDir = dir
		c.Indicators.PUE = 1.2
		c.Indicators.Tariff.PricePerKWh = &price
		c.Indicators.SCI.Embodied = config.Embodied{ServerKgCO2e: &server, GPUKgCO2e: &gpu, LifetimeYears: &years}
		c.Indicators.Location = time.UTC
	})
	sock := call{t: t, c: socketClient(r.socket), base: "http://agent"}
	get := func(q url.Values) string {
		t.Helper()
		q.Set("from", day.Format(time.RFC3339))
		q.Set("to", day.Add(24*time.Hour).Format(time.RFC3339))
		resp, body := sock.do("GET", api.PathReports+"?"+q.Encode(), nil)
		if resp.code != 200 {
			t.Fatalf("%s: %d %s", q.Encode(), resp.code, body)
		}
		return string(body)
	}
	kwh := indicators.JoulesPerKWh

	// The environmental report: 2.4 kWh of components, 2.88 kWh with the
	// PUE, 1,278.72 gCO2e, 7.20 of cost, 62.5% measured and 0.6 kWh that no
	// backend accounts for.
	var esg report.ESGReport
	if err := json.Unmarshal([]byte(get(url.Values{"kind": {"esg"}})), &esg); err != nil {
		t.Fatal(err)
	}
	if !near(esg.ComponentsJ, 2.4*kwh) || !near(esg.FacilityKWh, 2.88) || !near(esg.Indicators.CO2eG, 1278.72) ||
		esg.Indicators.Cost == nil || !near(*esg.Indicators.Cost, 7.2) || !near(esg.MeasuredShare, 0.625) ||
		!near(esg.UnattributedJ, 0.6*kwh) || len(esg.Backends) != 1 || !near(esg.Backends[0].EnergyJ, 1.8*kwh) {
		t.Fatalf("esg %+v", esg)
	}

	// The SCI by model: the node is (1,278.72 + 1,026.69) / 50 = 46.11 g per
	// 1,000 tokens, and qwen (911.09 + 677.62) / 40 = 39.72 g.
	var sci report.SCIReport
	if err := json.Unmarshal([]byte(get(url.Values{"kind": {"sci"}, "group_by": {store.ByModel}})), &sci); err != nil {
		t.Fatal(err)
	}
	m := 1_500_000 * 24 / (4 * 8766.0)
	n := sci.Node.SCI
	if !near(n.OperationalG, 1278.72) || n.EmbodiedG == nil || !near(*n.EmbodiedG, m) || n.Units != 50 ||
		n.GPerUnit == nil || !near(*n.GPerUnit, (1278.72+m)/50) {
		t.Fatalf("node %+v", n)
	}
	var qwen *report.SCIRow
	for i := range sci.Rows {
		if sci.Rows[i].Model == "qwen" {
			qwen = &sci.Rows[i]
		}
	}
	mq := (1_200_000*0.6 + 300_000*0.9) * 24 / 35064.0
	if qwen == nil || !near(qwen.ServerShare, 0.6) || !near(qwen.GPUShare, 0.9) || !near(qwen.SCI.OperationalG, 1.71*1.2*444) ||
		qwen.SCI.EmbodiedG == nil || !near(*qwen.SCI.EmbodiedG, mq) || qwen.SCI.Units != 40 ||
		qwen.SCI.GPerUnit == nil || !near(*qwen.SCI.GPerUnit, (1.71*1.2*444+mq)/40) {
		t.Fatalf("qwen %+v", qwen)
	}

	// The CSV carries the same totals.
	rows, err := csv.NewReader(strings.NewReader(get(url.Values{"kind": {"esg"}, "format": {"csv"}}))).ReadAll()
	if err != nil || len(rows) < 2 || rows[1][0] != "total" {
		t.Fatalf("esg csv %v %v", rows, err)
	}
	if co2e, _ := strconv.ParseFloat(rows[1][6], 64); !near(co2e, 1278.72) {
		t.Fatalf("esg csv total %v", rows[1])
	}
	rows, err = csv.NewReader(strings.NewReader(get(url.Values{"kind": {"sci"}, "group_by": {store.ByModel}, "format": {"csv"}}))).ReadAll()
	if err != nil || len(rows) < 2 || rows[1][0] != "node" {
		t.Fatalf("sci csv %v %v", rows, err)
	}
	if g, _ := strconv.ParseFloat(rows[1][11], 64); !near(g, (1278.72+m)/50) {
		t.Fatalf("sci csv node %v", rows[1])
	}

	// The printable pages, in both languages, show the figures of the JSON.
	for _, lang := range []string{"en", "es"} {
		page := get(url.Values{"kind": {"esg"}, "format": {"html"}, "lang": {lang}})
		for _, want := range []string{`<html lang="` + lang + `">`, units.Energy(esg.ComponentsJ, false), "2.880 kWh",
			units.CO2e(esg.Indicators.CO2eG, false), "7.20 MXN", "62.5%", units.Energy(esg.UnattributedJ, false),
			day.Format("2006-01-02 15:04"), "UTC"} {
			if !strings.Contains(page, want) {
				t.Errorf("%s esg page lacks %q", lang, want)
			}
		}
		page = get(url.Values{"kind": {"sci"}, "group_by": {store.ByModel}, "format": {"html"}, "lang": {lang}})
		for _, want := range []string{units.CO2e(*n.GPerUnit, false), units.CO2e(n.OperationalG, false), units.CO2e(*n.EmbodiedG, false),
			"ollama/qwen", units.CO2e(*qwen.SCI.GPerUnit, false), "60.0%", "90.0%"} {
			if !strings.Contains(page, want) {
				t.Errorf("%s sci page lacks %q", lang, want)
			}
		}
	}
}
