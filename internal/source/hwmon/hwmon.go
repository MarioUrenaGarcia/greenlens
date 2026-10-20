// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package hwmon reads processor, GPU and platform power sensors exposed
// through /sys/class/hwmon. Attribute names and labels vary by driver, so
// each supported driver has an entry in a table that maps its sensors to
// domains; the entries follow the driver sources in the Linux kernel.
package hwmon

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

// Source IDs.
const (
	CPUID  = "hwmon"
	GPUID  = "hwmon_gpu"
	ACPIID = "acpi_power_meter"
)

const hwmonDir = "sys/class/hwmon"

// graphicsDrivers are GPU drivers; their sensors are not CPU or platform
// sensors and must not be reported as unknown by the CPU source. The i915
// driver also registers one device per GT, named "i915_gtN".
var graphicsDrivers = map[string]bool{"amdgpu": true, "radeon": true, "nouveau": true, "i915": true, "xe": true}

func isGraphics(name string) bool {
	return graphicsDrivers[name] || strings.HasPrefix(name, "i915_gt")
}

var sensorFile = regexp.MustCompile(`^(power|energy)(\d+)_(input|average)$`)

// part is one hwmon attribute that contributes to a channel.
type part struct {
	file  string
	power bool // microwatts when true, microjoules otherwise
}

type channel struct {
	ch    source.Channel
	dir   string
	parts []part
	// pm is the runtime power state of the device, set for GPUs. A
	// suspended GPU is not read: amdgpu refuses the read and xe and i915
	// would wake the card on every sample.
	pm string
	// last is the most recent counter value, repeated while the device is
	// suspended so the interval adds no energy.
	last    float64
	hasLast bool
}

// driver maps the sensors of one hwmon device to channels. index counts the
// devices of the same driver, which stand for different sockets.
type driver func(fsys fs.FS, dir string, index int) []channel

// cpuDrivers measure processor packages and memory.
var cpuDrivers = map[string]driver{
	"amd_energy":  amdEnergy,
	"smpro_hwmon": smpro,
	"apm_xgene":   xgene,
}

// gpuDrivers measure graphics cards. An APU sensor that includes the CPU
// is reported as a package.
var gpuDrivers = map[string]driver{
	"amdgpu": amdgpu,
	"xe":     xe,
	"i915":   i915,
}

// platformDrivers measure the whole system.
var platformDrivers = map[string]driver{
	"power_meter": acpiPowerMeter,
}

// Source reads hwmon sensors of one class of drivers.
type Source struct {
	id       string
	priority int
	drivers  map[string]driver
	fsys     fs.FS
	channels []channel
}

// NewCPU returns the source for processor sensors.
func NewCPU(fsys fs.FS) *Source {
	return &Source{id: CPUID, priority: 3, drivers: cpuDrivers, fsys: fsys}
}

// NewGPU returns the source for GPU sensors. Its priority is that of the
// CPU sensors: GPUs are chosen one device at a time, and an APU sensor must
// not displace RAPL or perf_event for the package.
func NewGPU(fsys fs.FS) *Source {
	return &Source{id: GPUID, priority: 3, drivers: gpuDrivers, fsys: fsys}
}

// NewACPI returns the source for ACPI power meters.
func NewACPI(fsys fs.FS) *Source {
	return &Source{id: ACPIID, priority: 4, drivers: platformDrivers, fsys: fsys}
}

// ID implements source.Source.
func (s *Source) ID() string { return s.id }

// Priority implements source.Source.
func (s *Source) Priority() int { return s.priority }

// Close implements source.Source.
func (s *Source) Close() error { return nil }

