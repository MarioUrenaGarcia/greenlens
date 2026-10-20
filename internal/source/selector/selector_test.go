// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package selector_test

import (
	"context"
	"io/fs"
	"slices"
	"syscall"
	"testing"

	"github.com/MarioUrenaGarcia/greenlens/internal/help"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
	"github.com/MarioUrenaGarcia/greenlens/internal/source/ipmi"
	"github.com/MarioUrenaGarcia/greenlens/internal/source/perfevent"
	"github.com/MarioUrenaGarcia/greenlens/internal/source/registry"
	"github.com/MarioUrenaGarcia/greenlens/internal/source/selector"
	"github.com/MarioUrenaGarcia/greenlens/internal/source/sourcetest"
)

type counter struct{}

func (counter) Read() (uint64, error) { return 1, nil }
func (counter) Close() error          { return nil }

type perfOpener struct{ err error }

func (o perfOpener) Open(uint32, uint64, int) (perfevent.Counter, error) {
	if o.err != nil {
		return nil, o.err
	}
	return counter{}, nil
}

type noIPMI struct{}

func (noIPMI) Open(string) (ipmi.Device, error) { return nil, fs.ErrNotExist }

func probe(t *testing.T, host string, perfErr error) []selector.Candidate {
	t.Helper()
	srcs := registry.Sources(registry.Options{FS: sourcetest.Host(t, host), Perf: perfOpener{err: perfErr}, IPMI: noIPMI{}})
	cands, err := registry.Probe(context.Background(), srcs)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registry.Close(srcs) })
	return cands
}

func ids(chs []source.Channel) []string {
	var out []string
	for _, c := range chs {
		out = append(out, c.ID)
	}
	slices.Sort(out)
	return out
}

var gpus = []string{"hwmon_gpu:0000:03:00.0", "hwmon_gpu:0000:04:00.0", "hwmon_gpu:0000:05:00.0"}

func TestSelectOnRecordedHosts(t *testing.T) {
	tests := []struct {
		host      string
		want      []string
		estimated bool
	}{
		{"amd-zen4-desktop", []string{"rapl:package-0", "rapl:package-0:core"}, false},
		{"intel-client-1s", []string{"rapl:package-0", "rapl:package-0:core", "rapl:package-0:uncore", "rapl:psys"}, false},
		{"intel-server-2s", []string{"rapl:package-0", "rapl:package-0:dram", "rapl:package-1", "rapl:package-1:dram"}, false},
		{"laptop-battery", []string{"battery:BAT0", "rapl:package-0", "rapl:package-0:core", "rapl:psys"}, false},
		{"vm-no-sensors", []string{"estimate:cpu"}, true},
		{"arm-ampere-altra", []string{"hwmon:package-0", "hwmon:package-0:dram"}, false},
		{"server-acpi", []string{"acpi_power_meter:power_meter-0", "rapl:package-0"}, false},
		{"workstation-gpus-new", append(slices.Clone(gpus), "rapl:package-0", "rapl:package-0:core", "rapl:package-0:uncore", "rapl:psys"), false},
		{"workstation-gpus-old", append(slices.Clone(gpus), "rapl:package-0", "rapl:package-0:core", "rapl:package-0:uncore", "rapl:psys"), false},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			sel := selector.Select(probe(t, tt.host, nil), selector.Options{})
			slices.Sort(tt.want)
			if got := ids(sel.Channels); !slices.Equal(got, tt.want) {
				t.Fatalf("selected %v, want %v", got, tt.want)
			}
			if sel.Estimated != tt.estimated {
				t.Errorf("estimated = %v", sel.Estimated)
			}
			hasGL1007 := slices.ContainsFunc(sel.Problems, func(p source.Problem) bool { return p.Code == help.GL1007 })
			if hasGL1007 != tt.estimated {
				t.Errorf("GL1007 reported = %v", hasGL1007)
			}
			// No energy may be counted twice: every component domain and
			// instance is selected from a single source.
			seen := map[string]string{}
			for _, c := range sel.Channels {
				k := string(c.Domain) + "/" + c.Instance
				if prev, dup := seen[k]; dup && prev != c.Source {
					t.Errorf("%s measured by %s and %s", k, prev, c.Source)
				}
				seen[k] = c.Source
			}
			for _, d := range sel.Decisions {
				if d.Reason.Key == "" {
					t.Errorf("%s has no reason", d.Channel.ID)
				}
			}
		})
	}
}

func TestDuplicatesAreExplained(t *testing.T) {
	sel := selector.Select(probe(t, "amd-zen4-desktop", nil), selector.Options{})
	for _, d := range sel.Decisions {
		if d.Channel.ID == "perf_event:package-0" {
			if d.Selected || d.Reason.Key != "selector.reason.duplicate" || d.Reason.Args[1] != "rapl" {
				t.Fatalf("perf_event decision %+v", d)
			}
			return
		}
	}
	t.Fatal("perf_event channel missing from decisions")
}

func TestPerfEventWhenPowercapIsDisabled(t *testing.T) {
	sel := selector.Select(probe(t, "intel-server-2s", nil), selector.Options{Disable: []string{"rapl"}})
	want := []string{"perf_event:package-0", "perf_event:package-0:dram", "perf_event:package-1", "perf_event:package-1:dram"}
	if got := ids(sel.Channels); !slices.Equal(got, want) {
		t.Fatalf("selected %v, want %v", got, want)
	}
}

