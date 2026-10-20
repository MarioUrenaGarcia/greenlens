// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package rapl

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/MarioUrenaGarcia/greenlens/internal/help"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
	"github.com/MarioUrenaGarcia/greenlens/internal/source/sourcetest"
)

func TestProbe(t *testing.T) {
	tests := []struct {
		host string
		want map[string]source.Domain
	}{
		{"amd-zen4-desktop", map[string]source.Domain{"rapl:package-0": source.DomainPackage, "rapl:package-0:core": source.DomainCore}},
		{"intel-client-1s", map[string]source.Domain{
			"rapl:package-0": source.DomainPackage, "rapl:package-0:core": source.DomainCore,
			"rapl:package-0:uncore": source.DomainUncore, "rapl:psys": source.DomainPSys,
		}},
		{"intel-server-2s", map[string]source.Domain{
			"rapl:package-0": source.DomainPackage, "rapl:package-0:dram": source.DomainDRAM,
			"rapl:package-1": source.DomainPackage, "rapl:package-1:dram": source.DomainDRAM,
		}},
		{"vm-no-sensors", map[string]source.Domain{}},
		{"arm-ampere-altra", map[string]source.Domain{}},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			s := New(sourcetest.Host(t, tt.host))
			res, err := s.Probe(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]source.Domain{}
			for _, c := range res.Channels {
				got[c.ID] = c.Domain
				if c.Kind != source.KindCounter || c.Quality != source.QualityMeasured || c.RangeJ <= 0 {
					t.Errorf("%s: unexpected channel %+v", c.ID, c)
				}
			}
			if len(got) != len(tt.want) {
				t.Fatalf("channels %v, want %v", got, tt.want)
			}
			for id, d := range tt.want {
				if got[id] != d {
					t.Errorf("%s: domain %q, want %q", id, got[id], d)
				}
			}
		})
	}
}

func TestMMIODuplicateIsDropped(t *testing.T) {
	s := New(sourcetest.Host(t, "intel-client-1s"))
	res, err := s.Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, c := range res.Channels {
		if c.Domain == source.DomainPackage {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("got %d package channels, want 1 (MSR kept, MMIO dropped)", n)
	}
	for _, z := range s.zones {
		if z.channel.Domain == source.DomainPackage && filepath.Base(z.dir) != "intel-rapl:0" {
			t.Errorf("package read from %s, want the MSR zone", z.dir)
		}
	}
}

func TestRead(t *testing.T) {
	s := New(sourcetest.Host(t, "intel-server-2s"))
	if _, err := s.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	rs, err := s.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]float64{}
	for _, r := range rs {
		got[r.Channel] = r.EnergyJ
	}
	if got["rapl:package-1"] != 50000.000001 || got["rapl:package-0:dram"] != 9000 {
		t.Errorf("readings %v", got)
	}
}

func TestPermissionDenied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read files without permission bits")
	}
	dir := t.TempDir()
	zone := filepath.Join(dir, "sys", "class", "powercap", "intel-rapl:0")
	if err := os.MkdirAll(zone, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]string{"name": "package-0", "max_energy_range_uj": "262143328850", "energy_uj": "1"} {
		if err := os.WriteFile(filepath.Join(zone, name), []byte(v+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(zone, "energy_uj"), 0); err != nil {
		t.Fatal(err)
	}
	res, err := New(os.DirFS(dir)).Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Available() {
		t.Fatal("unreadable zone reported as available")
	}
	if len(res.Problems) != 1 || res.Problems[0].Code != help.GL1002 {
		t.Fatalf("problems %+v, want one GL1002", res.Problems)
	}
}

func TestClassify(t *testing.T) {
	tests := []struct {
		name   string
		sub    bool
		parent string
		domain source.Domain
		key    string
	}{
		{"package-0", false, "", source.DomainPackage, "package-0"},
		{"package-1-die-1", false, "", source.DomainPackage, "package-1-die-1"},
		{"psys", false, "", source.DomainPSys, "psys"},
		{"psys-1", false, "", source.DomainPSys, "psys-1"},
		{"dram", true, "1", source.DomainDRAM, "package-1:dram"},
		{"dram", false, "", source.DomainDRAM, "dram"},
		{"core", true, "0", source.DomainCore, "package-0:core"},
	}
	for _, tt := range tests {
		d, _, key, ok := classify(tt.name, tt.sub, tt.parent)
		if !ok || d != tt.domain || key != tt.key {
			t.Errorf("classify(%q) = %q %q %v, want %q %q", tt.name, d, key, ok, tt.domain, tt.key)
		}
	}
	if _, _, _, ok := classify("gpu", true, "0"); ok {
		t.Error("unknown zone name accepted")
	}
}