// Probe implements source.Source.
func (s *Source) Probe(_ context.Context) (source.ProbeResult, error) {
	res := source.ProbeResult{Source: s.id}
	s.channels = nil
	devices, err := hostfs.Dirs(s.fsys, hwmonDir)
	if err != nil {
		return res, err
	}
	count := map[string]int{}
	for _, dev := range devices {
		dir := path.Join(hwmonDir, dev)
		name := deviceName(s.fsys, dir)
		drv, known := s.drivers[name]
		if !known {
			// Only the CPU source reports unknown drivers, so each one
			// appears once in diagnostics.
			if s.id == CPUID && !isGraphics(name) && !knownElsewhere(name) && hasPowerSensors(s.fsys, dir) {
				res.Problems = append(res.Problems, source.Problem{Code: help.GL1009, Args: []any{"name", name}})
			}
			continue
		}
		idx := count[name]
		count[name]++
		for _, c := range drv(s.fsys, dir, idx) {
			c.dir = dir
			c.ch.Source = s.id
			c.ch.ID = s.id + ":" + c.ch.ID
			// Reading a suspended GPU would fail or wake it; its files are
			// checked on the first read after it resumes.
			if suspended(s.fsys, c.pm) {
				s.channels = append(s.channels, c)
				res.Channels = append(res.Channels, c.ch)
				continue
			}
			if p := unreadable(s.fsys, dir, c.parts); p != "" {
				res.Problems = append(res.Problems, source.Problem{
					Code: help.GL1005, Args: []any{"path", "/" + p}, Channel: c.ch.ID,
				})
				continue
			}
			s.channels = append(s.channels, c)
			res.Channels = append(res.Channels, c.ch)
		}
	}
	return res, nil
}

func knownElsewhere(name string) bool {
	_, cpu := cpuDrivers[name]
	_, platform := platformDrivers[name]
	return cpu || platform
}

// suspended reports whether a device is in runtime suspend. Devices
// without runtime power management have no such state.
func suspended(fsys fs.FS, pm string) bool {
	if pm == "" {
		return false
	}
	st, err := hostfs.String(fsys, pm)
	return err == nil && st == "suspended"
}

// pciSlot returns the PCI address of the device behind a hwmon directory,
// such as "0000:03:00.0", or "" when it is not a PCI device.
func pciSlot(fsys fs.FS, dir string) string {
	b, err := fs.ReadFile(fsys, path.Join(dir, "device", "uevent"))
	if err != nil {
		return ""
	}
	for line := range strings.Lines(string(b)) {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "PCI_SLOT_NAME="); ok {
			return strings.ToLower(v)
		}
	}
	return ""
}

// deviceName reads the chip name. Old drivers put it on the parent device.
func deviceName(fsys fs.FS, dir string) string {
	if n, err := hostfs.String(fsys, path.Join(dir, "name")); err == nil {
		return n
	}
	n, _ := hostfs.String(fsys, path.Join(dir, "device", "name"))
	return n
}

func hasPowerSensors(fsys fs.FS, dir string) bool {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if sensorFile.MatchString(e.Name()) {
			return true
		}
	}
	return false
}

func unreadable(fsys fs.FS, dir string, parts []part) string {
	for _, p := range parts {
		if _, err := hostfs.Int64(fsys, path.Join(dir, p.file)); err != nil {
			return path.Join(dir, p.file)
		}
	}
	return ""
}

// labels returns the label of every sensor of a kind, by sensor number.
func labels(fsys fs.FS, dir, kind string) map[int]string {
	out := map[int]string{}
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		n, ok := strings.CutPrefix(e.Name(), kind)
		if !ok {
			continue
		}
		num, ok := strings.CutSuffix(n, "_label")
		if !ok {
			continue
		}
		i, err := strconv.Atoi(num)
		if err != nil {
			continue
		}
		if l, err := hostfs.String(fsys, path.Join(dir, e.Name())); err == nil {
			out[i] = l
		}
	}
	return out
}

func newChannel(id string, d source.Domain, inst, label string, power bool, parts ...part) channel {
	ch := source.Channel{
		ID:       id,
		Domain:   d,
		Instance: inst,
		Quality:  source.QualityMeasured,
		Method:   source.MethodMeasured,
		Kind:     source.KindCounter,
		Label:    label,
	}
	if power {
		ch.Method = source.MethodIntegrated
		ch.Kind = source.KindPower
	}
	if d.Level() == source.LevelSystem {
		ch.Quality = source.QualityPlatform
	}
	return channel{ch: ch, parts: parts}
}

