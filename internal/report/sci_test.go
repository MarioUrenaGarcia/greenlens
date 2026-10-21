// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package report

import (
	"math"
	"testing"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/indicators"
	"github.com/MarioUrenaGarcia/greenlens/internal/store"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6*math.Max(1, math.Abs(b)) }

func ptr(v float64) *float64 { return &v }

// The figures are those worked by hand in the SCI explanation of the
// documentation.
func TestBuildSCI(t *testing.T) {
	kwh := indicators.JoulesPerKWh
	f := indicators.Default()
	f.PUE = 1.2
	from := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	qwen := f.Compute(1.71 * kwh)
	in := SCIInput{
		From: from, To: from.Add(24 * time.Hour), GroupBy: store.ByModel, Unit: indicators.UnitGeneratedTokens,
		Embodied: indicators.Embodied{ServerKg: ptr(1200), GPUKg: ptr(300), LifetimeYears: ptr(4)},
		Node:     Node{ServerJ: 1.5 * kwh, GPUJ: 0.9 * kwh, Indicators: f.Compute(2.4 * kwh), GeneratedTokens: 50_000, Requests: 900},
		Groups: []store.UsageEnergy{
			{Backend: "ollama", Kind: "ollama", Model: "qwen", EnergyJ: 1.71 * kwh, GPUEnergyJ: 0.81 * kwh, GeneratedTokens: 40_000, Requests: 700, Indicators: &qwen},
			{Backend: "ollama", Kind: "ollama", Model: "idle", EnergyJ: 0.1 * kwh},
		},
	}
	rep := BuildSCI(in)
	// The node: O = 2.4 x 1.2 x 444 = 1278.72 g, M = 1,500,000 x 24 /
	// 35064 = 1026.69 g, R = 50, SCI = 46.108 g per 1,000 tokens.
	n := rep.Node.SCI
	if !near(n.OperationalG, 1278.72) || n.EmbodiedG == nil || !near(*n.EmbodiedG, 1_500_000*24/35064.0) || n.Units != 50 ||
		!near(*n.GPerUnit, (1278.72+1_500_000*24/35064.0)/50) || len(rep.Missing) != 0 {
		t.Fatalf("node %+v missing %v", n, rep.Missing)
	}
	// qwen: 0.9 of 1.5 kWh of server energy (0.6) and 0.81 of 0.9 kWh of
	// GPU energy (0.9); O = 1.71 x 1.2 x 444 = 911.088 g, M = 677.62 g,
	// SCI = (911.088 + 677.62) / 40 = 39.718.
	q := rep.Rows[0]
	if !near(q.ServerShare, 0.6) || !near(q.GPUShare, 0.9) || !near(q.SCI.OperationalG, 911.088) ||
		!near(*q.SCI.EmbodiedG, 990_000*24/35064.0) || !near(*q.SCI.GPerUnit, (911.088+990_000*24/35064.0)/40) {
		t.Fatalf("qwen %+v", q)
	}
	// A model without tokens has no SCI per unit.
	if rep.Rows[1].SCI.GPerUnit != nil || rep.Rows[1].SCI.Units != 0 {
		t.Fatalf("idle %+v", rep.Rows[1].SCI)
	}
	// Without embodied carbon, SCI is O / R and the report says what is
	// missing.
	in.Embodied = indicators.Embodied{}
	rep = BuildSCI(in)
	if rep.Node.SCI.EmbodiedG != nil || !near(*rep.Node.SCI.GPerUnit, 1278.72/50) || len(rep.Missing) != 3 {
		t.Fatalf("without M %+v %v", rep.Node.SCI, rep.Missing)
	}
	// Per consumer, by backend: the consumers of each backend.
	in.Unit, in.GroupBy, in.Consumers = indicators.UnitConsumer, store.ByBackend, map[string]int64{"ollama": 2}
	in.Node.Consumers = 3
	in.Groups = []store.UsageEnergy{{Backend: "ollama", EnergyJ: 1.71 * kwh, Indicators: &qwen}}
	rep = BuildSCI(in)
	if rep.Node.SCI.Units != 3 || rep.Rows[0].SCI.Units != 2 || !near(*rep.Rows[0].SCI.GPerUnit, 911.088/2) {
		t.Fatalf("per consumer %+v %+v", rep.Node.SCI, rep.Rows[0].SCI)
	}
}
