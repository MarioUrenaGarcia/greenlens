// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package nvmlhelper

import (
	"bufio"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MarioUrenaGarcia/greenlens/internal/nvmlproto"
)

type fakeDevice struct {
	name, bus   string
	energy      uint64
	power       uint32
	noEnergy    bool
	noPower     bool
	readErr     bool
	util        uint32
	pids        []int
	procs       []nvmlproto.Process
	procsSince  []uint64
	processTime uint64
}

func notSupported(call string) error {
	return &nvmlproto.Error{Call: call, Return: nvmlproto.ErrNotSupported}
}

func (d *fakeDevice) Name() (string, error)  { return d.name, nil }
func (d *fakeDevice) BusID() (string, error) { return d.bus, nil }
func (d *fakeDevice) TotalEnergy() (uint64, error) {
	if d.noEnergy {
		return 0, notSupported("nvmlDeviceGetTotalEnergyConsumption")
	}
	if d.readErr {
		return 0, &nvmlproto.Error{Call: "nvmlDeviceGetTotalEnergyConsumption", Return: nvmlproto.ErrGPULost}
	}
	d.energy += 1000
	return d.energy, nil
}
func (d *fakeDevice) Power() (uint32, error) {
	if d.noPower {
		return 0, notSupported("nvmlDeviceGetPowerUsage")
	}
	return d.power, nil
}
func (d *fakeDevice) Utilization() (uint32, error) { return d.util, nil }
func (d *fakeDevice) PIDs() ([]int, error)         { return d.pids, nil }
func (d *fakeDevice) ProcessUtilization(since uint64) ([]nvmlproto.Process, uint64, error) {
	d.procsSince = append(d.procsSince, since)
	return d.procs, d.processTime, nil
}

type fakeLib struct {
	devices  []*fakeDevice
	shutdown bool
}

func (l *fakeLib) DriverVersion() (string, error) { return "595.99.02", nil }
func (l *fakeLib) Count() (int, error)            { return len(l.devices), nil }
func (l *fakeLib) Device(i int) (Device, error)   { return l.devices[i], nil }
func (l *fakeLib) Shutdown()                      { l.shutdown = true }

func serve(t *testing.T, open Opener, requests ...string) []string {
	t.Helper()
	var out strings.Builder
	if err := Serve(strings.NewReader(strings.Join(requests, "\n")+"\n"), &out, open); err != nil {
		t.Fatal(err)
	}
	var lines []string
	sc := bufio.NewScanner(strings.NewReader(out.String()))
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if len(lines) != len(requests) {
		t.Fatalf("%d answers to %d requests: %q", len(lines), len(requests), out.String())
	}
	return lines
}

func decode[T any](t *testing.T, line string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(line), &v); err != nil {
		t.Fatalf("%q: %v", line, err)
	}
	return v
}

func TestProbeAndRead(t *testing.T) {
	volta := &fakeDevice{name: "Tesla V100", bus: "00000000:D8:00.0", util: 90, pids: []int{7}, procs: []nvmlproto.Process{{PID: 7, SM: 88}}, processTime: 500}
	pascal := &fakeDevice{name: "Tesla P100", bus: "00000000:3B:00.0", noEnergy: true, power: 31250}
	headless := &fakeDevice{name: "no sensors", bus: "00000000:5E:00.0", noEnergy: true, noPower: true}
	lib := &fakeLib{devices: []*fakeDevice{volta, pascal, headless}}
	opens := 0
	open := func() (Library, error) { opens++; return lib, nil }
	lines := serve(t, open, `{"op":"probe"}`, `{"op":"read"}`, `{"op":"read"}`)
	if opens != 1 || !lib.shutdown {
		t.Errorf("opened %d times, shut down %v", opens, lib.shutdown)
	}
	p := decode[nvmlproto.ProbeResponse](t, lines[0])
	want := []nvmlproto.Device{
		{Index: 0, Name: "Tesla V100", PCI: "0000:d8:00.0", Energy: true, Power: true},
		{Index: 1, Name: "Tesla P100", PCI: "0000:3b:00.0", Power: true},
		{Index: 2, Name: "no sensors", PCI: "0000:5e:00.0"},
	}
	if p.Version != nvmlproto.Version || p.Driver != "595.99.02" || len(p.Devices) != 3 {
		t.Fatalf("probe %+v", p)
	}
	for i := range want {
		if p.Devices[i] != want[i] {
			t.Errorf("device %d = %+v, want %+v", i, p.Devices[i], want[i])
		}
	}
	r := decode[nvmlproto.ReadResponse](t, lines[2])
	if len(r.Samples) != 3 {
		t.Fatalf("read %+v", r)
	}
	// The probe read the counter once, each read once more.
	if s := r.Samples[0]; s.EnergyMJ != 3000 || s.PowerMW != 0 || s.Util != 90 || len(s.Processes) != 1 || s.Processes[0].SM != 88 {
		t.Errorf("volta %+v", s)
	}
	if s := r.Samples[1]; s.EnergyMJ != 0 || s.PowerMW != 31250 {
		t.Errorf("pascal %+v", s)
	}
	// Process samples are asked since the newest time stamp seen.
	if got := volta.procsSince; len(got) != 2 || got[0] != 0 || got[1] != 500 {
		t.Errorf("process utilization asked since %v", got)
	}
}

func TestErrors(t *testing.T) {
	failing := func() (Library, error) {
		return nil, &nvmlproto.Error{Call: "nvmlInit_v2", Return: nvmlproto.ErrDriverNotLoad, Text: "Driver Not Loaded"}
	}
	lines := serve(t, failing, `{"op":"probe"}`, `{"op":"read"}`)
	if p := decode[nvmlproto.ProbeResponse](t, lines[0]); p.Error == nil || p.Error.Return != nvmlproto.ErrDriverNotLoad || len(p.Devices) != 0 {
		t.Errorf("probe %+v", p)
	}
	if r := decode[nvmlproto.ReadResponse](t, lines[1]); r.Error == nil || r.Error.Call != "nvmlInit_v2" {
		t.Errorf("read %+v", r)
	}
}

func TestBadRequestsAreAnswered(t *testing.T) {
	lib := &fakeLib{devices: []*fakeDevice{{name: "a", bus: "00000000:01:00.0"}}}
	lines := serve(t, func() (Library, error) { return lib, nil }, `not json`, `{"op":"reboot"}`, `{"op":"read"}`)
	for _, l := range lines[:2] {
		if r := decode[nvmlproto.ReadResponse](t, l); r.Error == nil || r.Error.Call != "decode request" {
			t.Errorf("answer %q", l)
		}
	}
	if r := decode[nvmlproto.ReadResponse](t, lines[2]); r.Error != nil || len(r.Samples) != 1 {
		t.Errorf("a valid request after bad ones: %q", lines[2])
	}
}

func TestLostGPUDoesNotHideTheOthers(t *testing.T) {
	lost := &fakeDevice{name: "lost", bus: "00000000:01:00.0"}
	ok := &fakeDevice{name: "ok", bus: "00000000:02:00.0"}
	lib := &fakeLib{devices: []*fakeDevice{lost, ok}}
	s := &server{open: func() (Library, error) { return lib, nil }}
	if err := s.init(); err != nil {
		t.Fatal(err)
	}
	lost.readErr = true
	r := s.read()
	if len(r.Samples) != 2 || r.Samples[0].Error == nil || r.Samples[0].Error.Return != nvmlproto.ErrGPULost || r.Samples[1].Error != nil || r.Samples[1].EnergyMJ == 0 {
		t.Fatalf("read %+v", r)
	}
}
