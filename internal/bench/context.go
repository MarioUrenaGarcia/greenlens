// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package bench

import (
	"fmt"
	"io/fs"
	"path"
	"regexp"

	"github.com/MarioUrenaGarcia/greenlens/internal/calibrate"
	"github.com/MarioUrenaGarcia/greenlens/internal/hostfs"
	"github.com/MarioUrenaGarcia/greenlens/internal/source/estimate"
)

// Context describes the machine during a benchmark, so results from
// different days or machines can be judged comparable.
type Context struct {
	CPU          string   `json:"cpu,omitempty"`
	GPUs         []string `json:"gpus,omitempty"`
	Kernel       string   `json:"kernel,omitempty"`
	NVIDIADriver string   `json:"nvidia_driver,omitempty"`
	Governor     string   `json:"governor,omitempty"`
	FrequencyMHz float64  `json:"frequency_mhz,omitempty"`
	StartTempC   float64  `json:"start_temperature_c,omitempty"`
	EndTempC     float64  `json:"end_temperature_c,omitempty"`
	// Throttled is nil when the processor does not report throttling.
	Throttled *bool `json:"throttled,omitempty"`
	// OtherCPUShare is the part of the busy CPU time of the machine that
	// was not the backend.
	OtherCPUShare float64  `json:"other_cpu_share"`
	Channels      []string `json:"energy_channels,omitempty"`
}

var nvidiaVersion = regexp.MustCompile(`\s(\d+\.\d+(?:\.\d+)?)\s`)

// Snapshot reads what is known of the machine before a benchmark.
func Snapshot(fsys fs.FS) Context {
	var c Context
	if cpu, err := estimate.ReadCPU(fsys); err == nil {
		c.CPU = cpu.ModelName
	}
	c.Kernel, _ = hostfs.String(fsys, "proc/sys/kernel/osrelease")
	if v, err := hostfs.String(fsys, "proc/driver/nvidia/version"); err == nil {
		if m := nvidiaVersion.FindStringSubmatch(v); m != nil {
			c.NVIDIADriver = m[1]
		}
	}
	c.FrequencyMHz, c.Governor = calibrate.CPUFrequency(fsys)
	c.StartTempC = calibrate.Temperature(fsys)
	return c
}

// Throttles counts the thermal throttling events of every processor, from
// the counters Intel processors expose; ok is false without them.
func Throttles(fsys fs.FS) (n uint64, ok bool) {
	online, err := hostfs.String(fsys, "sys/devices/system/cpu/online")
	if err != nil {
		return 0, false
	}
	cpus, err := hostfs.CPUList(online)
	if err != nil {
		return 0, false
	}
	for _, cpu := range cpus {
		dir := fmt.Sprintf("sys/devices/system/cpu/cpu%d/thermal_throttle", cpu)
		for _, f := range []string{"core_throttle_count", "package_throttle_count"} {
			if v, err := hostfs.Uint64(fsys, path.Join(dir, f)); err == nil {
				n += v
				ok = true
			}
		}
	}
	return n, ok
}

// Causes of unstable runs.
const (
	CauseGovernor   = "governor"
	CauseHeat       = "temperature"
	CauseThrottling = "throttling"
	CauseOther      = "other_processes"
)

// Diagnose lists the likely causes of variation between runs when their
// coefficient of variation exceeds UnstableCV.
func Diagnose(r Result) []string {
	if r.JPerToken.CV <= UnstableCV {
		return nil
	}
	var out []string
	if r.Context.Governor != "" && r.Context.Governor != "performance" {
		out = append(out, CauseGovernor)
	}
	if r.Context.StartTempC > 0 && r.Context.EndTempC-r.Context.StartTempC > 10 {
		out = append(out, CauseHeat)
	}
	if r.Context.Throttled != nil && *r.Context.Throttled {
		out = append(out, CauseThrottling)
	}
	if r.Context.OtherCPUShare > 0.2 {
		out = append(out, CauseOther)
	}
	return out
}