// amdEnergy follows drivers/hwmon/amd_energy.c (kernels 5.8 to 5.12): one
// energy counter per core labeled "Ecore%03u" and one per socket labeled
// "Esocket%u", in microjoules. Only sockets are used: cores are contained in
// them.
func amdEnergy(fsys fs.FS, dir string, _ int) []channel {
	var out []channel
	for i, l := range labels(fsys, dir, "energy") {
		sock, ok := strings.CutPrefix(l, "Esocket")
		if !ok {
			continue
		}
		f := fmt.Sprintf("energy%d_input", i)
		out = append(out, newChannel("package-"+sock, source.DomainPackage, sock, l, false, part{file: f}))
	}
	return out
}

// smpro follows drivers/hwmon/smpro-hwmon.c (Ampere Altra): voltage
// regulator outputs in microwatts labeled CORE VRD, SoC, DIMM VRD1, DIMM VRD2
// and RCA VRD. The package is the sum of core, SoC and RCA; memory is the sum
// of both DIMM regulators.
func smpro(fsys fs.FS, dir string, index int) []channel {
	var pkg, dram []part
	for i, l := range labels(fsys, dir, "power") {
		p := part{file: fmt.Sprintf("power%d_input", i), power: true}
		switch {
		case strings.Contains(l, "DIMM VRD"):
			dram = append(dram, p)
		case strings.Contains(l, "CORE VRD"), strings.Contains(l, "SoC"), strings.Contains(l, "RCA VRD"):
			pkg = append(pkg, p)
		}
	}
	inst := strconv.Itoa(index)
	var out []channel
	if len(pkg) > 0 {
		out = append(out, newChannel("package-"+inst, source.DomainPackage, inst, "CORE VRD + SoC + RCA VRD", true, sorted(pkg)...))
	}
	if len(dram) > 0 {
		out = append(out, newChannel("package-"+inst+":dram", source.DomainDRAM, inst, "DIMM VRD", true, sorted(dram)...))
	}
	return out
}

// xgene follows drivers/hwmon/xgene-hwmon.c (APM X-Gene): power1 is "CPU
// power" and power2 is "IO power", both in microwatts; together they are the
// SoC.
func xgene(fsys fs.FS, dir string, index int) []channel {
	var parts []part
	for _, n := range []int{1, 2} {
		f := fmt.Sprintf("power%d_input", n)
		if hostfs.Exists(fsys, path.Join(dir, f)) {
			parts = append(parts, part{file: f, power: true})
		}
	}
	if len(parts) == 0 {
		return nil
	}
	inst := strconv.Itoa(index)
	return []channel{newChannel("package-"+inst, source.DomainPackage, inst, "CPU power + IO power", true, parts...)}
}

// acpiPowerMeter follows drivers/hwmon/acpi_power_meter.c: power1_average,
// in microwatts, averaged by the firmware over power1_average_interval.
func acpiPowerMeter(fsys fs.FS, dir string, index int) []channel {
	if !hostfs.Exists(fsys, path.Join(dir, "power1_average")) {
		return nil
	}
	inst := strconv.Itoa(index)
	return []channel{newChannel("power_meter-"+inst, source.DomainPlatform, inst, "ACPI power meter", true, part{file: "power1_average", power: true})}
}

// amdgpu follows drivers/gpu/drm/amd/pm/amdgpu_pm.c: power1_average and
// power1_input are in microwatts, the first averaged by the power
// management firmware, and power1_label names the limit they track, such as
// "PPT". The driver has no energy counter. On APUs both include the CPU, so
// the sensor measures the package rather than the GPU; APUs are recognized
// by the northbridge voltage, in1 labeled "vddnb", which the driver shows
// only on them. Multi-die APUs hide vddnb and are reported as GPUs.
func amdgpu(fsys fs.FS, dir string, _ int) []channel {
	file := ""
	for _, f := range []string{"power1_average", "power1_input"} {
		if hostfs.Exists(fsys, path.Join(dir, f)) {
			file = f
			break
		}
	}
	slot := pciSlot(fsys, dir)
	if file == "" || slot == "" {
		return nil
	}
	label := "amdgpu " + file
	if l, err := hostfs.String(fsys, path.Join(dir, "power1_label")); err == nil && l != "" {
		label = "amdgpu " + l
	}
	p := part{file: file, power: true}
	var c channel
	if l, err := hostfs.String(fsys, path.Join(dir, "in1_label")); err == nil && l == "vddnb" {
		c = newChannel("package-0", source.DomainPackage, "0", label+" (APU: CPU and GPU)", true, p)
	} else {
		c = newChannel(slot, source.DomainGPU, slot, label, true, p)
	}
	c.pm = path.Join(dir, "device", "power", "runtime_status")
	return []channel{c}
}