func TestForce(t *testing.T) {
	sel := selector.Select(probe(t, "intel-client-1s", nil), selector.Options{Force: map[source.Domain]string{source.DomainPackage: "perf_event"}})
	if !sel.Selected("perf_event:package-0") || sel.Selected("rapl:package-0") {
		t.Fatalf("selected %v", ids(sel.Channels))
	}
	// Core and uncore exist only in powercap; they follow the package.
	if sel.Selected("rapl:package-0:core") {
		t.Error("core selected from a source other than the package")
	}
	for _, d := range sel.Decisions {
		if d.Channel.ID == "rapl:package-0" && d.Reason.Key != "selector.reason.forced" {
			t.Errorf("reason %q", d.Reason.Key)
		}
	}
	// Forcing a source that lacks the domain falls back to the automatic choice.
	sel = selector.Select(probe(t, "intel-client-1s", nil), selector.Options{Force: map[source.Domain]string{source.DomainPackage: "battery"}})
	if !sel.Selected("rapl:package-0") {
		t.Errorf("selected %v", ids(sel.Channels))
	}
}

func TestPerfPermissionProblemIsKept(t *testing.T) {
	sel := selector.Select(probe(t, "amd-zen4-desktop", syscall.EACCES), selector.Options{})
	if !slices.ContainsFunc(sel.Problems, func(p source.Problem) bool { return p.Code == help.GL1003 }) {
		t.Fatalf("problems %+v", sel.Problems)
	}
	if !sel.Selected("rapl:package-0") {
		t.Fatal("powercap must still be used")
	}
}

func TestEverythingDisabledLeavesNothing(t *testing.T) {
	sel := selector.Select(probe(t, "vm-no-sensors", nil), selector.Options{Disable: []string{"estimate"}})
	if len(sel.Channels) != 0 || sel.Estimated {
		t.Fatalf("selection %+v", sel)
	}
	if got := sel.Sources(); len(got) != 0 {
		t.Fatalf("sources %v", got)
	}
}

// fake is a source with fixed channels.
type fake struct {
	id       string
	priority int
	channels []source.Channel
}

func (f fake) ID() string                                     { return f.id }
func (f fake) Priority() int                                  { return f.priority }
func (f fake) Read(context.Context) ([]source.Reading, error) { return nil, nil }
func (f fake) Close() error                                   { return nil }
func (f fake) Probe(context.Context) (source.ProbeResult, error) {
	return source.ProbeResult{Source: f.id, Channels: f.channels}, nil
}

func gpu(src, inst string) source.Channel {
	return source.Channel{ID: src + ":" + inst, Source: src, Domain: source.DomainGPU, Instance: inst}
}

func candidates(t *testing.T, srcs ...fake) []selector.Candidate {
	t.Helper()
	var out []selector.Candidate
	for _, s := range srcs {
		res, err := s.Probe(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, selector.Candidate{Source: s, Result: res})
	}
	return out
}

func TestGPUsAreChosenPerDevice(t *testing.T) {
	// The first source sees two cards, the second one of them and a third.
	cands := candidates(t,
		fake{id: "low", priority: 6, channels: []source.Channel{gpu("low", "0000:01:00.0"), gpu("low", "0000:02:00.0")}},
		fake{id: "high", priority: 1, channels: []source.Channel{gpu("high", "0000:02:00.0"), gpu("high", "0000:03:00.0")}},
	)
	sel := selector.Select(cands, selector.Options{})
	want := []string{"high:0000:02:00.0", "high:0000:03:00.0", "low:0000:01:00.0"}
	if got := ids(sel.Channels); !slices.Equal(got, want) {
		t.Fatalf("selected %v, want %v", got, want)
	}
	for _, d := range sel.Decisions {
		if d.Channel.ID == "low:0000:02:00.0" && (d.Selected || d.Reason.Key != "selector.reason.duplicate") {
			t.Errorf("decision %+v", d)
		}
	}
	// Forcing a source applies to every card it sees and leaves the others.
	sel = selector.Select(cands, selector.Options{Force: map[source.Domain]string{source.DomainGPU: "low"}})
	want = []string{"high:0000:03:00.0", "low:0000:01:00.0", "low:0000:02:00.0"}
	if got := ids(sel.Channels); !slices.Equal(got, want) {
		t.Fatalf("forced: selected %v, want %v", got, want)
	}
}

func TestAPUSensorDoesNotDisplacePowercap(t *testing.T) {
	sel := selector.Select(probe(t, "amd-zen4-desktop", nil), selector.Options{Disable: []string{"rapl", "perf_event"}})
	if !sel.Selected("hwmon_gpu:package-0") || sel.Estimated {
		t.Fatalf("without RAPL the APU sensor measures the package: %v", ids(sel.Channels))
	}
	sel = selector.Select(probe(t, "amd-zen4-desktop", nil), selector.Options{})
	if sel.Selected("hwmon_gpu:package-0") {
		t.Fatalf("RAPL must win the package: %v", ids(sel.Channels))
	}
}

func TestProblemsOfUnusedChannelsAreDropped(t *testing.T) {
	gl1010 := func(sel selector.Selection) bool {
		return slices.ContainsFunc(sel.Problems, func(p source.Problem) bool { return p.Code == help.GL1010 })
	}
	// The Ryzen 9 7900X is not in the estimation table.
	if sel := selector.Select(probe(t, "amd-zen4-desktop", nil), selector.Options{}); gl1010(sel) {
		t.Fatalf("GL1010 reported while RAPL measures the package: %+v", sel.Problems)
	}
	sel := selector.Select(probe(t, "amd-zen4-desktop", nil), selector.Options{Disable: []string{"rapl", "perf_event", "hwmon_gpu"}})
	if !sel.Estimated || !gl1010(sel) {
		t.Fatalf("GL1010 missing while the model is used: %+v", sel.Problems)
	}
}
