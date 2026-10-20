// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package hwmon

import (
	"context"
	"math"
	"testing"
	"testing/fstest"

	"github.com/MarioUrenaGarcia/greenlens/internal/help"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
	"github.com/MarioUrenaGarcia/greenlens/internal/source/sourcetest"
)

func TestAmpereAltra(t *testing.T) {
	s := NewCPU(sourcetest.Host(t, "arm-ampere-altra"))
	res, err := s.Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Channels) != 2 {
		t.Fatalf("channels %+v", res.Channels)
	}
	rs, err := s.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]float64{}
	for _, r := range rs {
		got[r.Channel] = r.PowerW
	}
	// CORE VRD 95 W + SoC 21 W + RCA VRD 4 W; DIMM VRD1 6.5 W + VRD2 6.4 W.
	if math.Abs(got["hwmon:package-0"]-120) > 1e-9 || math.Abs(got["hwmon:package-0:dram"]-12.9) > 1e-9 {
		t.Errorf("readings %v", got)
	}
	for _, c := range res.Channels {
		if c.Kind != source.KindPower || c.Method != source.MethodIntegrated {
			t.Errorf("%s: %+v", c.ID, c)
		}
	}
}

func TestACPIPowerMeter(t *testing.T) {
	s := NewACPI(sourcetest.Host(t, "server-acpi"))
	res, err := s.Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Channels) != 1 || res.Channels[0].Domain != source.DomainPlatform || res.Channels[0].Quality != source.QualityPlatform {
		t.Fatalf("channels %+v", res.Channels)
	}
	rs, err := s.Read(context.Background())
	if err != nil || len(rs) != 1 || rs[0].PowerW != 245 {
		t.Fatalf("readings %+v, err %v", rs, err)
	}
	cpu, err := NewCPU(sourcetest.Host(t, "server-acpi")).Probe(context.Background())
	if err != nil || cpu.Available() || len(cpu.Problems) != 0 {
		t.Fatalf("the CPU source must ignore power_meter: %+v", cpu)
	}
}

func TestGPUAndTemperatureSensorsAreIgnored(t *testing.T) {
	res, err := NewCPU(sourcetest.Host(t, "amd-zen4-desktop")).Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Available() || len(res.Problems) != 0 {
		t.Fatalf("amdgpu and temperature-only devices must be ignored: %+v", res)
	}
}

func TestUnknownDriverIsReported(t *testing.T) {
	fsys := sourcetest.Host(t, "arm-ampere-altra")
	s := &Source{id: CPUID, priority: 3, drivers: map[string]driver{}, fsys: fsys}
	res, err := s.Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Problems) != 0 {
		t.Fatalf("a driver known to the table must not be reported: %+v", res.Problems)
	}
	delete(cpuDrivers, "smpro_hwmon")
	defer func() { cpuDrivers["smpro_hwmon"] = smpro }()
	res, err = (&Source{id: CPUID, priority: 3, drivers: map[string]driver{}, fsys: fsys}).Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Problems) != 1 || res.Problems[0].Code != help.GL1009 {
		t.Fatalf("problems %+v, want GL1009", res.Problems)
	}
}

func readAll(t *testing.T, s *Source) (source.ProbeResult, map[string]source.Reading) {
	t.Helper()
	res, err := s.Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	rs, err := s.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]source.Reading{}
	for _, r := range rs {
		got[r.Channel] = r
	}
	return res, got
}

func TestAPUSensorIsThePackage(t *testing.T) {
	res, got := readAll(t, NewGPU(sourcetest.Host(t, "amd-zen4-desktop")))
	if len(res.Channels) != 1 {
		t.Fatalf("channels %+v", res.Channels)
	}
	c := res.Channels[0]
	if c.ID != "hwmon_gpu:package-0" || c.Domain != source.DomainPackage || c.Kind != source.KindPower || c.Method != source.MethodIntegrated {
		t.Fatalf("channel %+v", c)
	}
	if got[c.ID].PowerW != 12 {
		t.Errorf("readings %+v", got)
	}
}

