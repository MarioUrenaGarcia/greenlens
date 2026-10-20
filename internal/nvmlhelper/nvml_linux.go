// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

//go:build linux && (amd64 || arm64)

package nvmlhelper

import (
	"fmt"
	"slices"

	"github.com/ebitengine/purego"

	"github.com/MarioUrenaGarcia/greenlens/internal/nvmlproto"
)

// libraryName is the versioned name the NVIDIA driver installs; the
// unversioned link exists only with development packages.
const libraryName = "libnvidia-ml.so.1"

// Buffer sizes from nvml.h.
const (
	driverVersionSize = 80 // NVML_SYSTEM_DRIVER_VERSION_BUFFER_SIZE
	deviceNameSize    = 96 // NVML_DEVICE_NAME_V2_BUFFER_SIZE
)

// The structures below follow nvml.h field by field; fields GreenLens does
// not read are blank but keep their place.

// pciInfo is nvmlPciInfo_t.
type pciInfo struct {
	_     [16]byte  // busIdLegacy
	_     [5]uint32 // domain, bus, device, pciDeviceId, pciSubSystemId
	busID [32]byte
}

// utilization is nvmlUtilization_t.
type utilization struct {
	gpu uint32
	_   uint32 // memory
}

// processInfo is nvmlProcessInfo_v2_t, used by the _v3 process queries.
type processInfo struct {
	pid uint32
	_   uint64    // usedGpuMemory
	_   [2]uint32 // gpuInstanceId, computeInstanceId
}

// processSample is nvmlProcessUtilizationSample_t.
type processSample struct {
	pid       uint32
	timeStamp uint64
	smUtil    uint32
	_         [3]uint32 // memUtil, encUtil, decUtil
}

// lib holds the NVML entry points. Optional ones are nil when the driver
// does not export them.
type lib struct {
	initV2        func() int32
	shutdown      func() int32
	errorString   func(int32) string
	driverVersion func(*byte, uint32) int32
	countV2       func(*uint32) int32
	handleByIndex func(uint32, *uintptr) int32
	name          func(uintptr, *byte, uint32) int32
	pciInfoV3     func(uintptr, *pciInfo) int32
	totalEnergy   func(uintptr, *uint64) int32
	powerUsage    func(uintptr, *uint32) int32
	utilization   func(uintptr, *utilization) int32
	compute       func(uintptr, *uint32, *processInfo) int32
	graphics      func(uintptr, *uint32, *processInfo) int32
	processUtil   func(uintptr, *processSample, *uint32, uint64) int32
}

// Open loads libnvidia-ml and initializes it.
func Open() (Library, error) {
	h, err := purego.Dlopen(libraryName, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return nil, &nvmlproto.Error{Call: "dlopen " + libraryName, Return: nvmlproto.ErrLibraryMissing, Text: err.Error()}
	}
	l := &lib{}
	required := []struct {
		fn   any
		name string
	}{
		{&l.initV2, "nvmlInit_v2"},
		{&l.shutdown, "nvmlShutdown"},
		{&l.errorString, "nvmlErrorString"},
		{&l.driverVersion, "nvmlSystemGetDriverVersion"},
		{&l.countV2, "nvmlDeviceGetCount_v2"},
		{&l.handleByIndex, "nvmlDeviceGetHandleByIndex_v2"},
		{&l.name, "nvmlDeviceGetName"},
		{&l.pciInfoV3, "nvmlDeviceGetPciInfo_v3"},
		{&l.totalEnergy, "nvmlDeviceGetTotalEnergyConsumption"},
		{&l.powerUsage, "nvmlDeviceGetPowerUsage"},
		{&l.utilization, "nvmlDeviceGetUtilizationRates"},
	}
	for _, f := range required {
		sym, err := purego.Dlsym(h, f.name)
		if err != nil {
			return nil, &nvmlproto.Error{Call: "dlsym " + f.name, Return: nvmlproto.ErrLibraryMissing, Text: err.Error()}
		}
		purego.RegisterFunc(f.fn, sym)
	}
	optional := []struct {
		fn   any
		name string
	}{
		{&l.compute, "nvmlDeviceGetComputeRunningProcesses_v3"},
		{&l.graphics, "nvmlDeviceGetGraphicsRunningProcesses_v3"},
		{&l.processUtil, "nvmlDeviceGetProcessUtilization"},
	}
	for _, f := range optional {
		if sym, err := purego.Dlsym(h, f.name); err == nil {
			purego.RegisterFunc(f.fn, sym)
		}
	}
	if err := l.check("nvmlInit_v2", l.initV2()); err != nil {
		return nil, err
	}
	return l, nil
}

func (l *lib) check(call string, ret int32) error {
	if ret == nvmlproto.Success {
		return nil
	}
	return &nvmlproto.Error{Call: call, Return: int(ret), Text: l.errorString(ret)}
}

