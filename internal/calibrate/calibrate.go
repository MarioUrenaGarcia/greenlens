// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package calibrate measures the power of the node at rest. The median of
// each domain becomes the idle baseline that the dynamic attribution policy
// subtracts; the frequency, governor and temperature recorded with it help
// to judge whether a later baseline is comparable.
package calibrate

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/attribution"
	"github.com/MarioUrenaGarcia/greenlens/internal/hostfs"
	"github.com/MarioUrenaGarcia/greenlens/internal/sampler"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
)

// DefaultDuration is how long a calibration measures.
const DefaultDuration = 60 * time.Second

// Stats are percentiles of power, in watts.
type Stats struct {
	P10 float64 `json:"p10_w"`
	P50 float64 `json:"p50_w"`
	P90 float64 `json:"p90_w"`
}

// Result is one calibration.
type Result struct {
	StartedAt    time.Time                `json:"started_at"`
	Duration     time.Duration            `json:"duration"`
	Samples      int                      `json:"samples"`
	Domains      map[source.Domain]Stats  `json:"domains"`
	Quality      map[source.Domain]string `json:"quality"`
	FrequencyMHz float64                  `json:"frequency_mhz,omitempty"`
	Governor     string                   `json:"governor,omitempty"`
	TemperatureC float64                  `json:"temperature_c,omitempty"`
}

// Baseline returns the idle power used by attribution: the median of each
// domain.
func (r Result) Baseline() attribution.Baseline {
	return attribution.Baseline{
		CPUW:      r.Domains[source.DomainPackage].P50,
		DRAMW:     r.Domains[source.DomainDRAM].P50,
		PlatformW: r.Domains[source.DomainPlatform].P50,
	}
}

// ErrNoSamples is returned when the duration was too short to measure.
var ErrNoSamples = errors.New("calibration collected no samples")

// Run samples s every interval for duration. progress, when not nil, is
// called after every sample with the time elapsed.
func Run(ctx context.Context, s *sampler.Sampler, fsys fs.FS, duration, interval time.Duration, progress func(time.Duration)) (Result, error) {
	res := Result{StartedAt: time.Now(), Domains: map[source.Domain]Stats{}, Quality: map[source.Domain]string{}}
	if err := s.Prime(ctx); err != nil {
		return res, err
	}
	powers := map[source.Domain][]float64{}
	quality := map[source.Domain]string{}
	for _, ch := range s.Channels() {
		if ch.Domain.Level() != source.LevelContained {
			quality[ch.Domain] = string(ch.Quality)
		}
	}
	start := time.Now()
	t := time.NewTicker(interval)
	defer t.Stop()
	var errs []error
	for time.Since(start) < duration {
		select {
		case <-ctx.Done():
			return res, ctx.Err()
		case <-t.C:
		}
		smp, err := s.Sample(ctx)
		if err != nil {
			errs = append(errs, err)
		}
		for d := range quality {
			if w, ok := smp.DomainPower(d); ok {
				powers[d] = append(powers[d], w)
			}
		}
		res.Samples++
		if progress != nil {
			progress(time.Since(start))
		}
	}
	res.Duration = time.Since(start)
	for d, ws := range powers {
		if len(ws) == 0 {
			continue
		}
		res.Domains[d] = Stats{P10: Percentile(ws, 10), P50: Percentile(ws, 50), P90: Percentile(ws, 90)}
		res.Quality[d] = quality[d]
	}
	if len(res.Domains) == 0 {
		return res, errors.Join(append([]error{ErrNoSamples}, errs...)...)
	}
	res.FrequencyMHz, res.Governor = cpuFrequency(fsys)
	res.TemperatureC = temperature(fsys)
	return res, nil
}

// Percentile returns the p-th percentile of values with linear
// interpolation between the closest ranks.
func Percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return math.NaN()
	}
	v := slices.Clone(values)
	slices.Sort(v)
	if len(v) == 1 {
		return v[0]
	}
	rank := p / 100 * float64(len(v)-1)
	lo := int(math.Floor(rank))
	hi := int(math.Ceil(rank))
	return v[lo] + (v[hi]-v[lo])*(rank-float64(lo))
}

// cpuFrequency averages scaling_cur_freq (kHz) over the online CPUs and
// returns the governor of the first one.
func cpuFrequency(fsys fs.FS) (float64, string) {
	online, err := hostfs.String(fsys, "sys/devices/system/cpu/online")
	if err != nil {
		return 0, ""
	}
	cpus, err := hostfs.CPUList(online)
	if err != nil {
		return 0, ""
	}
	sum, n := 0.0, 0
	governor := ""
	for _, c := range cpus {
		dir := fmt.Sprintf("sys/devices/system/cpu/cpu%d/cpufreq", c)
		if khz, err := hostfs.Uint64(fsys, path.Join(dir, "scaling_cur_freq")); err == nil {
			sum += float64(khz) / 1000
			n++
		}
		if governor == "" {
			governor, _ = hostfs.String(fsys, path.Join(dir, "scaling_governor"))
		}
	}
	if n == 0 {
		return 0, governor
	}
	return sum / float64(n), governor
}

// cpuThermal lists hwmon drivers whose temperatures belong to the
// processor.
var cpuThermal = []string{"k10temp", "coretemp", "zenpower", "cpu_thermal", "smpro_hwmon"}

// temperature returns the highest processor temperature in degrees
// Celsius, or 0 when none is readable.
func temperature(fsys fs.FS) float64 {
	devices, err := hostfs.Dirs(fsys, "sys/class/hwmon")
	if err != nil {
		return 0
	}
	best := 0.0
	for _, d := range devices {
		dir := path.Join("sys/class/hwmon", d)
		name, _ := hostfs.String(fsys, path.Join(dir, "name"))
		if !slices.Contains(cpuThermal, name) {
			continue
		}
		entries, err := fs.ReadDir(fsys, dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !strings.HasPrefix(e.Name(), "temp") || !strings.HasSuffix(e.Name(), "_input") {
				continue
			}
			if mc, err := hostfs.Int64(fsys, path.Join(dir, e.Name())); err == nil {
				best = math.Max(best, float64(mc)/1000)
			}
		}
	}
	return best
}
