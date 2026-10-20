// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package proxy

import (
	"sync"
	"time"
)

// Meter gives each request through the proxy its part of the energy of
// its backend while it runs. Every sample of the agent splits the energy
// of a backend between its requests in flight by the pieces of output each
// streamed in the sample, or in equal parts when none streamed, as while a
// prompt is processed. It is safe for concurrent use.
type Meter struct {
	mu       sync.Mutex
	now      func() time.Time
	inflight map[*Request]struct{}
	power    map[string]float64
	last     time.Time
}

// Request is one request in flight.
type Request struct {
	backend string
	start   time.Time
	pieces  float64
	energyJ float64
}

// NewMeter returns an empty meter.
func NewMeter() *Meter {
	return &Meter{now: time.Now, inflight: map[*Request]struct{}{}, power: map[string]float64{}}
}

// Begin starts a request to backend.
func (m *Meter) Begin(backend string) *Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := &Request{backend: backend, start: m.now()}
	m.inflight[r] = struct{}{}
	return r
}

// Piece records one piece of streamed output.
func (m *Meter) Piece(r *Request) {
	m.mu.Lock()
	r.pieces++
	m.mu.Unlock()
}

func (m *Meter) shares(backend string) map[*Request]float64 {
	total, n := 0.0, 0
	for r := range m.inflight {
		if r.backend == backend {
			total += r.pieces
			n++
		}
	}
	out := map[*Request]float64{}
	for r := range m.inflight {
		if r.backend != backend {
			continue
		}
		if total > 0 {
			out[r] = r.pieces / total
		} else {
			out[r] = 1 / float64(n)
		}
	}
	return out
}

// Tick hands out the energy of every backend in one sample that ended at
// at and lasted interval.
func (m *Meter) Tick(at time.Time, interval time.Duration, energy map[string]float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	start := at.Add(-interval)
	m.power = map[string]float64{}
	for b, e := range energy {
		if interval > 0 {
			m.power[b] = e / interval.Seconds()
		}
		for r, share := range m.shares(b) {
			// A request that began during the sample gets the part of the
			// sample it was running for.
			f := 1.0
			if r.start.After(start) && interval > 0 {
				f = max(0, at.Sub(r.start).Seconds()/interval.Seconds())
			}
			r.energyJ += e * share * f
		}
	}
	for r := range m.inflight {
		r.pieces = 0
	}
	m.last = at
}

// End finishes a request and returns its energy: what the samples gave it
// plus its share of the time since the last sample at the power of that
// sample.
func (m *Meter) End(r *Request) float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	since := m.last
	if r.start.After(since) {
		since = r.start
	}
	if d := now.Sub(since).Seconds(); d > 0 {
		r.energyJ += m.power[r.backend] * d * m.shares(r.backend)[r]
	}
	delete(m.inflight, r)
	return r.energyJ
}
