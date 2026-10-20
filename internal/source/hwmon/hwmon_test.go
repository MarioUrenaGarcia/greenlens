// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package hwmon

import (
	"context"
	"math"
	"testing"

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
