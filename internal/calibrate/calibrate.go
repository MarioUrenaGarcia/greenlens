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
	StartedAt time.Time                `json:"started_at"`
	Duration  time.Duration            `json:"duration"`
	Samples   int                      `json:"samples"`
	Domains   map[source.Domain]Stats  `json:"domains"`
	Quality   map[source.Domain]string `json:"quality"`
	// GPUs holds the power of each GPU by PCI address; the gpu domain is
	// their sum.
	GPUs         map[string]Stats `json:"gpus,omitempty"`
	FrequencyMHz float64          `json:"frequency_mhz,omitempty"`
	Governor     string           `json:"governor,omitempty"`
	TemperatureC float64          `json:"temperature_c,omitempty"`
}

// Baseline returns the idle power used by attribution: the median of each
// domain.
func (r Result) Baseline() attribution.Baseline {
	b := attribution.Baseline{
		CPUW:      r.Domains[source.DomainPackage].P50,
		DRAMW:     r.Domains[source.DomainDRAM].P50,
		PlatformW: r.Domains[source.DomainPlatform].P50,
	}
	if len(r.GPUs) > 0 {
		b.GPUW = map[string]float64{}
		for inst, st := range r.GPUs {
			b.GPUW[inst] = st.P50
		}
	}
	return b
}

// ErrNoSamples is returned when the duration was too short to measure.
var ErrNoSamples = errors.New("calibration collected no samples")

// Collector adds up samples of an idle machine into a calibration.
type Collector struct {
	chans   map[string]source.Channel
	quality map[source.Domain]string
	powers  map[source.Domain][]float64
	gpus    map[string][]float64
	samples int
}

// NewCollector collects the samples of the given channels.
func NewCollector(chans map[string]source.Channel) *Collector {
	c := &Collector{chans: chans, quality: map[source.Domain]string{}, powers: map[source.Domain][]float64{}, gpus: map[string][]float64{}}
	for _, ch := range chans {
		if ch.Domain.Level() != source.LevelContained {
			c.quality[ch.Domain] = string(ch.Quality)
		}
	}
	return c
}

// Add records the power of every domain in one sample.
func (c *Collector) Add(smp sampler.Sample) {
	for d := range c.quality {
		if w, ok := smp.DomainPower(d); ok {
			c.powers[d] = append(c.powers[d], w)
		}
	}
	for _, ch := range smp.Channels {
		if ch.Domain == source.DomainGPU {
			inst := c.chans[ch.Channel].Instance
			c.gpus[inst] = append(c.gpus[inst], ch.PowerW)
		}
	}
	c.samples++
}

// Samples returns how many samples were added.
func (c *Collector) Samples() int { return c.samples }

// Result summarizes the samples. fsys gives the frequency and temperature
// of the processor at the end.
func (c *Collector) Result(start time.Time, d time.Duration, fsys fs.FS) (Result, error) {
	res := Result{StartedAt: start, Duration: d, Samples: c.samples, Domains: map[source.Domain]Stats{}, Quality: map[source.Domain]string{}}
	for dom, ws := range c.powers {
		if len(ws) == 0 {
			continue
		}
		res.Domains[dom] = Stats{P10: Percentile(ws, 10), P50: Percentile(ws, 50), P90: Percentile(ws, 90)}
		res.Quality[dom] = c.quality[dom]
	}
	for inst, ws := range c.gpus {
		if res.GPUs == nil {
			res.GPUs = map[string]Stats{}
		}
		res.GPUs[inst] = Stats{P10: Percentile(ws, 10), P50: Percentile(ws, 50), P90: Percentile(ws, 90)}
	}
	if len(res.Domains) == 0 {
		return res, ErrNoSamples
	}
	res.FrequencyMHz, res.Governor = CPUFrequency(fsys)
	res.TemperatureC = Temperature(fsys)
	return res, nil
}

// Run samples s every interval for duration. progress, when not nil, is
// called after every sample with the time elapsed.
func Run(ctx context.Context, s *sampler.Sampler, fsys fs.FS, duration, interval time.Duration, progress func(time.Duration)) (Result, error) {
	if err := s.Prime(ctx); err != nil {
		return Result{}, err
	}
	c := NewCollector(s.Channels())
	start := time.Now()
	t := time.NewTicker(interval)
	defer t.Stop()
	var errs []error
	for time.Since(start) < duration {
		select {
		case <-ctx.Done():
			return Result{}, ctx.Err()
		case <-t.C:
		}
		smp, err := s.Sample(ctx)
		if err != nil {
			errs = append(errs, err)
		}
		c.Add(smp)
		if progress != nil {
			progress(time.Since(start))
		}
	}
	res, err := c.Result(start, time.Since(start), fsys)
	if err != nil {
		return res, errors.Join(append([]error{err}, errs...)...)
	}
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

// CPUFrequency averages scaling_cur_freq (kHz) over the online CPUs and
// returns the governor of the first one.
func CPUFrequency(fsys fs.FS) (float64, string) {
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

// Temperature returns the highest processor temperature in degrees
// Celsius, or 0 when none is readable.
func Temperature(fsys fs.FS) float64 {
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
