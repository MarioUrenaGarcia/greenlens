// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package alerts

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"time"
)

// queueSize bounds the notifications waiting for delivery; beyond it new
// ones are dropped and logged, so a sink that hangs cannot grow memory.
const queueSize = 256

// Retries are the waits before each new attempt of a failed delivery.
var Retries = []time.Duration{5 * time.Second, 30 * time.Second, time.Minute}

// Dispatcher delivers notifications to the sinks in the background, so a
// slow sink never holds up the evaluation.
type Dispatcher struct {
	log   *slog.Logger
	queue chan Notification
	mu    sync.Mutex
	sinks []Sink
	wg    sync.WaitGroup
}

// NewDispatcher returns a dispatcher for sinks; Run delivers.
func NewDispatcher(log *slog.Logger, sinks []Sink) *Dispatcher {
	return &Dispatcher{log: log, queue: make(chan Notification, queueSize), sinks: slices.Clone(sinks)}
}

// SetSinks replaces the sinks for the next notifications.
func (d *Dispatcher) SetSinks(sinks []Sink) {
	d.mu.Lock()
	d.sinks = slices.Clone(sinks)
	d.mu.Unlock()
}

// Notify queues a notification and reports whether there was room.
func (d *Dispatcher) Notify(n Notification) bool {
	select {
	case d.queue <- n:
		return true
	default:
		d.log.Warn("alert notification dropped: too many waiting", "rule", n.Rule, "state", n.State)
		return false
	}
}

// Run delivers queued notifications until ctx ends, then waits for the
// deliveries in progress.
func (d *Dispatcher) Run(ctx context.Context) {
	defer d.wg.Wait()
	for {
		select {
		case <-ctx.Done():
			return
		case n := <-d.queue:
			d.mu.Lock()
			sinks := slices.Clone(d.sinks)
			d.mu.Unlock()
			for _, s := range sinks {
				d.wg.Add(1)
				go func() {
					defer d.wg.Done()
					d.deliver(ctx, s, n)
				}()
			}
		}
	}
}

func (d *Dispatcher) deliver(ctx context.Context, s Sink, n Notification) {
	for attempt := 0; ; attempt++ {
		sctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		err := s.Send(sctx, n)
		cancel()
		if err == nil {
			return
		}
		if attempt >= len(Retries) || ctx.Err() != nil {
			d.log.Warn("alert notification failed", "sink", s.Name(), "rule", n.Rule, "state", n.State, "error", err)
			return
		}
		select {
		case <-ctx.Done():
			d.log.Warn("alert notification failed", "sink", s.Name(), "rule", n.Rule, "state", n.State, "error", err)
			return
		case <-time.After(Retries[attempt]):
		}
	}
}
