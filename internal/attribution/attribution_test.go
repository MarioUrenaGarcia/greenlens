// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package attribution

import (
	"math"
	"math/rand/v2"
	"testing"
	"time"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestPolicies(t *testing.T) {
	n := Node{
		Interval: time.Second, CPUJ: 100, DRAMJ: 10, PlatformJ: 200, HasPlatform: true,
		Idle: Baseline{CPUW: 40, DRAMW: 4, PlatformW: 80}, BusyTicks: 100,
	}
	use := []Usage{{"a", 50}, {"b", 25}}
	tests := []struct {
		p                Policy
		aCPU, aDRAM, aPl float64
	}{
		{Dynamic, 30, 3, 0},      // (100-40) x 0.5
		{Proportional, 50, 5, 0}, // 100 x 0.5
		{Full, 100 * 2 / 3.0, 10 * 2 / 3.0, 0},
		{Wall, 30, 3, 60}, // (200-80) x 0.5
	}
	for _, tt := range tests {
		r := Split(n, use, tt.p)
		if !near(r[0].CPUJ, tt.aCPU) || !near(r[0].DRAMJ, tt.aDRAM) || !near(r[0].PlatformJ, tt.aPl) {
			t.Errorf("%s: %+v", tt.p, r[0])
		}
	}
	// Without system busy time, the consumers' own time is the denominator.
	if r := Split(Node{Interval: time.Second, CPUJ: 10}, use, Dynamic); !near(r[0].Share, 50.0/75) {
		t.Errorf("zero busy ticks: %+v", r)
	}
	if r := Split(Node{Interval: time.Second, CPUJ: 10, HasPlatform: false}, use, Wall); r[0].PlatformJ != 0 || r[0].TotalJ() != r[0].CPUJ+r[0].DRAMJ {
		t.Errorf("wall without platform: %+v", r[0])
	}
}

func TestIdleNodeAttributesNothingUnderDynamic(t *testing.T) {
	r := Split(Node{Interval: time.Second, CPUJ: 30, Idle: Baseline{CPUW: 40}, BusyTicks: 10}, []Usage{{"a", 10}}, Dynamic)
	if r[0].CPUJ != 0 {
		t.Fatalf("below idle: %+v", r[0])
	}
}

// TestNeverExceedsMeasured is the property of attribution: whatever the
// inputs, the attributed energy is non-negative and adds up to at most the
// measured energy.
func TestNeverExceedsMeasured(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < 20000; i++ {
		n := Node{
			Interval:    time.Duration(1+rng.IntN(5000)) * time.Millisecond,
			CPUJ:        rng.Float64() * 500,
			DRAMJ:       rng.Float64() * 50,
			PlatformJ:   rng.Float64() * 900,
			HasPlatform: rng.IntN(2) == 0,
			Idle:        Baseline{CPUW: rng.Float64() * 100, DRAMW: rng.Float64() * 10, PlatformW: rng.Float64() * 200},
			BusyTicks:   rng.Uint64N(1000),
		}
		var use []Usage
		for j := rng.IntN(6); j > 0; j-- {
			use = append(use, Usage{ID: "c", Ticks: rng.Uint64N(800)})
		}
		for _, p := range Policies {
			var cpu, dram, pl, share float64
			for _, r := range Split(n, use, p) {
				if r.CPUJ < 0 || r.DRAMJ < 0 || r.PlatformJ < 0 || r.Share < 0 {
					t.Fatalf("%s: negative %+v", p, r)
				}
				cpu += r.CPUJ
				dram += r.DRAMJ
				pl += r.PlatformJ
				share += r.Share
			}
			const eps = 1e-6
			if cpu > n.CPUJ+eps || dram > n.DRAMJ+eps || pl > n.PlatformJ+eps || share > 1+eps {
				t.Fatalf("%s: attributed cpu %v dram %v platform %v share %v exceeds %+v", p, cpu, dram, pl, share, n)
			}
		}
	}
}

func TestParsePolicy(t *testing.T) {
	for _, p := range Policies {
		if got, err := ParsePolicy(string(p)); err != nil || got != p {
			t.Errorf("ParsePolicy(%q)", p)
		}
	}
	if _, err := ParsePolicy("all"); err == nil {
		t.Error("unknown policy accepted")
	}
}

func TestSplitGPU(t *testing.T) {
	g := GPU{Interval: time.Second, EnergyJ: 100, IdleW: 20, Busy: map[string]float64{"vllm": 60, "ollama": 20}, Other: 20}
	got, m := SplitGPU(g, Dynamic)
	// 80 J above idle, split 60:20:20 with the share of other processes
	// left unattributed.
	if m != MethodGPUProcess || !near(got["vllm"], 48) || !near(got["ollama"], 16) || len(got) != 2 {
		t.Fatalf("%v %s", got, m)
	}
	if got, _ := SplitGPU(g, Full); !near(got["vllm"], 60) {
		t.Fatalf("full: %v", got)
	}
	// Without utilization, the only consumer on the GPU gets all of it.
	got, m = SplitGPU(GPU{Interval: time.Second, EnergyJ: 50, Holders: []string{"llama", "llama"}}, Dynamic)
	if m != MethodGPUProcess || !near(got["llama"], 50) {
		t.Fatalf("%v %s", got, m)
	}
	// Several consumers without utilization share it equally, as an estimate.
	got, m = SplitGPU(GPU{Interval: time.Second, EnergyJ: 30, Holders: []string{"b", "a", "c"}}, Proportional)
	if m != MethodEstimated || !near(got["a"], 10) || !near(got["c"], 10) {
		t.Fatalf("%v %s", got, m)
	}
	if got, m := SplitGPU(GPU{Interval: time.Second, EnergyJ: 30}, Dynamic); got != nil || m != "" {
		t.Fatalf("nobody on the GPU: %v %s", got, m)
	}
	// Below idle nothing is attributed.
	if got, _ := SplitGPU(GPU{Interval: time.Second, EnergyJ: 10, IdleW: 20, Holders: []string{"a"}}, Dynamic); got["a"] != 0 {
		t.Fatalf("%v", got)
	}
}

func TestSplitGPUNeverExceedsMeasured(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 11))
	for range 2000 {
		g := GPU{Interval: time.Duration(rng.IntN(5000)+1) * time.Millisecond, EnergyJ: rng.Float64() * 500, IdleW: rng.Float64() * 50, Other: rng.Float64() * 10, Busy: map[string]float64{}}
		for i := range rng.IntN(4) {
			g.Busy[string(rune('a'+i))] = rng.Float64()*100 - 10
		}
		for i := range rng.IntN(4) {
			g.Holders = append(g.Holders, string(rune('a'+i)))
		}
		for _, p := range Policies {
			got, _ := SplitGPU(g, p)
			sum := 0.0
			for _, e := range got {
				if e < 0 {
					t.Fatalf("negative energy %v for %+v", got, g)
				}
				sum += e
			}
			if sum > g.EnergyJ+1e-9 {
				t.Fatalf("%s: attributed %v of %v J", p, sum, g.EnergyJ)
			}
		}
	}
}
