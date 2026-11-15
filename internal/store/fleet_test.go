// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestFleetEnergy(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "hub.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	h := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	add := func(node string, at time.Time, domains map[string]float64) {
		t.Helper()
		if err := s.AddFleetEnergy(ctx, node, at, domains); err != nil {
			t.Fatal(err)
		}
	}
	add("node-a", h.Add(5*time.Minute), map[string]float64{"package": 100, "gpu": 40, "dram": 0})
	add("node-a", h.Add(50*time.Minute), map[string]float64{"package": 20.5})
	add("node-b", h.Add(59*time.Minute+59*time.Second), map[string]float64{"package": 7})
	add("node-a", h.Add(time.Hour), map[string]float64{"package": 3})
	// A time in another zone falls in the same hour.
	add("node-b", h.Add(10*time.Minute).In(time.FixedZone("x", -6*3600)), map[string]float64{"package": 1})

	got, err := s.FleetEnergyBetween(ctx, h, h.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	want := []FleetEnergy{
		{"node-a", h, "gpu", 40}, {"node-a", h, "package", 120.5}, {"node-b", h, "package", 8},
	}
	if len(got) != len(want) {
		t.Fatalf("%+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d: %+v, want %+v", i, got[i], want[i])
		}
	}
	all, err := s.FleetEnergyBetween(ctx, h, h.Add(2*time.Hour))
	if err != nil || len(all) != 4 || all[3].Hour != h.Add(time.Hour) || all[3].EnergyJ != 3 {
		t.Fatalf("%+v %v", all, err)
	}
	if none, err := s.FleetEnergyBetween(ctx, h.Add(-time.Hour), h); err != nil || none == nil || len(none) != 0 {
		t.Fatalf("%+v %v", none, err)
	}
	n, err := s.PruneFleetEnergy(ctx, h.Add(time.Hour))
	if err != nil || n != 3 {
		t.Fatalf("pruned %d, %v", n, err)
	}
	if left, err := s.FleetEnergyBetween(ctx, h, h.Add(2*time.Hour)); err != nil || len(left) != 1 {
		t.Fatalf("%+v %v", left, err)
	}
}
