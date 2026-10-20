// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package estimate

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/MarioUrenaGarcia/greenlens/internal/help"
	"github.com/MarioUrenaGarcia/greenlens/internal/source/sourcetest"
)

func TestLookup(t *testing.T) {
	tests := []struct {
		cpu  CPU
		want string
	}{
		{CPU{Vendor: "GenuineIntel", Family: 6, Model: 0x55, Stepping: 7}, "Cascade Lake"},
		{CPU{Vendor: "GenuineIntel", Family: 6, Model: 0x55, Stepping: 4}, "Skylake"},
		{CPU{Vendor: "GenuineIntel", Family: 6, Model: 0x55, Stepping: 11}, "generic"},
		{CPU{Vendor: "GenuineIntel", Family: 6, Model: 0x8F}, "Sapphire Rapids"},
		{CPU{Vendor: "GenuineIntel", Family: 6, Model: 0x97}, "generic"},
		{CPU{Vendor: "AuthenticAMD", Family: 0x17, Model: 0x31, ModelName: "AMD EPYC 7742 64-Core Processor"}, "AMD EPYC 2nd generation"},
		{CPU{Vendor: "AuthenticAMD", Family: 0x19, Model: 0x01, ModelName: "AMD EPYC 7763 64-Core Processor"}, "AMD EPYC 3rd generation"},
		{CPU{Vendor: "AuthenticAMD", Family: 0x19, Model: 0x11, ModelName: "AMD EPYC 9654 96-Core Processor"}, "AMD EPYC 4th generation"},
		{CPU{Vendor: "AuthenticAMD", Family: 0x19, Model: 0x61, ModelName: "AMD Ryzen 9 7900X 12-Core Processor"}, "generic"},
		{CPU{}, "generic"},
	}
	for _, tt := range tests {
		c, _ := Lookup(tt.cpu)
		if c.Name != tt.want {
			t.Errorf("Lookup(%+v) = %s, want %s", tt.cpu, c.Name, tt.want)
		}
	}
}

func TestZenRanges(t *testing.T) {
	for _, tt := range []struct{ family, model, want int }{
		{0x17, 0x01, 1}, {0x17, 0x31, 2}, {0x17, 0x71, 2}, {0x19, 0x01, 3}, {0x19, 0x21, 3},
		{0x19, 0x11, 4}, {0x19, 0xa0, 4}, {0x1a, 0x02, 5}, {0x1a, 0x50, 6}, {0x17, 0xb0, 0}, {0x15, 0x01, 0},
	} {
		if got := zen(tt.family, tt.model); got != tt.want {
			t.Errorf("zen(%#x, %#x) = %d, want %d", tt.family, tt.model, got, tt.want)
		}
	}
}

func TestVirtualMachine(t *testing.T) {
	s := New(sourcetest.Host(t, "vm-no-sensors"), Override{})
	res, err := s.Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Problems) != 0 || len(res.Channels) != 1 {
		t.Fatalf("result %+v", res)
	}
	// Four threads of Cascade Lake: 0.69 W idle and 4.06 W at full load each.
	if math.Abs(s.Power(0)-2.76) > 1e-9 || math.Abs(s.Power(1)-16.24) > 1e-9 || math.Abs(s.Power(0.5)-9.5) > 1e-9 {
		t.Errorf("power %v %v %v", s.Power(0), s.Power(0.5), s.Power(1))
	}
}

func TestUtilizationSincePreviousRead(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"sys/devices/system/cpu/online": "0-1",
		"proc/cpuinfo":                  "vendor_id\t: GenuineIntel\ncpu family\t: 6\nmodel\t\t: 151\nmodel name\t: test\n",
		"proc/stat":                     "cpu  100 0 0 100 0 0 0 0 0 0\n",
	} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s := New(os.DirFS(dir), Override{IdleW: 10, FullW: 110})
	res, err := s.Probe(context.Background())
	if err != nil || len(res.Problems) != 0 {
		t.Fatalf("configured model must not warn: %+v %v", res, err)
	}
	// 75 busy ticks out of 100 since the probe.
	if err := os.WriteFile(filepath.Join(dir, "proc/stat"), []byte("cpu  175 0 0 125 0 0 0 0 0 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rs, err := s.Read(context.Background())
	if err != nil || len(rs) != 1 || math.Abs(rs[0].PowerW-85) > 1e-9 {
		t.Fatalf("readings %+v, err %v", rs, err)
	}
}

func TestUnknownProcessorWarns(t *testing.T) {
	res, err := New(sourcetest.Host(t, "arm-ampere-altra"), Override{}).Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Problems) != 1 || res.Problems[0].Code != help.GL1010 {
		t.Fatalf("problems %+v", res.Problems)
	}
}
