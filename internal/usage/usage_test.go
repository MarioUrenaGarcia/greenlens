// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package usage

import (
	"errors"
	"math"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/attribution"
	"github.com/MarioUrenaGarcia/greenlens/internal/backends"
	"github.com/MarioUrenaGarcia/greenlens/internal/connector"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

var t0 = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

func node(cpuJ float64, busy uint64) attribution.Node {
	return attribution.Node{Interval: time.Second, CPUJ: cpuJ, DRAMJ: cpuJ / 10, BusyTicks: busy}
}

func TestAccountant(t *testing.T) {
	a := NewAccountant(attribution.Dynamic, t0)
	a.Add(Sample{
		Node:    node(100, 100),
		Ticks:   map[string]uint64{"vllm-8000": 50, "ollama-11434": 25},
		Quality: source.QualityMeasured,
		GPUs:    []GPU{{GPU: attribution.GPU{Interval: time.Second, EnergyJ: 80, Busy: map[string]float64{"vllm-8000": 90}, Other: 10}, Quality: source.QualityMeasured}},
	})
	a.Add(Sample{Node: node(100, 100), Ticks: map[string]uint64{"vllm-8000": 50}, Quality: source.QualityEstimated})
	c := a.Cut(t0.Add(Interval))
	v, o := c.Backends["vllm-8000"], c.Backends["ollama-11434"]
	if !near(v.CPUJ, 100) || !near(v.DRAMJ, 10) || !near(v.GPUJ, 72) || v.Method != MethodGPUProcess || v.Quality != source.QualityEstimated {
		t.Fatalf("vllm %+v", v)
	}
	if !near(o.CPUJ, 25) || o.GPUJ != 0 || o.Method != MethodCPUShare || o.Quality != source.QualityMeasured {
		t.Fatalf("ollama %+v", o)
	}
	if c.Quality != source.QualityEstimated || !c.Start.Equal(t0) {
		t.Fatalf("cut %+v", c)
	}
	if c2 := a.Cut(t0.Add(2 * Interval)); len(c2.Backends) != 0 || !c2.Start.Equal(t0.Add(Interval)) || c2.Quality != source.QualityMeasured {
		t.Fatalf("second cut %+v", c2)
	}
}

func reading(models ...connector.Counters) backends.Reading {
	return backends.Reading{Snapshot: connector.Snapshot{TokenCounters: true, Models: models}}
}

func cut(start time.Time, e map[string]Energy) Cut {
	return Cut{Start: start, End: start.Add(Interval), Backends: e, Quality: source.QualityMeasured}
}

func TestSplitByTokens(t *testing.T) {
	s := NewSplitter(50)
	e := Energy{CPUJ: 30, GPUJ: 60, Method: MethodGPUProcess, Quality: source.QualityMeasured}
	// The first reading only sets the starting point.
	rows := s.Split(cut(t0, map[string]Energy{"vllm": e}), map[string]backends.Reading{"vllm": reading(
		connector.Counters{Model: "qwen", PromptTokens: 1000, GeneratedTokens: 500},
		connector.Counters{Model: "llama", PromptTokens: 10, GeneratedTokens: 10},
	)})
	if len(rows) != 1 || rows[0].Model != "" || rows[0].PromptTokens != nil || !near(rows[0].TotalJ(), 90) {
		t.Fatalf("first interval %+v", rows)
	}
	// qwen processed 200 tokens and llama 100.
	rows = s.Split(cut(t0.Add(Interval), map[string]Energy{"vllm": e}), map[string]backends.Reading{"vllm": reading(
		connector.Counters{Model: "qwen", PromptTokens: 1100, GeneratedTokens: 600, Requests: 2},
		connector.Counters{Model: "llama", PromptTokens: 60, GeneratedTokens: 60, Requests: 1},
	)})
	if len(rows) != 2 {
		t.Fatalf("rows %+v", rows)
	}
	l, q := rows[0], rows[1]
	if l.Model != "llama" || q.Model != "qwen" || !near(q.TotalJ(), 60) || !near(l.TotalJ(), 30) || q.Method != MethodTokenWeighted {
		t.Fatalf("rows %+v %+v", l, q)
	}
	if *q.PromptTokens != 100 || *q.GeneratedTokens != 100 || *q.Requests != 2 || *l.PromptTokens != 50 {
		t.Fatalf("tokens %d %d %d %d", *q.PromptTokens, *q.GeneratedTokens, *q.Requests, *l.PromptTokens)
	}
	// One active model takes all the energy and keeps the method.
	rows = s.Split(cut(t0.Add(2*Interval), map[string]Energy{"vllm": e}), map[string]backends.Reading{"vllm": reading(
		connector.Counters{Model: "qwen", PromptTokens: 1200, GeneratedTokens: 700, Requests: 3},
		connector.Counters{Model: "llama", PromptTokens: 60, GeneratedTokens: 60, Requests: 1},
	)})
	if len(rows) != 1 || rows[0].Model != "qwen" || !near(rows[0].TotalJ(), 90) || rows[0].Method != MethodGPUProcess {
		t.Fatalf("rows %+v", rows)
	}
	// A restart starts the counters over.
	rows = s.Split(cut(t0.Add(3*Interval), map[string]Energy{"vllm": e}), map[string]backends.Reading{"vllm": reading(
		connector.Counters{Model: "qwen", PromptTokens: 5, GeneratedTokens: 5, Requests: 1},
	)})
	if len(rows) != 1 || *rows[0].PromptTokens != 5 {
		t.Fatalf("after restart %+v", rows)
	}
}

func TestSplitWithoutTokens(t *testing.T) {
	s := NewSplitter(50)
	e := Energy{CPUJ: 10, Method: MethodCPUShare, Quality: source.QualityMeasured}
	// Ollama does not count tokens; with one model loaded, it gets the energy.
	rows := s.Split(cut(t0, map[string]Energy{"ollama": e}), map[string]backends.Reading{"ollama": {Snapshot: connector.Snapshot{Loaded: []string{"qwen2.5:0.5b"}}}})
	if len(rows) != 1 || rows[0].Model != "qwen2.5:0.5b" || rows[0].PromptTokens != nil {
		t.Fatalf("%+v", rows)
	}
	rows = s.Split(cut(t0, map[string]Energy{"ollama": e}), map[string]backends.Reading{"ollama": {Snapshot: connector.Snapshot{Loaded: []string{"a", "b"}}}})
	if len(rows) != 1 || rows[0].Model != "" {
		t.Fatalf("two models loaded: %+v", rows)
	}
	rows = s.Split(cut(t0, map[string]Energy{"proc": e}), map[string]backends.Reading{"x": {Err: errors.New("refused")}})
	if len(rows) != 1 || rows[0].Backend != "proc" || rows[0].Model != "" {
		t.Fatalf("%+v", rows)
	}
	// Tokens without energy still count; the row takes the quality of the
	// interval.
	s.Split(cut(t0, nil), map[string]backends.Reading{"idle": reading(connector.Counters{Model: "m"})})
	rows = s.Split(cut(t0.Add(Interval), nil), map[string]backends.Reading{"idle": reading(connector.Counters{Model: "m", GeneratedTokens: 3})})
	if len(rows) != 1 || rows[0].TotalJ() != 0 || *rows[0].GeneratedTokens != 3 || rows[0].Method != MethodCPUShare || rows[0].Quality != source.QualityMeasured {
		t.Fatalf("%+v", rows)
	}
}

func TestCountersFromZero(t *testing.T) {
	s := NewSplitter(50)
	rd := reading(connector.Counters{Model: "m", Consumer: "team-a", GeneratedTokens: 7})
	rd.Snapshot.FromZero = true
	rows := s.Split(cut(t0, map[string]Energy{"ollama": {CPUJ: 3, Method: MethodCPUShare}}), map[string]backends.Reading{"ollama": rd})
	if len(rows) != 1 || rows[0].Consumer != "team-a" || *rows[0].GeneratedTokens != 7 {
		t.Fatalf("%+v", rows)
	}
}

func TestModelLimit(t *testing.T) {
	s := NewSplitter(2)
	prev := reading(connector.Counters{Model: "a"}, connector.Counters{Model: "b"}, connector.Counters{Model: "c"}, connector.Counters{Model: "d"})
	s.Split(cut(t0, nil), map[string]backends.Reading{"x": prev})
	rows := s.Split(cut(t0.Add(Interval), map[string]Energy{"x": {CPUJ: 8, Method: MethodCPUShare}}), map[string]backends.Reading{"x": reading(
		connector.Counters{Model: "a", GeneratedTokens: 1}, connector.Counters{Model: "b", GeneratedTokens: 1},
		connector.Counters{Model: "c", GeneratedTokens: 1}, connector.Counters{Model: "d", GeneratedTokens: 1},
	)})
	got := map[string]float64{}
	for _, r := range rows {
		got[r.Model] = r.TotalJ()
	}
	if len(got) != 3 || !near(got["a"], 2) || !near(got["b"], 2) || !near(got[Other], 4) {
		t.Fatalf("%v", got)
	}
}

// The energy of the rows of an interval is never more than the energy of
// the backends, however tokens fall.
func TestRowsNeverExceedEnergy(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 5))
	s := NewSplitter(3)
	counters := map[string]float64{}
	for i := range 500 {
		e := Energy{CPUJ: rng.Float64() * 100, GPUJ: rng.Float64() * 100, Method: MethodCPUShare}
		var models []connector.Counters
		for _, m := range []string{"a", "b", "c", "d", "e"} {
			if rng.IntN(3) == 0 {
				counters[m] += float64(rng.IntN(1000))
			}
			if rng.IntN(20) == 0 {
				counters[m] = 0
			}
			models = append(models, connector.Counters{Model: m, GeneratedTokens: counters[m]})
		}
		rows := s.Split(cut(t0.Add(time.Duration(i)*Interval), map[string]Energy{"x": e}), map[string]backends.Reading{"x": reading(models...)})
		sum := 0.0
		for _, r := range rows {
			sum += r.TotalJ()
		}
		if sum > e.TotalJ()+1e-9 || (e.TotalJ() > 0 && !near(sum, e.TotalJ())) {
			t.Fatalf("interval %d: rows %v J of %v J", i, sum, e.TotalJ())
		}
	}
}

