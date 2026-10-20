// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package attribution

import (
	"math"
	"slices"
	"time"
)

// Methods of GPU attribution.
const (
	// MethodGPUProcess splits the energy of a GPU by the use the driver
	// reports for each process, or gives it all to the only consumer
	// running on it.
	MethodGPUProcess = "attributed_gpu_process"
	// MethodEstimated splits the energy of a GPU in equal parts between
	// the consumers running on it, when the driver reports no use per
	// process.
	MethodEstimated = "estimated"
)

// GPU is the energy of one GPU over one interval and what is known of the
// consumers that used it.
type GPU struct {
	Interval time.Duration
	EnergyJ  float64
	// IdleW is the power of the GPU at rest, from calibration.
	IdleW float64
	// Busy is the use each consumer made of the GPU, in any unit, from the
	// per-process utilization of the driver; Other is the use by processes
	// of no consumer.
	Busy  map[string]float64
	Other float64
	// Holders are the consumers with a context on the GPU.
	Holders []string
}

// SplitGPU attributes the energy of one GPU and returns the energy of each
// consumer with the method used; the method is empty when nothing can be
// attributed. Under the dynamic and wall policies only the energy above
// idle is attributed. The sum never exceeds the energy measured.
func SplitGPU(g GPU, p Policy) (map[string]float64, string) {
	e := math.Max(0, g.EnergyJ)
	if p == Dynamic || p == Wall {
		e = math.Max(0, e-g.IdleW*g.Interval.Seconds())
	}
	total := g.Other
	for _, w := range g.Busy {
		total += math.Max(0, w)
	}
	out := map[string]float64{}
	if total > 0 {
		for id, w := range g.Busy {
			if w > 0 {
				out[id] = e * w / total
			}
		}
		return out, MethodGPUProcess
	}
	holders := slices.Compact(slices.Sorted(slices.Values(g.Holders)))
	switch len(holders) {
	case 0:
		return nil, ""
	case 1:
		out[holders[0]] = e
		return out, MethodGPUProcess
	}
	for _, id := range holders {
		out[id] = e / float64(len(holders))
	}
	return out, MethodEstimated
}
