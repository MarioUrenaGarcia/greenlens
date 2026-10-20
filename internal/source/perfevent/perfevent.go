// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package perfevent reads the RAPL energy counters through the perf_event
// "power" PMU. It reaches the same hardware counters as powercap without
// changing sysfs permissions, but needs perf_event_paranoid 0 or less, or
// the CAP_PERFMON capability.
package perfevent

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/MarioUrenaGarcia/greenlens/internal/help"
	"github.com/MarioUrenaGarcia/greenlens/internal/hostfs"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
)

// ID names this source.
const ID = "perf_event"

const pmuDir = "sys/bus/event_source/devices/power"

// events maps the RAPL perf events to their domains.
var events = []struct {
	name   string
	domain source.Domain
}{
	{"energy-pkg", source.DomainPackage},
	{"energy-ram", source.DomainDRAM},
	{"energy-psys", source.DomainPSys},
}

var eventConfig = regexp.MustCompile(`^event=0x([0-9a-fA-F]+)$`)

// Counter is an open perf event that counts energy in raw units.
type Counter interface {
	Read() (uint64, error)
	Close() error
}

// Opener opens a system-wide counter of a PMU event on one CPU.
type Opener interface {
	Open(pmuType uint32, config uint64, cpu int) (Counter, error)
}

type counter struct {
	channel source.Channel
	c       Counter
	scale   float64
}

// Source reads RAPL energy through perf_event.
type Source struct {
	fsys     fs.FS
	opener   Opener
	counters []counter
}

// New returns a source that probes fsys and opens counters with opener.
func New(fsys fs.FS, opener Opener) *Source { return &Source{fsys: fsys, opener: opener} }

// ID implements source.Source.
func (s *Source) ID() string { return ID }

// Priority implements source.Source. The counters are the same as those of
// powercap, which needs no special capability, so powercap goes first.
func (s *Source) Priority() int { return 2 }

// Close implements source.Source.
func (s *Source) Close() error {
	var errs []error
	for _, c := range s.counters {
		errs = append(errs, c.c.Close())
	}
	s.counters = nil
	return errors.Join(errs...)
}

// Probe implements source.Source.
func (s *Source) Probe(_ context.Context) (source.ProbeResult, error) {
	res := source.ProbeResult{Source: ID}
	if err := s.Close(); err != nil {
		return res, err
	}
	pmuType, err := hostfs.Uint64(s.fsys, path.Join(pmuDir, "type"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return res, nil
		}
		return res, err
	}
	mask, err := hostfs.String(s.fsys, path.Join(pmuDir, "cpumask"))
	if err != nil {
		return res, err
	}
	cpus, err := hostfs.CPUList(mask)
	if err != nil {
		return res, err
	}
	paranoid, _ := hostfs.String(s.fsys, "proc/sys/kernel/perf_event_paranoid")
	denied := false
	for _, ev := range events {
		cfg, scale, ok := s.event(ev.name)
		if !ok {
			continue
		}
		// psys is one counter for the whole platform; package and dram
		// have one counter per package, read on the CPU listed for it.
		targets := cpus
		if ev.domain == source.DomainPSys && len(cpus) > 1 {
			targets = cpus[:1]
		}
		for _, cpu := range targets {
			inst := s.packageOf(cpu)
			id := ID + ":" + key(ev.domain, inst)
			if ev.domain == source.DomainPSys {
				inst = ""
				id = ID + ":psys"
			}
			c, err := s.opener.Open(uint32(pmuType), cfg, cpu) //nolint:gosec // PMU type ids are small
			if err != nil {
				if isPermission(err) {
					if !denied {
						res.Problems = append(res.Problems, source.Problem{
							Code: help.GL1003, Args: []any{"paranoid", paranoid}, Channel: id,
						})
						denied = true
					}
					continue
				}
				return res, fmt.Errorf("open %s on cpu %d: %w", ev.name, cpu, err)
			}
			ch := source.Channel{
				ID:       id,
				Source:   ID,
				Domain:   ev.domain,
				Instance: inst,
				Quality:  source.QualityMeasured,
				Method:   source.MethodMeasured,
				Kind:     source.KindCounter,
				Label:    ev.name,
			}
			s.counters = append(s.counters, counter{channel: ch, c: c, scale: scale})
			res.Channels = append(res.Channels, ch)
		}
	}
	return res, nil
}

func key(d source.Domain, inst string) string {
	if d == source.DomainDRAM {
		return "package-" + inst + ":dram"
	}
	return "package-" + inst
}

// event reads the config value and the joules-per-count scale of an event.
func (s *Source) event(name string) (uint64, float64, bool) {
	spec, err := hostfs.String(s.fsys, path.Join(pmuDir, "events", name))
	if err != nil {
		return 0, 0, false
	}
	m := eventConfig.FindStringSubmatch(spec)
	if m == nil {
		return 0, 0, false
	}
	cfg, err := strconv.ParseUint(m[1], 16, 64)
	if err != nil {
		return 0, 0, false
	}
	unit, err := hostfs.String(s.fsys, path.Join(pmuDir, "events", name+".unit"))
	if err != nil || unit != "Joules" {
		return 0, 0, false
	}
	scaleStr, err := hostfs.String(s.fsys, path.Join(pmuDir, "events", name+".scale"))
	if err != nil {
		return 0, 0, false
	}
	scale, err := strconv.ParseFloat(scaleStr, 64)
	if err != nil || scale <= 0 {
		return 0, 0, false
	}
	return cfg, scale, true
}

func (s *Source) packageOf(cpu int) string {
	id, err := hostfs.String(s.fsys, fmt.Sprintf("sys/devices/system/cpu/cpu%d/topology/physical_package_id", cpu))
	if err != nil || strings.HasPrefix(id, "-") {
		return strconv.Itoa(cpu)
	}
	return id
}

// Read implements source.Source.
func (s *Source) Read(_ context.Context) ([]source.Reading, error) {
	out := make([]source.Reading, 0, len(s.counters))
	var errs []error
	for _, c := range s.counters {
		raw, err := c.c.Read()
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", c.channel.ID, err))
			continue
		}
		out = append(out, source.Reading{Channel: c.channel.ID, EnergyJ: float64(raw) * c.scale})
	}
	return out, errors.Join(errs...)
}