func cString(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

func (l *lib) Shutdown() { _ = l.shutdown() }

func (l *lib) DriverVersion() (string, error) {
	var buf [driverVersionSize]byte
	if err := l.check("nvmlSystemGetDriverVersion", l.driverVersion(&buf[0], uint32(len(buf)))); err != nil {
		return "", err
	}
	return cString(buf[:]), nil
}

func (l *lib) Count() (int, error) {
	var n uint32
	if err := l.check("nvmlDeviceGetCount_v2", l.countV2(&n)); err != nil {
		return 0, err
	}
	return int(n), nil
}

func (l *lib) Device(index int) (Device, error) {
	var h uintptr
	if index < 0 || index > 1<<16 {
		return nil, fmt.Errorf("device index %d out of range", index)
	}
	if err := l.check("nvmlDeviceGetHandleByIndex_v2", l.handleByIndex(uint32(index), &h)); err != nil {
		return nil, err
	}
	return &dev{l: l, h: h}, nil
}

type dev struct {
	l *lib
	h uintptr
}

func (d *dev) Name() (string, error) {
	var buf [deviceNameSize]byte
	if err := d.l.check("nvmlDeviceGetName", d.l.name(d.h, &buf[0], uint32(len(buf)))); err != nil {
		return "", err
	}
	return cString(buf[:]), nil
}

func (d *dev) BusID() (string, error) {
	var p pciInfo
	if err := d.l.check("nvmlDeviceGetPciInfo_v3", d.l.pciInfoV3(d.h, &p)); err != nil {
		return "", err
	}
	return cString(p.busID[:]), nil
}

func (d *dev) TotalEnergy() (uint64, error) {
	var mj uint64
	return mj, d.l.check("nvmlDeviceGetTotalEnergyConsumption", d.l.totalEnergy(d.h, &mj))
}

func (d *dev) Power() (uint32, error) {
	var mw uint32
	return mw, d.l.check("nvmlDeviceGetPowerUsage", d.l.powerUsage(d.h, &mw))
}

func (d *dev) Utilization() (uint32, error) {
	var u utilization
	return u.gpu, d.l.check("nvmlDeviceGetUtilizationRates", d.l.utilization(d.h, &u))
}

// maxProcesses bounds the buffers of the process queries.
const maxProcesses = 4096

func (d *dev) PIDs() ([]int, error) {
	if d.l.compute == nil || d.l.graphics == nil {
		return nil, &nvmlproto.Error{Call: "nvmlDeviceGetComputeRunningProcesses_v3", Return: nvmlproto.ErrNotSupported}
	}
	var out []int
	for _, q := range []struct {
		call string
		fn   func(uintptr, *uint32, *processInfo) int32
	}{
		{"nvmlDeviceGetComputeRunningProcesses_v3", d.l.compute},
		{"nvmlDeviceGetGraphicsRunningProcesses_v3", d.l.graphics},
	} {
		infos, err := d.processes(q.call, q.fn)
		if err != nil {
			return nil, err
		}
		// A process with both kinds of context appears in both lists.
		for _, p := range infos {
			if !slices.Contains(out, int(p.pid)) {
				out = append(out, int(p.pid))
			}
		}
	}
	return out, nil
}

// processes calls a running-process query, growing the buffer when more
// processes appeared between the size query and the read.
func (d *dev) processes(call string, fn func(uintptr, *uint32, *processInfo) int32) ([]processInfo, error) {
	var n uint32
	ret := fn(d.h, &n, nil)
	if ret == nvmlproto.Success {
		return nil, nil
	}
	for range 3 {
		if ret != nvmlproto.ErrInsufficient {
			return nil, d.l.check(call, ret)
		}
		size := min(n+8, maxProcesses)
		buf := make([]processInfo, size)
		n = size
		if ret = fn(d.h, &n, &buf[0]); ret == nvmlproto.Success {
			return buf[:min(n, size)], nil
		}
	}
	return nil, d.l.check(call, ret)
}

func (d *dev) ProcessUtilization(since uint64) ([]nvmlproto.Process, uint64, error) {
	const call = "nvmlDeviceGetProcessUtilization"
	if d.l.processUtil == nil {
		return nil, since, &nvmlproto.Error{Call: call, Return: nvmlproto.ErrNotSupported}
	}
	var n uint32
	ret := d.l.processUtil(d.h, nil, &n, since)
	switch ret {
	case nvmlproto.ErrNotFound:
		// No process ran since the previous read.
		return nil, since, nil
	case nvmlproto.Success, nvmlproto.ErrInsufficient:
	default:
		return nil, since, d.l.check(call, ret)
	}
	if n == 0 {
		return nil, since, nil
	}
	size := min(n+8, maxProcesses)
	buf := make([]processSample, size)
	n = size
	ret = d.l.processUtil(d.h, &buf[0], &n, since)
	if ret == nvmlproto.ErrNotFound {
		return nil, since, nil
	}
	if err := d.l.check(call, ret); err != nil {
		return nil, since, err
	}
	seen := since
	byPID := map[uint32]uint32{}
	var order []uint32
	for _, s := range buf[:min(n, size)] {
		seen = max(seen, s.timeStamp)
		if _, ok := byPID[s.pid]; !ok {
			order = append(order, s.pid)
		}
		// The driver can return several samples of one process; the
		// largest describes the period best.
		byPID[s.pid] = max(byPID[s.pid], s.smUtil)
	}
	out := make([]nvmlproto.Process, 0, len(order))
	for _, pid := range order {
		out = append(out, nvmlproto.Process{PID: int(pid), SM: byPID[pid]})
	}
	return out, seen, nil
}
