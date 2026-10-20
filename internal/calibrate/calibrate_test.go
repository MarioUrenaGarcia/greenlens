// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package calibrate

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/sampler"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
	"github.com/MarioUrenaGarcia/greenlens/internal/source/selector"
	"github.com/MarioUrenaGarcia/greenlens/internal/source/sourcetest"
)

func TestPercentile(t *testing.T) {
	v := []float64{5, 1, 4, 2, 3}
	for p, want := range map[float64]float64{0: 1, 50: 3, 100: 5, 10: 1.4, 90: 4.6} {
		if got := Percentile(v, p); math.Abs(got-want) > 1e-9 {
			t.Errorf("P%v = %v, want %v", p, got, want)
		}
	}
	if Percentile([]float64{7}, 90) != 7 || !math.IsNaN(Percentile(nil, 50)) {
		t.Error("edge cases")
	}
}

// constant reports the same power every time.
type constant struct{ w float64 }

func (constant) ID() string    { return "const" }
func (constant) Priority() int { return 1 }
func (constant) Close() error  { return nil }
func (constant) Probe(context.Context) (source.ProbeResult, error) {
	return source.ProbeResult{}, nil
}
func (c constant) Read(context.Context) ([]source.Reading, error) {
	return []source.Reading{{Channel: "const:pkg", PowerW: c.w}}, nil
}

func TestRun(t *testing.T) {
	ch := source.Channel{ID: "const:pkg", Source: "const", Domain: source.DomainPackage, Kind: source.KindPower, Quality: source.QualityMeasured}
	s := sampler.New(selector.Selection{Channels: []source.Channel{ch}}, []source.Source{constant{w: 12}})
	res, err := Run(context.Background(), s, sourcetest.Host(t, "amd-zen4-desktop"), 120*time.Millisecond, 20*time.Millisecond, nil)
	if err != nil {
		t.Fatal(err)
	}
	st := res.Domains[source.DomainPackage]
	if res.Samples < 3 || math.Abs(st.P50-12) > 1e-6 || res.Quality[source.DomainPackage] != "measured" {
		t.Fatalf("result %+v", res)
	}
	if b := res.Baseline(); math.Abs(b.CPUW-12) > 1e-6 || b.DRAMW != 0 {
		t.Errorf("baseline %+v", b)
	}
	// The fixture has k10temp at 47.25 °C, amdgpu at 44 °C (not a CPU sensor).
	if res.TemperatureC != 47.25 {
		t.Errorf("temperature %v", res.TemperatureC)
	}
}

func TestRunWithoutSamples(t *testing.T) {
	s := sampler.New(selector.Selection{}, nil)
	if _, err := Run(context.Background(), s, sourcetest.Host(t, "vm-no-sensors"), 30*time.Millisecond, 10*time.Millisecond, nil); err == nil {
		t.Fatal("no channels must fail")
	}
}

func TestIdleTracker(t *testing.T) {
	tr := NewIdleTracker()
	base := time.Unix(1_700_000_000, 0)
	if _, ok := tr.P5(base); ok {
		t.Fatal("empty tracker returned a value")
	}
	// 95 samples at 30 W and 5 at 10 W: the 5th percentile is the low group.
	for i := 0; i < 95; i++ {
		tr.Add(base.Add(time.Duration(i)*time.Minute), 30)
	}
	for i := 0; i < 5; i++ {
		tr.Add(base.Add(time.Duration(i)*time.Second), 10)
	}
	p5, ok := tr.P5(base.Add(2 * time.Hour))
	if !ok || math.Abs(p5-10.125) > 1e-9 {
		t.Fatalf("p5 = %v", p5)
	}
	// After the window passes, old hours are forgotten.
	tr.Add(base.Add(30*time.Hour), 50)
	p5, _ = tr.P5(base.Add(30 * time.Hour))
	if math.Abs(p5-50.125) > 1e-9 || len(tr.hours) != 1 {
		t.Fatalf("after the window p5 = %v, hours kept %d", p5, len(tr.hours))
	}
	if d := Drift(12, 10); math.Abs(d-0.2) > 1e-9 || Drift(1, 0) != 0 {
		t.Errorf("drift %v", d)
	}
}
