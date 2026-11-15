// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package poller reads a power value over the network in the background.
// A management controller or a smart plug may take a second to answer, or
// not answer at all, while the sampler reads every source in turn within
// its interval; with a poller the source returns at once the latest value
// it has.
package poller

import (
	"context"
	"sync"
	"time"
)

// staleAfter is how many intervals a value stays usable. Beyond it the
// channel has no reading, so the sampler does not integrate across a gap
// in which the power is unknown.
const staleAfter = 3

// Fetch reads the power now, in watts.
type Fetch func(ctx context.Context) (float64, error)

// Poller calls a Fetch every interval and keeps its latest value.
type Poller struct {
	interval time.Duration
	fetch    Fetch
	now      func() time.Time

	mu     sync.Mutex
	watts  float64
	at     time.Time
	err    error
	cancel context.CancelFunc
	done   chan struct{}
}

// New returns a stopped poller. Each fetch gets the interval as its
// deadline, so a slow device never delays the next one.
func New(interval time.Duration, fetch Fetch) *Poller {
	return &Poller{interval: interval, fetch: fetch, now: time.Now}
}

// Set records a value that was read outside the loop, such as the one of
// the probe.
func (p *Poller) Set(watts float64) {
	p.mu.Lock()
	p.watts, p.at, p.err = watts, p.now(), nil
	p.mu.Unlock()
}

// Start begins polling. It does nothing when the poller already runs.
func (p *Poller) Start() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel, p.done = cancel, make(chan struct{})
	go p.loop(ctx, p.done)
}

func (p *Poller) loop(ctx context.Context, done chan struct{}) {
	defer close(done)
	t := time.NewTicker(p.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		fctx, cancel := context.WithTimeout(ctx, p.interval)
		w, err := p.fetch(fctx)
		cancel()
		if ctx.Err() != nil {
			return
		}
		p.mu.Lock()
		if err != nil {
			p.err = err
		} else {
			p.watts, p.at, p.err = w, p.now(), nil
		}
		p.mu.Unlock()
	}
}

// Stop ends the polling and waits for the fetch in flight.
func (p *Poller) Stop() {
	p.mu.Lock()
	cancel, done := p.cancel, p.done
	p.cancel, p.done = nil, nil
	p.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

// Latest returns the latest value while it is fresh. Without a fresh
// value it returns false and the error of the latest fetch, if it failed.
func (p *Poller) Latest() (float64, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.at.IsZero() && p.now().Sub(p.at) <= staleAfter*p.interval {
		return p.watts, true, nil
	}
	return 0, false, p.err
}
