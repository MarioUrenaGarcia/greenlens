// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package perfevent

import (
	"context"
	"syscall"
	"testing"

	"github.com/MarioUrenaGarcia/greenlens/internal/help"
	"github.com/MarioUrenaGarcia/greenlens/internal/source"
	"github.com/MarioUrenaGarcia/greenlens/internal/source/sourcetest"
)

type fakeCounter struct {
	value  uint64
	closed bool
}

func (c *fakeCounter) Read() (uint64, error) { return c.value, nil }
func (c *fakeCounter) Close() error          { c.closed = true; return nil }

type fakeOpener struct {
	err    error
	opened []struct {
		pmuType uint32
		config  uint64
		cpu     int
	}
	counters []*fakeCounter
}

func (o *fakeOpener) Open(pmuType uint32, config uint64, cpu int) (Counter, error) {
	if o.err != nil {
		return nil, o.err
	}
	o.opened = append(o.opened, struct {
		pmuType uint32
		config  uint64
		cpu     int
	}{pmuType, config, cpu})
	c := &fakeCounter{value: 1 << 32}
	o.counters = append(o.counters, c)
	return c, nil
}

func TestProbeTwoSockets(t *testing.T) {
	o := &fakeOpener{}
	s := New(sourcetest.Host(t, "intel-server-2s"), o)
	res, err := s.Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]source.Domain{
		"perf_event:package-0": source.DomainPackage, "perf_event:package-1": source.DomainPackage,
		"perf_event:package-0:dram": source.DomainDRAM, "perf_event:package-1:dram": source.DomainDRAM,
	}
	if len(res.Channels) != len(want) {
		t.Fatalf("channels %+v", res.Channels)
	}
	for _, c := range res.Channels {
		if want[c.ID] != c.Domain {
			t.Errorf("unexpected channel %s (%s)", c.ID, c.Domain)
		}
	}
	for _, op := range o.opened {
		if op.pmuType != 23 || (op.cpu != 0 && op.cpu != 24) || (op.config != 2 && op.config != 3) {
			t.Errorf("opened %+v", op)
		}
	}
	rs, err := s.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rs {
		// 2^32 counts of 2^-32 J each.
		if r.EnergyJ != 1 {
			t.Errorf("%s: %v J, want 1", r.Channel, r.EnergyJ)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	for _, c := range o.counters {
		if !c.closed {
			t.Error("counter left open")
		}
	}
}

func TestPsysOnlyOnce(t *testing.T) {
	s := New(sourcetest.Host(t, "intel-client-1s"), &fakeOpener{})
	res, err := s.Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, c := range res.Channels {
		ids[c.ID] = true
	}
	if !ids["perf_event:psys"] || !ids["perf_event:package-0"] || len(ids) != 2 {
		t.Errorf("channels %v; energy-cores and energy-gpu must be ignored", ids)
	}
}

func TestPermissionDenied(t *testing.T) {
	s := New(sourcetest.Host(t, "amd-zen4-desktop"), &fakeOpener{err: syscall.EACCES})
	res, err := s.Probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Available() || len(res.Problems) != 1 || res.Problems[0].Code != help.GL1003 {
		t.Fatalf("result %+v", res)
	}
	if res.Problems[0].Args[1] != "4" {
		t.Errorf("paranoid level not reported: %v", res.Problems[0].Args)
	}
}

func TestNoPMU(t *testing.T) {
	res, err := New(sourcetest.Host(t, "vm-no-sensors"), &fakeOpener{}).Probe(context.Background())
	if err != nil || res.Available() || len(res.Problems) != 0 {
		t.Fatalf("result %+v, err %v", res, err)
	}
}
