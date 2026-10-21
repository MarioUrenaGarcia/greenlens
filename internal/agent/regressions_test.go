// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package agent

import (
	"context"
	"encoding/json"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/api"
	"github.com/MarioUrenaGarcia/greenlens/internal/config"
	"github.com/MarioUrenaGarcia/greenlens/internal/paths"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
	"github.com/MarioUrenaGarcia/greenlens/internal/store"
	"github.com/MarioUrenaGarcia/greenlens/internal/usage"
)

func TestRegressionsOfAModel(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	st, err := store.Open(ctx, filepath.Join(dir, paths.DatabaseName))
	if err != nil {
		t.Fatal(err)
	}
	// Nine days near 0.20 J per token, then one at 0.30.
	jpt := []float64{0.20, 0.21, 0.19, 0.20, 0.22, 0.18, 0.20, 0.21, 0.19, 0.30}
	var rows []usage.Row
	for i, v := range jpt {
		gen := int64(10_000)
		at := today.AddDate(0, 0, i-len(jpt)+1).Add(time.Hour)
		if at.After(now) {
			at = today
		}
		rows = append(rows, usage.Row{Start: at, Interval: usage.Interval, Backend: "ollama", Model: "qwen", GeneratedTokens: &gen,
			Energy: usage.Energy{CPUJ: v * 10_000, Method: usage.MethodTokenWeighted, Quality: source.QualityMeasured}})
	}
	if err := st.InsertUsage(ctx, rows, map[string]store.BackendInfo{"ollama": {Name: "ollama", Kind: "ollama"}}, now); err != nil {
		t.Fatal(err)
	}
	if err := st.Rollup(ctx, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordContext(ctx, today, store.ContextBackendVersion, "ollama", "0.41.0", "0.40.6"); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	r := startAgentWith(t, "", func(c *config.Config) { c.Agent.DataDir = dir })
	sock := call{t: t, c: socketClient(r.socket), base: "http://agent"}
	q := url.Values{"from": {today.Format(time.RFC3339)}, "to": {now.Add(time.Minute).Format(time.RFC3339)}, "model": {"qwen"}}
	resp, body := sock.do("GET", api.PathRegressions+"?"+q.Encode(), nil)
	var doc api.Regressions
	if resp.code != 200 || json.Unmarshal(body, &doc) != nil || len(doc.Models) != 1 || len(doc.Models[0].Days) != 1 {
		t.Fatalf("regressions: %d %s", resp.code, body)
	}
	d := doc.Models[0].Days[0]
	if d.Status != "regression" || d.BaselineDays != 9 || d.Z == nil || *d.Z < 3 {
		t.Fatalf("day %+v", d)
	}
	found := false
	for _, e := range doc.Events {
		if e.Kind == store.ContextBackendVersion && e.Value == "0.41.0" && e.Previous == "0.40.6" {
			found = true
		}
	}
	if !found {
		t.Fatalf("events %+v", doc.Events)
	}
	// The agent recorded its own version at start.
	if !hasKind(doc.Events, store.ContextAgentVersion) {
		t.Fatalf("no agent version in %+v", doc.Events)
	}
}

func hasKind(evs []store.ContextEvent, kind string) bool {
	for _, e := range evs {
		if e.Kind == kind {
			return true
		}
	}
	return false
}
