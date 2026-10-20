// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package attribution splits the energy of the node between the processes
// that used it. A process gets the part of the energy that matches its
// share of the CPU time of the interval:
//
//	share_b = cputime_b / cputime_busy
//	E_b     = E_attributable x share_b
//
// The idle policy decides what the attributable energy is.
package attribution

import (
	"fmt"
	"math"
	"time"
)

// Policy decides how idle energy is treated.
type Policy string

// Policies.
const (
	// Dynamic attributes only the energy above the idle baseline.
	Dynamic Policy = "dynamic"
	// Proportional also spreads the idle energy by CPU share.
	Proportional Policy = "proportional"
	// Full gives all the energy of the node to the active consumers, for
	// nodes dedicated to inference.
	Full Policy = "full"
	// Wall starts from a platform or wall measurement, so the energy of the
	// rest of the system is also attributed. Without such a measurement it
	// behaves as Dynamic.
	Wall Policy = "wall"
)

// Policies lists every policy.
var Policies = []Policy{Dynamic, Proportional, Full, Wall}

// ParsePolicy validates a policy name.
func ParsePolicy(s string) (Policy, error) {
	for _, p := range Policies {
		if string(p) == s {
			return p, nil
		}
	}
	return "", fmt.Errorf("unknown idle policy %q", s)
}

// Method is reported with every attributed value.
const Method = "attributed_cpu_share"

// Baseline is the idle power of the node, from calibration.
type Baseline struct {
	CPUW      float64 `json:"cpu_w"`
	DRAMW     float64 `json:"dram_w"`
	PlatformW float64 `json:"platform_w"`
}

// Node is the energy of the node over one interval.
type Node struct {
	Interval    time.Duration
	CPUJ        float64
	DRAMJ       float64
	PlatformJ   float64
	HasPlatform bool
	Idle        Baseline
	// BusyTicks is the CPU time of all the work of the system in the
	// interval, in clock ticks.
	BusyTicks uint64
}

// Usage is the CPU time of one consumer in the interval, in clock ticks.
type Usage struct {
	ID    string
	Ticks uint64
}

// Result is the energy attributed to one consumer.
type Result struct {
	ID        string  `json:"id"`
	Share     float64 `json:"share"`
	CPUJ      float64 `json:"cpu_j"`
	DRAMJ     float64 `json:"dram_j"`
	PlatformJ float64 `json:"platform_j,omitempty"`
}

// TotalJ is the energy attributed to the consumer. Under the wall policy it
// is the platform energy; otherwise processor plus memory.
func (r Result) TotalJ() float64 {
	if r.PlatformJ > 0 {
		return r.PlatformJ
	}
	return r.CPUJ + r.DRAMJ
}

// Split attributes the energy of n to the consumers. The sum of what is
// attributed never exceeds what was measured.
func Split(n Node, usage []Usage, p Policy) []Result {
	var sum uint64
	for _, u := range usage {
		sum += u.Ticks
	}
	// Accounting of process and system time is not perfectly simultaneous;
	// the larger denominator keeps the shares from adding up to more than 1.
	denom := max(n.BusyTicks, sum)
	if p == Full {
		denom = sum
	}
	dt := n.Interval.Seconds()
	above := func(e, idleW float64) float64 { return math.Max(0, e-idleW*dt) }
	cpu, dram := above(n.CPUJ, n.Idle.CPUW), above(n.DRAMJ, n.Idle.DRAMW)
	if p == Proportional || p == Full {
		cpu, dram = math.Max(0, n.CPUJ), math.Max(0, n.DRAMJ)
	}
	platform := 0.0
	if p == Wall && n.HasPlatform {
		platform = above(n.PlatformJ, n.Idle.PlatformW)
	}

	out := make([]Result, 0, len(usage))
	for _, u := range usage {
		share := 0.0
		if denom > 0 {
			share = float64(u.Ticks) / float64(denom)
		}
		out = append(out, Result{
			ID:        u.ID,
			Share:     share,
			CPUJ:      cpu * share,
			DRAMJ:     dram * share,
			PlatformJ: platform * share,
		})
	}
	return out
}
