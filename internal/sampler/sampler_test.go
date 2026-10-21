// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package sampler

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/help"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
	"github.com/MarioUrenaGarcia/greenlens/internal/source/selector"
)

// scripted returns one prepared set of readings per call.
type scripted struct {
	id    string
	steps [][]source.Reading
	err   error
	n     int
}

func (s *scripted) ID() string    { return s.id }
func (s *scripted) Priority() int { return 1 }
func (s *scripted) Close() error  { return nil }
func (s *scripted) Probe(context.Context) (source.ProbeResult, error) {
	return source.ProbeResult{}, nil
}
func (s *scripted) Read(context.Context) ([]source.Reading, error) {
	if s.n >= len(s.steps) {
		return nil, s.err
	}
	r := s.steps[s.n]
	s.n++
	return r, s.err
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }
func (c *clock) advance(d time.Duration) {
	c.t = c.t.Add(d)
}

func setup(src *scripted, chs ...source.Channel) (*Sampler, *clock) {
	sel := selector.Selection{Channels: chs}
	s := New(sel, []source.Source{src})
	c := &clock{t: time.Unix(1_700_000_000, 0)}
	s.now = c.now
	return s, c
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestCounterWraps(t *testing.T) {
	ch := source.Channel{ID: "c", Source: "x", Domain: source.DomainPackage, Kind: source.KindCounter, RangeJ: 1000}
	src := &scripted{id: "x", steps: [][]source.Reading{
		{{Channel: "c", EnergyJ: 900}},
		{{Channel: "c", EnergyJ: 950}},
		{{Channel: "c", EnergyJ: 30}}, // wrapped: 50 to the top, then 30
	}}
	s, c := setup(src, ch)
	if err := s.Prime(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.advance(time.Second)
	got, err := s.Sample(context.Background())
	if err != nil || len(got.Channels) != 1 || !near(got.Channels[0].EnergyJ, 50) || got.Interval != time.Second {
		t.Fatalf("first interval %+v %v", got, err)
	}
	c.advance(2 * time.Second)
	got, _ = s.Sample(context.Background())
	if !near(got.Channels[0].EnergyJ, 80) || !near(got.Channels[0].PowerW, 40) {
		t.Fatalf("wrapped interval %+v", got)
	}
}

func TestCounterResetWithoutRangeIsDropped(t *testing.T) {
	ch := source.Channel{ID: "c", Source: "x", Domain: source.DomainPackage, Kind: source.KindCounter}
	src := &scripted{id: "x", steps: [][]source.Reading{{{Channel: "c", EnergyJ: 100}}, {{Channel: "c", EnergyJ: 5}}}}
	s, c := setup(src, ch)
	_ = s.Prime(context.Background())
	c.advance(time.Second)
	got, _ := s.Sample(context.Background())
	if len(got.Channels) != 0 {
		t.Fatalf("reset counter produced %+v", got.Channels)
	}
}

func TestPowerTrapezoidAndGaps(t *testing.T) {
	ch := source.Channel{ID: "p", Source: "x", Domain: source.DomainPlatform, Kind: source.KindPower}
	src := &scripted{id: "x", steps: [][]source.Reading{
		{{Channel: "p", PowerW: 10}},
		{{Channel: "p", PowerW: 30}},
		{}, // no value: charging
		{{Channel: "p", PowerW: 20}},
	}}
	s, c := setup(src, ch)
	_ = s.Prime(context.Background())
	c.advance(time.Second)
	got, _ := s.Sample(context.Background())
	if !near(got.Channels[0].EnergyJ, 20) {
		t.Fatalf("trapezoid %+v", got)
	}
	c.advance(time.Second)
	got, _ = s.Sample(context.Background())
	if len(got.Channels) != 0 {
		t.Fatalf("gap produced %+v", got)
	}
	c.advance(time.Second)
	got, _ = s.Sample(context.Background())
	// After a gap the integration restarts from the current value only.
	if !near(got.Channels[0].EnergyJ, 20) {
		t.Fatalf("after gap %+v", got)
	}
}

// A constant power reads back exactly, whatever the length of the interval.
func TestPowerIsNotRounded(t *testing.T) {
	ch := source.Channel{ID: "p", Source: "x", Domain: source.DomainPlatform, Kind: source.KindPower}
	var steps [][]source.Reading
	for range 200 {
		steps = append(steps, []source.Reading{{Channel: "p", PowerW: 3}})
	}
	s, c := setup(&scripted{id: "x", steps: steps}, ch)
	_ = s.Prime(context.Background())
	for i := range 199 {
		c.advance(50*time.Millisecond + time.Duration(i)*time.Microsecond*137)
		got, _ := s.Sample(context.Background())
		if got.Channels[0].PowerW != 3 {
			t.Fatalf("interval %d: power %v", i, got.Channels[0].PowerW)
		}
	}
}

func TestErrorsKeepOtherChannels(t *testing.T) {
	ch := source.Channel{ID: "c", Source: "x", Domain: source.DomainDRAM, Kind: source.KindCounter}
	src := &scripted{id: "x", err: errors.New("boom"), steps: [][]source.Reading{{{Channel: "c", EnergyJ: 1}}, {{Channel: "c", EnergyJ: 3}}}}
	s, c := setup(src, ch)
	_ = s.Prime(context.Background())
	c.advance(time.Second)
	got, err := s.Sample(context.Background())
	if err == nil || len(got.Channels) != 1 || !near(got.Channels[0].EnergyJ, 2) {
		t.Fatalf("got %+v err %v", got, err)
	}
	if e, ok := got.DomainEnergy(source.DomainDRAM); !ok || !near(e, 2) {
		t.Error("DomainEnergy")
	}
	if w, ok := got.DomainPower(source.DomainDRAM); !ok || !near(w, 2) {
		t.Error("DomainPower")
	}
}

func TestUnselectedChannelsAreIgnored(t *testing.T) {
	ch := source.Channel{ID: "a", Source: "x", Domain: source.DomainPackage, Kind: source.KindCounter}
	src := &scripted{id: "x", steps: [][]source.Reading{
		{{Channel: "a", EnergyJ: 1}, {Channel: "b", EnergyJ: 1}},
		{{Channel: "a", EnergyJ: 2}, {Channel: "b", EnergyJ: 9}},
	}}
	s, c := setup(src, ch)
	_ = s.Prime(context.Background())
	c.advance(time.Second)
	got, _ := s.Sample(context.Background())
	if len(got.Channels) != 1 || got.ComponentEnergy() != 1 {
		t.Fatalf("got %+v", got)
	}
}

func TestWrapRisk(t *testing.T) {
	// A 1000 J range at 100 W wraps every 10 s; intervals of 5 s or more
	// are at risk.
	ch := source.Channel{ID: "c", Source: "x", Domain: source.DomainPackage, Kind: source.KindCounter, RangeJ: 1000}
	src := &scripted{id: "x", steps: [][]source.Reading{{{Channel: "c", EnergyJ: 0}}, {{Channel: "c", EnergyJ: 100}}}}
	s, c := setup(src, ch)
	_ = s.Prime(context.Background())
	c.advance(time.Second)
	_, _ = s.Sample(context.Background())
	if p := s.WrapRisk(time.Second); len(p) != 0 {
		t.Fatalf("1 s flagged: %+v", p)
	}
	p := s.WrapRisk(6 * time.Second)
	if len(p) != 1 || p[0].Code != help.GL1008 {
		t.Fatalf("6 s not flagged: %+v", p)
	}
}

func TestCounterDelta(t *testing.T) {
	for _, tt := range []struct {
		prev, cur, rng, want float64
		ok                   bool
	}{
		{1, 3, 0, 2, true},
		{3, 3, 10, 0, true},
		{9, 1, 10, 2, true},
		{9, 1, 0, 0, false},
		{20, 1, 10, 0, false},
	} {
		got, ok := counterDelta(tt.prev, tt.cur, tt.rng)
		if ok != tt.ok || !near(got, tt.want) {
			t.Errorf("counterDelta(%v, %v, %v) = %v %v", tt.prev, tt.cur, tt.rng, got, ok)
		}
	}
}
