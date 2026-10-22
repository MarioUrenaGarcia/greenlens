// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/alerts"
)

func TestAlertsAreKept(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	t0 := time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC)
	a := alerts.Alert{Rule: "high_jpt", Expr: alerts.ModelJPerToken, Op: ">", Threshold: 5, ForS: 600,
		Labels: map[string]string{"backend": "ollama", "model": "qwen"}, State: alerts.Pending, Value: 6, StartedAt: t0}
	id, err := s.InsertAlert(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	active, err := s.ActiveAlerts(ctx)
	if err != nil || len(active) != 1 || active[0].ID != id || active[0].Labels["model"] != "qwen" || active[0].ForS != 600 || !active[0].StartedAt.Equal(t0) {
		t.Fatalf("active %+v %v", active, err)
	}
	// Pending alerts are not history until they fire.
	if h, err := s.AlertHistory(ctx, t0.Add(-time.Hour), t0.Add(time.Hour), 10); err != nil || len(h) != 0 {
		t.Fatalf("history %+v %v", h, err)
	}
	fired, resolved := t0.Add(10*time.Minute), t0.Add(30*time.Minute)
	a.ID, a.State, a.FiredAt, a.Value = id, alerts.Firing, &fired, 6.5
	if err := s.UpdateAlert(ctx, a); err != nil {
		t.Fatal(err)
	}
	a.State, a.ResolvedAt, a.Value = alerts.Resolved, &resolved, 4
	if err := s.UpdateAlert(ctx, a); err != nil {
		t.Fatal(err)
	}
	h, err := s.AlertHistory(ctx, t0.Add(-time.Hour), t0.Add(time.Hour), 10)
	if err != nil || len(h) != 1 || h[0].State != alerts.Resolved || !h[0].FiredAt.Equal(fired) || !h[0].ResolvedAt.Equal(resolved) || h[0].Value != 4 {
		t.Fatalf("history %+v %v", h, err)
	}
	if active, _ := s.ActiveAlerts(ctx); len(active) != 0 {
		t.Fatalf("still active %+v", active)
	}
	// A pending alert that never fired leaves no row.
	id2, _ := s.InsertAlert(ctx, alerts.Alert{Rule: "r", Expr: alerts.SourceUp, Op: "<", Labels: map[string]string{"source": "rapl"}, State: alerts.Pending, StartedAt: t0})
	if err := s.DeleteAlert(ctx, id2); err != nil {
		t.Fatal(err)
	}
	if active, _ := s.ActiveAlerts(ctx); len(active) != 0 {
		t.Fatalf("deleted alert still active %+v", active)
	}
	// Resolved alerts go with the hourly rollups.
	if _, err := s.Prune(ctx, resolved.Add(3*time.Hour), Retention{Raw: time.Hour, Rollup1m: time.Hour, Rollup1h: time.Hour}); err != nil {
		t.Fatal(err)
	}
	if h, _ := s.AlertHistory(ctx, t0.Add(-time.Hour), t0.Add(time.Hour), 10); len(h) != 0 {
		t.Fatalf("pruned alert kept %+v", h)
	}
}
