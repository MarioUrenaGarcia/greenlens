// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

// Package nvmlhelper is the body of greenlens-nvml: it answers the requests
// of package nvmlproto with the values of the NVIDIA Management Library.
package nvmlhelper

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/MarioUrenaGarcia/greenlens/internal/nvmlproto"
)

// Library is the part of NVML the helper uses. Errors are *nvmlproto.Error.
type Library interface {
	DriverVersion() (string, error)
	Count() (int, error)
	Device(index int) (Device, error)
	Shutdown()
}

// Device is one GPU.
type Device interface {
	Name() (string, error)
	BusID() (string, error)
	// TotalEnergy is in millijoules since the driver was loaded.
	TotalEnergy() (uint64, error)
	// Power is in milliwatts.
	Power() (uint32, error)
	// Utilization is the percentage of time a kernel ran.
	Utilization() (uint32, error)
	// PIDs lists the processes with a compute or graphics context.
	PIDs() ([]int, error)
	// ProcessUtilization returns the samples newer than since, a CPU time
	// stamp in microseconds, and the newest time stamp seen.
	ProcessUtilization(since uint64) ([]nvmlproto.Process, uint64, error)
}

// Opener loads and initializes the library.
type Opener func() (Library, error)

// maxLine bounds a request; requests are a few bytes.
const maxLine = 4096

type device struct {
	nvmlproto.Device
	dev      Device
	lastSeen uint64
}

type server struct {
	open    Opener
	lib     Library
	openErr *nvmlproto.Error
	opened  bool
	driver  string
	devices []*device
}

// Serve answers requests from in on out until in is closed. The library is
// opened on the first request and shut down when Serve returns.
func Serve(in io.Reader, out io.Writer, open Opener) error {
	s := &server{open: open}
	defer func() {
		if s.lib != nil {
			s.lib.Shutdown()
		}
	}()
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 256), maxLine)
	enc := json.NewEncoder(out)
	for sc.Scan() {
		var req nvmlproto.Request
		var resp any
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			resp = nvmlproto.ReadResponse{Error: &nvmlproto.Error{Call: "decode request", Text: err.Error()}}
		} else {
			switch req.Op {
			case nvmlproto.OpProbe:
				resp = s.probe()
			case nvmlproto.OpRead:
				resp = s.read()
			default:
				resp = nvmlproto.ReadResponse{Error: &nvmlproto.Error{Call: "decode request", Text: fmt.Sprintf("unknown operation %q", req.Op)}}
			}
		}
		if err := enc.Encode(resp); err != nil {
			return fmt.Errorf("write response: %w", err)
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("read request: %w", err)
	}
	return nil
}

func asNVML(err error, call string) *nvmlproto.Error {
	var e *nvmlproto.Error
	if errors.As(err, &e) {
		return e
	}
	return &nvmlproto.Error{Call: call, Text: err.Error()}
}

// init opens the library and lists the devices once; the set of GPUs does
// not change while the driver stays loaded.
func (s *server) init() *nvmlproto.Error {
	if s.opened {
		return s.openErr
	}
	s.opened = true
	lib, err := s.open()
	if err != nil {
		s.openErr = asNVML(err, "nvmlInit_v2")
		return s.openErr
	}
	s.lib = lib
	if s.driver, err = lib.DriverVersion(); err != nil {
		s.driver = ""
	}
	n, err := lib.Count()
	if err != nil {
		s.openErr = asNVML(err, "nvmlDeviceGetCount_v2")
		return s.openErr
	}
	for i := range n {
		dev, err := lib.Device(i)
		if err != nil {
			// A GPU that fails here, for example one that fell off the
			// bus, is left out so the others can still be measured.
			continue
		}
		d := &device{dev: dev, Device: nvmlproto.Device{Index: i}}
		if d.Name, err = dev.Name(); err != nil {
			d.Name = ""
		}
		bus, err := dev.BusID()
		if err != nil {
			continue
		}
		if d.PCI, err = nvmlproto.PCIAddress(bus); err != nil {
			continue
		}
		_, err = dev.TotalEnergy()
		d.Energy = err == nil
		_, err = dev.Power()
		d.Power = err == nil
		s.devices = append(s.devices, d)
	}
	return nil
}

func (s *server) probe() nvmlproto.ProbeResponse {
	resp := nvmlproto.ProbeResponse{Version: nvmlproto.Version}
	if err := s.init(); err != nil {
		resp.Error = err
		return resp
	}
	resp.Driver = s.driver
	for _, d := range s.devices {
		resp.Devices = append(resp.Devices, d.Device)
	}
	return resp
}

func (s *server) read() nvmlproto.ReadResponse {
	var resp nvmlproto.ReadResponse
	if err := s.init(); err != nil {
		resp.Error = err
		return resp
	}
	for _, d := range s.devices {
		resp.Samples = append(resp.Samples, d.sample())
	}
	return resp
}

func (d *device) sample() nvmlproto.Sample {
	smp := nvmlproto.Sample{Index: d.Index}
	var err error
	switch {
	case d.Energy:
		if smp.EnergyMJ, err = d.dev.TotalEnergy(); err != nil {
			smp.Error = asNVML(err, "nvmlDeviceGetTotalEnergyConsumption")
			return smp
		}
	case d.Power:
		if smp.PowerMW, err = d.dev.Power(); err != nil {
			smp.Error = asNVML(err, "nvmlDeviceGetPowerUsage")
			return smp
		}
	}
	// Utilization and processes refine the attribution; a device that does
	// not report them is still measured.
	if u, err := d.dev.Utilization(); err == nil {
		smp.Util = u
	}
	if pids, err := d.dev.PIDs(); err == nil {
		smp.PIDs = pids
	}
	if procs, seen, err := d.dev.ProcessUtilization(d.lastSeen); err == nil {
		smp.Processes = procs
		d.lastSeen = max(d.lastSeen, seen)
	}
	return smp
}