func TestRegress(t *testing.T) {
	rng := rand.New(rand.NewPCG(9, 9))
	var obs []Observation
	for range 120 {
		p, g := float64(rng.IntN(3000)), float64(rng.IntN(400))
		obs = append(obs, Observation{PromptTokens: p, GeneratedTokens: g, EnergyJ: 0.01*p + 0.4*g + 50 + rng.NormFloat64()*3})
	}
	r, err := Regress(obs, 0.7)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(r.PromptJ-0.01) > 2*r.PromptCI95 || math.Abs(r.GeneratedJ-0.4) > 2*r.GeneratedCI95 || math.Abs(r.BaseW-5) > 1 || r.R2 < 0.9 || r.Intervals != 120 || r.Method != MethodRegression {
		t.Fatalf("%+v", r)
	}
	if _, err := Regress(obs[:10], 0.7); !errors.Is(err, ErrFewIntervals) {
		t.Fatalf("few: %v", err)
	}
	// Energy unrelated to tokens is not reported.
	noise := make([]Observation, len(obs))
	for i, o := range obs {
		noise[i] = Observation{PromptTokens: o.PromptTokens, GeneratedTokens: o.GeneratedTokens, EnergyJ: 100 + rng.NormFloat64()*30}
	}
	if _, err := Regress(noise, 0.7); !errors.Is(err, ErrPoorFit) {
		t.Fatalf("noise: %v", err)
	}
}
