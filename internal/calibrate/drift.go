// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package calibrate

import (
	"math"
	"time"
)

// IdleTracker estimates the current idle power as the 5th percentile of the
// power seen over the last 24 hours. It keeps one histogram per hour with
// bins of BinW watts, so memory stays bounded however long the agent runs.
type IdleTracker struct {
	hours map[int64]map[int]uint32
}

// BinW is the resolution of the tracker.
const BinW = 0.25

// Window is the period the tracker covers.
const Window = 24 * time.Hour

// NewIdleTracker returns an empty tracker.
func NewIdleTracker() *IdleTracker { return &IdleTracker{hours: map[int64]map[int]uint32{}} }

// Add records the power of one sample.
func (t *IdleTracker) Add(at time.Time, w float64) {
	if w < 0 || math.IsNaN(w) {
		return
	}
	h := at.Unix() / 3600
	bins := t.hours[h]
	if bins == nil {
		bins = map[int]uint32{}
		t.hours[h] = bins
		for old := range t.hours {
			if old <= h-int64(Window/time.Hour) {
				delete(t.hours, old)
			}
		}
	}
	bins[int(w/BinW)]++
}

// P5 returns the 5th percentile over the window ending at now, and false
// when nothing was recorded.
func (t *IdleTracker) P5(now time.Time) (float64, bool) {
	from := now.Unix()/3600 - int64(Window/time.Hour) + 1
	merged := map[int]uint32{}
	var total uint64
	maxBin := 0
	for h, bins := range t.hours {
		if h < from {
			continue
		}
		for b, n := range bins {
			merged[b] += n
			total += uint64(n)
			maxBin = max(maxBin, b)
		}
	}
	if total == 0 {
		return 0, false
	}
	target := uint64(math.Ceil(float64(total) * 0.05))
	var acc uint64
	for b := 0; b <= maxBin; b++ {
		acc += uint64(merged[b])
		if acc >= target {
			return (float64(b) + 0.5) * BinW, true
		}
	}
	return (float64(maxBin) + 0.5) * BinW, true
}

// Drift is the relative difference between the current idle estimate and a
// calibrated baseline. A value above 0.2 means the calibration no longer
// describes the machine, for example after a hardware or BIOS change.
func Drift(current, baseline float64) float64 {
	if baseline <= 0 {
		return 0
	}
	return math.Abs(current-baseline) / baseline
}
