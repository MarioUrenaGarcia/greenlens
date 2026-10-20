// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package rapl reads the RAPL energy counters that the powercap framework
// exposes under /sys/class/powercap, on Intel processors and on AMD Zen
// processors with kernel 5.8 or later.
package rapl

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"

	"github.com/MarioUrenaGarcia/greenlens/internal/help"
	"github.com/MarioUrenaGarcia/greenlens/internal/hostfs"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
)

// ID names this source.
const ID = "rapl"

const powercapDir = "sys/class/powercap"

// Zone directories are <control type>:<zone>[:<subzone>]. The MSR and TPMI
// drivers register the "intel-rapl" control type; the MMIO driver registers
// "intel-rapl-mmio" and duplicates some package zones on Intel clients.
var zoneDir = regexp.MustCompile(`^(intel-rapl|intel-rapl-mmio):(\d+)(?::(\d+))?$`)

// Zone names written by drivers/powercap/intel_rapl_common.c.
var (
	packageName = regexp.MustCompile(`^package-(\d+)(?:-die-(\d+))?$`)
	psysName    = regexp.MustCompile(`^psys(?:-(\d+))?$`)
)

type zone struct {
	dir     string
	channel source.Channel
}

// Source reads the powercap RAPL zones.
type Source struct {
	fsys  fs.FS
	zones []zone
}

// New returns a source that reads the host file system fsys.
func New(fsys fs.FS) *Source { return &Source{fsys: fsys} }

// ID implements source.Source.
func (s *Source) ID() string { return ID }

// Priority implements source.Source.
func (s *Source) Priority() int { return 1 }

// Close implements source.Source.
func (s *Source) Close() error { return nil }

// Probe implements source.Source.
func (s *Source) Probe(_ context.Context) (source.ProbeResult, error) {
	res := source.ProbeResult{Source: ID}
	s.zones = nil
	entries, err := hostfs.Dirs(s.fsys, powercapDir)
	if err != nil {
		return res, err
	}
	type found struct {
		zone
		mmio bool
	}
	byKey := map[string]found{}
	var order []string
	// Top-level zones first, so subzones can take the instance of their
	// package from the name of the parent zone.
	parentInstance := map[string]string{}
	for pass := 0; pass < 2; pass++ {
		for _, name := range entries {
			m := zoneDir.FindStringSubmatch(name)
			if m == nil || (pass == 0) != (m[3] == "") {
				continue
			}
			dir := path.Join(powercapDir, name)
			zname, err := hostfs.String(s.fsys, path.Join(dir, "name"))
			if err != nil {
				continue
			}
			parent := m[1] + ":" + m[2]
			domain, instance, key, ok := classify(zname, m[3] != "", parentInstance[parent])
			if !ok {
				continue
			}
			if m[3] == "" && domain == source.DomainPackage {
				parentInstance[parent] = instance
			}
			rangeUJ, err := hostfs.Uint64(s.fsys, path.Join(dir, "max_energy_range_uj"))
			if err != nil {
				continue
			}
			energyPath := path.Join(dir, "energy_uj")
			if _, err := hostfs.Uint64(s.fsys, energyPath); err != nil {
				// Every zone has the same permissions, so the first
				// unreadable one is enough to explain the problem.
				if hostfs.IsPermission(err) && len(res.Problems) == 0 {
					res.Problems = append(res.Problems, source.Problem{
						Code:    help.GL1002,
						Args:    []any{"path", "/" + energyPath},
						Channel: ID + ":" + key,
					})
				}
				continue
			}
			mmio := m[1] == "intel-rapl-mmio"
			if prev, dup := byKey[key]; dup && (mmio || !prev.mmio) {
				// The MMIO interface reports the same counter as the MSR
				// one; keep a single copy and prefer MSR.
				continue
			}
			if _, dup := byKey[key]; !dup {
				order = append(order, key)
			}
			byKey[key] = found{zone: zone{dir: dir, channel: source.Channel{
				ID:       ID + ":" + key,
				Source:   ID,
				Domain:   domain,
				Instance: instance,
				Quality:  source.QualityMeasured,
				Method:   source.MethodMeasured,
				Kind:     source.KindCounter,
				RangeJ:   float64(rangeUJ) / 1e6,
				Label:    zname,
			}}, mmio: mmio}
		}
	}
	for _, key := range order {
		z := byKey[key].zone
		s.zones = append(s.zones, z)
		res.Channels = append(res.Channels, z.channel)
	}
	return res, nil
}

// classify maps a zone name to its domain. Subzones (core, uncore, dram)
// belong to the package of their parent zone.
func classify(name string, sub bool, parentInstance string) (source.Domain, string, string, bool) {
	if m := packageName.FindStringSubmatch(name); m != nil && !sub {
		inst := m[1]
		if m[2] != "" {
			inst += "-die-" + m[2]
		}
		return source.DomainPackage, inst, "package-" + inst, true
	}
	if m := psysName.FindStringSubmatch(name); m != nil {
		return source.DomainPSys, m[1], name, true
	}
	var d source.Domain
	switch name {
	case "core":
		d = source.DomainCore
	case "uncore":
		d = source.DomainUncore
	case "dram":
		d = source.DomainDRAM
	default:
		return "", "", "", false
	}
	if parentInstance == "" {
		// A top-level dram zone, as on some servers, has no package; it
		// is still a separate component.
		return d, "", name, true
	}
	return d, parentInstance, "package-" + parentInstance + ":" + name, true
}

// Read implements source.Source.
func (s *Source) Read(_ context.Context) ([]source.Reading, error) {
	out := make([]source.Reading, 0, len(s.zones))
	var errs []error
	for _, z := range s.zones {
		uj, err := hostfs.Uint64(s.fsys, path.Join(z.dir, "energy_uj"))
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", z.channel.ID, err))
			continue
		}
		out = append(out, source.Reading{Channel: z.channel.ID, EnergyJ: float64(uj) / 1e6})
	}
	return out, errors.Join(errs...)
}
