// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package agent

import "github.com/MarioUrenaGarcia/greenlens/internal/sampler"

// ring keeps the most recent samples at full resolution for live views,
// without touching the database.
type ring struct {
	buf  []sampler.Sample
	next int
	full bool
}

func newRing(capacity int) *ring {
	return &ring{buf: make([]sampler.Sample, max(capacity, 1))}
}

func (r *ring) add(s sampler.Sample) {
	r.buf[r.next] = s
	r.next = (r.next + 1) % len(r.buf)
	if r.next == 0 {
		r.full = true
	}
}

// all returns the samples from the oldest to the newest.
func (r *ring) all() []sampler.Sample {
	if !r.full {
		return append([]sampler.Sample(nil), r.buf[:r.next]...)
	}
	out := make([]sampler.Sample, 0, len(r.buf))
	out = append(out, r.buf[r.next:]...)
	return append(out, r.buf[:r.next]...)
}