func TestDiscreteGPUsOnCurrentDrivers(t *testing.T) {
	fsys := sourcetest.Host(t, "workstation-gpus-new")
	res, got := readAll(t, NewGPU(fsys))
	want := map[string]struct {
		label  string
		kind   source.Kind
		energy float64
		power  float64
	}{
		// power1_average wins over power1_input.
		"hwmon_gpu:0000:03:00.0": {"amdgpu PPT", source.KindPower, 0, 187},
		// The card counter includes the package one.
		"hwmon_gpu:0000:04:00.0": {"xe card", source.KindCounter, 5400, 0},
		// i915_gt0 is part of the device counter.
		"hwmon_gpu:0000:05:00.0": {"i915 card", source.KindCounter, 2900, 0},
	}
	if len(res.Channels) != len(want) || len(res.Problems) != 0 {
		t.Fatalf("probe %+v", res)
	}
	for _, c := range res.Channels {
		w, ok := want[c.ID]
		if !ok || c.Label != w.label || c.Kind != w.kind || c.Domain != source.DomainGPU || c.Instance != c.ID[len(GPUID)+1:] {
			t.Errorf("channel %+v", c)
		}
		if r := got[c.ID]; math.Abs(r.EnergyJ-w.energy) > 1e-9 || math.Abs(r.PowerW-w.power) > 1e-9 {
			t.Errorf("%s reads %+v", c.ID, r)
		}
	}
	cpu, err := NewCPU(fsys).Probe(context.Background())
	if err != nil || cpu.Available() || len(cpu.Problems) != 0 {
		t.Fatalf("GPU devices must not be CPU sensors or unknown drivers: %+v", cpu)
	}
}

func TestDiscreteGPUsOnOlderDriversAndSuspended(t *testing.T) {
	res, got := readAll(t, NewGPU(sourcetest.Host(t, "workstation-gpus-old")))
	if len(res.Channels) != 3 || len(res.Problems) != 0 {
		t.Fatalf("probe %+v", res)
	}
	// A suspended card draws no power; its sensor is not read.
	if r, ok := got["hwmon_gpu:0000:03:00.0"]; !ok || r.PowerW != 0 {
		t.Errorf("suspended amdgpu reads %+v", got)
	}
	if r := got["hwmon_gpu:0000:04:00.0"]; r.EnergyJ != 7700 {
		t.Errorf("xe of Linux 6.8 reads %+v", r)
	}
	// A counter never read has no value to repeat.
	if _, ok := got["hwmon_gpu:0000:05:00.0"]; ok {
		t.Errorf("suspended i915 reported before its first read: %+v", got)
	}
	for _, c := range res.Channels {
		if c.ID == "hwmon_gpu:0000:04:00.0" && c.Label != "xe energy1" {
			t.Errorf("label %q", c.Label)
		}
	}
}

func gpuHost(status, energy string) fstest.MapFS {
	return fstest.MapFS{
		"sys/class/hwmon/hwmon0/name":                        {Data: []byte("i915\n")},
		"sys/class/hwmon/hwmon0/energy1_input":               {Data: []byte(energy + "\n")},
		"sys/class/hwmon/hwmon0/device/uevent":               {Data: []byte("DRIVER=i915\nPCI_SLOT_NAME=0000:0A:00.0\n")},
		"sys/class/hwmon/hwmon0/device/power/runtime_status": {Data: []byte(status + "\n")},
	}
}

func TestSuspendedCounterHoldsItsValue(t *testing.T) {
	fsys := gpuHost("active", "1000000")
	s := NewGPU(fsys)
	res, err := s.Probe(context.Background())
	if err != nil || len(res.Channels) != 1 || res.Channels[0].Instance != "0000:0a:00.0" {
		t.Fatalf("probe %+v, err %v", res, err)
	}
	read := func() []source.Reading {
		t.Helper()
		rs, err := s.Read(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return rs
	}
	if rs := read(); len(rs) != 1 || rs[0].EnergyJ != 1 {
		t.Fatalf("active %+v", rs)
	}
	// While suspended the file is not read, so a changed value is not seen.
	fsys["sys/class/hwmon/hwmon0/device/power/runtime_status"].Data = []byte("suspended\n")
	fsys["sys/class/hwmon/hwmon0/energy1_input"].Data = []byte("bogus\n")
	if rs := read(); len(rs) != 1 || rs[0].EnergyJ != 1 {
		t.Fatalf("suspended %+v", rs)
	}
	fsys["sys/class/hwmon/hwmon0/device/power/runtime_status"].Data = []byte("active\n")
	fsys["sys/class/hwmon/hwmon0/energy1_input"].Data = []byte("3000000\n")
	if rs := read(); len(rs) != 1 || rs[0].EnergyJ != 3 {
		t.Fatalf("resumed %+v", rs)
	}
}

func TestUnreadableGPUSensorIsReported(t *testing.T) {
	fsys := gpuHost("active", "1000000")
	fsys["sys/class/hwmon/hwmon0/energy1_input"].Data = []byte("x\n")
	res, err := NewGPU(fsys).Probe(context.Background())
	if err != nil || res.Available() || len(res.Problems) != 1 || res.Problems[0].Code != help.GL1005 {
		t.Fatalf("probe %+v, err %v", res, err)
	}
}
