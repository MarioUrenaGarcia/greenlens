// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package sampler turns the readings of the selected channels into energy
// per interval. Counters are differenced, handling wraparound; power
// channels are integrated with the trapezoidal rule. Intervals are measured
// with the monotonic clock, so changes of the wall clock do not distort
// them.
package sampler

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/help"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
	"github.com/MarioUrenaGarcia/greenlens/internal/source/selector"
)

// ChannelSample is the energy of one channel over one interval.
type ChannelSample struct {
	Channel string        `json:"channel"`
	Domain  source.Domain `json:"domain"`
	EnergyJ float64       `json:"energy_j"`
	PowerW  float64       `json:"power_w"`
}

// Sample is the energy of every selected channel over one interval. A
// channel without a valid value in the interval, such as a battery that
// started charging, is absent.
type Sample struct {
	Time     time.Time       `json:"time"`
	Interval time.Duration   `json:"interval"`
	Channels []ChannelSample `json:"channels"`
}

// DomainEnergy returns the energy of all channels of a domain and whether
// any channel of that domain has a value.
func (s Sample) DomainEnergy(d source.Domain) (float64, bool) {
	total, ok := 0.0, false
	for _, c := range s.Channels {
		if c.Domain == d {
			total += c.EnergyJ
			ok = true
		}
	}
	return total, ok
}

// DomainPower is DomainEnergy divided by the interval.
func (s Sample) DomainPower(d source.Domain) (float64, bool) {
	e, ok := s.DomainEnergy(d)
	if !ok || s.Interval <= 0 {
		return 0, false
	}
	return e / s.Interval.Seconds(), true
}

// ComponentEnergy is the energy of the domains that add up: packages,
// memory and GPUs.
func (s Sample) ComponentEnergy() float64 {
	total := 0.0
	for _, c := range s.Channels {
		if c.Domain.Level() == source.LevelComponent {
			total += c.EnergyJ
		}
	}
	return total
}

type previous struct {
	value float64 // last counter value in J, or last power in W
	ok    bool
}

// Sampler reads the selected channels of a set of sources. Sample and
// WrapRisk may be called from different goroutines.
type Sampler struct {
	mu       sync.Mutex
	sources  []source.Source
	channels map[string]source.Channel
	prev     map[string]previous
	last     time.Time
	maxPower map[string]float64
	now      func() time.Time
	errs     map[string]error
	observe  func(source string, d time.Duration, err error)
}

// Observe sets a function called after each read of a source, with its
// duration and error, for the metrics of the agent.
func (s *Sampler) Observe(f func(source string, d time.Duration, err error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.observe = f
}

// New returns a sampler for the selected channels. sources must include
// every source that owns a selected channel; others are not read.
func New(sel selector.Selection, sources []source.Source) *Sampler {
	s := &Sampler{
		channels: map[string]source.Channel{},
		prev:     map[string]previous{},
		maxPower: map[string]float64{},
		now:      time.Now,
		errs:     map[string]error{},
	}
	owners := map[string]bool{}
	for _, c := range sel.Channels {
		s.channels[c.ID] = c
		owners[c.Source] = true
	}
	for _, src := range sources {
		if owners[src.ID()] {
			s.sources = append(s.sources, src)
		}
	}
	return s
}

// Channels returns the channels the sampler reads.
func (s *Sampler) Channels() map[string]source.Channel { return s.channels }

// Prime takes the first readings, which only serve as the starting point
// of the first interval.
func (s *Sampler) Prime(ctx context.Context) error {
	_, err := s.Sample(ctx)
	return err
}

// Sample reads every source and returns the energy since the previous call.
// The first call after New, or after Prime, returns an empty sample with
// the start time of the next interval. Errors of individual sources are
// returned joined, together with the sample of the channels that could be
// read.
func (s *Sampler) Sample(ctx context.Context) (Sample, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	readings, readErr := s.readAll(ctx)
	now := s.now()
	first := s.last.IsZero()
	dt := now.Sub(s.last)
	s.last = now
	out := Sample{Time: now}
	if !first && dt > 0 {
		out.Interval = dt
	}
	seen := map[string]bool{}
	for _, r := range readings {
		ch, ok := s.channels[r.Channel]
		if !ok {
			continue
		}
		seen[r.Channel] = true
		p := s.prev[r.Channel]
		var e, w float64
		valid := false
		switch ch.Kind {
		case source.KindCounter:
			if p.ok {
				e, valid = counterDelta(p.value, r.EnergyJ, ch.RangeJ)
			}
			if out.Interval > 0 {
				w = e / dt.Seconds()
			}
			s.prev[r.Channel] = previous{value: r.EnergyJ, ok: true}
		case source.KindPower:
			if out.Interval > 0 {
				avg := r.PowerW
				if p.ok {
					avg = (p.value + r.PowerW) / 2
				}
				// The power is the average itself: dividing the energy by
				// the interval again would round it.
				e, w, valid = avg*dt.Seconds(), avg, true
			}
			s.prev[r.Channel] = previous{value: r.PowerW, ok: true}
		}
		if !valid || out.Interval <= 0 {
			continue
		}
		if w > s.maxPower[r.Channel] {
			s.maxPower[r.Channel] = w
		}
		out.Channels = append(out.Channels, ChannelSample{Channel: r.Channel, Domain: ch.Domain, EnergyJ: e, PowerW: w})
	}
	// A channel without a reading starts over: integrating across the gap
	// would invent energy.
	for id := range s.channels {
		if !seen[id] {
			delete(s.prev, id)
		}
	}
	return out, readErr
}

func (s *Sampler) readAll(ctx context.Context) ([]source.Reading, error) {
	var out []source.Reading
	var errs []error
	for _, src := range s.sources {
		start := time.Now()
		rs, err := src.Read(ctx)
		if s.observe != nil {
			s.observe(src.ID(), time.Since(start), err)
		}
		out = append(out, rs...)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", src.ID(), err))
		}
	}
	return out, errors.Join(errs...)
}

// counterDelta returns the increase of a counter. When the value goes down
// on a counter with a known range, it wrapped once; on a counter without a
// range it was reset, and the interval has no valid value.
func counterDelta(prev, cur, rangeJ float64) (float64, bool) {
	if cur >= prev {
		return cur - prev, true
	}
	if rangeJ > 0 && prev <= rangeJ {
		return rangeJ - prev + cur, true
	}
	return 0, false
}

// WrapRisk lists the counters that can wrap more than once within interval
// at the highest power seen so far, which would make their deltas wrong.
// Each problem carries the interval below which the channel is safe.
func (s *Sampler) WrapRisk(interval time.Duration) []source.Problem {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []source.Problem
	for id, ch := range s.channels {
		w := s.maxPower[id]
		if ch.Kind != source.KindCounter || ch.RangeJ <= 0 || w <= 0 {
			continue
		}
		period := time.Duration(ch.RangeJ / w * float64(time.Second))
		// Half the wrap period leaves margin for power peaks between
		// samples.
		limit := period / 2
		if interval < limit {
			continue
		}
		out = append(out, source.Problem{
			Code:    help.GL1008,
			Channel: id,
			Args: []any{
				"channel", id, "range", fmt.Sprintf("%.0f J", ch.RangeJ), "period", period.Round(time.Second),
				"interval", interval, "limit", limit.Round(time.Second),
			},
		})
	}
	return out
}