// xe follows drivers/gpu/drm/xe/xe_hwmon.c. Since Linux 6.10 energy1_input
// is the card and energy2_input the GPU package, in microjoules, labeled
// "card" and "pkg"; either may be missing on a given platform. The card
// includes the package, so only one is used. Linux 6.8 and 6.9 have a single
// unlabeled energy1_input for the package. The driver extends the hardware
// counter to 63 bits and registers hwmon only for discrete cards, whose
// energy is not part of the processor package.
func xe(fsys fs.FS, dir string, _ int) []channel {
	slot := pciSlot(fsys, dir)
	if slot == "" {
		return nil
	}
	file, label := "", ""
	byLabel := map[string]int{}
	for i, l := range labels(fsys, dir, "energy") {
		byLabel[l] = i
	}
	for _, want := range []string{"card", "pkg"} {
		if n, ok := byLabel[want]; ok {
			file, label = fmt.Sprintf("energy%d_input", n), "xe "+want
			break
		}
	}
	if file == "" && len(byLabel) == 0 {
		file, label = "energy1_input", "xe energy1"
	}
	if file == "" || !hostfs.Exists(fsys, path.Join(dir, file)) {
		return nil
	}
	c := newChannel(slot, source.DomainGPU, slot, label, false, part{file: file})
	c.pm = path.Join(dir, "device", "power", "runtime_status")
	return []channel{c}
}

// i915 follows drivers/gpu/drm/i915/i915_hwmon.c: the device-level hwmon,
// named "i915", has energy1_input in microjoules for the whole card. The
// per-GT devices, named "i915_gtN", are part of it. The driver extends the
// counter to 63 bits and registers hwmon only for discrete cards.
func i915(fsys fs.FS, dir string, _ int) []channel {
	slot := pciSlot(fsys, dir)
	if slot == "" || !hostfs.Exists(fsys, path.Join(dir, "energy1_input")) {
		return nil
	}
	c := newChannel(slot, source.DomainGPU, slot, "i915 card", false, part{file: "energy1_input"})
	c.pm = path.Join(dir, "device", "power", "runtime_status")
	return []channel{c}
}

func sorted(p []part) []part {
	for i := 1; i < len(p); i++ {
		for j := i; j > 0 && p[j].file < p[j-1].file; j-- {
			p[j], p[j-1] = p[j-1], p[j]
		}
	}
	return p
}

// Read implements source.Source.
func (s *Source) Read(_ context.Context) ([]source.Reading, error) {
	out := make([]source.Reading, 0, len(s.channels))
	var errs []error
	for i := range s.channels {
		c := &s.channels[i]
		if suspended(s.fsys, c.pm) {
			if r, ok := c.idle(); ok {
				out = append(out, r)
			}
			continue
		}
		r := source.Reading{Channel: c.ch.ID}
		var err error
		for _, p := range c.parts {
			var v int64
			if v, err = hostfs.Int64(s.fsys, path.Join(c.dir, p.file)); err != nil {
				break
			}
			if p.power {
				r.PowerW += float64(v) / 1e6
			} else {
				r.EnergyJ += float64(v) / 1e6
			}
		}
		switch {
		case err == nil:
			c.last, c.hasLast = r.EnergyJ, true
			out = append(out, r)
		case suspended(s.fsys, c.pm):
			// The device suspended between the check and the read.
			if r, ok := c.idle(); ok {
				out = append(out, r)
			}
		default:
			errs = append(errs, fmt.Errorf("%s: %w", c.ch.ID, err))
		}
	}
	return out, errors.Join(errs...)
}

// idle is the reading of a suspended device: no power, and a counter that
// does not move. A counter never read has no value yet.
func (c *channel) idle() (source.Reading, bool) {
	if c.ch.Kind == source.KindPower {
		return source.Reading{Channel: c.ch.ID}, true
	}
	return source.Reading{Channel: c.ch.ID, EnergyJ: c.last}, c.hasLast
}
