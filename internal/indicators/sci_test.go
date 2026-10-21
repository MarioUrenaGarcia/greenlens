// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package indicators

import (
	"slices"
	"testing"
	"time"
)

func ptr(v float64) *float64 { return &v }

// The cases are worked by hand in the SCI explanation of the documentation.
func TestSCIWorkedCases(t *testing.T) {
	// Case 1: a node over one day with 2.4 kWh of components, PUE 1.2 and
	// 444 gCO2e/kWh: O = 2.4 x 1.2 x 444 = 1278.72 g. A server of 1200
	// kgCO2e and a GPU of 300 kgCO2e over 4 years: M = (1,200,000 + 300,000)
	// x 24 / (4 x 8766) = 1026.69 g. 50,000 generated tokens are 50 units:
	// SCI = (1278.72 + 1026.69) / 50 = 46.108 g per 1,000 tokens.
	f := Default()
	f.PUE = 1.2
	o := f.Compute(2.4 * JoulesPerKWh).CO2eG
	if !near(o, 1278.72) {
		t.Fatalf("O = %v", o)
	}
	e := Embodied{ServerKg: ptr(1200), GPUKg: ptr(300), LifetimeYears: ptr(4)}
	m := e.Grams(24*time.Hour, 1, 1)
	if m == nil || !near(*m, 1_500_000*24/(4*8766.0)) {
		t.Fatalf("M = %v", m)
	}
	s := ComputeSCI(UnitGeneratedTokens, UnitsOf(UnitGeneratedTokens, 50_000, 900, 3), o, m)
	if s.Units != 50 || s.GPerUnit == nil || !near(*s.GPerUnit, (1278.72+*m)/50) {
		t.Fatalf("SCI %+v", s)
	}
	// Case 2: a model that used 60% of the processor and memory energy
	// and 90% of the GPU energy of that day takes 0.6 of the server and
	// 0.9 of the GPU: M = (1,200,000 x 0.6 + 300,000 x 0.9) x 24 / 35064
	// = 677.62 g.
	mm := e.Grams(24*time.Hour, 0.6, 0.9)
	if !near(*mm, (720_000+270_000)*24/35064.0) {
		t.Fatalf("model M = %v", *mm)
	}
	// Per request: 900 requests.
	if s := ComputeSCI(UnitRequest, UnitsOf(UnitRequest, 50_000, 900, 3), o, m); s.Units != 900 || !near(*s.GPerUnit, (o+*m)/900) {
		t.Fatalf("per request %+v", s)
	}
	// Without embodied carbon, SCI is O / R and M is absent.
	none := Embodied{}
	if none.Grams(24*time.Hour, 1, 1) != nil || none.Configured() {
		t.Fatal("M without data")
	}
	if !slices.Equal(none.Missing(), []string{"indicators.sci.embodied.server_kgco2e", "indicators.sci.embodied.gpu_kgco2e", "indicators.sci.embodied.lifetime_years"}) {
		t.Fatalf("missing %v", none.Missing())
	}
	if s := ComputeSCI(UnitGeneratedTokens, 50, o, nil); s.EmbodiedG != nil || !near(*s.GPerUnit, o/50) {
		t.Fatalf("without M %+v", s)
	}
	// Only the server: the GPU adds nothing.
	server := Embodied{ServerKg: ptr(1200), LifetimeYears: ptr(4)}
	if g := server.Grams(24*time.Hour, 1, 1); !near(*g, 1_200_000*24/35064.0) {
		t.Fatalf("server only %v", *g)
	}
	// No units: no SCI.
	if s := ComputeSCI(UnitConsumer, 0, o, m); s.GPerUnit != nil {
		t.Fatalf("no units %+v", s)
	}
}

func TestShare(t *testing.T) {
	for _, tt := range []struct{ part, whole, want float64 }{{3, 4, 0.75}, {5, 4, 1}, {-1, 4, 0}, {1, 0, 0}} {
		if got := Share(tt.part, tt.whole); got != tt.want {
			t.Errorf("Share(%v, %v) = %v", tt.part, tt.whole, got)
		}
	}
}
