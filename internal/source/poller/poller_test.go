// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package poller

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLatestFollowsTheFetches(t *testing.T) {
	var n atomic.Int64
	p := New(5*time.Millisecond, func(context.Context) (float64, error) { return float64(100 + n.Add(1)), nil })
	if _, ok, err := p.Latest(); ok || err != nil {
		t.Fatalf("a value before the first fetch: %v %v", ok, err)
	}
	p.Set(100)
	if w, ok, _ := p.Latest(); !ok || w != 100 {
		t.Fatalf("the value of the probe: %v %v", w, ok)
	}
	p.Start()
	p.Start()
	defer p.Stop()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if w, ok, _ := p.Latest(); ok && w >= 103 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the value did not follow the fetches")
		}
		time.Sleep(time.Millisecond)
	}
	p.Stop()
	p.Stop()
	after := n.Load()
	time.Sleep(20 * time.Millisecond)
	if n.Load() != after {
		t.Fatal("fetches after Stop")
	}
}

func TestStaleValuesAreNotReturned(t *testing.T) {
	var mu sync.Mutex
	now := time.Unix(1_800_000_000, 0)
	failing := errors.New("no answer")
	p := New(time.Second, func(context.Context) (float64, error) { return 0, failing })
	p.now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	p.Set(250)
	advance := func(d time.Duration) {
		mu.Lock()
		now = now.Add(d)
		mu.Unlock()
	}
	advance(3 * time.Second)
	if w, ok, err := p.Latest(); !ok || w != 250 || err != nil {
		t.Fatalf("within three intervals: %v %v %v", w, ok, err)
	}
	advance(time.Millisecond)
	if _, ok, err := p.Latest(); ok || err != nil {
		t.Fatalf("a stale value without a failed fetch: %v %v", ok, err)
	}
	p.mu.Lock()
	p.err = failing
	p.mu.Unlock()
	if _, ok, err := p.Latest(); ok || !errors.Is(err, failing) {
		t.Fatalf("a stale value after a failed fetch: %v %v", ok, err)
	}
}

func TestFetchGetsADeadline(t *testing.T) {
	got := make(chan time.Duration, 1)
	p := New(20*time.Millisecond, func(ctx context.Context) (float64, error) {
		d, ok := ctx.Deadline()
		if ok {
			select {
			case got <- time.Until(d):
			default:
			}
		}
		return 1, nil
	})
	p.Start()
	defer p.Stop()
	select {
	case d := <-got:
		if d <= 0 || d > 20*time.Millisecond {
			t.Fatalf("deadline in %s", d)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no fetch with a deadline")
	}
}
